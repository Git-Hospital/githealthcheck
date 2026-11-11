package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/complexity"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/history"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/tests"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// A suppression marks the finding, keeps it in every report, takes it out of the exit code and the
// text total, and changes no metric or score (D21, architecture section 5).
func TestSuppressionMarksFindingsOnly(t *testing.T) {
	dir := gitRepo(t)
	before, code, out := scanJSON(t, dir)
	if code != report.ExitFindings {
		t.Fatalf("the fixture needs a finding: exit %d\n%s", code, out)
	}
	var rule string
	for _, f := range before.Families {
		for _, fd := range f.Findings {
			rule = fd.RuleID
		}
	}
	conf := "version: 1\nsuppress:\n  - rule: " + rule + "\n    reason: \"a fixture: it only wires\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	after, code, out := scanJSON(t, dir)
	if code != report.ExitClean {
		t.Fatalf("with every finding suppressed: exit %d, want %d\n%s", code, report.ExitClean, out)
	}
	for i, f := range after.Families {
		b, _ := json.Marshal(before.Families[i].Metrics)
		a, _ := json.Marshal(f.Metrics)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: metrics changed by a suppression:\n%s\n%s", f.Family, b, a)
		}
		bs, _ := json.Marshal(before.Families[i].Score)
		as, _ := json.Marshal(f.Score)
		if !bytes.Equal(as, bs) {
			t.Errorf("%s: score changed by a suppression: %s, %s", f.Family, bs, as)
		}
		if len(f.Findings) != len(before.Families[i].Findings) {
			t.Errorf("%s: findings dropped: %d, want %d", f.Family, len(f.Findings), len(before.Families[i].Findings))
		}
		for _, fd := range f.Findings {
			if !fd.Suppressed || fd.SuppressReason != "a fixture: it only wires" {
				t.Errorf("finding not marked: %+v", fd)
			}
		}
	}

	for format, want := range map[string]string{
		"text":      "Suppressed in .githealthcheck.yml (",
		"markdown":  `(suppressed: a fixture\: it only wires)`,
		"markdown ": " of them suppressed)\n",
	} {
		var stdout, stderr bytes.Buffer
		run([]string{"scan", "--format", strings.TrimSpace(format), dir}, &stdout, &stderr)
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("%s output lacks %q:\n%s", format, want, stdout.String())
		}
		if format == "text" && strings.Contains(stdout.String(), "\nFindings (") {
			t.Errorf("a suppressed finding is in the text total:\n%s", stdout.String())
		}
	}
}

