package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
			"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestScanJSON(t *testing.T) {
	dir := gitRepo(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	// Nothing is measured yet, so the honest answer is "incomplete", never "clean".
	if code != report.ExitIncomplete {
		t.Fatalf("exit %d, want %d; stderr: %s", code, report.ExitIncomplete, stderr.String())
	}
	var r report.Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if r.SchemaVersion != report.SchemaVersion || len(r.Repository.Head) != 40 {
		t.Errorf("schemaVersion %d head %q", r.SchemaVersion, r.Repository.Head)
	}
	if len(r.Families) != 4 {
		t.Fatalf("families = %d, want 4", len(r.Families))
	}
	for _, f := range r.Families {
		if f.Measured || !f.Score.IsNull() || f.Score.Reason != report.NotImplemented {
			t.Errorf("family %s = %+v, want null not_implemented", f.Family, f)
		}
	}
	if len(r.Languages) != 1 || r.Languages[0].Name != "go" || r.Languages[0].Lines != 3 {
		t.Errorf("languages = %+v", r.Languages)
	}
	if strings.Contains(stdout.String(), dir) {
		t.Error("report contains the absolute repository path")
	}
}

func TestScanWritesOnlyWhenAsked(t *testing.T) {
	dir := gitRepo(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	run([]string{"scan", dir}, &stdout, &stderr)
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("scan without -o wrote %v into the working directory", entries)
	}
	out := filepath.Join(cwd, "r.json")
	run([]string{"scan", "--format", "json", "-o", out, dir}, &stdout, &stderr)
	if _, err := os.Stat(out); err != nil {
		t.Errorf("-o did not write the report: %v", err)
	}
}

func TestErrors(t *testing.T) {
	dir := gitRepo(t)
	cases := [][]string{
		{},
		{"nope"},
		{"scan"},
		{"scan", t.TempDir()},
		{"scan", filepath.Join(dir, "missing")},
		{"scan", "--format", "yaml", dir},
		{"scan", "--fail-under", "11", dir},
		{"scan", "--fail-under", "x", dir},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != report.ExitError {
			t.Errorf("run(%q) = %d, want %d", args, code, report.ExitError)
		}
	}
}

func TestFailUnderWithNullOverallIsIncomplete(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--fail-under", "5", gitRepo(t)}, &stdout, &stderr); code != report.ExitIncomplete {
		t.Errorf("exit %d, want %d", code, report.ExitIncomplete)
	}
}
