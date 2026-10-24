package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Git-Hospital/githealthcheck/internal/analyze/tests"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// A module whose only package is fully covered by profile.
var (
	covered = map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.22\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { A() }\n",
	}
	profile = "mode: set\nexample.com/m/a/a.go:3.14,3.24 1 1\n"
)

func scanJSON(t *testing.T, args ...string) (report.Report, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"scan", "--format", "json"}, args...), &stdout, &stderr)
	var r report.Report
	if code != report.ExitError {
		if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
			t.Fatalf("%v\n%s\n%s", err, stdout.String(), stderr.String())
		}
	}
	return r, code, stdout.String() + stderr.String()
}

func testsFamily(r report.Report) report.FamilyResult {
	for _, f := range r.Families {
		if f.Family == report.Tests {
			return f
		}
	}
	return report.FamilyResult{}
}

func coverageOf(t *testing.T, r report.Report) report.Measurement {
	t.Helper()
	return testsFamily(r).Metrics[tests.StatementCoverage]
}

func TestScanFindsUntrackedCoverage(t *testing.T) {
	dir := gitRepoWith(t, covered)
	// CI writes the report after checkout and never commits it.
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte(profile), 0o644)
	r, code, out := scanJSON(t, dir)
	if m := coverageOf(t, r); m.IsNull() || *m.Value != 1 || code != report.ExitClean {
		t.Fatalf("coverage %+v exit %d\n%s", m, code, out)
	}
	if c := r.Inputs.Coverage; c == nil || c.Path != "coverage.out" || c.Tracked || c.Format != "go-coverprofile" || *c.Files != 1 || *c.ModuleFiles != 1 || *c.FilesAtHead != 1 {
		t.Errorf("inputs.coverage = %+v", c)
	}

	// A committed report is marked tracked.
	git(t, dir, "add", "coverage.out")
	git(t, dir, "commit", "-q", "-m", "report")
	if r, _, _ := scanJSON(t, dir); r.Inputs.Coverage == nil || !r.Inputs.Coverage.Tracked {
		t.Errorf("tracked report: %+v", r.Inputs.Coverage)
	}
}

func TestScanCoverageDiscoveryIsGuarded(t *testing.T) {
	dir := gitRepoWith(t, covered)
	// coverage.out is a symlink to a profile outside the repository, cover.out a FIFO: neither is
	// read, both are listed, and the next name is used.
	outside := filepath.Join(t.TempDir(), "secret.out")
	os.WriteFile(outside, []byte(profile), 0o644)
	os.Symlink(outside, filepath.Join(dir, "coverage.out"))
	syscall.Mkfifo(filepath.Join(dir, "cover.out"), 0o644)
	os.WriteFile(filepath.Join(dir, "coverage.txt"), []byte(profile), 0o644)

	done := make(chan struct{})
	var r report.Report
	var out string
	go func() { r, _, out = scanJSON(t, dir); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("scan blocked on a FIFO named like a coverage report")
	}
	if r.Inputs.Coverage == nil || r.Inputs.Coverage.Path != "coverage.txt" {
		t.Errorf("used %+v, want coverage.txt", r.Inputs.Coverage)
	}
	want := []report.Skip{{Path: "coverage.out", Reason: report.SkipSymlink}, {Path: "cover.out", Reason: report.SkipNotRegular}}
	if !slices.Equal(r.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", r.Skipped, want)
	}
	if strings.Contains(out, outside) {
		t.Error("the symlink's target reached the output")
	}
}

