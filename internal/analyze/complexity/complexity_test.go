package complexity

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func goFile(path string, funcs ...facts.Function) facts.File {
	return facts.File{Path: path, Language: "go", Analysed: true, Package: dirOf(path), Lines: 100, Funcs: funcs, Functions: len(funcs)}
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
		"cancelled":    {cancelled, &facts.Repo{Files: []facts.File{goFile("a/a.go", fns(30)...)}}, report.Timeout},
		"not analysed": {context.Background(), py, report.UnsupportedLanguage},
		"no own code":  {context.Background(), &facts.Repo{Files: []facts.File{vendored}}, report.InsufficientInput},
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
	rf := &facts.Repo{Modules: mods, Paths: map[string]bool{"b/b.go": true, "tools/x/x.go": true, "util/u.go": true}, Files: []facts.File{
		file("a/a.go", "fmt", "net/http/httptest", "C", "github.com/dep/one", "github.com/dep/one/sub", "gopkg.in/yaml.v3",
			"example.com/root/b", "example.com/tools/x", // modules of the repository
			"encoding/yaml",          // 9: not in the standard library
			"github.com/dep/oneplus", // 10: a prefix of a requirement is not one
			"github.com/dep/two"),    // 11: required by the other module only
		file("tools/t.go", "github.com/dep/two", "github.com/dep/one"),    // 4: not required here
		file("c/c.go", "example.com/root/utils", "example.com/root/util"), // 3: no such directory
	}}
	res := Analyzer{}.Run(context.Background(), rf)
	want := []string{"GHC-CPLX-005 a/a.go:11", "GHC-CPLX-005 a/a.go:12", "GHC-CPLX-005 a/a.go:13", "GHC-CPLX-005 c/c.go:3", "GHC-CPLX-005 tools/t.go:4"}
	if got := ids(res.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !slices.ContainsFunc(res.Findings, func(f report.Finding) bool {
		return f.Message == `imports "encoding/yaml", which is not in the standard library, this repository or the requirements of go.mod`
	}) {
		t.Errorf("messages: %+v", res.Findings)
	}
	// With a go.work, a module builds with what any module of the workspace requires.
	rf.Paths["go.work"] = true
	res = Analyzer{}.Run(context.Background(), rf)
	want = []string{"GHC-CPLX-005 a/a.go:11", "GHC-CPLX-005 a/a.go:12", "GHC-CPLX-005 c/c.go:3"}
	if got := ids(res.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("go.work: got %v, want %v", got, want)
	}
	delete(rf.Paths, "go.work")
	// A go.work the go command ignores changes nothing.
	rf.Paths["testdata/go.work"] = true
	if got := ids(Analyzer{}.Run(context.Background(), rf).Findings); !slices.Contains(got, "GHC-CPLX-005 a/a.go:13") {
		t.Errorf("ignored go.work: %v", got)
	}
	delete(rf.Paths, "testdata/go.work")
	// Of two go.mod files with one module path, the shallower is the module.
	rf.Modules = append(rf.Modules, facts.Module{Dir: "examples/starter", Path: "example.com/root"})
	if got := ids(Analyzer{}.Run(context.Background(), rf).Findings); slices.Contains(got, "GHC-CPLX-005 a/a.go:9") {
		t.Errorf("duplicate module path: %v", got)
	}
	rf.Modules = rf.Modules[:2]
	// A module on a Go newer than the standard library list may import packages the list lacks.
	rf.Modules[0].Go = "go1.99"
	res = Analyzer{}.Run(context.Background(), rf)
	if got := ids(res.Findings); slices.Contains(got, "GHC-CPLX-005 a/a.go:11") || !slices.Contains(got, "GHC-CPLX-005 a/a.go:12") {
		t.Errorf("newer Go: %v", got)
	}
	rf.Modules[0].Go = ""
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
	// A Go file of the package that was not read may hold the reference.
	paths := map[string]bool{"p/a.go": true, "p/a_test.go": true, "p/gen.go": true, "p/big.go": true}
	res = Analyzer{}.Run(context.Background(), &facts.Repo{Paths: paths, Files: []facts.File{a, test, gen}})
	if len(res.Findings) != 0 {
		t.Errorf("skipped file: %v", ids(res.Findings))
	}
}

func TestCapPerFile(t *testing.T) {
	var funcs []facts.Function
	for i := range MaxPerFile + 5 {
		funcs = append(funcs, facts.Function{Name: fmt.Sprintf("F%d", i), Line: i + 1, Cognitive: 99, Exported: true})
	}
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{goFile("a/a.go", funcs...)}})
	last := res.Findings[len(res.Findings)-1]
	if len(res.Findings) != MaxPerFile+1 || last.Line != 0 || last.Message != "5 more findings of this rule in this file are not listed" {
		t.Errorf("%d findings, last %+v", len(res.Findings), last)
	}
	// Smells a file did not keep are in the count.
	f := goFile("b/b.go")
	f.Smells = []facts.Smell{{Kind: facts.CommentedCode, Line: 1, EndLine: 1}}
	f.SmellsDropped = map[facts.SmellKind]int{facts.CommentedCode: 9}
	res2 := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{f}})
	if got := ids(res2.Findings); len(got) != 2 || res2.Findings[1].Message != "9 more findings of this rule in this file are not listed" ||
		*res2.Metrics[SmellsPerKLOC].Value != 100 {
		t.Errorf("dropped smells: %+v %+v", res2.Findings, res2.Metrics[SmellsPerKLOC])
	}
	if res.Metrics[ComplexFunctionShare].Inputs != MaxPerFile+5 {
		t.Errorf("the share counts every function: %+v", res.Metrics[ComplexFunctionShare])
	}
}

// The tests of the densities use small sources; TestMinCodeLines checks the bound itself.
func TestMain(m *testing.M) {
	minCodeLines = 0
	os.Exit(m.Run())
}

// Below analyze.MinCodeLines the densities are null, and the findings are still reported.
func TestMinCodeLines(t *testing.T) {
	minCodeLines = analyze.MinCodeLines
	defer func() { minCodeLines = 0 }()
	f := goFile("a/a.go", fns(MinFunctions)...)
	f.Lines = analyze.MinCodeLines - 1
	f.Smells = []facts.Smell{{Kind: facts.CommentedCode, Line: 1, EndLine: 1}}
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{f}})
	if res.Metrics[SmellsPerKLOC].Reason != report.InsufficientInput || res.Metrics[DuplicatedLineShare].Reason != report.InsufficientInput {
		t.Errorf("metrics %+v", res.Metrics)
	}
	if !res.Measured || res.Metrics[ComplexFunctionShare].IsNull() || len(res.Findings) != 1 {
		t.Errorf("measured %v, findings %v", res.Measured, ids(res.Findings))
	}
	// With nothing measured, the family is not measured, and keeps its findings.
	small := goFile("a/a.go", fns(2)...)
	small.Smells = f.Smells
	res = Analyzer{}.Run(context.Background(), &facts.Repo{Files: []facts.File{small}})
	if res.Measured || res.Reason != report.InsufficientInput || len(res.Findings) != 1 {
		t.Errorf("small: %+v", res)
	}
	f.Lines = analyze.MinCodeLines
	if res := (Analyzer{}).Run(context.Background(), &facts.Repo{Files: []facts.File{f}}); res.Metrics[SmellsPerKLOC].IsNull() {
		t.Errorf("at the bound: %+v", res.Metrics[SmellsPerKLOC])
	}
}
