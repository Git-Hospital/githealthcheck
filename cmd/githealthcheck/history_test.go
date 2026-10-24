package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/analyze/history"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// historyEpoch is day 0 of a history.txt: 2024-01-01 UTC.
const historyEpoch = 1704067200

// gitRepoFromHistory builds a repository from a history.txt, so a fixture's history is the same on
// every run. Each line is one commit, or with a day range one commit per day:
//
//	<day>[..<last>[/<step>]] <email> <change>...
//
// A change is A:path (add), M:path (modify), D:path (delete) or R:old>new (rename). Blank lines and
// lines that start with # are skipped. The commits are written with git fast-import.
func gitRepoFromHistory(t *testing.T, spec string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	var stream bytes.Buffer
	version := 0
	content := func() string {
		version++
		return fmt.Sprintf("x = %d\n", version)
	}
	for n, line := range strings.Split(spec, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 3 {
			t.Fatalf("history line %d: %q", n+1, line)
		}
		first, last, step := parseDays(t, f[0])
		for day := first; day <= last; day += step {
			when := historyEpoch + day*86400
			name, _, _ := strings.Cut(f[1], "@")
			fmt.Fprintf(&stream, "commit refs/heads/main\nauthor %s <%s> %d +0000\ncommitter %[1]s <%[2]s> %[3]d +0000\ndata 0\n",
				name, f[1], when)
			for _, ch := range f[2:] {
				op, path, _ := strings.Cut(ch, ":")
				switch op {
				case "A", "M":
					body := content()
					fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n%s\n", path, len(body), body)
				case "D":
					fmt.Fprintf(&stream, "D %s\n", path)
				case "R":
					old, newPath, _ := strings.Cut(path, ">")
					fmt.Fprintf(&stream, "R %s %s\n", old, newPath)
				default:
					t.Fatalf("history line %d: change %q", n+1, ch)
				}
			}
			stream.WriteString("\n")
		}
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir = dir
	cmd.Stdin = &stream
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	git(t, dir, "reset", "-q", "--hard")
	return dir
}

func parseDays(t *testing.T, s string) (first, last, step int64) {
	t.Helper()
	span, stepText, hasStep := strings.Cut(s, "/")
	from, to, isRange := strings.Cut(span, "..")
	var err1, err2, err3 error
	first, err1 = strconv.ParseInt(from, 10, 64)
	last, step = first, 1
	if isRange {
		last, err2 = strconv.ParseInt(to, 10, 64)
	}
	if hasStep {
		step, err3 = strconv.ParseInt(stepText, 10, 64)
	}
	if err1 != nil || err2 != nil || err3 != nil || step < 1 || last < first {
		t.Fatalf("history days %q", s)
	}
	return first, last, step
}

// fixtureRepo builds a rule fixture: from its history.txt when it has one, else as one commit of its
// files.
func fixtureRepo(t *testing.T, dir string) string {
	t.Helper()
	if spec, err := os.ReadFile(filepath.Join(dir, "history.txt")); err == nil {
		return gitRepoFromHistory(t, string(spec))
	}
	return gitRepoWith(t, readTree(t, dir))
}

func historyFamily(t *testing.T, r report.Report) report.FamilyResult {
	t.Helper()
	for _, f := range r.Families {
		if f.Family == report.History {
			return f
		}
	}
	t.Fatal("no history family")
	return report.FamilyResult{}
}

// The .mailmap in the work tree joins identities, as git's own would; a .mailmap git could block on is
// skipped and listed instead.
func TestScanHistoryMailmap(t *testing.T) {
	// Without the .mailmap, ann's two identities, with different names and emails, are two
	// developers who each author half: TF 2.
	spec := "0 ann@example.com A:a.py A:b.py\n0 a.n@home.example A:c.py A:d.py\n" +
		"1..55/2 ann@example.com M:a.py\n2..56/2 a.n@home.example M:c.py\n"
	dir := gitRepoFromHistory(t, spec)
	r, _, out := scanJSON(t, dir)
	if f := historyFamily(t, r); !f.Measured || *f.Metrics[history.TruckFactorMetric].Value != 2 || r.Inputs.Commits != 58 {
		t.Fatalf("without .mailmap: %+v\n%s", f, out)
	}

	if err := os.WriteFile(filepath.Join(dir, ".mailmap"), []byte("Ann <ann@example.com> <a.n@home.example>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, code, out := scanJSON(t, dir)
	f := historyFamily(t, r)
	if *f.Metrics[history.TruckFactorMetric].Value != 1 || len(f.Findings) != 1 || f.Findings[0].RuleID != history.TruckFactorOne || code != report.ExitFindings {
		t.Errorf("with .mailmap: %+v exit %d\n%s", f, code, out)
	}

	if err := os.Remove(filepath.Join(dir, ".mailmap")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, ".mailmap")); err != nil {
		t.Fatal(err)
	}
	r, _, out = scanJSON(t, dir)
	if f := historyFamily(t, r); *f.Metrics[history.TruckFactorMetric].Value != 2 || len(r.Skipped) != 1 || r.Skipped[0].Path != ".mailmap" || r.Skipped[0].Reason != report.SkipSymlink {
		t.Errorf("symlinked .mailmap: %+v skipped %+v\n%s", f, r.Skipped, out)
	}
}

// A shallow clone has lost the history before its depth: the family is null, not a truck factor of
// whoever made the last commits.
func TestScanHistoryShallow(t *testing.T) {
	src := fixtureRepo(t, filepath.Join("..", "..", "testdata", "pairs", history.TruckFactorOne, "clean"))
	dir := filepath.Join(t.TempDir(), "shallow")
	// Deep enough for MinCommits, so only the shallowness can make the family null.
	git(t, t.TempDir(), "clone", "-q", "--depth", strconv.Itoa(history.MinCommits+5), "file://"+src, dir)
	r, _, out := scanJSON(t, dir)
	if f := historyFamily(t, r); f.Measured || f.Reason != report.HistoryTooShort || !r.Repository.Shallow {
		t.Errorf("shallow clone: %+v shallow %v\n%s", f, r.Repository.Shallow, out)
	}
}

// When git log fails, here on a missing parent commit, the history family is null and the others
// still report.
func TestScanHistoryLogFails(t *testing.T) {
	dir := gitRepoWith(t, map[string]string{"go.mod": "module m\n", "a.go": "package m\n"})
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	parent := strings.TrimSpace(string(out))
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package m\n\nvar X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-q", "-am", "two")
	if err := os.Remove(filepath.Join(dir, ".git", "objects", parent[:2], parent[2:])); err != nil {
		t.Fatal(err)
	}
	r, code, text := scanJSON(t, dir)
	if f := historyFamily(t, r); f.Measured || f.Reason != report.ToolCrashed || code != report.ExitIncomplete {
		t.Errorf("history %+v exit %d\n%s", f, code, text)
	}
	if f := testsFamily(r); f.Family != report.Tests || f.Reason == report.ToolCrashed {
		t.Errorf("tests %+v\n%s", f, text)
	}
	if strings.Contains(text, parent) {
		t.Errorf("git's error text reached the report:\n%s", text)
	}
}
