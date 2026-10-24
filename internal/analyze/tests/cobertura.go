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

// Bounds far above anything coverage.py writes, whose elements nest six deep.
const (
	maxSources = 64
	maxLine    = 1 << 24
	maxDepth   = 32
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
	seen := map[string]bool{}
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
			case name == "class":
				file := attr(t, "filename")
				if file == "" {
					return nil, fmt.Errorf("line %d: class without a filename", lineOf(d))
				}
				rel, ok := find(file, sources, resolve)
				if !seen[file] {
					seen[file] = true
					p.files++
					p.inModules++
				}
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
			if len(stack) > 0 && stack[len(stack)-1] == "source" {
				if text.Len()+len(t) > maxLineBytes {
					return nil, fmt.Errorf("line %d: source longer than %d bytes", lineOf(d), maxLineBytes)
				}
				text.Write(t)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				if stack[len(stack)-1] == "source" {
					if len(sources) == maxSources {
						return nil, fmt.Errorf("more than %d sources", maxSources)
					}
					sources = append(sources, strings.TrimSpace(text.String()))
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

// find resolves a file of the report to a path in the commit. The file joined with a source is a path
// on the machine that ran the tests, which ends with the repository path: of its suffixes, the longest
// that is in the commit is taken, trying each source in turn, then the file on its own.
func find(file string, sources []string, resolve pathResolver) (string, bool) {
	file = strings.ReplaceAll(file, `\`, "/")
	for _, s := range append(sources, "") {
		full := file
		if !path.IsAbs(file) && !isDrive(file) {
			full = path.Join(strings.ReplaceAll(s, `\`, "/"), file)
		}
		parts := strings.Split(strings.TrimPrefix(path.Clean("/"+full), "/"), "/")
		if len(parts) > maxPathSegments {
			continue
		}
		for i := range parts {
			cand := strings.Join(parts[i:], "/")
			if cand == "" || cand == "." || strings.HasPrefix(cand, "../") {
				continue
			}
			if rel, ok := resolve(cand); ok {
				return rel, true
			}
		}
	}
	return "", false
}

// isDrive reports a Windows path such as C:/work/x.py.
func isDrive(p string) bool {
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' && (p[0]|0x20) >= 'a' && (p[0]|0x20) <= 'z'
}
