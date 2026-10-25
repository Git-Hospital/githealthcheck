package complexity

import (
	"context"
	"fmt"
	"hash/maphash"
	"maps"
	"slices"
	"sort"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

const (
	// MinCloneTokens and MinCloneLines are the size from which a repeated block is a finding:
	// SonarSource's, "at least 100 successive and duplicated tokens" spanning at least 10 lines
	// (D34).
	MinCloneTokens = 100
	MinCloneLines  = 10
	// MinCloneNames is the number of identifiers a block needs: one with fewer is a table of
	// literals, data rather than code.
	MinCloneNames = MinCloneTokens / 10
	// candidates is the number of earlier places a block is compared with, among those that look
	// the same to the hash.
	candidates = 8
)

// The flags of facts.Tokens.Name.
const (
	nameLocal   = 1 << 30
	nameFixed   = 1 << 31
	nameLiteral = nameLocal | nameFixed
)

// clone is one repeated block: the later copy, and the earlier one it repeats.
type clone struct {
	file, from, to int // the later copy: a file of the input and a token range
	orig, ofrom    int // the earlier copy
}

// value is what the hash and the first comparison see of a token: its kind, and for a name that may
// not change (a function, field, type or constant) the name too. Locals and literals are kinds only.
func value(t *facts.Tokens, i int) uint64 {
	if n := t.Name[i]; n&nameLiteral == nameFixed {
		return 1<<32 | uint64(n)
	}
	return uint64(t.Kind[i]) + 1
}

// clones finds the blocks of at least MinCloneTokens tokens over at least MinCloneLines lines that
// repeat an earlier block, up to a consistent renaming of the functions' own parameters and locals
// and any change of literals (a Type-2 clone in the terms of Roy, Cordy and Koschke 2009, checked with
// Baker's parameterised match); other names must be the same. Blocks start at the first token of a
// line and end with a whole line. Each block is reported once, against the first of up to candidates
// earlier places, in the order of files, whose first MinCloneTokens tokens are the same up to a
// consistent renaming.
func clones(ctx context.Context, files []*facts.File) ([]clone, error) {
	cf := &cloneFinder{
		files: files,
		// A base chosen per run: a crafted repository cannot plant blocks whose hashes collide.
		base:   new(maphash.Hash).Sum64() | 1,
		lone:   map[uint64]at{},
		places: map[uint64][]at{},
	}
	for fi, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(f.Tokens.Kind) >= MinCloneTokens {
			cf.file(fi)
		}
	}
	return cf.out, nil
}

// at is a token of a file.
type at struct{ file, tok int32 }

// cloneFinder holds the places seen so far. Places are keyed by the window's values and by the
// pattern of its locals, so only windows that are the same up to a consistent renaming meet, and up
// to candidates places are kept per key. The pattern costs a pass over the window, so it is computed
// only once a second window with the same values appears: until then the first waits in lone.
type cloneFinder struct {
	files  []*facts.File
	base   uint64
	lone   map[uint64]at
	places map[uint64][]at
	out    []clone
}

func (cf *cloneFinder) key(h uint64, o at) uint64 {
	return h ^ localsHash(&cf.files[o.file].Tokens, int(o.tok), cf.base)
}

// file finds the copies of earlier blocks in file fi.
func (cf *cloneFinder) file(fi int) {
	t := &cf.files[fi].Tokens
	hashes := windowHashes(t, MinCloneTokens, cf.base)
	covered := 0 // tokens before this are already in a reported copy
	// failed holds, for an alignment of this file with an earlier place (its file and the difference
	// of positions), how far a comparison that reported nothing reached: a later start on that
	// alignment is the same comparison.
	failed := map[[2]int]int{}
	for i := 0; i+MinCloneTokens <= len(t.Kind); i++ {
		if i > 0 && t.Line[i] == t.Line[i-1] {
			continue // blocks start at a line
		}
		cands, ok := cf.place(hashes[i], at{int32(fi), int32(i)})
		if ok && i >= covered {
			covered = cf.match(cands, fi, i, covered, failed)
		}
	}
}

// place records the window at here and returns the earlier places it may copy; ok is false for the
// first window with its values.
func (cf *cloneFinder) place(h uint64, here at) ([]at, bool) {
	switch o, ok := cf.lone[h]; {
	case !ok:
		cf.lone[h] = here
		return nil, false
	case o.file >= 0:
		cf.places[cf.key(h, o)] = []at{o}
		cf.lone[h] = at{-1, -1} // no longer alone
	}
	k := cf.key(h, here)
	cands := cf.places[k]
	if len(cands) < candidates {
		cf.places[k] = append(cands, here)
	}
	return cands, true
}

