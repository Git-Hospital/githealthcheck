package complexity

import (
	"context"
	"fmt"
	"slices"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

const (
	// MinCloneTokens and MinCloneLines are the size from which a repeated block is a finding:
	// SonarSource's, "at least 100 successive and duplicated tokens" spanning at least 10 lines
	// (D34).
	MinCloneTokens = 100
	MinCloneLines  = 10
	// MaxDupTokens bounds the tokens duplication is looked for in, about two and a half million
	// lines of Go; a larger repository gets no duplication result.
	MaxDupTokens = 20_000_000
	// MinCloneNames is the number of identifiers a block needs: one with fewer is a table of
	// literals, data rather than code.
	MinCloneNames = MinCloneTokens / 10
)

// The flags of facts.Tokens.Name.
const (
	nameLocal   = 1 << 30
	nameFixed   = 1 << 31
	nameLiteral = nameLocal | nameFixed
)

// dupLimit is MaxDupTokens, lowered by tests.
var dupLimit = MaxDupTokens

// clone is one repeated block: the later copy, and the earlier one it repeats.
type clone struct {
	file, from, to int // the later copy: a file of the input and a token range
	orig, ofrom    int // the earlier copy
}

// clones finds the blocks of at least MinCloneTokens tokens over at least MinCloneLines lines that
// repeat an earlier block, up to a consistent renaming of the functions' own parameters and locals
// and any change of literals (a Type-2 clone in the terms of Roy, Cordy and Koschke 2009, checked with
// Baker's parameterised match); other names must be the same. Blocks start at the first token of a line. Each block is reported once, against the first
// place its first MinCloneTokens tokens occur, in the order of files.
func clones(ctx context.Context, files []*facts.File) ([]clone, error) {
	type at struct{ file, tok int32 }
	first := map[uint64]at{}
	var out []clone
	for fi, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t := &f.Tokens
		n := len(t.Kind)
		if n < MinCloneTokens {
			continue
		}
		hashes := windowHashes(t.Kind, MinCloneTokens)
		covered := 0 // tokens before this are already in a reported copy
		for i := 0; i+MinCloneTokens <= n; i++ {
			if i > 0 && t.Line[i] == t.Line[i-1] {
				continue // blocks start at a line
			}
			h := hashes[i]
			o, seen := first[h]
			if !seen {
				first[h] = at{int32(fi), int32(i)}
				continue
			}
			if i < covered {
				continue
			}
			of := &files[o.file].Tokens
			oi := int(o.tok)
			// The copies may not overlap: a block that repeats right after itself stops where the
			// later copy starts.
			limit := min(n-i, len(of.Kind)-oi)
			if int(o.file) == fi {
				limit = min(limit, i-oi)
			}
			l := 0
			for l < limit && t.Kind[i+l] == of.Kind[oi+l] {
				l++
			}
			if l < MinCloneTokens {
				continue // a hash collision, or an overlap
			}
			covered = i + l
			for _, r := range consistentRuns(of, oi, t, i, l) {
				if r[1]-r[0] >= MinCloneTokens && names(t.Name[i+r[0]:i+r[1]]) >= MinCloneNames &&
					distinctLines(t.Line[i+r[0]:i+r[1]]) >= MinCloneLines &&
					distinctLines(of.Line[oi+r[0]:oi+r[1]]) >= MinCloneLines {
					out = append(out, clone{file: fi, from: i + r[0], to: i + r[1], orig: int(o.file), ofrom: oi + r[0]})
				}
			}
		}
	}
	return out, nil
}

// windowHashes returns, for each position, a polynomial hash of the w kinds from it.
func windowHashes(kind []uint8, w int) []uint64 {
	const base = 1099511628211
	pow := uint64(1)
	for range w {
		pow *= base
	}
	hashes := make([]uint64, len(kind)-w+1)
	var h uint64
	for i, k := range kind {
		h = h*base + uint64(k) + 1
		if i >= w {
			h -= pow * (uint64(kind[i-w]) + 1)
		}
		if i >= w-1 {
			hashes[i-w+1] = h
		}
	}
	return hashes
}

