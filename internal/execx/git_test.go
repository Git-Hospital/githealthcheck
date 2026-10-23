package execx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Each attack test is a control/treatment pair. The control proves the attack works with plain git
// on this machine; without it a passing treatment could mean the attack never worked at all.

// plainGit runs git without any hardening, but with the user's own config out of the way.
func plainGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(Env(os.Environ()),
		"HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := plainGit(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

// newRepo creates a repository with two commits that change a.txt.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	for i, body := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGit(t, dir, "add", "a.txt")
		mustGit(t, dir, "commit", "-q", "-m", []string{"first", "second"}[i])
	}
	return dir
}

// markerScript writes an executable that creates marker and then runs tail (shell text).
func markerScript(t *testing.T, tail string) (script, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	script = filepath.Join(dir, "evil.sh")
	body := "#!/bin/sh\ntouch '" + marker + "'\n" + tail + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func ran(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

func newGit(t *testing.T, dir string) *Git {
	t.Helper()
	g, err := NewGit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestFSMonitorDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "core.fsmonitor", script)

	// Control: an index refresh runs the repository's fsmonitor command.
	_, _ = plainGit(t, dir, "status")
	if !ran(marker) {
		t.Skip("this git does not run core.fsmonitor on status; the attack cannot be tested here")
	}
	os.Remove(marker)

	// Treatment: everything Git is allowed to run, plus status under the same -c options.
	g := newGit(t, dir)
	for _, args := range [][]string{{"log", "--numstat"}, {"rev-parse", "HEAD"}} {
		if _, err := g.Run(context.Background(), args[0], args[1:]...); err != nil {
			t.Fatal(err)
		}
	}
	prefix := gitArgs(g.root, "rev-parse", nil)
	prefix = prefix[:len(prefix)-1]
	if _, err := g.runner.Run(context.Background(), "git", append(prefix, "status")...); err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("core.fsmonitor from the scanned repository ran")
	}
}

func TestTextconvDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, `cat "$1"`)
	mustGit(t, dir, "config", "diff.evil.textconv", script)
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("*.txt diff=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = plainGit(t, dir, "log", "-p")
	if !ran(marker) {
		t.Fatal("control failed: plain git log -p did not run the textconv driver")
	}
	os.Remove(marker)

	g := newGit(t, dir)
	for _, args := range [][]string{{"log", "-p"}, {"log", "--numstat"}} {
		if _, err := g.Run(context.Background(), args[0], args[1:]...); err != nil {
			t.Fatal(err)
		}
	}
	if ran(marker) {
		t.Fatal("textconv driver from the scanned repository ran")
	}
}

func TestExternalDiffDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "diff.external", script)

	_, _ = plainGit(t, dir, "log", "-p", "--ext-diff")
	if !ran(marker) {
		t.Fatal("control failed: plain git log -p --ext-diff did not run diff.external")
	}
	os.Remove(marker)

	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log", "-p"); err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("diff.external from the scanned repository ran")
	}
}

// signHead rewrites HEAD as a commit with a (fake) PGP signature header, so log --show-signature
// calls gpg.program for it.
func signHead(t *testing.T, dir string) {
	t.Helper()
	raw := mustGit(t, dir, "cat-file", "commit", "HEAD")
	head, msg, _ := strings.Cut(raw, "\n\n")
	sig := "gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE\n -----END PGP SIGNATURE-----"
	obj := head + "\n" + sig + "\n\n" + msg + "\n"
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(obj)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "update-ref", "HEAD", strings.TrimSpace(string(out)))
}

func TestSignatureProgramDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	signHead(t, dir)
	script, marker := markerScript(t, "exit 1")
	mustGit(t, dir, "config", "gpg.program", script)
	mustGit(t, dir, "config", "log.showSignature", "true")

	_, _ = plainGit(t, dir, "log")
	if !ran(marker) {
		t.Fatal("control failed: plain git log did not run gpg.program")
	}
	os.Remove(marker)

	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log"); err != nil {
		t.Fatal(err)
	}
	if ran(marker) {
		t.Fatal("gpg.program from the scanned repository ran")
	}
}

func TestNewGitRejectsNonRoot(t *testing.T) {
	dir := newRepo(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGit(context.Background(), sub); err == nil {
		t.Error("NewGit accepted a subdirectory of a repository")
	}

	// A plain directory inside a repository must not resolve to the parent repository.
	plain := filepath.Join(dir, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGit(context.Background(), plain); err == nil {
		t.Error("NewGit used the parent repository of a plain directory")
	}

	if _, err := NewGit(context.Background(), t.TempDir()); err == nil {
		t.Error("NewGit accepted a directory that is not a repository")
	}
}

func TestGitArgsRejectsOtherSubcommands(t *testing.T) {
	for _, sub := range []string{"status", "diff", "fetch", "checkout", "config"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("gitArgs allowed %q", sub)
				}
			}()
			gitArgs("/r", sub, nil)
		}()
	}
	for _, arg := range []string{"--ext-diff", "--textconv", "--show-signature", "--git-dir=/x"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("gitArgs allowed the argument %q", arg)
				}
			}()
			gitArgs("/r", "log", []string{arg})
		}()
	}
}

func TestGitEnvDropsCallerGitVariables(t *testing.T) {
	env := gitEnv([]string{"PATH=/bin", "GIT_DIR=/elsewhere", "GIT_EXTERNAL_DIFF=/x", "GITHUB_TOKEN=t"}, "/r/repo")
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"GIT_DIR=", "GIT_EXTERNAL_DIFF=", "GITHUB_TOKEN="} {
		if strings.Contains(joined, bad) {
			t.Errorf("env keeps %s", bad)
		}
	}
	if !slices.Contains(env, "GIT_CEILING_DIRECTORIES=/r") {
		t.Errorf("env has no ceiling at the parent of the root:\n%s", joined)
	}
}