// match compares the block at token i of file fi with each candidate until one is a copy, and returns
// how far the reported copies reach.
func (cf *cloneFinder) match(cands []at, fi, i, covered int, failed map[[2]int]int) int {
	for _, o := range cands {
		align := [2]int{int(o.file), int(o.tok) - i}
		if failed[align] > i {
			continue
		}
		c, l, ok := compare(cf.files, int(o.file), int(o.tok), fi, i)
		if ok {
			for _, x := range c {
				covered = max(covered, x.to)
			}
			cf.out = append(cf.out, c...)
			return covered
		}
		failed[align] = i + max(l, 1)
	}
	return covered
}

// localsHash hashes the pattern of the locals in the MinCloneTokens tokens from i: each numbered by
// its first occurrence in the window, so two windows that rename consistently hash equal.
func localsHash(t *facts.Tokens, i int, base uint64) uint64 {
	var seen [MinCloneTokens]uint32
	n := 0
	h := uint64(0)
	for k := i; k < i+MinCloneTokens; k++ {
		name := t.Name[k]
		if name&nameLiteral != nameLocal {
			continue
		}
		id := 0
		for id < n && seen[id] != name {
			id++
		}
		if id == n && n < len(seen) {
			seen[n] = name
			n++
		}
		h = h*base + uint64(k-i)<<8 + uint64(id) + 1
	}
	return h * base
}

// compare extends the match of the block at i in file fi with the earlier one at oi in file of, and
// returns the blocks it reports, the length of the match, and whether there were any.
func compare(files []*facts.File, of, oi, fi, i int) ([]clone, int, bool) {
	t, ot := &files[fi].Tokens, &files[of].Tokens
	// The copies may not overlap: a block that repeats right after itself stops where the later
	// copy starts.
	limit := min(len(t.Kind)-i, len(ot.Kind)-oi)
	if of == fi {
		limit = min(limit, i-oi)
	}
	l := 0
	for l < limit && value(t, i+l) == value(ot, oi+l) {
		l++
	}
	if l < MinCloneTokens {
		return nil, l, false // a hash collision, or an overlap
	}
	var out []clone
	for _, r := range consistentRuns(ot, oi, t, i, l) {
		from, to := wholeLines(t, i+r[0], i+r[1])
		ofrom, oto := oi+from-i, oi+to-i
		if f2, t2 := wholeLines(ot, ofrom, oto); f2 != ofrom || t2 != oto {
			continue // the lines of the copies do not line up
		}
		if to-from >= MinCloneTokens && names(t.Name[from:to]) >= MinCloneNames &&
			distinctLines(t.Line[from:to]) >= MinCloneLines && distinctLines(ot.Line[ofrom:oto]) >= MinCloneLines {
			out = append(out, clone{file: fi, from: from, to: to, orig: of, ofrom: ofrom})
		}
	}
	return out, l, len(out) > 0
}

// wholeLines narrows a token range to the lines it holds in full.
func wholeLines(t *facts.Tokens, from, to int) (int, int) {
	for from < to && from > 0 && t.Line[from] == t.Line[from-1] {
		from++
	}
	for to > from && to < len(t.Line) && t.Line[to] == t.Line[to-1] {
		to--
	}
	return from, to
}

// windowHashes returns, for each position, a polynomial hash of the values of the w tokens from it.
func windowHashes(t *facts.Tokens, w int, base uint64) []uint64 {
	pow := uint64(1)
	for range w {
		pow *= base
	}
	hashes := make([]uint64, len(t.Kind)-w+1)
	var h uint64
	for i := range t.Kind {
		h = h*base + value(t, i)
		if i >= w {
			h -= pow * value(t, i-w)
		}
		if i >= w-1 {
			hashes[i-w+1] = h
		}
	}
	return hashes
}

// consistentRuns splits a stretch of l tokens with equal values, at a in x and b in y, into the runs
// in which the locals of one copy map one to one onto those of the other. Where a local would map to
// two names, a run ends and the next starts there, with a fresh mapping.
func consistentRuns(x *facts.Tokens, a int, y *facts.Tokens, b, l int) [][2]int {
	var runs [][2]int
	fwd, back := map[uint32]uint32{}, map[uint32]uint32{}
	start := 0
	for k := 0; k < l; k++ {
		p, q := x.Name[a+k], y.Name[b+k]
		if p&nameLiteral != nameLocal || q&nameLiteral != nameLocal {
			if p&nameLiteral == nameLocal || q&nameLiteral == nameLocal {
				// A local of one copy where the other has a fixed name.
				runs = append(runs, [2]int{start, k})
				clear(fwd)
				clear(back)
				start = k + 1
			}
			continue // equal fixed names, a literal, or no name
		}
		mp, okp := fwd[p]
		mq, okq := back[q]
		if okp && mp != q || okq && mq != p {
			runs = append(runs, [2]int{start, k})
			clear(fwd)
			clear(back)
			start = k
		}
		fwd[p], back[q] = q, p
	}
	return append(runs, [2]int{start, l})
}

