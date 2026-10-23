package tests

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

var _ analyze.Analyzer = Analyzer{}

// goFile is a file of the repository's own Go code with the facts the walk would give it.
func goFile(path string, lines, functions int) facts.File {
	dir := path[:max(strings.LastIndex(path, "/"), 0)]
	if dir == "" {
		dir = "."
	}
	return facts.File{Path: path, Language: "go", Lines: lines, Functions: functions, Package: dir,
		Test: strings.HasSuffix(path, "_test.go")}
}

func repoOf(files ...facts.File) *facts.Repo {
	rf := &facts.Repo{Files: files, Paths: map[string]bool{}, Modules: []facts.Module{{Dir: ".", Path: "example.com/m"}}}
	for _, f := range files {
		rf.Paths[f.Path] = true
	}
	return rf
}

func withProfile(rf *facts.Repo, lines ...string) *facts.Repo {
	rf.Coverage = &facts.Coverage{Name: "coverage.out", Data: []byte("mode: set\n" + strings.Join(lines, "\n") + "\n")}
	return rf
}

func ids(fs []report.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.RuleID+" "+f.Path)
	}
	return out
}

func value(t *testing.T, m report.Measurement) float64 {
	t.Helper()
	if m.IsNull() {
		t.Fatalf("null (%s), want a value", m.Reason)
	}
	return *m.Value
}

func TestRunWithoutGo(t *testing.T) {
	ctx := context.Background()
	py := facts.File{Path: "a.py", Language: "python", Lines: 10}
	if r := (Analyzer{}).Run(ctx, repoOf(py)); r.Measured || r.Reason != report.UnsupportedLanguage {
		t.Errorf("python only: %+v", r)
	}
	vendored := goFile("vendor/x/x.go", 10, 1)
	vendored.Vendored = true
	generated := goFile("gen.go", 10, 1)
	generated.Generated = true
	ignored := goFile("testdata/t.go", 10, 1)
	ignored.Package = ""
	if r := (Analyzer{}).Run(ctx, repoOf(vendored, generated, ignored)); r.Measured || r.Reason != report.InsufficientInput {
		t.Errorf("no own Go code: %+v", r)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if r := (Analyzer{}).Run(cancelled, repoOf(goFile("a.go", 1, 1))); r.Measured || r.Reason != report.Timeout {
		t.Errorf("cancelled: %+v", r)
	}
}

func TestRunWithoutCoverage(t *testing.T) {
	vendored := goFile("vendor/v/v.go", 1000, 50)
	vendored.Vendored = true
	gen := goFile("gen/gen.go", 1000, 50)
	gen.Generated = true
	ignored := goFile("testdata/fixture.go", 1000, 50)
	ignored.Package = ""
	r := Analyzer{}.Run(context.Background(), repoOf(
		goFile("a/a.go", 60, 3), goFile("a/a_test.go", 30, 2),
		goFile("b/b.go", 40, 1),
		goFile("types/types.go", 20, 0), // only types: nothing to test
		goFile("main.go", 10, 1),
		vendored, gen, ignored,
	))
	if !r.Measured || r.Reason != "" {
		t.Fatalf("result %+v", r)
	}
	// Test lines over code lines of own, non-test code only: 30 / (60+40+20+10).
	if got := value(t, r.Metrics[TestCodeLineRatio]); got != 30.0/130 || r.Metrics[TestCodeLineRatio].Inputs != 5 {
		t.Errorf("ratio = %v (%d inputs)", got, r.Metrics[TestCodeLineRatio].Inputs)
	}
	// a, b and the root package declare functions; a is tested.
	if got := value(t, r.Metrics[UntestedPackageShare]); got != 2.0/3 || r.Metrics[UntestedPackageShare].Inputs != 3 {
		t.Errorf("untested share = %v", got)
	}
	if m := r.Metrics[StatementCoverage]; m.Reason != report.NoCoverageReport || r.Coverage != nil {
		t.Errorf("coverage = %+v, input %+v", m, r.Coverage)
	}
	if got, want := ids(r.Findings), []string{UntestedPackage + " .", UntestedPackage + " b"}; !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
	if !strings.Contains(r.Findings[1].Message, "1 functions") {
		t.Errorf("message = %q", r.Findings[1].Message)
	}
}

func TestRunOnlyTests(t *testing.T) {
	// Code lines of zero and no package with functions: both are null, not 0 or 100%.
	r := Analyzer{}.Run(context.Background(), repoOf(goFile("a/a_test.go", 30, 2)))
	if r.Metrics[TestCodeLineRatio].Reason != report.InsufficientInput || r.Metrics[UntestedPackageShare].Reason != report.InsufficientInput {
		t.Errorf("metrics = %+v", r.Metrics)
	}
}

func TestCoverage(t *testing.T) {
	ctx := context.Background()
	base := func() *facts.Repo {
		gen := goFile("a/gen.go", 50, 1)
		gen.Generated = true
		return repoOf(goFile("a/a.go", 20, 2), goFile("a/a_test.go", 10, 1), goFile("b/b.go", 10, 1),
			goFile("c/c.go", 3, 1), gen)
	}
	r := Analyzer{}.Run(ctx, withProfile(base(),
		"example.com/m/a/a.go:3.1,5.2 3 1",
		"example.com/m/a/a.go:7.1,9.2 1 0",
		"example.com/m/b/b.go:3.1,4.2 2 0",
		"example.com/m/a/gen.go:3.1,40.2 100 0", // generated: matched but not counted
		"example.com/m/c/c.go:3.11,3.11 0 0",    // an empty function: no statement, no finding
	))
	m := r.Metrics[StatementCoverage]
	if got := value(t, m); got != 3.0/6 || m.Inputs != 3 {
		t.Errorf("coverage = %v from %d files, want 0.5 from 3", got, m.Inputs)
	}
	if c := r.Coverage; c == nil || *c != (report.CoverageInput{Path: "coverage.out", Format: "go-coverprofile", Files: 4, FilesAtHead: 4}) {
		t.Errorf("input = %+v", c)
	}
	// a is at 75%; b is at 0%, a package finding and a file finding.
	want := []string{UntestedPackage + " b", UntestedPackage + " c", LowCoverage + " b", UncoveredFile + " b/b.go"}
	if got := ids(r.Findings); !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
}

func TestCoverageThreshold(t *testing.T) {
	// Exactly 60% is acceptable; one statement less is not.
	for covered, want := range map[int]bool{60: false, 59: true} {
		rf := withProfile(repoOf(goFile("a/a.go", 200, 1), goFile("a/a_test.go", 1, 1)),
			fmt.Sprintf("example.com/m/a/a.go:1.1,1.5 %d 1", covered),
			fmt.Sprintf("example.com/m/a/a.go:2.1,2.5 %d 0", 100-covered))
		got := slices.Contains(ids(Analyzer{}.Run(context.Background(), rf).Findings), LowCoverage+" a")
		if got != want {
			t.Errorf("%d%% covered: finding %v, want %v", covered, got, want)
		}
	}
}

func TestCoverageNulls(t *testing.T) {
	ctx := context.Background()
	files := func() *facts.Repo {
		var fs []facts.File
		for i := range 10 {
			fs = append(fs, goFile(fmt.Sprintf("p%d/f.go", i), 10, 1))
		}
		return repoOf(fs...)
	}
	lines := func(n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("example.com/m/p%d/f.go:1.1,2.1 1 1", i))
		}
		return out
	}
	for _, c := range []struct {
		name    string
		rf      *facts.Repo
		reason  report.NullReason
		atHead  int
		noInput bool
	}{
		{name: "no report", rf: files(), reason: report.NoCoverageReport, noInput: true},
		{name: "not a profile", rf: func() *facts.Repo {
			rf := files()
			rf.Coverage = &facts.Coverage{Name: "coverage.txt", Data: []byte("total: 80%\n")}
			return rf
		}(), reason: report.InvalidCoverageReport},
		{name: "no blocks", rf: withProfile(files()), reason: report.InsufficientInput},
		{name: "9 of 10 files at HEAD is enough", rf: withProfile(files(), append(lines(9), "example.com/m/gone/f.go:1.1,2.1 1 1")...), atHead: 9},
		{name: "8 of 10 is stale", rf: withProfile(files(), append(lines(8), "example.com/m/gone/f.go:1.1,2.1 1 1", "example.com/m/gone2/f.go:1.1,2.1 1 1")...), reason: report.StaleCoverageReport, atHead: 8},
		{name: "another module's files are not at HEAD", rf: withProfile(files(), "example.com/other/p0/f.go:1.1,2.1 1 1"), reason: report.StaleCoverageReport},
		{name: "a block past the last line", rf: withProfile(files(), append(lines(9), "example.com/m/p9/f.go:11.1,11.5 1 1")...), reason: report.StaleCoverageReport, atHead: 10},
		{name: "a block on the last line", rf: withProfile(files(), append(lines(9), "example.com/m/p9/f.go:10.1,10.5 1 1")...), atHead: 10},
		{name: "only zero-statement blocks", rf: withProfile(files(), "example.com/m/p0/f.go:1.1,1.1 0 0"), reason: report.InsufficientInput, atHead: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := Analyzer{}.Run(ctx, c.rf)
			m := r.Metrics[StatementCoverage]
			if m.Reason != c.reason || (c.reason == "") == m.IsNull() {
				t.Errorf("coverage = %+v, want reason %q", m, c.reason)
			}
			if c.noInput != (r.Coverage == nil) || (r.Coverage != nil && r.Coverage.FilesAtHead != c.atHead) {
				t.Errorf("input = %+v, want %d at HEAD", r.Coverage, c.atHead)
			}
			// A null coverage produces no coverage findings.
			if m.IsNull() && slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.RuleID != UntestedPackage }) {
				t.Errorf("findings from an unused report: %v", ids(r.Findings))
			}
		})
	}
}

