package execx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The scanned repository is untrusted, and so is its .git/config. Several git settings in a
// repository's own config run a command: core.fsmonitor (on any index refresh), diff.external and
// textconv drivers (on diffs), gpg.program with log.showSignature (on log), and a promisor remote
// in a partial clone makes git fetch over the network. Git therefore runs only through Git, which
// overrides those settings from the command scope (it wins over repository config), allows only
// read-only subcommands that never refresh the index, and stops git from looking for a repository
// above the one it was given.

// gitConfig is passed as -c options before every subcommand.
var gitConfig = []string{
	"core.fsmonitor=false",
	"core.hooksPath=/dev/null",
	"core.pager=cat",
	"diff.external=",
	"log.showSignature=false",
	"protocol.allow=never",
	"color.ui=false",
}

// gitSubcommands are the only subcommands Git runs. None of them refreshes the index.
var gitSubcommands = map[string][]string{
	"rev-parse": nil,
	"log":       {"--no-ext-diff", "--no-textconv", "--no-show-signature", "--no-color"},
}

// Git runs read-only git commands against one repository root.
type Git struct {
	root   string
	runner Runner
}

// NewGit returns a Git for the repository whose work tree is exactly root. It fails when root is not
// the top level of a repository, so git never falls back to a parent repository.
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
	out, err := g.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not a git repository: %w", root, err)
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, err
	}
	if top != abs {
		return nil, fmt.Errorf("%s is inside the repository %s; pass the repository root", root, top)
	}
	return g, nil
}

// Root returns the repository root as an absolute path with symlinks resolved.
func (g *Git) Root() string { return g.root }

// Run executes an allowed git subcommand with the hardening options. The subcommand's own safety
// flags are inserted right after it, before the caller's arguments.
func (g *Git) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	out, err := g.runner.Run(ctx, "git", gitArgs(g.root, subcommand, args)...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", subcommand, err)
	}
	return out, nil
}

func gitArgs(root, subcommand string, args []string) []string {
	safety, ok := gitSubcommands[subcommand]
	if !ok {
		// A programming error, not a runtime condition: fail loudly in tests.
		panic("execx: git subcommand not allowed: " + subcommand)
	}
	for _, a := range args {
		// A later flag wins in git, so these would undo the safety flags inserted above.
		for _, bad := range []string{"--ext-diff", "--textconv", "--show-signature", "-c", "--exec-path", "--git-dir", "--work-tree"} {
			if a == bad || strings.HasPrefix(a, bad+"=") {
				panic("execx: git argument not allowed: " + a)
			}
		}
	}
	// safe.directory is honoured from the command scope. It names only this root, so a container or
	// CI runner with a different uid can read the repository without trusting every directory.
	full := []string{"-c", "safe.directory=" + root}
	for _, kv := range gitConfig {
		full = append(full, "-c", kv)
	}
	full = append(full, subcommand)
	full = append(full, safety...)
	return append(full, args...)
}

// gitEnv is the reduced environment plus the variables that keep git away from system and global
// config, terminals, optional locks, lazy fetches and parent directories.
func gitEnv(environ []string, root string) []string {
	return append(Env(environ),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_PAGER=cat",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(root),
	)
}
