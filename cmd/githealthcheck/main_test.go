package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/repo"
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
	if !r.Inputs.WalkComplete || !strings.Contains(stdout.String(), `"skipped": []`) {
		t.Errorf("walkComplete %v; skipped must encode as [] not null:\n%s", r.Inputs.WalkComplete, stdout.String())
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
		{"scan", "--fail-under", "NaN", dir},
		{"scan", "--fail-under", "5abc", dir},
		{"scan", "--fail-under", "7,5", dir},
		{"scan", "--fail-under", "Inf", dir},
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

// The deadline expires during the walk, after the repository is open: the report must still come out,
// marked partial, with every family null(timeout) and exit 3.
func TestScanWalkTimeoutStillReports(t *testing.T) {
	dir := gitRepo(t)
	t.Cleanup(func() { walk = repo.Walk })
	walk = func(ctx context.Context, root string, entries []repo.Entry, id func([]byte) string) (*repo.Tree, error) {
		tree, _ := repo.Walk(context.Background(), root, entries, id)
		<-ctx.Done()
		return tree, ctx.Err()
	}
	for _, format := range []string{"json", "text"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"scan", "--format", format, "--timeout", "300ms", dir}, &stdout, &stderr)
		if code != report.ExitIncomplete {
			t.Fatalf("%s: exit %d, want %d; stderr %s", format, code, report.ExitIncomplete, stderr.String())
		}
		if format == "text" {
			if !strings.Contains(stdout.String(), "partial") {
				t.Errorf("text report does not say the counts are partial:\n%s", stdout.String())
			}
			continue
		}
		var r report.Report
		if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
			t.Fatalf("no JSON report on a walk timeout: %v\n%s", err, stdout.String())
		}
		if r.Inputs.WalkComplete {
			t.Error("walkComplete is true after a walk timeout")
		}
		for _, f := range r.Families {
			if f.Reason != report.Timeout {
				t.Errorf("family %s reason %q, want timeout", f.Family, f.Reason)
			}
		}
	}
}

// When the user's --timeout runs out before the repository is open there is no report, and the run
// is incomplete; a repository that cannot be opened at all is an error.
func TestScanOpenTimeoutAndRefusal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--timeout", "1ns", gitRepo(t)}, &stdout, &stderr); code != report.ExitIncomplete {
		t.Errorf("--timeout 1ns: exit %d, want %d", code, report.ExitIncomplete)
	}
	dir := gitRepo(t)
	cfg := filepath.Join(dir, ".git", "config")
	os.Remove(cfg)
	if err := syscall.Mkfifo(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	start := time.Now()
	if code := run([]string{"scan", dir}, &stdout, &stderr); code != report.ExitError || time.Since(start) > 10*time.Second {
		t.Errorf("FIFO config: exit %d after %v, want %d at once; stderr %s", code, time.Since(start), report.ExitError, stderr.String())
	}

	// A config include that names a FIFO outside the repository passes the .git check; git blocks
	// on it and the per-command timeout fires while --timeout has plenty left. That is an error.
	dir = gitRepo(t)
	fifo := filepath.Join(t.TempDir(), "fifo")
	syscall.Mkfifo(fifo, 0o644)
	cfg = filepath.Join(dir, ".git", "config")
	f, _ := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("[include]\n\tpath = " + fifo + "\n")
	f.Close()
	t.Cleanup(func(d time.Duration) func() { return func() { execx.QuickTimeout = d } }(execx.QuickTimeout))
	execx.QuickTimeout = 500 * time.Millisecond
	stderr.Reset()
	if code := run([]string{"scan", "--timeout", "1m", dir}, &stdout, &stderr); code != report.ExitError {
		t.Errorf("include FIFO: exit %d, want %d; stderr %s", code, report.ExitError, stderr.String())
	}
	if !strings.Contains(stderr.String(), "timed out while opening") {
		t.Errorf("include FIFO: message does not say what happened: %s", stderr.String())
	}
}

// Only the commit's files count. Untracked and ignored files are left out; a tracked file changed in
// the work tree makes the report dirty.
func TestScanCountsOnlyCommittedFiles(t *testing.T) {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".venv/\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".venv", "lib"), 0o755)
	os.WriteFile(filepath.Join(dir, ".venv", "lib", "site.py"), []byte("x = 1\ny = 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package main\n"), 0o644)

	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	var r report.Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if len(r.Languages) != 1 || r.Languages[0].Name != "go" || r.Languages[0].Files != 1 || r.Inputs.FilesTotal != 1 {
		t.Errorf("languages %+v filesTotal %d, want only the committed main.go", r.Languages, r.Inputs.FilesTotal)
	}
	if r.Repository.Dirty {
		t.Errorf("clean work tree reported dirty: %+v", r.Repository)
	}

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println() }\n"), 0o644)
	stdout.Reset()
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	r = report.Report{}
	json.Unmarshal(stdout.Bytes(), &r)
	if !r.Repository.Dirty || r.Repository.ModifiedFiles != 1 {
		t.Errorf("modified main.go not reported: %+v", r.Repository)
	}
	stdout.Reset()
	run([]string{"scan", dir}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "1 files modified") {
		t.Errorf("text output does not say the work tree differs:\n%s", stdout.String())
	}
}

func TestScanUnbornHead(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--format", "json", dir}, &stdout, &stderr); code != report.ExitIncomplete {
		t.Fatalf("exit %d, want %d; stderr %s", code, report.ExitIncomplete, stderr.String())
	}
	if strings.Contains(stdout.String(), `"head"`) {
		t.Errorf("unborn branch reported a head:\n%s", stdout.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestScanReportWriteFailureIsError(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"scan", gitRepo(t)}, failingWriter{}, &stderr); code != report.ExitError {
		t.Errorf("exit %d, want %d when the report cannot be written", code, report.ExitError)
	}
}

// -o refuses a symlink or FIFO planted at the output path instead of writing through it or blocking.
func TestScanOutputDoesNotFollowSymlink(t *testing.T) {
	dir := gitRepo(t)
	outDir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	link := filepath.Join(outDir, "report.json")
	os.Symlink(victim, link)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "--format", "json", "-o", link, dir}, &stdout, &stderr); code != report.ExitError {
		t.Errorf("-o onto a symlink: exit %d, want %d", code, report.ExitError)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("-o wrote through a symlink into its target: %q", b)
	}

	fifo := filepath.Join(outDir, "fifo.json")
	syscall.Mkfifo(fifo, 0o644)
	done := make(chan int, 1)
	go func() { done <- run([]string{"scan", "-o", fifo, dir}, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != report.ExitError {
			t.Errorf("-o to a FIFO: exit %d, want %d", code, report.ExitError)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("-o blocked on a FIFO")
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 2 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestScanUnreadableFileKeepsPathsOut(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	dir := gitRepo(t)
	// The committed main.go becomes unreadable in the work tree.
	if err := os.Chmod(filepath.Join(dir, "main.go"), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"scan", "--format", "json", dir}, &stdout, &stderr)
	if strings.Contains(stdout.String(), dir) {
		t.Errorf("report contains the absolute path:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"main.go"`) || !strings.Contains(stdout.String(), "permission denied") {
		t.Errorf("unreadable file not listed in skipped:\n%s", stdout.String())
	}
}