func TestScanCoverageFlag(t *testing.T) {
	dir := gitRepoWith(t, covered)

	// Outside the repository: named by its base name only.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "unit.cov")
	os.WriteFile(outside, []byte(profile), 0o644)
	// A report at a discovered name is ignored when --coverage names another.
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte("mode: set\n"), 0o644)
	r, code, out := scanJSON(t, "--coverage", outside, dir)
	if c := r.Inputs.Coverage; c == nil || c.Path != "unit.cov" || c.Tracked || code != report.ExitClean {
		t.Errorf("outside report: %+v exit %d", c, code)
	}
	if strings.Contains(out, outsideDir) {
		t.Error("the report's directory reached the output")
	}

	// Inside the repository, relative to the working directory: named relative to the root.
	os.MkdirAll(filepath.Join(dir, "build"), 0o755)
	os.WriteFile(filepath.Join(dir, "build", "c.prof"), []byte(profile), 0o644)
	t.Chdir(filepath.Join(dir, "build"))
	if r, _, _ := scanJSON(t, "--coverage", "c.prof", dir); r.Inputs.Coverage == nil || r.Inputs.Coverage.Path != "build/c.prof" || r.Inputs.Coverage.Tracked {
		t.Errorf("inside report: %+v", r.Inputs.Coverage)
	}

	// Content that is not a profile is a null with its reason, not an error.
	bad := filepath.Join(t.TempDir(), "bad.out")
	os.WriteFile(bad, []byte("total: 91%\n"), 0o644)
	if r, code, _ := scanJSON(t, "--coverage", bad, dir); coverageOf(t, r).Reason != report.InvalidCoverageReport || code == report.ExitError {
		t.Errorf("invalid report: %+v exit %d", coverageOf(t, r), code)
	}

	// A named report that cannot be read is a usage error, at once.
	fifo := filepath.Join(t.TempDir(), "fifo.out")
	syscall.Mkfifo(fifo, 0o644)
	for _, name := range []string{filepath.Join(t.TempDir(), "missing.out"), t.TempDir(), fifo} {
		start := time.Now()
		if _, code, out := scanJSON(t, "--coverage", name, dir); code != report.ExitError || !strings.Contains(out, "--coverage") || time.Since(start) > 10*time.Second {
			t.Errorf("--coverage %s: exit %d after %v\n%s", name, code, time.Since(start), out)
		}
	}
}

func TestScanStaleCoverage(t *testing.T) {
	dir := gitRepoWith(t, covered)
	// The report was written when a.go was longer.
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte("mode: set\nexample.com/m/a/a.go:3.14,30.2 9 1\n"), 0o644)
	if r, code, _ := scanJSON(t, dir); coverageOf(t, r).Reason != report.StaleCoverageReport || code != report.ExitClean {
		t.Errorf("stale report: %+v exit %d", coverageOf(t, r), code)
	}

	// A go.mod the walk refuses to read leaves no module to map the report's paths onto.
	dir = gitRepoWith(t, covered)
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte(profile), 0o644)
	os.Remove(filepath.Join(dir, "go.mod"))
	os.Symlink(filepath.Join(t.TempDir(), "go.mod"), filepath.Join(dir, "go.mod"))
	r, _, _ := scanJSON(t, dir)
	if coverageOf(t, r).Reason != report.StaleCoverageReport || !slices.Contains(r.Skipped, report.Skip{Path: "go.mod", Reason: report.SkipSymlink}) {
		t.Errorf("symlinked go.mod: %+v, skipped %+v", coverageOf(t, r), r.Skipped)
	}
}

// Every rule has a page and a fixture pair, and the pair proves it: the defect fixture yields exactly
// that rule and exits 1, the clean one yields nothing and exits 0 (architecture §7).
func TestRulePairs(t *testing.T) {
	root := filepath.Join("..", "..")
	rules := map[string]bool{}
	for _, a := range analyzers {
		for _, rule := range a.Rules() {
			rules[rule.ID] = true
			if _, err := os.Stat(filepath.Join(root, "docs", "rules", rule.ID+".md")); err != nil {
				t.Errorf("%s has no page: %v", rule.ID, err)
			}
			for kind, want := range map[string]int{"defect": report.ExitFindings, "clean": report.ExitClean} {
				t.Run(rule.ID+"/"+kind, func(t *testing.T) {
					dir := gitRepoWith(t, readTree(t, filepath.Join(root, "testdata", "pairs", rule.ID, kind)))
					r, code, out := scanJSON(t, dir)
					var got []string
					for _, f := range r.Families {
						for _, fd := range f.Findings {
							got = append(got, fd.RuleID)
						}
					}
					wantIDs := []string(nil)
					if kind == "defect" {
						wantIDs = []string{rule.ID}
					}
					if !slices.Equal(got, wantIDs) || code != want {
						t.Errorf("findings %v exit %d, want %v exit %d\n%s", got, code, wantIDs, want, out)
					}
				})
			}
		}
	}
	// No pair without a rule.
	entries, err := os.ReadDir(filepath.Join(root, "testdata", "pairs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !rules[e.Name()] {
			t.Errorf("testdata/pairs/%s has no rule", e.Name())
		}
	}
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("fixture %s: %v", dir, err)
	}
	return files
}