// A suppression with a path covers the findings in that file only, and SARIF carries it as a
// suppression on the result.
func TestSuppressionByPathInSARIF(t *testing.T) {
	dir := fixtureRepo(t, filepath.Join("..", "..", "testdata", "pairs", "GHC-CPLX-001", "defect"))
	before, code, out := scanJSON(t, dir)
	if code != report.ExitFindings {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var file string
	for _, f := range before.Families {
		for _, fd := range f.Findings {
			file = fd.Path
		}
	}
	conf := "version: 1\nsuppress:\n  - rule: GHC-CPLX-001\n    path: " + file + "\n    reason: generated\n" +
		"  - rule: GHC-CPLX-001\n    path: " + file + ".bak\n    reason: another file\n"
	if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, out := scanJSON(t, dir); code != report.ExitClean {
		t.Fatalf("exit %d, want %d\n%s", code, report.ExitClean, out)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "sarif", dir}, &stdout, &stderr)
	var sarif struct {
		Runs []struct {
			Results []struct {
				RuleID       string `json:"ruleId"`
				Suppressions []struct {
					Kind          string `json:"kind"`
					Justification string `json:"justification"`
				} `json:"suppressions"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sarif); err != nil || len(sarif.Runs) != 1 || len(sarif.Runs[0].Results) == 0 {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	// The second entry names a file with no finding, as a mistyped path would: it is reported, the
	// first is not.
	unused := `githealthcheck: warning: .githealthcheck.yml line 6: the suppression of GHC-CPLX-001 under "` + file + `.bak" matches no finding`
	if !strings.Contains(stderr.String(), unused) || strings.Contains(stderr.String(), "line 3:") {
		t.Errorf("stderr %q, want only %q", stderr.String(), unused)
	}
	for _, res := range sarif.Runs[0].Results {
		if len(res.Suppressions) != 1 || res.Suppressions[0].Justification != "generated" {
			t.Errorf("result %s: suppressions %+v", res.RuleID, res.Suppressions)
		}
	}
}

// A file that does not parse stops the run with exit 2 and names the line.
func TestInvalidConfigIsAnError(t *testing.T) {
	for name, tc := range map[string]struct{ conf, want string }{
		"missing reason": {"version: 1\nsuppress:\n  - rule: GHC-TEST-001\n", "invalid_config: .githealthcheck.yml line 3: a suppression needs a reason"},
		"unknown key":    {"version: 1\nsuppress:\n  - rule: GHC-TEST-001\n    reasn: x\n", `line 4: unknown key "reasn"`},
		"unknown rule":   {"version: 1\nsuppress:\n  - rule: GHC-TEST-999\n    reason: x\n", "line 3: no rule GHC-TEST-999 in this build"},
		"empty path":     {"version: 1\nsuppress:\n  - rule: GHC-TEST-001\n    path: # TODO\n    reason: x\n", "line 4: path is empty"},
	} {
		dir := gitRepo(t)
		if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(tc.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run([]string{"scan", "--format", "json", dir}, &stdout, &stderr); code != report.ExitError {
			t.Errorf("%s: exit %d, want %d", name, code, report.ExitError)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%s: stderr %q, want %q", name, stderr.String(), tc.want)
		}
	}
	dir := gitRepo(t)
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, ".githealthcheck.yml")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", dir}, &stdout, &stderr); code != report.ExitError || !strings.Contains(stderr.String(), "symlink") {
		t.Errorf("a symlinked config: exit %d, stderr %q", code, stderr.String())
	}
}

// timedOut is a family that ran out of time after the walk: it reports no findings.
type timedOut struct{ analyze.Analyzer }

func (timedOut) Run(context.Context, *facts.Repo) analyze.Result {
	return analyze.Result{Reason: report.Timeout}
}

// A family that timed out found nothing, so its rules' suppressions are not reported as unused; the
// other families' still are.
func TestNoUnusedWarningForATimedOutFamily(t *testing.T) {
	saved := analyzers
	t.Cleanup(func() { analyzers = saved })
	analyzers = []analyze.Analyzer{tests.Analyzer{}, history.Analyzer{}, timedOut{complexity.Analyzer{}}}
	dir := gitRepo(t)
	conf := "version: 1\nsuppress:\n  - rule: GHC-CPLX-001\n    reason: generated\n" +
		"  - rule: GHC-TEST-001\n    path: no/such/dir\n    reason: typo\n"
	if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "GHC-CPLX-001") || !strings.Contains(stderr.String(), `line 5: the suppression of GHC-TEST-001 under "no/such/dir" matches no finding`) {
		t.Errorf("stderr %q", stderr.String())
	}
}

// A shallow clone, or a history under 50 commits, ends history with history_too_short: its rules did
// not run, so a valid suppression of them is not called unused, and a user is not told to delete it
// (#83).
func TestNoUnusedWarningWhenHistoryDidNotRun(t *testing.T) {
	dir := gitRepo(t) // one commit
	conf := "version: 1\nsuppress:\n  - rule: GHC-HIST-001\n    reason: one maintainer\n"
	if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "GHC-HIST-001") {
		t.Errorf("a suppression of a rule that did not run is called unused: %q", stderr.String())
	}
}

// Without python3 the Python files are not analysed. A suppression on a Python file, or one without
// a path that could be about Python, is not called unused; one on a Go file still is (#83).
func TestNoUnusedWarningForALanguageThatWasNotAnalysed(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	dir := gitRepoWith(t, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"app.py":  "def f():\n    try:\n        pass\n    except Exception:\n        pass\n",
	})
	conf := "version: 1\nsuppress:\n" +
		"  - rule: GHC-CPLX-004\n    path: app.py\n    reason: best effort\n" +
		"  - rule: GHC-CPLX-001\n    reason: generated\n" +
		"  - rule: GHC-CPLX-001\n    path: main.go\n    reason: typo, nothing to match\n"
	if err := os.WriteFile(filepath.Join(dir, ".githealthcheck.yml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin) // git, and no python3
	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	got := stderr.String()
	if strings.Contains(got, "GHC-CPLX-004") || strings.Contains(got, "line 6:") {
		t.Errorf("a suppression that may be about Python is called unused: %q", got)
	}
	if !strings.Contains(got, `line 8: the suppression of GHC-CPLX-001 under "main.go" matches no finding`) {
		t.Errorf("the unused suppression on a Go file is not named: %q", got)
	}
	var r report.Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	for _, l := range r.Languages {
		if l.Name == "python" && l.Analysed {
			t.Fatal("python was analysed, so the test proves nothing; it needs python3 off PATH")
		}
	}
}
