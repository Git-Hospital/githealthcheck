package tests

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"strconv"
	"strings"
)

// A Cobertura XML report, as coverage.py writes it (coverage xml):
//
//	<coverage ...>
//	  <sources><source>/home/ci/work/project/src</source></sources>
//	  <packages><package name="pkg"><classes>
//	    <class name="mod.py" filename="pkg/mod.py" ...>
//	      <methods/>
//	      <lines><line number="3" hits="1"/><line number="4" hits="0"/></lines>
//	    </class>
//	  </classes></package></packages>
//	</coverage>
//
// Each line element is one line with a statement and how often it ran; a file name is relative to
// one of the sources, which are absolute paths on the machine that ran the tests. Other Cobertura
// writers also list each method's lines under methods, which repeat the class's.

// Bounds far above anything coverage.py writes, whose elements nest six deep. maxLookups bounds the
// work of mapping the report's files to the commit: a few per file in a real report.
const (
	maxSources   = 64
	maxNameBytes = 1024
	maxLine      = 1 << 24
	maxDepth     = 32
	maxLookups   = 1 << 21
)

// isCobertura reports whether a coverage report is XML rather than a Go profile.
func isCobertura(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	return bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("<"))
}

// pathResolver maps a file of a report, joined with one of its sources, to its repository path, and
// reports whether the file is in the commit.
type pathResolver func(full string) (rel string, atHead bool)

// parseCobertura parses a Cobertura report into a profile of one block per line. Every file it lists
// counts as the repository's own: unlike a Go profile, it has no module path to tell the
// repository's code from the rest.
func parseCobertura(ctx context.Context, data []byte, resolve pathResolver) (*profile, error) {
	c := &cobertura{
		ctx:     ctx,
		d:       xml.NewDecoder(bytes.NewReader(data)),
		resolve: resolve,
		p:       &profile{mode: "set", blocks: map[string]map[blockKey]*block{}},
		names:   map[string]bool{},
		seen:    map[string]found{},
		budget:  maxLookups,
	}
	c.d.Strict = true
	for n := 0; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tok, err := c.d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			err = c.start(t)
		case xml.CharData:
			err = c.charData(t)
		case xml.EndElement:
			err = c.end()
		}
		if err != nil {
			return nil, err
		}
	}
	if !c.sawRoot {
		return nil, fmt.Errorf("no coverage element")
	}
	return c.finish(), nil
}

// found is how a file name of the report resolved, with the sources read so far.
type found struct {
	rel string
	ok  bool
}

// cobertura is the state of parseCobertura while it reads the report's elements.
type cobertura struct {
	ctx     context.Context
	d       *xml.Decoder
	resolve pathResolver
	p       *profile
	sources []string
	stack   []string
	text    strings.Builder
	lines   map[blockKey]*block // the lines of the current class, nil when its file is not in the commit
	names   map[string]bool     // each file name of the report
	seen    map[string]found
	budget  int
	// inSource is true inside coverage/sources/source; sawRoot once the root element was read.
	inSource, sawRoot bool
}

func (c *cobertura) start(t xml.StartElement) error {
	name := t.Name.Local
	parent := ""
	if len(c.stack) > 0 {
		parent = c.stack[len(c.stack)-1]
	}
	c.stack = append(c.stack, name)
	depth := len(c.stack)
	switch {
	case depth > maxDepth:
		return fmt.Errorf("line %d: elements nested more than %d deep", lineOf(c.d), maxDepth)
	case depth == 1:
		if name != "coverage" {
			return fmt.Errorf("root element %q, not coverage", name)
		}
		c.sawRoot = true
	case name == "source" && depth == 3 && c.stack[1] == "sources":
		// Only coverage/sources/source: a sources element elsewhere names nothing.
		c.text.Reset()
		c.inSource = true
	case name == "class":
		return c.class(t)
	case name == "line" && parent == "lines" && depth >= 3 && c.stack[depth-3] == "class":
		return c.line(t)
	}
	return nil
}

