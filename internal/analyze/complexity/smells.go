package complexity

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// smellFindings turns the D32 patterns of the repository's own Go files into findings: test files
// included, since generated tests drop errors and invent imports as generated code does.
func smellFindings(rf *facts.Repo, own []*facts.File) []report.Finding {
	var out []report.Finding
	add := func(rule string, f *facts.File, line int, msg string) {
		out = append(out, report.Finding{RuleID: rule, Path: f.Path, Line: line, Message: msg})
	}
	for _, f := range own {
		for _, s := range f.Smells {
			switch s.Kind {
			case facts.EmptyErrorCheck:
				add(SwallowedError, f, s.Line, fmt.Sprintf("%s checks an error and does nothing with it", s.Func))
			case facts.ErrorDropped:
				add(SwallowedError, f, s.Line, fmt.Sprintf("%s checks an error and returns nil in its place", s.Func))
			case facts.Placeholder:
				add(Placeholder, f, s.Line, fmt.Sprintf("%s only says it is not implemented", s.Func))
			case facts.CommentedCode:
				if s.EndLine > s.Line {
					add(CommentedCode, f, s.Line, fmt.Sprintf("lines %d-%d are commented-out code", s.Line, s.EndLine))
				} else {
					add(CommentedCode, f, s.Line, fmt.Sprintf("line %d is commented-out code", s.Line))
				}
			}
		}
		if m := moduleOf(rf.Modules, f.Path); m != nil {
			for _, im := range f.Imports {
				if !resolves(rf.Modules, m, im.Path) {
					add(UnresolvedImport, f, im.Line, fmt.Sprintf("imports %q, which is not in the standard library, this repository or the requirements of %s",
						im.Path, gomod(m)))
				}
			}
		}
	}
	return append(out, unused(rf)...)
}

func gomod(m *facts.Module) string {
	if m.Dir == "." {
		return "go.mod"
	}
	return m.Dir + "/go.mod"
}

// moduleOf returns the module whose directory holds path most closely, or nil.
func moduleOf(mods []facts.Module, path string) *facts.Module {
	var best *facts.Module
	for i := range mods {
		m := &mods[i]
		if m.Dir != "." && !strings.HasPrefix(path, m.Dir+"/") {
			continue
		}
		if best == nil || len(m.Dir) > len(best.Dir) || best.Dir == "." {
			best = m
		}
	}
	return best
}

// within reports whether an import path is p or a package under it.
func within(path, p string) bool { return path == p || strings.HasPrefix(path, p+"/") }

// resolves reports whether an import can be satisfied without inventing a package: it is in the
// standard library, in a module of the repository, or under a module the file's go.mod requires or
// replaces. A package missing from a required module is not caught: that needs the module's source.
func resolves(mods []facts.Module, m *facts.Module, path string) bool {
	if golang.Std(path) {
		return true
	}
	for _, o := range mods {
		if within(path, o.Path) {
			return true
		}
	}
	return slices.ContainsFunc(m.Requires, func(r string) bool { return within(path, r) })
}

// unused finds unexported functions that no file of their package references. Methods are left out,
// since an interface can call them without naming them, and so are init, main and _. A package with a
// file that did not parse in full is not judged: its references are incomplete.
func unused(rf *facts.Repo) []report.Finding {
	type pkg struct {
		refs    map[string]bool
		partial bool
		decls   []*facts.File
	}
	pkgs := map[string]*pkg{}
	for i := range rf.Files {
		f := &rf.Files[i]
		if f.Language != "go" || f.Package == "" || f.Vendored {
			continue
		}
		p := pkgs[f.Package]
		if p == nil {
			p = &pkg{refs: map[string]bool{}}
			pkgs[f.Package] = p
		}
		p.partial = p.partial || f.Partial
		for _, r := range f.Refs {
			p.refs[r] = true
		}
		if !f.Generated {
			p.decls = append(p.decls, f)
		}
	}
	var out []report.Finding
	for _, p := range pkgs {
		if p.partial {
			continue
		}
		for _, f := range p.decls {
			for _, fn := range f.Funcs {
				if fn.Method || fn.Exported || fn.Name == "init" || fn.Name == "main" || fn.Name == "_" || p.refs[fn.Name] {
					continue
				}
				out = append(out, report.Finding{RuleID: UnusedFunction, Path: f.Path, Line: fn.Line,
					Message: fmt.Sprintf("%s is never referenced in its package", fn.Name)})
			}
		}
	}
	return out
}
