package execx

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Each attack test is a control/treatment pair. The control proves the attack works with plain git
// on this machine; without it a passing treatment could mean the attack never worked at all. Where a
// defence has several layers that each stop the attack, each layer is also tested alone.

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

// prefix is gitPrefix without the -c options whose key is in drop, to test one layer of a defence
// with the other layers removed.
func prefix(g *Git, drop ...string) []string {
	var p []string
	all := gitPrefix(g.root)
	for i := 0; i < len(all); i++ {
		if all[i] == "-c" && slices.ContainsFunc(drop, func(k string) bool { return strings.HasPrefix(all[i+1], k+"=") }) {
			i++
			continue
		}
		p = append(p, all[i])
	}
	return p
}

// raw runs git with the hardened global options but none of gitArgs' policy or inserted flags, to
// prove that the -c overrides hold even when a flag that re-enables an attack slips through.
func raw(t *testing.T, g *Git, args ...string) {
	t.Helper()
	_, _ = g.runner.Run(context.Background(), "git", append(gitPrefix(g.root), args...)...)
}

// rawLog runs log with the hardened prefix minus drop and the flags gitArgs inserts after log, but
// without the argument allowlist (so -p can be used).
func rawLog(g *Git, drop []string, args ...string) (string, error) {
	full := append(prefix(g, drop...), "log")
	full = append(full, gitSubcommands["log"]...)
	out, err := g.runner.Run(context.Background(), "git", append(full, args...)...)
	return string(out), err
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

const hashFormat = "--format=format:%H"

func TestFSMonitorDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "core.fsmonitor", script)

	_, _ = plainGit(t, dir, "status")
	control(t, marker, "plain git status")

	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log", hashFormat, "--numstat"); err != nil {
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

	// -p is outside the allowlist; --no-textconv must hold on its own even with it.
	g := newGit(t, dir)
	if _, err := g.Run(context.Background(), "log", hashFormat, "--numstat"); err != nil {
		t.Fatal(err)
	}
	if _, err := rawLog(g, nil, hashFormat, "-p"); err != nil {
		t.Fatal(err)
	}
	treatment(t, marker, "hardened git log -p / --numstat")
}

func TestExternalDiffDoesNotRun(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, "")
	mustGit(t, dir, "config", "diff.external", script)

	_, _ = plainGit(t, dir, "log", "-p", "--ext-diff")
	control(t, marker, "plain git log -p --ext-diff")

	g := newGit(t, dir)
	// -c diff.external= alone: --ext-diff after the hardened prefix, without --no-ext-diff.
	raw(t, g, "log", hashFormat, "-p", "--ext-diff")
	treatment(t, marker, "-c diff.external= alone")
	// --no-ext-diff alone: the repository's diff.external is left in place.
	_, _ = rawLog(g, []string{"diff.external"}, hashFormat, "-p")
	treatment(t, marker, "--no-ext-diff alone")
}