// consistentRuns splits a stretch of l tokens with equal kinds, at a in x and b in y, into the runs in
// which the identifiers of one copy map one to one onto those of the other. Where a name would map to
// two names, a run ends and the next starts there.
func consistentRuns(x *facts.Tokens, a int, y *facts.Tokens, b, l int) [][2]int {
	var runs [][2]int
	fwd, back := map[uint32]uint32{}, map[uint32]uint32{}
	start := 0
	for k := 0; k < l; k++ {
		p, q := x.Name[a+k], y.Name[b+k]
		if p == 0 || p&nameLiteral == nameLiteral {
			continue // a literal may change
		}
		mp, okp := fwd[p]
		mq, okq := back[q]
		// A fixed name (a function, field, type or constant) must be kept; a local may be renamed,
		// consistently.
		fixed := p&nameFixed != 0 || q&nameFixed != 0
		if fixed && p != q || !fixed && (okp && mp != q || okq && mq != p) {
			runs = append(runs, [2]int{start, k})
			clear(fwd)
			clear(back)
			start = k
		}
		if !fixed {
			fwd[p], back[q] = q, p
		}
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

// sameBodies groups the functions of files whose bodies are equal up to a consistent renaming of
// their own parameters and locals, and returns, for each function after the first of its
// group, its index and the first one's. A function needs a body of at least MinIdenticalStatements
// statements.
func sameBodies(files []*facts.File) [][2]fnRef {
	first := map[uint64]fnRef{}
	var out [][2]fnRef
	for fi, f := range files {
		t := &f.Tokens
		for gi, fn := range f.Funcs {
			from, to := int(fn.Body[0]), int(fn.Body[1])
			if fn.Statements < MinIdenticalStatements || to <= from || to > len(t.Kind) {
				continue
			}
			h := bodyHash(t, from, to)
			ref := fnRef{fi, gi}
			o, seen := first[h]
			if !seen {
				first[h] = ref
				continue
			}
			if platformVariants(files[o.file], files[o.file].Funcs[o.fn], f, fn) {
				continue
			}
			if equalBodies(&files[o.file].Tokens, files[o.file].Funcs[o.fn].Body, t, fn.Body) {
				out = append(out, [2]fnRef{ref, o})
			}
		}
	}
	return out
}

type fnRef struct{ file, fn int }

// platformVariants reports two functions of one name in one package. Go allows that only in files that
// never build together, under different build constraints (one for each platform), where repeating
// the body is how the code is meant to be written.
func platformVariants(f *facts.File, fn facts.Function, g *facts.File, gn facts.Function) bool {
	return f.Package == g.Package && fn.Name == gn.Name && f.Path != g.Path
}

// bodyHash hashes a body with each local name numbered by its first occurrence, so that bodies that
// rename consistently hash equal, and every other name as it is.
func bodyHash(t *facts.Tokens, from, to int) uint64 {
	const base = 1099511628211
	seen := map[uint32]uint64{}
	h := uint64(14695981039346656037)
	for k := from; k < to; k++ {
		v := uint64(t.Kind[k]) + 1
		if name := t.Name[k]; name&nameFixed != 0 {
			v = 1<<33 + uint64(name) // a fixed name or a literal, as it is
		} else if name != 0 {
			id, ok := seen[name]
			if !ok {
				id = uint64(len(seen)) + 1
				seen[name] = id
			}
			v = 256 + id
		}
		h = h*base + v
	}
	return h
}

// equalBodies reports whether two bodies have equal kinds and identifiers that map one to one.
func equalBodies(x *facts.Tokens, xb [2]int32, y *facts.Tokens, yb [2]int32) bool {
	a, b := int(xb[0]), int(yb[0])
	l := int(xb[1] - xb[0])
	if l != int(yb[1]-yb[0]) || !slices.Equal(x.Kind[a:a+l], y.Kind[b:b+l]) {
		return false
	}
	for k := range l {
		if p := x.Name[a+k]; p&nameLiteral == nameLiteral && p != y.Name[b+k] {
			return false // a function's literals are part of what it does
		}
	}
	return len(consistentRuns(x, a, y, b, l)) == 1
}

// duplication runs both engines over the repository's own code that is not a test, and returns their
// findings and the share of lines in a repeated block, either copy, as SonarSource's
// duplicated_lines_density counts them: over all the lines of those files.
func duplication(ctx context.Context, own []*facts.File) ([]report.Finding, report.Measurement, error) {
	var files []*facts.File
	total, lines := 0, 0
	for _, f := range own {
		if f.Test {
			continue
		}
		files = append(files, f)
		total += len(f.Tokens.Kind)
		lines += f.Lines
	}
	if lines == 0 {
		return nil, report.Null(report.InsufficientInput), nil
	}
	if total > dupLimit {
		return nil, report.Null(report.TooLarge), nil
	}
	found, err := clones(ctx, files)
	if err != nil {
		return nil, report.Measurement{}, err
	}
	var out []report.Finding
	same := sameBodies(files)
	// A block inside two functions that are reported as the same is not reported again.
	pair := map[[2]fnRef]bool{}
	for _, p := range same {
		pair[p] = true
		f, o := files[p[0].file], files[p[1].file]
		fn, ofn := f.Funcs[p[0].fn], o.Funcs[p[1].fn]
		out = append(out, report.Finding{RuleID: IdenticalFunctions, Path: f.Path, Line: fn.Line,
			Message: fmt.Sprintf("%s has the body of %s at %s:%d, up to the names of its locals", fn.Name, ofn.Name, o.Path, ofn.Line)})
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
		mark(c.file, t, c.from, c.to)
		mark(c.orig, ot, c.ofrom, oto)
		in, oin := enclosing(f, c.file, t.Line[c.from], t.Line[c.to-1]), enclosing(o, c.orig, ot.Line[c.ofrom], ot.Line[oto-1])
		if pair[[2]fnRef{in, oin}] || in.fn >= 0 && oin.fn >= 0 && platformVariants(f, f.Funcs[in.fn], o, o.Funcs[oin.fn]) {
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
	return out, report.Measured(float64(n)/float64(lines), len(files)), nil
}

// enclosing returns the function of f whose declaration spans the lines from to to, or {-1, -1}.
func enclosing(f *facts.File, fi int, from, to int32) fnRef {
	for gi, fn := range f.Funcs {
		if int32(fn.Line) <= from && to <= int32(fn.EndLine) {
			return fnRef{fi, gi}
		}
	}
	return fnRef{-1, -1}
}
