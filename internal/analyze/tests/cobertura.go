package tests

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
	maxSources   = 16
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
	p := &profile{mode: "set", blocks: map[string]map[blockKey]*block{}}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	var sources []string
	var stack []string
	var text strings.Builder
	var lines map[blockKey]*block // the lines of the current class, nil when its file is not in the commit
	type found struct {
		rel string
		ok  bool
	}
	seen := map[string]found{} // each file of the report, as it resolved
	budget := maxLookups
	inSource := false
	sawRoot := false
	for n := 0; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, name)
			if len(stack) > maxDepth {
				return nil, fmt.Errorf("line %d: elements nested more than %d deep", lineOf(d), maxDepth)
			}
			switch {
			case len(stack) == 1:
				if name != "coverage" {
					return nil, fmt.Errorf("root element %q, not coverage", name)
				}
				sawRoot = true
			case name == "source" && parent == "sources":
				text.Reset()
				inSource = true
			case name == "class":
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				file := strings.ReplaceAll(attr(t, "filename"), `\`, "/")
				if file != "" {
					file = path.Clean(file) // one file however it is written
				}
				if file == "" || len(file) > maxNameBytes {
					return nil, fmt.Errorf("line %d: class without a filename, or one longer than %d bytes", lineOf(d), maxNameBytes)
				}
				r, known := seen[file]
				if !known {
					r.rel, r.ok = find(file, sources, resolve, &budget)
					if budget < 0 {
						return nil, fmt.Errorf("line %d: the report's files cost more than %d lookups to map", lineOf(d), maxLookups)
					}
					seen[file] = r
					p.files++
					p.inModules++
				}
				rel, ok := r.rel, r.ok
				lines = nil
				if ok {
					if lines = p.blocks[rel]; lines == nil {
						lines = map[blockKey]*block{}
						p.blocks[rel] = lines
					}
				}
			case name == "line" && parent == "lines" && len(stack) >= 3 && stack[len(stack)-3] == "class":
				if lines == nil {
					continue
				}
				num, err1 := strconv.Atoi(attr(t, "number"))
				hits, err2 := strconv.Atoi(attr(t, "hits"))
				if err1 != nil || err2 != nil || num < 1 || num > maxLine || hits < 0 {
					return nil, fmt.Errorf("line %d: bad line element", lineOf(d))
				}
				k := blockKey{num, 0, num, 0}
				if b := lines[k]; b != nil {
					b.covered = b.covered || hits > 0
				} else {
					lines[k] = &block{stmts: 1, covered: hits > 0}
				}
			}
		case xml.CharData:
			if inSource {
				if text.Len()+len(t) > maxNameBytes {
					return nil, fmt.Errorf("line %d: source longer than %d bytes", lineOf(d), maxNameBytes)
				}
				text.Write(t)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				if inSource && stack[len(stack)-1] == "source" {
					inSource = false
					if len(sources) == maxSources {
						return nil, fmt.Errorf("more than %d sources", maxSources)
					}
					sources = append(sources, strings.ReplaceAll(strings.TrimSpace(text.String()), `\`, "/"))
				}
				if stack[len(stack)-1] == "class" {
					lines = nil
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !sawRoot {
		return nil, fmt.Errorf("no coverage element")
	}
	return p, nil
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
// An absolute name is its own source and keeps at least its base name. Of the candidates in the
// commit, over every source, the longest is taken. The name's own parts are never dropped, so a file
// missing from the commit does not land on another file with the same tail. Each lookup costs one of
// budget.
func find(file string, sources []string, resolve pathResolver, budget *int) (string, bool) {
	type base struct {
		full string
		keep int // the parts at the end that must stay
	}
	var bases []base
	if path.IsAbs(file) || isDrive(file) {
		bases = []base{{file, 1}}
	} else {
		keep := 0
		for p := range strings.SplitSeq(path.Clean(file), "/") {
			if p != ".." && p != "." {
				keep++
			}
		}
		for _, s := range sources {
			bases = append(bases, base{path.Join(s, file), keep})
		}
		bases = append(bases, base{file, keep})
	}
	best := ""
	for _, b := range bases {
		full := strings.TrimPrefix(path.Clean("/"+b.full), "/")
		parts := strings.Count(full, "/") + 1
		if b.keep == 0 || full == "" || parts > maxPathSegments {
			continue
		}
		// Suffixes from the longest: full itself, then past each slash, while the name stays whole.
		for i, drop := 0, 0; drop <= parts-b.keep; drop++ {
			cand := full[i:]
			if len(cand) <= len(best) {
				break // the rest are shorter than what was found
			}
			if *budget--; *budget < 0 {
				return "", false
			}
			if cand != "." && !strings.HasPrefix(cand, "../") {
				if rel, ok := resolve(cand); ok {
					best = rel
					break
				}
			}
			next := strings.IndexByte(full[i:], '/')
			if next < 0 {
				break
			}
			i += next + 1
		}
	}
	return best, best != ""
}

// isDrive reports a Windows path such as C:/work/x.py.
func isDrive(p string) bool {
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z'
}