func TestResolve(t *testing.T) {
	mods := []facts.Module{
		{Dir: ".", Path: "example.com/m"},
		{Dir: "tools", Path: "example.com/m/tools"}, // nested module: longer path wins
		{Dir: "other", Path: "example.com/mm"},      // a prefix of the string, not of the path
		{Dir: "dup", Path: "example.com/m/tools"},   // same path as an earlier module: the earlier wins
	}
	for ip, want := range map[string]string{
		"example.com/m/a/a.go":       "a/a.go",
		"example.com/m/main.go":      "main.go",
		"example.com/m/tools/t.go":   "tools/t.go",
		"example.com/m/tools/x/x.go": "tools/x/x.go",
		"example.com/mm/x.go":        "other/x.go",
		"example.com/m":              "",
		"example.com/mx/a.go":        "",
		"_/home/u/m/a.go":            "",
		"/abs/a.go":                  "",
	} {
		got, ok := resolve(mods, ip)
		if got != want || ok != (want != "") {
			t.Errorf("resolve(%q) = %q, %v; want %q", ip, got, ok, want)
		}
	}
}

func TestRules(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range (Analyzer{}).Rules() {
		if seen[r.ID] || !strings.HasPrefix(r.ID, "GHC-TEST-") || r.Title == "" {
			t.Errorf("rule %+v", r)
		}
		seen[r.ID] = true
	}
}
