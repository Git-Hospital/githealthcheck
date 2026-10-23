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
// on this machine; without it a passing treatment could mean the attack never worked at all. Where a
// defence has two layers, each layer is also tested alone.

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

// raw runs git with the hardened global options but bypasses gitArgs' argument policy, to prove that
// the -c overrides hold even when a flag that re-enables an attack slips through.
func raw(t *testing.T, g *Git, args ...string) {
	t.Helper()
	_, _ = g.runner.Run(context.Background(), "git", append(gitPrefix(g.root), args...)...)
}

func control(t *testing.T, marker string, what string) {
	t.Helper()
	if !ran(marker) {
		t.Fatalf("control failed: %s did not run the planted program, so this test proves nothing", what)
	}
	os.Remove(marker)
}

func treatment(t *testing.T, marker string, what string) {
	t.Helper()
	if ran(marker) {
		t.Fatalf("%s ran the program planted in the scanned repository", what)
	}
}

func TestFSMonitorDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "core.fsmonitor", script)

	_, _ = plainGit(t, dir, "status")
	control(t, marker, "plain git status")

	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log", "--format=%H", "--numstat"); err != nil {
		t.Fatal(err)
	}
	raw(t, g, "status")
	treatment(t, marker, "hardened git (log, and status under the same -c options)")
}

func TestTextconvDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, `cat "$1"`)
	mustGit(t, dir, "config", "diff.evil.textconv", script)
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("*.txt diff=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = plainGit(t, dir, "log", "-p")
	control(t, marker, "plain git log -p")

	g := newGit(t, dir)
	for _, args := range [][]string{{"--format=%H", "-p"}, {"--format=%H", "--numstat"}} {
		if _, err := g.Run(context.Background(), "log", args...); err != nil {
			t.Fatal(err)
		}
	}
	treatment(t, marker, "hardened git log -p / --numstat")
}

func TestExternalDiffDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "diff.external", script)

	_, _ = plainGit(t, dir, "log", "-p", "--ext-diff")
	control(t, marker, "plain git log -p --ext-diff")

	// --ext-diff itself is refused by gitArgs; raw passes it anyway, so this proves that
	// -c diff.external= holds on its own.
	g := newGit(t, dir)
	raw(t, g, "log", "--format=%H", "-p", "--ext-diff")
	treatment(t, marker, "hardened git log -p --ext-diff")
}

// signHead rewrites HEAD as a commit with a (fake) PGP signature header, so verifying it calls
// gpg.program.
func signHead(t *testing.T, dir string) {
	t.Helper()
	rawCommit := mustGit(t, dir, "cat-file", "commit", "HEAD")
	head, msg, _ := strings.Cut(rawCommit, "\n\n")
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

func signedRepo(t *testing.T) (dir, marker string) {
	t.Helper()
	dir = newRepo(t)
	signHead(t, dir)
	script, marker := markerScript(t, "exit 1")
	mustGit(t, dir, "config", "gpg.program", script)
	return dir, marker
}

func TestShowSignatureProgramDoesNotRun(t *testing.T) {
	dir, marker := signedRepo(t)
	mustGit(t, dir, "config", "log.showSignature", "true")

	_, _ = plainGit(t, dir, "log")
	control(t, marker, "plain git log with log.showSignature")

	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log", "--format=%H"); err != nil {
		t.Fatal(err)
	}
	raw(t, g, "log", "--show-signature")
	treatment(t, marker, "hardened git log (and --show-signature under the same -c options)")
}

func TestPrettyFormatSignatureDoesNotRun(t *testing.T) {
	for name, cfg := range map[string][][2]string{
		"format.pretty":   {{"format.pretty", "format:%H %G?"}},
		"pretty alias":    {{"pretty.x", "format:%H %GS"}, {"format.pretty", "x"}},
		"format with %GK": {{"format.pretty", "format:%GK"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, marker := signedRepo(t)
			for _, kv := range cfg {
				mustGit(t, dir, "config", kv[0], kv[1])
			}

			_, _ = plainGit(t, dir, "log")
			control(t, marker, "plain git log with "+name)

			g := newGit(t, dir)
			if _, err := g.Run(context.Background(), "log", "--format=%H", "--numstat"); err != nil {
				t.Fatal(err)
			}
			// Without --format, log uses the repository's format.pretty; gpg.program=false must
			// still stop the verification.
			raw(t, g, "log")
			treatment(t, marker, "hardened git log with "+name)
		})
	}
}

