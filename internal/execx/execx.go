// Package execx runs external commands for githealthcheck. Commands never go through a shell, always
// run under a context so they can be timed out and cancelled, and get a reduced environment.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotFound is returned when the command is not installed. Callers turn it into a null measurement
// with reason tool_not_found instead of failing the run.
var ErrNotFound = errors.New("command not found")

// envAllowed lists the variables a child may inherit. Everything else is dropped, so a variable such
// as GIT_DIR, GIT_EXTERNAL_DIFF or a token in the caller's environment cannot change what a tool does.
var envAllowed = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LANG": true, "TZ": true,
}

// Env returns the allowed variables from environ, plus LC_* locale settings.
func Env(environ []string) []string {
	var env []string
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if ok && (envAllowed[key] || strings.HasPrefix(key, "LC_")) {
			env = append(env, kv)
		}
	}
	return env
}

// Runner executes commands in Dir with a fixed environment.
type Runner struct {
	Env []string
	Dir string
}

// Run executes name with args and returns stdout. On failure the error carries the trimmed stderr.
func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = r.Env
	cmd.Dir = r.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", name, ctx.Err())
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
