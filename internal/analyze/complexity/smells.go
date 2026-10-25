package complexity

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// smellFindings turns the D32 patterns of the repository's own files into findings: test files
// included, since generated tests drop errors and invent imports as generated code does. It stops
// with ctx.
func smellFindings(ctx context.Context, rf *facts.Repo, own []*facts.File) ([]report.Finding, error) {
	var out []report.Finding
	ix := newImportIndex(rf)
	for _, f := range own {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, s := range f.Smells {
			if rule, msg := smellMessage(s); rule != "" {
				out = append(out, report.Finding{RuleID: rule, Path: f.Path, Line: s.Line, Message: msg})
			}
		}
		out = append(out, ix.unresolved(rf.Modules, f)...)
	}
	return append(out, unused(rf)...), nil
}

// smellMessage returns the rule and message of a smell; the rule is empty for a kind without one.
func smellMessage(s facts.Smell) (rule, msg string) {
	switch s.Kind {
	case facts.EmptyErrorCheck:
		return SwallowedError, fmt.Sprintf("%s checks an error and does nothing with it", s.Func)
	case facts.ErrorDropped:
		return SwallowedError, fmt.Sprintf("%s checks an error and returns nil in its place", s.Func)
	case facts.ExceptIgnored:
		where := cmp.Or(s.Func, "module code")
		return SwallowedError, fmt.Sprintf("%s catches every exception and does nothing with it", where)
	case facts.Placeholder:
		return Placeholder, fmt.Sprintf("%s only says it is not implemented", s.Func)
	case facts.CommentedCode:
		if s.EndLine > s.Line {
			return CommentedCode, fmt.Sprintf("lines %d-%d are commented-out code", s.Line, s.EndLine)
		}
		return CommentedCode, fmt.Sprintf("line %d is commented-out code", s.Line)
	}
	return "", ""
}

// unresolved finds the imports of a Go file that its module cannot satisfy.
func (ix *importIndex) unresolved(mods []facts.Module, f *facts.File) []report.Finding {
	m := moduleOf(mods, f.Path)
	if m == nil || f.Language != "go" {
		return nil
	}
	var out []report.Finding
	for _, im := range f.Imports {
		if !ix.resolves(m, im.Path) {
			out = append(out, report.Finding{RuleID: UnresolvedImport, Path: f.Path, Line: im.Line,
				Message: fmt.Sprintf("imports %q, which is not in the standard library, this repository or the requirements of %s", im.Path, gomod(m))})
		}
	}
	return out
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
		if best == nil || best.Dir == "." || len(m.Dir) > len(best.Dir) {
			best = m
		}
	}
	return best
}

// importIndex answers whether an import resolves, in time proportional to the depth of its path.
type importIndex struct {
	modules   map[string]*facts.Module // the repository's modules by path
	goDirs    map[string]bool          // directories of the commit that hold a .go file
	workspace map[string]bool          // with a go.work: what any module requires
	requires  map[*facts.Module]map[string]bool
}

func newImportIndex(rf *facts.Repo) *importIndex {
	ix := &importIndex{modules: map[string]*facts.Module{}, goDirs: map[string]bool{}, requires: map[*facts.Module]map[string]bool{}}
	workspace := false
	for p := range rf.Paths {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			ix.goDirs[path.Dir(p)] = true
		}
		workspace = workspace || path.Base(p) == "go.work" && golang.Package(p) != ""
	}
	for i := range rf.Modules {
		m := &rf.Modules[i]
		if _, ok := ix.modules[m.Path]; !ok {
			ix.modules[m.Path] = m // of two go.mod files with one path, the shallower, as modules() keeps
		}
		ix.requires[m] = setOf(m.Requires)
	}
	// In workspace mode a module builds with what any module of the workspace requires. Which
	// modules a go.work uses is not read: all of them count.
	if workspace {
		ix.workspace = map[string]bool{}
		for _, m := range rf.Modules {
			maps.Copy(ix.workspace, setOf(m.Requires))
		}
	}
	return ix
}

func setOf(xs []string) map[string]bool {
	set := make(map[string]bool, len(xs))
	for _, x := range xs {
		set[x] = true
	}
	return set
}

// resolves reports whether an import can be satisfied without inventing a package: it is in the
// standard library; or a directory with Go files of a module of the repository; or under a module
// the file's go.mod (or, with a go.work, any go.mod) requires or replaces. A package missing from a
// required module is not caught: that needs the module's source.
func (ix *importIndex) resolves(m *facts.Module, imp string) bool {
	if golang.Std(imp) {
		return true
	}
	first, _, _ := strings.Cut(imp, "/")
	if !strings.Contains(first, ".") && golang.NewerStd(m.Go) {
		return true // perhaps a standard package newer than the list
	}
	// The longest prefix decides: a module of the repository, or a requirement.
	for p := imp; ; {
		if own, ok := ix.modules[p]; ok {
			rel := strings.TrimPrefix(strings.TrimPrefix(imp, p), "/")
			return ix.goDirs[path.Join(own.Dir, rel)]
		}
		if ix.requires[m][p] || ix.workspace[p] {
			return true
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return false
		}
		p = p[:i]
	}
}

// unused finds unexported functions that no file of their package references. Methods are left out,
// since an interface can call them without naming them, and so are init, main and _. A package with a
// Go file that was not read or did not parse in full is not judged: its references are incomplete.
func unused(rf *facts.Repo) []report.Finding {
	var out []report.Finding
	for _, p := range goPackages(rf) {
		if p.partial {
			continue
		}
		for _, f := range p.decls {
			for _, fn := range f.Funcs {
				if !fn.Method && !fn.Exported && fn.Name != "init" && fn.Name != "main" && fn.Name != "_" && !p.refs[fn.Name] {
					out = append(out, report.Finding{RuleID: UnusedFunction, Path: f.Path, Line: fn.Line,
						Message: fmt.Sprintf("%s is never referenced in its package", fn.Name)})
				}
			}
		}
	}
	return out
}

// goPackage is what unused needs of a Go package: the names its files reference, the files that
// declare its functions, and whether a file of it was not read in full.
type goPackage struct {
	refs    map[string]bool
	partial bool
	decls   []*facts.File
}

func goPackages(rf *facts.Repo) map[string]*goPackage {
	pkgs := map[string]*goPackage{}
	read := map[string]bool{}
	for i := range rf.Files {
		f := &rf.Files[i]
		read[f.Path] = true
		if f.Language != "go" || f.Package == "" || f.Vendored {
			continue
		}
		p := pkgs[f.Package]
		if p == nil {
			p = &goPackage{refs: map[string]bool{}}
			pkgs[f.Package] = p
		}
		p.partial = p.partial || f.Partial
		maps.Copy(p.refs, setOf(f.Refs))
		if !f.Generated {
			p.decls = append(p.decls, f)
		}
	}
	// A Go file of the commit that was not read (too large, a symlink, unreadable) may hold the
	// references: its package is not judged.
	for p := range rf.Paths {
		if !strings.HasSuffix(p, ".go") || read[p] {
			continue
		}
		if q := pkgs[golang.Package(p)]; q != nil {
			q.partial = true
		}
	}
	return pkgs
}