// names counts the identifiers of a run of tokens.
func names(name []uint32) int {
	n := 0
	for _, v := range name {
		if v != 0 && v&nameLiteral != nameLiteral {
			n++
		}
	}
	return n
}

// distinctLines counts the lines of a run of tokens, which are in line order.
func distinctLines(lines []int32) int {
	n := 0
	for i, l := range lines {
		if i == 0 || l != lines[i-1] {
			n++
		}
	}
	return n
}

// MinIdenticalStatements is the size from which a function whose body repeats another's is a finding:
// two statements, as SonarSource's S4144 has it.
const MinIdenticalStatements = 2

type fnRef struct{ file, fn int }

// sameBodies groups the functions of files whose signatures and bodies are equal up to a consistent
// renaming of their own parameters and locals, and returns, for each function after the first of its
// group, its index and the first one's. A function needs a body of at least MinIdenticalStatements
// statements. platforms leaves out Go's functions written once per platform.
func sameBodies(files []*facts.File, platforms bool) [][2]fnRef {
	first := map[uint64][]fnRef{}
	var out [][2]fnRef
	for fi, f := range files {
		t := &f.Tokens
		for gi, fn := range f.Funcs {
			from, to := int(fn.Span[0]), int(fn.Span[1])
			if fn.Statements < MinIdenticalStatements || to <= from || to > len(t.Kind) {
				continue
			}
			h := spanHash(t, from, to)
			ref := fnRef{fi, gi}
			if o, ok := sameAs(files, first[h], f, fn, platforms); ok {
				out = append(out, [2]fnRef{ref, o})
			} else if len(first[h]) < candidates {
				first[h] = append(first[h], ref)
			}
		}
	}
	return out
}

// sameAs returns the first of cands whose function has fn's body.
func sameAs(files []*facts.File, cands []fnRef, f *facts.File, fn facts.Function, platforms bool) (fnRef, bool) {
	for _, o := range cands {
		of := files[o.file]
		if !(platforms && platformVariants(of, of.Funcs[o.fn], f, fn)) && equalSpans(&of.Tokens, of.Funcs[o.fn].Span, &f.Tokens, fn.Span) {
			return o, true
		}
	}
	return fnRef{}, false
}

// platformVariants reports two functions of one name, other than init and _, in one package. Go
// allows that only in files that never build together, under different build constraints (one for
// each platform), where repeating the body is how the code is meant to be written.
func platformVariants(f *facts.File, fn facts.Function, g *facts.File, gn facts.Function) bool {
	return f.Package == g.Package && fn.Name == gn.Name && f.Path != g.Path && fn.Name != "init" && fn.Name != "_"
}

// exclusiveFiles returns a test for two files of one package that declare a function of the same name
// (other than init and _): files that never build together, one for each platform.
func exclusiveFiles(files []*facts.File) func(a, b int) bool {
	memo := map[[2]int]bool{}
	return func(a, b int) bool {
		if a == b || files[a].Package != files[b].Package {
			return false
		}
		if a > b {
			a, b = b, a
		}
		key := [2]int{a, b}
		if v, ok := memo[key]; ok {
			return v
		}
		declared := map[string]bool{}
		for _, fn := range files[a].Funcs {
			declared[fn.Name] = true
		}
		v := false
		for _, fn := range files[b].Funcs {
			if declared[fn.Name] && fn.Name != "init" && fn.Name != "_" {
				v = true
				break
			}
		}
		memo[key] = v
		return v
	}
}

// spanHash hashes a function's span with each local numbered by its first occurrence, so that
// functions that rename consistently hash equal, and every other name and literal as it is.
func spanHash(t *facts.Tokens, from, to int) uint64 {
	const base = 1099511628211
	seen := map[uint32]uint64{}
	h := uint64(14695981039346656037)
	for k := from; k < to; k++ {
		v := uint64(t.Kind[k]) + 1
		if name := t.Name[k]; name&nameLiteral == nameLocal {
			id, ok := seen[name]
			if !ok {
				id = uint64(len(seen)) + 1
				seen[name] = id
			}
			v = 256 + id
		} else if name != 0 {
			v = 1<<33 + uint64(name) // a fixed name or a literal, as it is
		}
		h = h*base + v
	}
	return h
}

// equalSpans reports whether two functions have equal values, equal literals, and locals that map one
// to one.
func equalSpans(x *facts.Tokens, xs [2]int32, y *facts.Tokens, ys [2]int32) bool {
	a, b := int(xs[0]), int(ys[0])
	l := int(xs[1] - xs[0])
	if l != int(ys[1]-ys[0]) {
		return false
	}
	for k := range l {
		if value(x, a+k) != value(y, b+k) {
			return false
		}
		if p := x.Name[a+k]; p&nameLiteral == nameLiteral && p != y.Name[b+k] {
			return false // a function's literals are part of what it does
		}
	}
	runs := consistentRuns(x, a, y, b, l)
	return len(runs) == 1
}