// A go.mod under testdata is a fixture, not a module: it must not capture the report's paths, even
// when it declares a longer module path.
func TestScanIgnoresFixtureModules(t *testing.T) {
	files := map[string]string{
		"testdata/fake/go.mod": "module example.com/m/a\n",
		"testdata/fake/a.go":   "package a\n\nfunc A() int { return 1 }\n",
	}
	for k, v := range covered {
		files[k] = v
	}
	dir := gitRepoWith(t, files)
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte(profile), 0o644)
	if r, _, out := scanJSON(t, dir); coverageOf(t, r).IsNull() || *coverageOf(t, r).Value != 1 {
		t.Errorf("coverage = %+v\n%s", coverageOf(t, r), out)
	}
}

// A repository without Go: the tests family is null with its own reason, and nothing was measured.
func TestScanWithoutGo(t *testing.T) {
	r, code, _ := scanJSON(t, gitRepoWith(t, map[string]string{"app.py": "print(1)\n"}))
	f := testsFamily(r)
	if f.Measured || f.Reason != report.UnsupportedLanguage || f.Score.Reason != report.UnsupportedLanguage || code != report.ExitIncomplete {
		t.Errorf("tests family %+v, exit %d", f, code)
	}
	if r.Languages[0].Analysed {
		t.Errorf("python reported as analysed: %+v", r.Languages)
	}
}

// A report that was not parsed has no file counts: absent in JSON, not printed in text.
func TestScanUnparsedCoverageHasNoCounts(t *testing.T) {
	dir := gitRepoWith(t, covered)
	os.WriteFile(filepath.Join(dir, "coverage.out"), []byte("garbage\n"), 0o644)
	r, _, out := scanJSON(t, dir)
	if c := r.Inputs.Coverage; c == nil || c.Files != nil || c.FilesAtHead != nil || strings.Contains(out, `"filesAtHead"`) || strings.Contains(out, `"moduleFiles"`) {
		t.Errorf("inputs.coverage = %+v\n%s", c, out)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"scan", dir}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "Coverage report coverage.out (untracked)\n") {
		t.Errorf("text output:\n%s", stdout.String())
	}
}

// Paths from the scanned repository cannot inject terminal control sequences or forge report lines in
// the text output.
func TestScanTextQuotesRepositoryPaths(t *testing.T) {
	evil := "x\x1b[2K\rOverall  10.0  (4 of 4 families measured)\nok"
	dir := gitRepoWith(t, map[string]string{evil + "/a.go": "package a\n\nfunc A() {}\n"})
	var stdout, stderr bytes.Buffer
	run([]string{"scan", dir}, &stdout, &stderr)
	out := stdout.String()
	if strings.ContainsAny(out, "\x1b\r") || strings.Contains(out, "\nok:") || !strings.Contains(out, `"x\x1b[2K\rOverall`) {
		t.Errorf("control characters reached the text output:\n%q", out)
	}
	for _, s := range []string{"plain/dir", "ünïcode/路径"} {
		if printable(s) != s {
			t.Errorf("printable(%q) = %q", s, printable(s))
		}
	}
	for _, s := range []string{"a\u202eb", "a\xffb", "a\tb", "a\u0085b"} {
		if printable(s) == s {
			t.Errorf("printable(%q) left it unquoted", s)
		}
	}
}
