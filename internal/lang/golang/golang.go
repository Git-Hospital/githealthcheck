// Package golang extracts the facts of Go source files with the standard go/parser.
package golang

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// Support is the Go language support.
type Support struct{}

func (Support) Name() string { return "go" }

// Fill sets the Go facts of f from its source.
func (Support) Fill(f *facts.File, src []byte) {
	f.Test = strings.HasSuffix(f.Path, "_test.go")
	f.Package = Package(f.Path)
	f.Generated = generated(src)
	f.Functions = functions(f.Path, src)
}

// Package returns the directory of a Go file, or "" when the go command ignores the file: anything
// under a testdata directory, and any file or directory whose name starts with _ or .
// (https://pkg.go.dev/cmd/go#hdr-Package_lists_and_patterns).
func Package(rel string) string {
	for part := range strings.SplitSeq(rel, "/") {
		if part == "testdata" || strings.HasPrefix(part, "_") || strings.HasPrefix(part, ".") {
			return ""
		}
	}
	return path.Dir(rel)
}

// functions counts the function and method declarations with a body. A file that does not parse is
// counted as far as the parser got.
func functions(name string, src []byte) int {
	file, _ := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if file == nil {
		return 0
	}
	n := 0
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			n++
		}
	}
	return n
}

// goGenerated is the convention from https://go.dev/s/generatedcode.
var goGenerated = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// generated reports whether a file marks itself as generated before its package clause.
func generated(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := sc.Text()
		if goGenerated.MatchString(line) {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}

// ModulePath returns the module path a go.mod file declares, or "" when it declares none that can be
// read: no module line, a module block, or a malformed quoted path.
func ModulePath(gomod []byte) string {
	for line := range strings.SplitSeq(string(gomod), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		p := fields[1]
		if strings.HasPrefix(p, `"`) || strings.HasPrefix(p, "`") {
			var err error
			if p, err = strconv.Unquote(p); err != nil {
				return ""
			}
		}
		if p == "(" {
			return ""
		}
		return p
	}
	return ""
}
