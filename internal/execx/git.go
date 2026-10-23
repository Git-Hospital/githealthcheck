package execx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// Other settings and files make git read without end or block: diff.orderFile, core.attributesFile,
// .gitattributes, .mailmap and info/grafts pointed at /dev/zero or a FIFO, or a blob that inflates a
// thousandfold. And a symlink inside .git (objects, refs) would make git read another repository.
//
// Git therefore runs only through Git, which overrides those settings from the command scope, adds
// --no-lazy-fetch and GIT_ALLOW_PROTOCOL=none (two independent guards against any transport), reads
// attributes from the empty tree only, allows only read-only subcommands that never refresh the index
// and only the arguments it knows, and accepts a repository only when its git directory is a real .git
// directory inside the root that holds nothing but directories and regular files.

// MinGitVersion is the oldest git that understands --no-lazy-fetch. Older git ignores
// GIT_NO_LAZY_FETCH silently, so it is refused instead of run with one guard missing.
var MinGitVersion = [2]int{2, 44}

// noProgram is what the signature programs are set to: an absolute path that does not exist, so
// nothing is looked up on PATH.
const noProgram = "/nonexistent/githealthcheck-disabled"

// gitConfig is passed as -c options before every subcommand. The first group stops programs and
// connections, the second stops unbounded reads, the third pins settings that change the output that
// githealthcheck parses.
var gitConfig = []string{
	"core.fsmonitor=false",
	"core.hooksPath=/dev/null",
	"core.pager=cat",
	"diff.external=",
	"log.showSignature=false",
	"gpg.program=" + noProgram,
	"gpg.ssh.program=" + noProgram,
	"gpg.x509.program=" + noProgram,
	"protocol.allow=never", // a repository can override it per protocol; GIT_ALLOW_PROTOCOL cannot be

	"diff.orderFile=/dev/null",
	"core.attributesFile=/dev/null",
	"mailmap.file=/dev/null",
	"mailmap.blob=",
	"log.mailmap=false",
	"core.bigFileThreshold=1m", // larger blobs count as binary and are never inflated by --numstat

	"color.ui=false",
	"core.quotePath=true",
	"diff.noprefix=false",
	"diff.mnemonicPrefix=false",
	"diff.relative=false",
	"diff.algorithm=myers",
	"diff.renameLimit=1000",
	"i18n.logOutputEncoding=UTF-8",
	"log.showRoot=true",
}

// gitSubcommands are the only subcommands Git runs, with the flags inserted after each. None of them
// refreshes the index.
var gitSubcommands = map[string][]string{
	"rev-parse": nil,
	"log":       {"--no-ext-diff", "--no-textconv", "--no-show-signature", "--no-color", "--no-mailmap"},
}

// gitAllowed are the arguments each subcommand accepts: exact flags, and flags that take a value after
// "=". Anything else, including -p and every patch format (which inflate blobs without limit), panics.
// Revisions must be full object names or HEAD; after "--" come pathspecs.
var gitAllowed = map[string]struct{ exact, prefix []string }{
	"rev-parse": {
		exact: []string{"--show-toplevel", "--absolute-git-dir", "--git-common-dir", "--verify", "--quiet",
			"--is-shallow-repository", "--show-object-format"},
	},
	"log": {
		exact: []string{"--numstat", "--name-status", "--no-merges", "--first-parent", "--no-renames", "-M",
			"--find-renames", "-z", "--reverse", "--topo-order", "--date-order"},
		prefix: []string{"--format=", "--since=", "--until=", "--max-count=", "--diff-filter=", "--find-renames=",
			"--date="},
	},
}

// emptyTree is git's well-known empty tree object for each object format; it exists in every
// repository without being stored.
var emptyTree = map[string]string{
	"sha1":   "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
	"sha256": "6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321",
}

var revisionRE = regexp.MustCompile(`^(HEAD|[0-9a-f]{40}|[0-9a-f]{64})$`)

// formatDenied are placeholders a log format may not use: %G runs gpg.program; the mailmap-aware
// author and committer placeholders read .mailmap, which can be a FIFO; %(describe) walks all tags.
// The optional -, + or space is a placeholder modifier.
var formatDenied = regexp.MustCompile(`%[-+ ]?(G|[ac][NEL]|\(describe)`)

