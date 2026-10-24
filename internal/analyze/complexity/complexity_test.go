package complexity

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func goFile(path string, funcs ...facts.Function) facts.File {
	return facts.File{Path: path, Language: "go", Package: dirOf(path), Lines: 100, Funcs: funcs, Functions: len(funcs)}
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// fns returns n plain exported functions, enough for the shares.
func fns(n int) []facts.Function {
	var out []facts.Function
	for i := range n {
		out = append(out, facts.Function{Name: fmt.Sprintf("F%d", i), Line: i + 1, Cyclomatic: 2, Exported: true})
	}
	return out
}

func ids(fs []report.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%s %s:%d", f.RuleID, f.Path, f.Line))
	}
	return out
}

func TestThresholds(t *testing.T) {
	edge := []facts.Function{
		{Name: "AtCognitive", Line: 100, Cognitive: MaxCognitive, Lines: MaxLines, Nesting: MaxNesting, Cyclomatic: 1, Exported: true},
		{Name: "OverCognitive", Line: 101, Cognitive: MaxCognitive + 1, Cyclomatic: 1, Exported: true},
		{Name: "OverLines", Line: 102, Lines: MaxLines + 1, Cyclomatic: 1, Exported: true},
		{Name: "OverNesting", Line: 103, Nesting: MaxNesting + 1, Cyclomatic: 1, Exported: true},
	}
	test := goFile("a/a_test.go", facts.Function{Name: "TestHuge", Cognitive: 99, Lines: 999, Nesting: 9, Exported: true})
	test.Test = true
	rf := &facts.Repo{Files: []facts.File{goFile("a/a.go", append(fns(16), edge...)...), test}}
	res := Analyzer{}.Run(context.Background(), rf)
	want := []string{"GHC-CPLX-001 a/a.go:101", "GHC-CPLX-002 a/a.go:102", "GHC-CPLX-003 a/a.go:103"}
	if got := ids(res.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("findings %v, want %v (tests are not judged for shape)", got, want)
	}
	// 20 functions, cyclomatic 16*2 + 4*1 = 36; 1 of 20 over each threshold.
	m := res.Metrics
	if *m[ComplexFunctionShare].Value != 0.05 || *m[LongFunctionShare].Value != 0.05 || *m[MeanCyclomatic].Value != 1.8 ||
		m[MeanCyclomatic].Inputs != 20 || *m[SmellsPerKLOC].Value != 0 || m[SmellsPerKLOC].Inputs != 2 {
		t.Errorf("metrics %+v", m)
	}
	if MaxCognitive != 15 || MaxLines != 90 || MaxNesting != 4 || MinFunctions != 20 {
		t.Error("a documented threshold changed")
	}
}

func TestNotMeasured(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	py := &facts.Repo{Files: []facts.File{{Path: "a.py", Language: "python"}}}
	vendored := goFile("vendor/x/x.go", fns(30)...)
	vendored.Vendored = true
	for name, c := range map[string]struct {
		ctx  context.Context
		rf   *facts.Repo
		want report.NullReason
	}{
		"cancelled":   {cancelled, &facts.Repo{Files: []facts.File{goFile("a/a.go", fns(30)...)}}, report.Timeout},
		"no Go":       {context.Background(), py, report.UnsupportedLanguage},
		"no own code": {context.Background(), &facts.Repo{Files: []facts.File{vendored}}, report.InsufficientInput},
	} {
		if res := (Analyzer{}).Run(c.ctx, c.rf); res.Measured || res.Reason != c.want {
			t.Errorf("%s: %+v, want %s", name, res, c.want)
		}
	}
	// Under 20 functions the shares are null, and a finding still counts.
	few := goFile("a/a.go", facts.Function{Name: "Big", Line: 3, Cognitive: 40, Exported: true})
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{few}})
	if !res.Measured || res.Metrics[ComplexFunctionShare].Reason != report.InsufficientInput || len(res.Findings) != 1 {
		t.Errorf("few functions: %+v", res)
	}
}