// class starts a class: its file is resolved once per name, and its lines go to that file's blocks.
func (c *cobertura) class(t xml.StartElement) error {
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	file := strings.ReplaceAll(attr(t, "filename"), `\`, "/")
	if file != "" {
		file = path.Clean(file) // one file however it is written
	}
	if file == "" || len(file) > maxNameBytes {
		return fmt.Errorf("line %d: class without a filename, or one longer than %d bytes", lineOf(c.d), maxNameBytes)
	}
	r, known := c.seen[file]
	if !known {
		r.rel, r.ok = find(file, c.sources, c.resolve, &c.budget)
		if c.budget < 0 {
			return fmt.Errorf("line %d: the report's files cost more than %d lookups to map", lineOf(c.d), maxLookups)
		}
		c.seen[file] = r
		c.names[file] = true
	}
	c.lines = nil
	if r.ok {
		if c.lines = c.p.blocks[r.rel]; c.lines == nil {
			c.lines = map[blockKey]*block{}
			c.p.blocks[r.rel] = c.lines
		}
	}
	return nil
}

func (c *cobertura) line(t xml.StartElement) error {
	if c.lines == nil {
		return nil
	}
	num, err1 := strconv.Atoi(attr(t, "number"))
	hits, err2 := strconv.Atoi(attr(t, "hits"))
	if err1 != nil || err2 != nil || num < 1 || num > maxLine || hits < 0 {
		return fmt.Errorf("line %d: bad line element", lineOf(c.d))
	}
	k := blockKey{num, 0, num, 0}
	if b := c.lines[k]; b != nil {
		b.covered = b.covered || hits > 0
	} else {
		c.lines[k] = &block{stmts: 1, covered: hits > 0}
	}
	return nil
}

func (c *cobertura) charData(t xml.CharData) error {
	if !c.inSource || len(c.stack) != 3 {
		return nil
	}
	if c.text.Len()+len(t) > maxNameBytes {
		return fmt.Errorf("line %d: source longer than %d bytes", lineOf(c.d), maxNameBytes)
	}
	c.text.Write(t)
	return nil
}

func (c *cobertura) end() error {
	if len(c.stack) == 0 {
		return nil
	}
	if c.inSource && len(c.stack) == 3 {
		c.inSource = false
		if len(c.sources) == maxSources {
			return fmt.Errorf("more than %d sources", maxSources)
		}
		c.sources = append(c.sources, strings.ReplaceAll(strings.TrimSpace(c.text.String()), `\`, "/"))
		// A name that was not found is tried again with this source.
		maps.DeleteFunc(c.seen, func(_ string, r found) bool { return !r.ok })
	}
	if c.stack[len(c.stack)-1] == "class" {
		c.lines = nil
	}
	c.stack = c.stack[:len(c.stack)-1]
	return nil
}

// finish counts the files. One file written two ways is one file: the files that count are those
// found, each once, and the names that were not.
func (c *cobertura) finish() *profile {
	c.p.files = len(c.names)
	c.p.inModules = len(c.p.blocks)
	for n := range c.names {
		if !c.seen[n].ok {
			c.p.inModules++
		}
	}
	return c.p
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func lineOf(d *xml.Decoder) int {
	line, _ := d.InputPos()
	return line
}

// find resolves a file of the report to a path in the commit. A relative file name is relative to one
// of the sources, absolute paths on the machine that ran the tests: the source joined with the name
// ends with the repository path, and each of its suffixes that keeps the name whole is a candidate.
// An absolute name under a source is that source and a relative name; one under none keeps its last
// two parts (its directory and base name). Of the candidates in the commit, over every source, the
// longest is taken. The name's own parts are never dropped, so a file missing from the commit does
// not land on another file with the same tail. Each lookup costs one of budget.
func find(file string, sources []string, resolve pathResolver, budget *int) (string, bool) {
	best := ""
	for _, b := range findBases(file, sources) {
		if rel, ok := b.longest(len(best), resolve, budget); ok {
			best = rel
		}
		if *budget < 0 {
			return "", false
		}
	}
	return best, best != ""
}

// findBase is a path the repository path may be a suffix of, and how many of its parts at the end
// must stay.
type findBase struct {
	full string
	keep int
}

func findBases(file string, sources []string) []findBase {
	if !path.IsAbs(file) && !isDrive(file) {
		keep := 0
		for p := range strings.SplitSeq(path.Clean(file), "/") {
			if p != ".." && p != "." {
				keep++
			}
		}
		var bases []findBase
		for _, s := range sources {
			bases = append(bases, findBase{path.Join(s, file), keep})
		}
		return append(bases, findBase{file, keep})
	}
	var bases []findBase
	for _, s := range sources {
		if rest, ok := strings.CutPrefix(file, strings.TrimSuffix(s, "/")+"/"); ok && s != "" {
			bases = append(bases, findBase{file, len(strings.Split(path.Clean(rest), "/"))})
		}
	}
	if bases == nil {
		bases = []findBase{{file, min(2, strings.Count(strings.Trim(file, "/"), "/")+1)}}
	}
	return bases
}

// longest tries b's suffixes from the longest, full itself and then past each slash, while the kept
// parts stay whole and the suffix is longer than best, the length of the best match so far. It
// returns the first one in the commit.
func (b findBase) longest(best int, resolve pathResolver, budget *int) (string, bool) {
	full := strings.TrimPrefix(path.Clean("/"+b.full), "/")
	parts := strings.Count(full, "/") + 1
	if b.keep == 0 || full == "" || parts > maxPathSegments {
		return "", false
	}
	for i, drop := 0, 0; drop <= parts-b.keep; drop++ {
		cand := full[i:]
		if len(cand) <= best {
			return "", false // the rest are shorter than what was found
		}
		if *budget--; *budget < 0 {
			return "", false
		}
		if cand != "." && !strings.HasPrefix(cand, "../") {
			if rel, ok := resolve(cand); ok {
				return rel, true
			}
		}
		next := strings.IndexByte(cand, '/')
		if next < 0 {
			return "", false
		}
		i += next + 1
	}
	return "", false
}

// isDrive reports a Windows path such as C:/work/x.py.
func isDrive(p string) bool {
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z'
}