// signHead rewrites HEAD as a commit with a fake signature in the given armour, so verifying it calls
// the matching gpg.*.program.
func signHead(t *testing.T, dir, armour string) {
	t.Helper()
	rawCommit := mustGit(t, dir, "cat-file", "commit", "HEAD")
	head, msg, _ := strings.Cut(rawCommit, "\n\n")
	sig := "gpgsig -----BEGIN " + armour + "-----\n \n iQEzBAABCAAdFiEE\n -----END " + armour + "-----"
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

// signers are the signature formats git verifies, each with the program setting it calls.
var signers = []struct{ name, armour, program string }{
	{"openpgp", "PGP SIGNATURE", "gpg.program"},
	{"ssh", "SSH SIGNATURE", "gpg.ssh.program"},
	{"x509", "SIGNED MESSAGE", "gpg.x509.program"},
}

func signedRepo(t *testing.T, armour, program string) (dir, marker string) {
	t.Helper()
	dir = newRepo(t)
	signHead(t, dir, armour)
	script, marker := markerScript(t, "exit 1")
	mustGit(t, dir, "config", program, script)
	// ssh verification needs an allowed-signers file before it calls the program.
	allowed := filepath.Join(t.TempDir(), "allowed")
	os.WriteFile(allowed, []byte("a@example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA\n"), 0o644)
	mustGit(t, dir, "config", "gpg.ssh.allowedSignersFile", allowed)
	return dir, marker
}

func TestShowSignatureProgramDoesNotRun(t *testing.T) {
	for _, s := range signers {
		t.Run(s.name, func(t *testing.T) {
			dir, marker := signedRepo(t, s.armour, s.program)
			mustGit(t, dir, "config", "log.showSignature", "true")

			_, _ = plainGit(t, dir, "log")
			control(t, marker, "plain git log with log.showSignature")

			g := newGit(t, dir)
			if _, err := g.Run(context.Background(), "log", hashFormat); err != nil {
				t.Fatal(err)
			}
			treatment(t, marker, "hardened git log")
			// Each layer alone.
			raw(t, g, "log", "--show-signature")
			treatment(t, marker, "-c "+s.program+" alone (with --show-signature)")
			_, _ = g.runner.Run(context.Background(), "git", append(prefix(g, s.program), "log", hashFormat)...)
			treatment(t, marker, "-c log.showSignature=false alone")
			_, _ = rawLog(g, []string{s.program, "log.showSignature"}, hashFormat)
			treatment(t, marker, "--no-show-signature alone")
		})
	}
}

func TestPrettyFormatSignatureDoesNotRun(t *testing.T) {
	for name, cfg := range map[string][][2]string{
		"format.pretty":   {{"format.pretty", "format:%H %G?"}},
		"pretty alias":    {{"pretty.x", "format:%H %GS"}, {"format.pretty", "x"}},
		"format with %GK": {{"format.pretty", "format:%GK"}},
		"modifier %+G?":   {{"format.pretty", "format:%+G?"}},
	} {
		for _, s := range signers {
			t.Run(name+"/"+s.name, func(t *testing.T) {
				dir, marker := signedRepo(t, s.armour, s.program)
				for _, kv := range cfg {
					mustGit(t, dir, "config", kv[0], kv[1])
				}

				_, _ = plainGit(t, dir, "log")
				control(t, marker, "plain git log with "+name)

				g := newGit(t, dir)
				if _, err := g.Run(context.Background(), "log", hashFormat, "--numstat"); err != nil {
					t.Fatal(err)
				}
				// Without --format, log uses the repository's format.pretty; the program override
				// must still stop the verification.
				raw(t, g, "log")
				treatment(t, marker, "hardened git log with "+name)
			})
		}
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
	_, _ = g.Run(context.Background(), "log", hashFormat, "--numstat")
	treatment(t, marker, "hardened git log --numstat")

	// Each guard alone, as on a git that ignores the other one.
	base := g.runner.Env
	g.runner.Env = without(base, "GIT_NO_LAZY_FETCH")
	args := slices.DeleteFunc(gitArgs(g.root, "log", []string{hashFormat, "--numstat"}),
		func(a string) bool { return a == "--no-lazy-fetch" })
	_, _ = g.runner.Run(context.Background(), "git", args...)
	treatment(t, marker, "GIT_ALLOW_PROTOCOL=none alone")

	g.runner.Env = without(without(base, "GIT_NO_LAZY_FETCH"), "GIT_ALLOW_PROTOCOL")
	_, _ = g.Run(context.Background(), "log", hashFormat, "--numstat")
	treatment(t, marker, "--no-lazy-fetch alone")
}

// stalls reports whether git, run as plain git with args, blocks for 2 seconds: the control for a
// planted FIFO.
func stalls(t *testing.T, dir string, args ...string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(Env(os.Environ()), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1")
	_ = cmd.Run()
	return ctx.Err() != nil
}

func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Files that git reads during log, pointed at a FIFO that never gets a writer. Plain git blocks on
// each; the hardened log must finish. (Pointed at /dev/zero instead, the same files make git allocate
// gigabytes per second, which is why they are not used as controls here.)
func TestLogDoesNotBlockOnPlantedFIFOs(t *testing.T) {
	cases := map[string]struct {
		plant func(t *testing.T, dir, fifo string)
		args  []string
	}{
		"diff.orderFile": {
			func(t *testing.T, dir, fifo string) { mustGit(t, dir, "config", "diff.orderFile", fifo) },
			[]string{"log", "--numstat"},
		},
		"core.attributesFile": {
			func(t *testing.T, dir, fifo string) { mustGit(t, dir, "config", "core.attributesFile", fifo) },
			[]string{"log", "-p"},
		},
		".gitattributes in the work tree": {
			func(t *testing.T, dir, _ string) { mkfifo(t, filepath.Join(dir, ".gitattributes")) },
			[]string{"log", "-p"},
		},
		".mailmap in the work tree": {
			func(t *testing.T, dir, _ string) { mkfifo(t, filepath.Join(dir, ".mailmap")) },
			[]string{"log"},
		},
		"mailmap.file": {
			func(t *testing.T, dir, fifo string) { mustGit(t, dir, "config", "mailmap.file", fifo) },
			[]string{"log"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newRepo(t)
			fifo := filepath.Join(t.TempDir(), "fifo")
			mkfifo(t, fifo)
			c.plant(t, dir, fifo)
			if !stalls(t, dir, c.args...) {
				t.Fatalf("control failed: plain git %v did not block, so this test proves nothing", c.args)
			}

			g := newGit(t, dir)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, args := range [][]string{{hashFormat + " %an %ae", "--numstat"}, {hashFormat, "--name-status", "--find-renames=100%"}} {
				if _, err := g.Run(ctx, "log", args...); err != nil {
					t.Fatalf("hardened git log %v: %v", args, err)
				}
			}

			// Plain git opens the mailmap even for %an. Three layers each keep it closed.
			if !strings.Contains(name, "mailmap") {
				return
			}
			noFlag := slices.DeleteFunc(slices.Clone(gitSubcommands["log"]), func(a string) bool { return a == "--no-mailmap" })
			layers := map[string][]string{
				"--no-mailmap alone":           append(append(prefix(g, "log.mailmap", "mailmap.file"), "log"), gitSubcommands["log"]...),
				"log.mailmap=false alone":      append(append(prefix(g, "mailmap.file"), "log"), noFlag...),
				"mailmap.file=/dev/null alone": append(append(prefix(g, "log.mailmap"), "log"), noFlag...),
			}
			for layer, args := range layers {
				if layer == "mailmap.file=/dev/null alone" && name != "mailmap.file" {
					continue // it only replaces the configured file, not the work tree's .mailmap
				}
				if _, err := g.runner.Run(ctx, "git", append(args, hashFormat)...); err != nil {
					t.Errorf("%s: %v", layer, err)
				}
			}
		})
	}
}

func TestHistoryIsNotRewrittenOrRemapped(t *testing.T) {
	dir := newRepo(t)
	// info/grafts cuts HEAD off from its parent; a replace ref swaps HEAD for its parent; .mailmap
	// renames the author.
	head := mustGit(t, dir, "rev-parse", "HEAD")
	parent := mustGit(t, dir, "rev-parse", "HEAD~1")
	os.WriteFile(filepath.Join(dir, ".git", "info", "grafts"), []byte(head+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".mailmap"), []byte("Mapped <m@example.com> <a@example.com>\n"), 0o644)
	if out := mustGit(t, dir, "-c", "advice.graftFileDeprecated=false", "log", "--format=%H %aN"); strings.Count(out, "\n") != 0 || !strings.Contains(out, "Mapped") {
		t.Fatalf("control failed: plain git log shows %q, want one mapped commit", out)
	}
	os.Remove(filepath.Join(dir, ".git", "info", "grafts"))
	mustGit(t, dir, "replace", head, parent)
	if out := mustGit(t, dir, "log", "-1", "--format=%s"); out != "first" {
		t.Fatalf("control failed: replace ref not in effect, HEAD subject %q", out)
	}
	os.WriteFile(filepath.Join(dir, ".git", "info", "grafts"), []byte(head+"\n"), 0o644)

	g := newGit(t, dir)
	out, err := g.Run(context.Background(), "log", "--format=format:%H %s %an")
	if err != nil {
		t.Fatal(err)
	}
	want := head + " second a\n" + parent + " first a"
	if string(out) != want {
		t.Errorf("hardened log = %q, want %q", out, want)
	}
}

// Above core.bigFileThreshold git counts a blob as binary without inflating it; a repository's own
// threshold of 512m would let a 1 MB .git inflate to gigabytes.
func TestBigBlobsAreNotInflated(t *testing.T) {
	dir := newRepo(t)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("a\n", 1<<20)), 0o644)
	mustGit(t, dir, "add", "big.txt")
	mustGit(t, dir, "commit", "-q", "-m", "big")
	mustGit(t, dir, "config", "core.bigFileThreshold", "512m")
	if out := mustGit(t, dir, "log", "-1", "--numstat", "--format="); !strings.HasPrefix(out, "1048576\t0") {
		t.Fatalf("control failed: plain numstat %q", out)
	}
	g := newGit(t, dir)
	out, err := g.Run(context.Background(), "log", "--max-count=1", "--numstat", "--format=format:")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "-\t-\tbig.txt") {
		t.Errorf("hardened numstat = %q, want big.txt counted as binary", out)
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
	if _, err := NewGit(context.Background(), t.TempDir()); err == nil {
		t.Error("NewGit accepted a directory that is not a repository")
	}
}

// Each case plants something that would make the scan read another repository on the machine (whose
// HEAD and history would end up in the report), or block or read without end. The want string is part
// of the error, to check the case is refused by the guard meant for it.
func TestNewGitRejectsForeignGitDir(t *testing.T) {
	victim := newRepo(t)
	vgit := filepath.Join(victim, ".git")
	initRepo := func(t *testing.T, dir string) string {
		mustGit(t, dir, "init", "-q")
		return filepath.Join(dir, ".git")
	}
	swap := func(t *testing.T, dotGit, rel, target string) {
		p := filepath.Join(dotGit, rel)
		os.RemoveAll(p)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]struct {
		plant func(t *testing.T, dir string)
		want  string
	}{
		"gitfile": {func(t *testing.T, dir string) {
			os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+vgit+"\n"), 0o644)
		}, "no .git directory"},
		"symlink": {func(t *testing.T, dir string) {
			os.Symlink(vgit, filepath.Join(dir, ".git"))
		}, "no .git directory"},
		"alternates": {func(t *testing.T, dir string) {
			os.WriteFile(filepath.Join(initRepo(t, dir), "objects", "info", "alternates"),
				[]byte(filepath.Join(vgit, "objects")+"\n"), 0o644)
		}, "alternates"},
		"commondir": {func(t *testing.T, dir string) {
			os.WriteFile(filepath.Join(initRepo(t, dir), "commondir"), []byte(vgit+"\n"), 0o644)
		}, "outside the repository root"},
		"core.worktree": {func(t *testing.T, dir string) {
			initRepo(t, dir)
			mustGit(t, dir, "config", "core.worktree", victim)
		}, "work tree is"},
		"objects symlink": {func(t *testing.T, dir string) {
			swap(t, initRepo(t, dir), "objects", filepath.Join(vgit, "objects"))
		}, "objects is a symlink"},
		"refs symlink": {func(t *testing.T, dir string) {
			swap(t, initRepo(t, dir), "refs", filepath.Join(vgit, "refs"))
		}, "refs is a symlink"},
		"HEAD symlink": {func(t *testing.T, dir string) {
			swap(t, initRepo(t, dir), "HEAD", filepath.Join(vgit, "HEAD"))
		}, "HEAD is a symlink"},
		"grafts to /dev/zero": {func(t *testing.T, dir string) {
			swap(t, initRepo(t, dir), "info/grafts", "/dev/zero")
		}, "grafts is a symlink"},
		"config FIFO": {func(t *testing.T, dir string) {
			p := filepath.Join(initRepo(t, dir), "config")
			os.Remove(p)
			mkfifo(t, p)
		}, "config is a FIFO"},
		"info/attributes FIFO": {func(t *testing.T, dir string) {
			mkfifo(t, filepath.Join(initRepo(t, dir), "info", "attributes"))
		}, "attributes is a FIFO"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			c.plant(t, dir)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			g, err := NewGit(ctx, dir)
			if err == nil {
				head, _ := g.Run(context.Background(), "rev-parse", "--verify", "HEAD")
				t.Fatalf("NewGit accepted it (HEAD %s)", head)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err, c.want)
			}
			if strings.Contains(err.Error(), "timed out") {
				t.Errorf("refused only by the timeout: %v", err)
			}
		})
	}
}

