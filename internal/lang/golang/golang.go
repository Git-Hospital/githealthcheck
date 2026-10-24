// Package golang extracts the facts of Go source files with the standard go/parser.
package golang

//go:generate go run gen_stdlib.go

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"go/version"
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
	fset := token.NewFileSet()
	// A file that does not parse is measured as far as the parser got.
	file, err := parser.ParseFile(fset, f.Path, src, parser.SkipObjectResolution|parser.ParseComments)
	f.Partial = err != nil
	if file != nil {
		code := codeLines(src)
		f.Funcs = measure(fset, file, code)
		f.Imports = imports(fset, file)
		f.Smells, f.SmellsDropped = smells(fset, file, code)
		f.Refs = refs(file)
	}
	f.Functions = len(f.Funcs)
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

// ModuleRequires returns the module paths a go.mod requires or replaces, in file order: the first
// field of each require line, and the left side of each replace, in single lines and in blocks.
func ModuleRequires(gomod []byte) []string {
	var out []string
	block := ""
	for line := range strings.SplitSeq(string(gomod), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		verb := block
		switch {
		case block != "" && fields[0] == ")":
			block = ""
			continue
		case block == "" && len(fields) == 2 && fields[1] == "(":
			block = fields[0]
			continue
		case block == "" && len(fields) == 1 && strings.HasSuffix(fields[0], "("):
			block = strings.TrimSuffix(fields[0], "(") // require( as well as require (
			continue
		case block == "":
			verb, fields = fields[0], fields[1:]
		}
		if (verb == "require" || verb == "replace") && len(fields) > 0 {
			p := fields[0]
			if u, err := strconv.Unquote(p); err == nil {
				p = u
			}
			out = append(out, p)
		}
	}
	return out
}

// Std reports whether an import path is in the standard library of the toolchain stdlib.go was
// generated with.
func Std(path string) bool { return stdlib[path] }

// ModuleGo returns the Go version a go.mod declares with its go directive, such as "go1.22", or "".
func ModuleGo(gomod []byte) string {
	for line := range strings.SplitSeq(string(gomod), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "go" {
			if v := "go" + f[1]; version.IsValid(v) {
				return v
			}
		}
	}
	return ""
}

// NewerStd reports whether a module that declares Go version v may import standard packages newer
// than the list in stdlib.go.
func NewerStd(v string) bool {
	return v != "" && version.Compare(version.Lang(v), version.Lang(stdlibVersion)) > 0
}