// duplication runs both engines over the repository's own code that is not a test, each language on
// its own, and returns their findings and the share of lines in a repeated block, either copy, as
// SonarSource's duplicated_lines_density counts them: over all the lines of those files. Blocks
// between Go files written once per platform are neither findings nor counted.
func duplication(ctx context.Context, own []*facts.File) ([]report.Finding, report.Measurement, error) {
	byLang := map[string][]*facts.File{}
	lines, n, inputs := 0, 0, 0
	for _, f := range own {
		if f.TokensDropped {
			return nil, report.Null(report.TooLarge), nil
		}
		if f.Test {
			continue
		}
		byLang[f.Language] = append(byLang[f.Language], f)
		lines += f.Lines
		inputs++
	}
	var out []report.Finding
	for _, l := range slices.Sorted(maps.Keys(byLang)) {
		found, dup, err := duplicationIn(ctx, byLang[l], l == "go")
		if err != nil {
			return nil, report.Measurement{}, err
		}
		out = append(out, found...)
		n += dup
	}
	// The blocks are findings at any size; their share of the lines needs enough lines.
	if lines == 0 || lines < minCodeLines {
		return out, report.Null(report.InsufficientInput), nil
	}
	return out, report.Measured(float64(n)/float64(lines), inputs), nil
}

// duplicationIn finds the clones among files of one language and returns them with the number of
// lines in a repeated block. platforms says the files are Go, where two files of one package that
// declare one function are written once per platform.
func duplicationIn(ctx context.Context, files []*facts.File, platforms bool) ([]report.Finding, int, error) {
	found, err := clones(ctx, files)
	if err != nil {
		return nil, 0, err
	}
	var out []report.Finding
	same := sameBodies(files, platforms)
	// A block inside two functions that are reported as the same is not reported again.
	pair := map[[2]fnRef]bool{}
	for _, p := range same {
		pair[p] = true
		f, o := files[p[0].file], files[p[1].file]
		fn, ofn := f.Funcs[p[0].fn], o.Funcs[p[1].fn]
		out = append(out, report.Finding{RuleID: IdenticalFunctions, Path: f.Path, Line: fn.Line,
			Message: fmt.Sprintf("%s has the signature and body of %s at %s:%d, up to the names of its parameters and locals",
				fn.Name, ofn.Name, o.Path, ofn.Line)})
	}
	excl := func(a, b int) bool { return false }
	if platforms {
		excl = exclusiveFiles(files)
	}
	dup := make([]map[int32]bool, len(files))
	mark := func(fi int, t *facts.Tokens, from, to int) {
		if dup[fi] == nil {
			dup[fi] = map[int32]bool{}
		}
		for _, l := range t.Line[from:to] {
			dup[fi][l] = true
		}
	}
	for _, c := range found {
		f, o := files[c.file], files[c.orig]
		t, ot := &f.Tokens, &o.Tokens
		oto := c.ofrom + c.to - c.from
		if excl(c.file, c.orig) {
			continue // files written once per platform
		}
		in, oin := enclosing(f, c.file, t.Line[c.from], t.Line[c.to-1]), enclosing(o, c.orig, ot.Line[c.ofrom], ot.Line[oto-1])
		mark(c.file, t, c.from, c.to)
		mark(c.orig, ot, c.ofrom, oto)
		if pair[[2]fnRef{in, oin}] {
			continue
		}
		where := o.Path
		if c.orig == c.file {
			where = "this file"
		}
		out = append(out, report.Finding{RuleID: DuplicatedBlock, Path: f.Path, Line: int(t.Line[c.from]),
			Message: fmt.Sprintf("lines %d-%d repeat lines %d-%d of %s (%d tokens)",
				t.Line[c.from], t.Line[c.to-1], ot.Line[c.ofrom], ot.Line[oto-1], where, c.to-c.from)})
	}
	n := 0
	for _, d := range dup {
		n += len(d)
	}
	return out, n, nil
}

// enclosing returns the function of f whose declaration spans the lines from to to, or {-1, -1}. The
// functions are in source order and do not nest.
func enclosing(f *facts.File, fi int, from, to int32) fnRef {
	i := sort.Search(len(f.Funcs), func(i int) bool { return int32(f.Funcs[i].Line) > from }) - 1
	if i >= 0 && to <= int32(f.Funcs[i].EndLine) {
		return fnRef{fi, i}
	}
	return fnRef{-1, -1}
}