// QuickTimeout bounds version and rev-parse, so a config include that names a FIFO cannot stall a run
// until the global timeout. log is bounded by the caller's context only: its time grows with the
// history, and the files it reads that could block are pinned or refused above.
var QuickTimeout = 30 * time.Second

var quick = map[string]bool{"version": true, "rev-parse": true}

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
	if err := checkGitDir(ctx, dotGit); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s: timed out while opening the repository: %w", root, err)
		}
		return nil, fmt.Errorf("%s: refused: %w", root, err)
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
		// Either root is a subdirectory, or the repository's core.worktree points somewhere else.
		return nil, fmt.Errorf("%s: the repository's work tree is %s, not this directory; pass the repository root", root, top)
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
	// Attributes come from the empty tree, never from the work tree's .gitattributes (a FIFO there
	// blocks every diff). The empty tree's name depends on the object format.
	out, err = g.Run(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	tree, ok := emptyTree[strings.TrimSpace(string(out))]
	if !ok {
		return nil, fmt.Errorf("%s: unknown object format %q", root, strings.TrimSpace(string(out)))
	}
	g.runner.Env = append(g.runner.Env, "GIT_ATTR_SOURCE="+tree)
	return g, nil
}

// checkGitDir refuses a .git that holds anything but directories and regular files. A symlink (objects,
// refs, packed-refs) would make git read another repository; a FIFO or device (config, info/grafts,
// info/attributes) would make it block or read without end. It runs before git reads the config.
func checkGitDir(ctx context.Context, dotGit string) error {
	return filepath.WalkDir(dotGit, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if t := d.Type(); !t.IsDir() && !t.IsRegular() {
			rel, _ := filepath.Rel(filepath.Dir(dotGit), path)
			return fmt.Errorf("%s is a %s; a .git directory may hold only directories and regular files",
				filepath.ToSlash(rel), kind(t))
		}
		return nil
	})
}

func kind(t fs.FileMode) string {
	switch {
	case t&fs.ModeSymlink != 0:
		return "symlink"
	case t&fs.ModeNamedPipe != 0:
		return "FIFO"
	case t&fs.ModeDevice != 0:
		return "device"
	case t&fs.ModeSocket != 0:
		return "socket"
	}
	return "special file"
}

var gitVersionRE = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// checkVersion runs plain "git version": with the global options an old git would fail on
// --no-lazy-fetch with a message that does not say what is wrong.
func (g *Git) checkVersion(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, QuickTimeout)
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
	if quick[subcommand] {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, QuickTimeout)
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

// gitArgs builds the full argument list. A disallowed subcommand or argument is a programming error,
// not a runtime condition, so it panics and fails loudly in tests.
func gitArgs(root, subcommand string, args []string) []string {
	safety, ok := gitSubcommands[subcommand]
	if !ok {
		panic("execx: git subcommand not allowed: " + subcommand)
	}
	allowed := gitAllowed[subcommand]
	formats := 0
	for _, a := range args {
		if a == "--" {
			break // the rest are pathspecs
		}
		if strings.HasPrefix(a, "--format=") {
			formats++
			// Only format:/tformat: strings: a bare name would be looked up as the repository's own
			// pretty.<name> alias, whose expansion is never seen here.
			f := strings.TrimPrefix(a, "--format=")
			if !strings.HasPrefix(f, "format:") && !strings.HasPrefix(f, "tformat:") {
				panic("execx: git log --format must start with format: or tformat:, got " + a)
			}
			if formatDenied.MatchString(f) {
				panic("execx: placeholder not allowed in " + a)
			}
			continue
		}
		if slices.Contains(allowed.exact, a) || revisionRE.MatchString(a) ||
			slices.ContainsFunc(allowed.prefix, func(p string) bool { return strings.HasPrefix(a, p) }) {
			continue
		}
		panic("execx: git " + subcommand + " argument not allowed: " + a)
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
		"GIT_GRAFT_FILE=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1", // refs/replace/* would swap the history that is measured
	)
}