func TestNewGitSHA256(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "--object-format=sha256")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	mustGit(t, dir, "add", "a.txt")
	mustGit(t, dir, "commit", "-q", "-m", "a")
	g := newGit(t, dir)
	out, err := g.Run(context.Background(), "log", hashFormat, "--numstat")
	if err != nil || !strings.Contains(string(out), "1\t0\ta.txt") {
		t.Errorf("log in a sha256 repository: %q %v", out, err)
	}
}

func TestNewGitRefusesOldGit(t *testing.T) {
	dir := newRepo(t)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho 'git version 2.43.0'\n"), 0o755)
	t.Setenv("PATH", bin)
	if _, err := NewGit(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("NewGit with git 2.43: %v, want a too-old error", err)
	}
}

// A container or CI runner often scans a checkout owned by another uid. safe.directory from the
// command scope must let git open it; a repository-level safe.directory=* must not be what does it.
// Needs root to chown; CI runs it with sudo.
func TestSafeDirectoryForOtherOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to give the repository another owner")
	}
	dir := newRepo(t)
	if err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, _ error) error { return os.Lchown(p, 65534, 65534) }); err != nil {
		t.Fatal(err)
	}
	if out, err := plainGit(t, dir, "rev-parse", "HEAD"); err == nil || !strings.Contains(out, "dubious ownership") {
		t.Fatalf("control failed: plain git opened a repository owned by another user: %s", out)
	}
	if _, err := NewGit(context.Background(), dir); err != nil {
		t.Errorf("NewGit refused a repository owned by another user: %v", err)
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
	for _, arg := range []string{"--textconv", "--filters", "-p", "-t", "-s", "-e", "--batch-command",
		"--batch-all-objects", "--follow-symlinks", "--path=a.txt", "--batch=%(rest)", "--allow-unknown-type", "blob", "HEAD:a.txt"} {
		mustPanic(t, "cat-file "+arg, func() { gitArgs("/r", "cat-file", []string{arg}) })
	}
	for _, sub := range []string{"status", "diff", "fetch", "checkout", "config", "version", "show", "hash-object"} {
		mustPanic(t, "subcommand "+sub, func() { gitArgs("/r", sub, nil) })
	}
	for _, args := range [][]string{
		{hashFormat, "--ext-diff"},
		{hashFormat, "--textconv"},
		{hashFormat, "--show-signature"},
		{hashFormat, "--output=/tmp/x"},
		{hashFormat, "--pretty=%G?"},
		{hashFormat, "--oneline"},
		{hashFormat, "-p"},
		{hashFormat, "--patch"},
		{hashFormat, "-u"},
		{hashFormat, "--stat"},
		{hashFormat, "-O/dev/zero"},
		{hashFormat, "--use-mailmap"},
		{hashFormat, "-c"},
		{hashFormat, "--git-dir=/x"},
		{hashFormat, "main"},
		{hashFormat, "HEAD~1"},
		{"--format=format:%H %G?"},
		{"--format=format:%+G?"},
		{"--format=format:%-GS"},
		{"--format=format:% GK"},
		{"--format=tformat:%aN"},
		{"--format=format:%cE"},
		{"--format=format:%aL"},
		{"--format=format:%(describe)"},
		{"--format=%H"},
		{"--format=sig"},
		{"--format=oneline"},
		{"--numstat"},
		{hashFormat, "--format=format:%an"},
		{hashFormat, "-M"},
		{hashFormat, "--find-renames"},
		{hashFormat, "--find-renames=50%"},
		{hashFormat, "-M100%"},
	} {
		mustPanic(t, "log "+strings.Join(args, " "), func() { gitArgs("/r", "log", args) })
	}
	for _, args := range [][]string{{"--show-toplevel", "--git-dir"}, {"HEAD:a.txt"}, {"--verify", "HEAD@{1}"}} {
		mustPanic(t, "rev-parse "+strings.Join(args, " "), func() { gitArgs("/r", "rev-parse", args) })
	}
	for _, args := range [][]string{{"-r", "--format=%(objectname)"}, {"-r", "HEAD:sub"}, {"--long"}} {
		mustPanic(t, "ls-tree "+strings.Join(args, " "), func() { gitArgs("/r", "ls-tree", args) })
	}

	// What the families and NewGit use must stay allowed.
	sha := strings.Repeat("a", 40)
	for sub, args := range map[string][]string{
		"log":       {"--format=format:%H%x00%an%x00%ae%x00%at", "--numstat", "-z", "--no-merges", "--find-renames=100%", "--since=2020-01-01", sha, "--", "a.txt", "-weird-path"},
		"rev-parse": {"--verify", "--quiet", "HEAD"},
		"ls-tree":   {"-r", "-z", "--full-tree", sha},
	} {
		got := gitArgs("/r", sub, args)
		if got[0] != "--no-lazy-fetch" || !slices.Equal(got[len(got)-len(args):], args) {
			t.Errorf("gitArgs(%s) = %q", sub, got)
		}
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
	env := gitEnv([]string{"PATH=/bin::.:bin:/usr/bin", "GIT_DIR=/elsewhere", "GIT_EXTERNAL_DIFF=/x", "GITHUB_TOKEN=t",
		"GIT_CONFIG_PARAMETERS='core.fsmonitor=x'", "GIT_ATTR_SOURCE=HEAD"}, "/r/repo")
	for _, kv := range env {
		for _, bad := range []string{"GIT_DIR=", "GIT_EXTERNAL_DIFF=", "GITHUB_TOKEN=", "GIT_CONFIG_PARAMETERS=", "GIT_ATTR_SOURCE="} {
			if strings.HasPrefix(kv, bad) {
				t.Errorf("env keeps %s", kv)
			}
		}
	}
	want := []string{
		"PATH=/bin:/usr/bin",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=none",
		"GIT_PAGER=cat",
		"GIT_CEILING_DIRECTORIES=/r",
		"GIT_GRAFT_FILE=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
	}
	for _, w := range want {
		if !slices.Contains(env, w) {
			t.Errorf("env has no %s", w)
		}
	}
	if len(env) != len(want) {
		t.Errorf("env = %q, want exactly %q", env, want)
	}
}

// batch feeds names to the hardened cat-file and returns its output.
func batch(t *testing.T, g *Git, mode string, names ...string) (string, error) {
	t.Helper()
	var out []byte
	err := g.Pipe(context.Background(), strings.NewReader(strings.Join(names, "\n")+"\n"), 1<<20, func(r io.Reader) error {
		var err error
		out, err = io.ReadAll(r)
		return err
	}, "cat-file", mode, "--buffer")
	return string(out), err
}

func TestCatFileRunsNoFilterOrTextconv(t *testing.T) {
	dir := newRepo(t)
	script, marker := markerScript(t, `cat`)
	mustGit(t, dir, "config", "diff.evil.textconv", script)
	mustGit(t, dir, "config", "filter.evil.smudge", script)
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("*.txt diff=evil filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = plainGit(t, dir, "cat-file", "--textconv", "HEAD:a.txt")
	control(t, marker, "plain git cat-file --textconv")
	_, _ = plainGit(t, dir, "cat-file", "--filters", "HEAD:a.txt")
	control(t, marker, "plain git cat-file --filters")

	g := newGit(t, dir)
	blob := mustGit(t, dir, "rev-parse", "HEAD:a.txt")
	out, err := batch(t, g, "--batch", blob)
	if err != nil {
		t.Fatal(err)
	}
	if want := blob + " blob 4\ntwo\n\n"; out != want {
		t.Errorf("cat-file --batch = %q, want %q", out, want)
	}
	treatment(t, marker, "hardened git cat-file --batch")
}

func TestCatFileDoesNotFetchLazily(t *testing.T) {
	dir, marker := partialClone(t)
	blob := mustGit(t, dir, "rev-parse", "HEAD:a.txt")
	_, _ = plainGit(t, dir, "cat-file", "-p", blob)
	control(t, marker, "plain git cat-file -p in a partial clone")

	g := newGit(t, dir)
	for _, mode := range []string{"--batch-check", "--batch"} {
		out, err := batch(t, g, mode, blob)
		if err != nil {
			t.Fatal(err)
		}
		if out != blob+" missing\n" {
			t.Errorf("cat-file %s = %q, want the blob missing", mode, out)
		}
		treatment(t, marker, "hardened git cat-file "+mode)
	}
}