// partialClone makes dir look like a partial clone whose promisor remote is an ext:: command, with
// the blob of a.txt at HEAD missing, so reading HEAD's diff needs a lazy fetch.
func partialClone(t *testing.T) (dir, marker string) {
	t.Helper()
	dir = newRepo(t)
	_, marker = markerScript(t, "")
	blob := mustGit(t, dir, "rev-parse", "HEAD:a.txt")
	if err := os.Remove(filepath.Join(dir, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "config", "core.repositoryformatversion", "1")
	mustGit(t, dir, "config", "extensions.partialClone", "origin")
	mustGit(t, dir, "config", "remote.origin.promisor", "true")
	mustGit(t, dir, "config", "remote.origin.url", "ext::sh -c touch% "+marker)
	// A repository's protocol.<name>.allow beats the command-line protocol.allow=never.
	mustGit(t, dir, "config", "protocol.ext.allow", "always")
	return dir, marker
}

func without(env []string, key string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
}

func TestLazyFetchDoesNotRun(t *testing.T) {
	dir, marker := partialClone(t)
	_, _ = plainGit(t, dir, "log", "--numstat")
	control(t, marker, "plain git log --numstat in a partial clone")

	g := newGit(t, dir)
	_, _ = g.Run(context.Background(), "log", "--format=%H", "--numstat")
	treatment(t, marker, "hardened git log --numstat")

	// Each guard alone, as on a git that ignores the other one.
	base := g.runner.Env
	g.runner.Env = without(base, "GIT_NO_LAZY_FETCH")
	args := slices.DeleteFunc(gitArgs(g.root, "log", []string{"--format=%H", "--numstat"}),
		func(a string) bool { return a == "--no-lazy-fetch" })
	_, _ = g.runner.Run(context.Background(), "git", args...)
	treatment(t, marker, "GIT_ALLOW_PROTOCOL=none alone")

	g.runner.Env = without(without(base, "GIT_NO_LAZY_FETCH"), "GIT_ALLOW_PROTOCOL")
	_, _ = g.Run(context.Background(), "log", "--format=%H", "--numstat")
	treatment(t, marker, "--no-lazy-fetch alone")
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
	if _, err := NewGit(context.Background(), t.TempDir()); err == nil {
		t.Error("NewGit accepted a directory that is not a repository")
	}
}

// A planted .git file, .git symlink, alternates or commondir would let the scan read another
// repository on the machine; the report would then carry that repository's HEAD and history.
func TestNewGitRejectsForeignGitDir(t *testing.T) {
	victim := newRepo(t)
	cases := map[string]func(dir string){
		"gitfile": func(dir string) {
			os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+filepath.Join(victim, ".git")+"\n"), 0o644)
		},
		"symlink": func(dir string) {
			os.Symlink(filepath.Join(victim, ".git"), filepath.Join(dir, ".git"))
		},
		"alternates": func(dir string) {
			mustGit(t, dir, "init", "-q")
			os.WriteFile(filepath.Join(dir, ".git", "objects", "info", "alternates"),
				[]byte(filepath.Join(victim, ".git", "objects")+"\n"), 0o644)
		},
		"commondir": func(dir string) {
			mustGit(t, dir, "init", "-q")
			os.WriteFile(filepath.Join(dir, ".git", "commondir"), []byte(filepath.Join(victim, ".git")+"\n"), 0o644)
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			plant(dir)
			g, err := NewGit(context.Background(), dir)
			if err == nil {
				head, _ := g.Run(context.Background(), "rev-parse", "HEAD")
				t.Fatalf("NewGit accepted a repository whose git directory is redirected (HEAD %s)", head)
			}
			// A .git that is not a directory is refused before git runs, with a message that says why.
			if (name == "gitfile" || name == "symlink") && !strings.Contains(err.Error(), "no .git directory") {
				t.Errorf("error %q does not explain that .git must be a directory", err)
			}
		})
	}
}

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("gitArgs allowed %s", what)
		}
	}()
	f()
}

func TestGitArgsPolicy(t *testing.T) {
	for _, sub := range []string{"status", "diff", "fetch", "checkout", "config", "version"} {
		mustPanic(t, "subcommand "+sub, func() { gitArgs("/r", sub, nil) })
	}
	for _, args := range [][]string{
		{"--format=%H", "--ext-diff"},
		{"--format=%H", "--textconv"},
		{"--format=%H", "--show-signature"},
		{"--format=%H", "--output=/tmp/x"},
		{"--format=%H", "--pretty=%G?"},
		{"--format=%H", "--oneline"},
		{"--format=%H %G?"},
		{"--format=%GK"},
		{"--numstat"},
		{"--format=%H", "--format=%an"},
	} {
		mustPanic(t, "log "+strings.Join(args, " "), func() { gitArgs("/r", "log", args) })
	}
	got := gitArgs("/r", "log", []string{"--format=%H", "--numstat"})
	if got[0] != "--no-lazy-fetch" {
		t.Errorf("args do not start with --no-lazy-fetch: %q", got)
	}
}

func TestCheckGitVersion(t *testing.T) {
	for out, ok := range map[string]bool{
		"git version 2.50.1 (Apple Git-155)": true,
		"git version 2.44.0":                 true,
		"git version 3.0.0":                  true,
		"git version 2.43.5":                 false,
		"git version 1.99.0":                 false,
		"something else":                     false,
	} {
		if err := checkGitVersion(out); (err == nil) != ok {
			t.Errorf("checkGitVersion(%q) = %v, want ok=%v", out, err, ok)
		}
	}
}

func TestGitEnv(t *testing.T) {
	env := gitEnv([]string{"PATH=/bin", "GIT_DIR=/elsewhere", "GIT_EXTERNAL_DIFF=/x", "GITHUB_TOKEN=t"}, "/r/repo")
	for _, kv := range env {
		for _, bad := range []string{"GIT_DIR=", "GIT_EXTERNAL_DIFF=", "GITHUB_TOKEN="} {
			if strings.HasPrefix(kv, bad) {
				t.Errorf("env keeps %s", kv)
			}
		}
	}
	for _, want := range []string{"GIT_CEILING_DIRECTORIES=/r", "GIT_ALLOW_PROTOCOL=none", "GIT_NO_LAZY_FETCH=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("env has no %s", want)
		}
	}
}