func TestSmellFindings(t *testing.T) {
	a := goFile("svc/a.go", facts.Function{Name: "Run", Line: 1, Exported: true})
	a.Smells = []facts.Smell{
		{Kind: facts.EmptyErrorCheck, Line: 5, Func: "Run"},
		{Kind: facts.ErrorDropped, Line: 9, Func: "Run"},
		{Kind: facts.Placeholder, Line: 12, EndLine: 14, Func: "Todo"},
		{Kind: facts.CommentedCode, Line: 20, EndLine: 22},
		{Kind: facts.CommentedCode, Line: 30, EndLine: 30},
	}
	test := goFile("svc/a_test.go")
	test.Test = true
	test.Smells = []facts.Smell{{Kind: facts.CommentedCode, Line: 4, EndLine: 4}}
	gen := goFile("svc/gen.go")
	gen.Generated = true
	gen.Smells = []facts.Smell{{Kind: facts.CommentedCode, Line: 4, EndLine: 4}}
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{a, test, gen}})
	var got []string
	for _, f := range res.Findings {
		got = append(got, fmt.Sprintf("%s %s:%d %s", f.RuleID, f.Path, f.Line, f.Message))
	}
	want := []string{
		"GHC-CPLX-004 svc/a.go:5 Run checks an error and does nothing with it",
		"GHC-CPLX-004 svc/a.go:9 Run checks an error and returns nil in its place",
		"GHC-CPLX-007 svc/a.go:20 lines 20-22 are commented-out code",
		"GHC-CPLX-007 svc/a.go:30 line 30 is commented-out code",
		"GHC-CPLX-007 svc/a_test.go:4 line 4 is commented-out code",
		"GHC-CPLX-008 svc/a.go:12 Todo only says it is not implemented",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	// 6 smells over 300 lines of own code (a, test, gen are 100 each; gen is not own).
	if m := res.Metrics[SmellsPerKLOC]; *m.Value != 30 || m.Inputs != 2 {
		t.Errorf("smells per kloc %+v", m)
	}
}

func TestUnresolvedImports(t *testing.T) {
	mods := []facts.Module{
		{Dir: ".", Path: "example.com/root", Requires: []string{"github.com/dep/one", "gopkg.in/yaml.v3"}},
		{Dir: "tools", Path: "example.com/tools", Requires: []string{"github.com/dep/two"}},
	}
	file := func(path string, imports ...string) facts.File {
		f := goFile(path)
		for i, p := range imports {
			f.Imports = append(f.Imports, facts.Import{Path: p, Line: i + 3})
		}
		return f
	}
	rf := &facts.Repo{Modules: mods, Files: []facts.File{
		file("a/a.go", "fmt", "net/http/httptest", "C", "github.com/dep/one", "github.com/dep/one/sub", "gopkg.in/yaml.v3",
			"example.com/root/b", "example.com/tools/x", // modules of the repository
			"encoding/yaml",          // 9: not in the standard library
			"github.com/dep/oneplus", // 10: a prefix of a requirement is not one
			"github.com/dep/two"),    // 11: required by the other module only
		file("tools/t.go", "github.com/dep/two", "github.com/dep/one"), // 4: not required here
	}}
	res := Analyzer{}.Run(context.Background(), rf)
	want := []string{"GHC-CPLX-005 a/a.go:11", "GHC-CPLX-005 a/a.go:12", "GHC-CPLX-005 a/a.go:13", "GHC-CPLX-005 tools/t.go:4"}
	if got := ids(res.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !slices.ContainsFunc(res.Findings, func(f report.Finding) bool {
		return f.Message == `imports "encoding/yaml", which is not in the standard library, this repository or the requirements of go.mod`
	}) {
		t.Errorf("messages: %+v", res.Findings)
	}
	// Without a go.mod above it, a file is not judged.
	rf.Modules = nil
	if res := (Analyzer{}).Run(context.Background(), rf); len(res.Findings) != 0 {
		t.Errorf("no module: %v", ids(res.Findings))
	}
}

func TestUnused(t *testing.T) {
	a := goFile("p/a.go",
		facts.Function{Name: "Exported", Line: 1, Exported: true},
		facts.Function{Name: "used", Line: 2},
		facts.Function{Name: "usedByTest", Line: 3},
		facts.Function{Name: "dead", Line: 4},
		facts.Function{Name: "T.method", Line: 5, Method: true},
		facts.Function{Name: "init", Line: 6},
		facts.Function{Name: "main", Line: 7},
		facts.Function{Name: "_", Line: 8},
		facts.Function{Name: "usedByGenerated", Line: 9},
	)
	a.Refs = []string{"used"}
	test := goFile("p/a_test.go", facts.Function{Name: "deadInTest", Line: 1})
	test.Test, test.Refs = true, []string{"usedByTest"}
	gen := goFile("p/gen.go", facts.Function{Name: "deadGenerated", Line: 1})
	gen.Generated, gen.Refs = true, []string{"usedByGenerated"}
	other := goFile("q/q.go", facts.Function{Name: "dead", Line: 1})
	other.Refs = []string{"dead"} // same name in its own package
	broken := goFile("r/r.go", facts.Function{Name: "maybeDead", Line: 1})
	broken.Partial = true
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{a, test, gen, other, broken}})
	want := []string{"GHC-CPLX-006 p/a.go:4", "GHC-CPLX-006 p/a_test.go:1"}
	if got := ids(res.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
