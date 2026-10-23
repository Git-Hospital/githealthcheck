package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The scanned repository is untrusted, and so is its .git/config. Settings in a repository's own
// config can run a command or open a connection:
//
//   - core.fsmonitor runs on any index refresh;
//   - diff.external and textconv drivers run on diffs;
//   - gpg.program (and the ssh/x509 variants) runs whenever a signature is verified, which
//     log.showSignature, --show-signature, and the %G placeholders in format.pretty or a
//     pretty.<alias> all trigger;
//   - in a partial clone, a missing object makes git fetch from the promisor remote, whose URL can be
//     ext::<command>, and a repository's protocol.<name>.allow beats a command-line protocol.allow.
//
// Git therefore runs only through Git, which overrides those settings from the command scope, adds
// --no-lazy-fetch and GIT_ALLOW_PROTOCOL=none (two independent guards against any transport), allows
// only read-only subcommands that never refresh the index, requires an explicit --format on log, and
// accepts a repository only when its git directory is a real .git directory inside the root.

// MinGitVersion is the oldest git that understands --no-lazy-fetch. Older git ignores
// GIT_NO_LAZY_FETCH silently, so it is refused instead of run with one guard missing.
var MinGitVersion = [2]int{2, 44}

// gitConfig is passed as -c options before every subcommand.
var gitConfig = []string{
	"core.fsmonitor=false",
	"core.hooksPath=/dev/null",
	"core.pager=cat",
	"diff.external=",
	"log.showSignature=false",
	"gpg.program=false",
	"gpg.ssh.program=false",
	"gpg.x509.program=false",
	"mailmap.file=/dev/null",
	"protocol.allow=never",
	"color.ui=false",
}

// gitSubcommands are the only subcommands Git runs, with the flags inserted after each. None of them
// refreshes the index.
var gitSubcommands = map[string][]string{
	"rev-parse": nil,
	"log":       {"--no-ext-diff", "--no-textconv", "--no-show-signature", "--no-color"},
}

// gitTimeouts bound the quick subcommands, so a planted FIFO in the config cannot stall a run until
// the global timeout. log is bounded by the caller's context only: its time grows with the history.
var gitTimeouts = map[string]time.Duration{
	"version":   30 * time.Second,
	"rev-parse": 30 * time.Second,
}

// deniedArgs undo a safety flag (a later flag wins in git) or write a file.
var deniedArgs = []string{"--ext-diff", "--textconv", "--show-signature", "--output", "--pretty", "--oneline"}

// Git runs read-only git commands against one repository root.
type Git struct {
	root   string
	runner Runner
}

// NewGit returns a Git for the repository whose work tree is exactly root and whose git directory is
// root/.git. Worktrees, submodules and any other setup whose git directory lives elsewhere are refused:
// a planted .git file, symlink, alternates or commondir could otherwise point at another repository on
// the machine, whose history would end up in the report.
func NewGit(ctx context.Context, root string) (*Git, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	g := &Git{root: abs, runner: Runner{Env: gitEnv(os.Environ(), abs), Dir: abs}}

	if err := g.checkVersion(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: timed out while opening the repository: %w", root, err)
		}
		return nil, err
	}
	dotGit := filepath.Join(abs, ".git")
	if fi, err := os.Lstat(dotGit); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s has no .git directory; only a repository root with its own .git directory can be scanned (not a worktree, submodule or bare repository)", root)
	}
	out, err := g.Run(ctx, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: timed out while opening the repository: %w", root, err)
		}
		return nil, fmt.Errorf("%s is not a git repository: %w", root, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		return nil, fmt.Errorf("%s: unexpected rev-parse output %q", root, out)
	}
	top, err := filepath.EvalSymlinks(lines[0])
	if err != nil {
		return nil, err
	}
	if top != abs {
		return nil, fmt.Errorf("%s is inside the repository %s; pass the repository root", root, top)
	}
	for _, dir := range lines[1:] {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(abs, dir)
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || resolved != dotGit {
			return nil, fmt.Errorf("%s: its git directory resolves to %s, outside the repository root", root, dir)
		}
	}
	if _, err := os.Lstat(filepath.Join(dotGit, "objects", "info", "alternates")); err == nil {
		return nil, fmt.Errorf("%s borrows objects from another repository (objects/info/alternates); not supported", root)
	}
	return g, nil
}

var gitVersionRE = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// checkVersion runs plain "git version": with the global options an old git would fail on
// --no-lazy-fetch with a message that does not say what is wrong.
func (g *Git) checkVersion(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, gitTimeouts["version"])
	defer cancel()
	out, err := g.runner.Run(ctx, "git", "version")
	if err != nil {
		return err
	}
	return checkGitVersion(string(out))
}

func checkGitVersion(out string) error {
	m := gitVersionRE.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return fmt.Errorf("cannot read the git version from %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < MinGitVersion[0] || major == MinGitVersion[0] && minor < MinGitVersion[1] {
		return fmt.Errorf("git %d.%d is too old; githealthcheck needs git %d.%d or newer to read untrusted repositories safely",
			major, minor, MinGitVersion[0], MinGitVersion[1])
	}
	return nil
}

// Root returns the repository root as an absolute path with symlinks resolved.
func (g *Git) Root() string { return g.root }

// Run executes an allowed git subcommand with the hardening options. The subcommand's own safety
// flags are inserted right after it, before the caller's arguments.
func (g *Git) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	full := gitArgs(g.root, subcommand, args)
	if d, ok := gitTimeouts[subcommand]; ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	out, err := g.runner.Run(ctx, "git", full...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", subcommand, err)
	}
	return out, nil
}

// gitPrefix is everything before the subcommand: the global options that apply to every call.
func gitPrefix(root string) []string {
	// safe.directory is honoured from the command scope. It names only this root, so a container or
	// CI runner with a different uid can read the repository without trusting every directory. That
	// is safe only because every config that could run a program is overridden below.
	p := []string{"--no-lazy-fetch", "-c", "safe.directory=" + root}
	for _, kv := range gitConfig {
		p = append(p, "-c", kv)
	}
	return p
}

func gitArgs(root, subcommand string, args []string) []string {
	safety, ok := gitSubcommands[subcommand]
	if !ok {
		// A programming error, not a runtime condition: fail loudly in tests.
		panic("execx: git subcommand not allowed: " + subcommand)
	}
	formats := 0
	for _, a := range args {
		for _, bad := range deniedArgs {
			if a == bad || strings.HasPrefix(a, bad+"=") {
				panic("execx: git argument not allowed: " + a)
			}
		}
		if strings.HasPrefix(a, "--format=") {
			formats++
			if strings.Contains(a, "%G") {
				panic("execx: signature placeholders are not allowed in " + a)
			}
		}
	}
	// Without an explicit --format, log would use the repository's format.pretty, which can name a
	// pretty.<alias> with %G placeholders that run gpg.program.
	if subcommand == "log" && formats != 1 {
		panic("execx: git log needs exactly one explicit --format=")
	}
	full := append(gitPrefix(root), subcommand)
	full = append(full, safety...)
	return append(full, args...)
}

// gitEnv is the reduced environment plus the variables that keep git away from system and global
// config, terminals, optional locks, lazy fetches, every transport, and parent directories.
func gitEnv(environ []string, root string) []string {
	return append(Env(environ),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=none",
		"GIT_PAGER=cat",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(root),
	)
}
