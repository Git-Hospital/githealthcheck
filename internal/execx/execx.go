// Package execx runs external commands for githealthcheck. Commands never go through a shell, always
// run under a context so they can be timed out and cancelled, and get a reduced environment.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound is returned when the command is not installed. Callers turn it into a null measurement
// with reason tool_not_found instead of failing the run.
var ErrNotFound = errors.New("command not found")

// envAllowed lists the variables a child may inherit. Everything else is dropped, so a variable such
// as GIT_DIR, GIT_EXTERNAL_DIFF or a token in the caller's environment cannot change what a tool does.
var envAllowed = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LANG": true, "TZ": true,
}

// Env returns the allowed variables from environ, plus LC_* locale settings. PATH keeps only absolute
// directories: commands run inside the scanned repository, and an empty or relative PATH entry would
// make a child find a program planted there.
func Env(environ []string) []string {
	var env []string
	for _, kv := range environ {
		key, val, ok := strings.Cut(kv, "=")
		switch {
		case !ok:
		case key == "PATH":
			env = append(env, "PATH="+absolutePath(val))
		case envAllowed[key] || strings.HasPrefix(key, "LC_"):
			env = append(env, kv)
		}
	}
	return env
}

func absolutePath(path string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if filepath.IsAbs(dir) {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(filepath.ListSeparator))
}

// MaxOutput caps a command's stdout. A repository can make git print without end (a mailmap that
// never ends, a huge commit message); past the cap the command is killed and the call fails.
const MaxOutput = 512 << 20

// waitDelay bounds how long Wait blocks after the process is killed, in case a grandchild still
// holds the output pipes.
const waitDelay = 5 * time.Second

// ErrOutputTooLarge is returned when a command writes more than MaxOutput bytes.
var ErrOutputTooLarge = errors.New("output exceeds the limit")

// Runner executes commands in Dir with a fixed environment.
type Runner struct {
	Env []string
	Dir string
}

// capped is a buffer that holds at most max bytes. Past that it either fails, which stops the
// command's copy goroutine so Run reports ErrOutputTooLarge (stdout), or drops the rest (stderr,
// which only feeds error messages).
type capped struct {
	buf      bytes.Buffer
	max      int
	truncate bool
	over     bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		c.over = true
		if !c.truncate {
			return 0, ErrOutputTooLarge
		}
		c.buf.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return c.buf.Write(p)
}

// Run executes name with args and returns stdout. On failure the error carries the trimmed stderr.
func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd, stderr, err := r.command(ctx, name, args)
	if err != nil {
		return nil, err
	}
	stdout := &capped{max: MaxOutput}
	cmd.Stdout = stdout
	if err := cmd.Run(); err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("%s: %w", name, ctx.Err())
		case stdout.over:
			return nil, fmt.Errorf("%s: %w", name, ErrOutputTooLarge)
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.Bytes(), nil
}

// command is the setup Run and Pipe share: the reduced environment, the directory, the wait bound
// and a truncated stderr.
func (r *Runner) command(ctx context.Context, name string, args []string) (*exec.Cmd, *capped, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = r.Env
	cmd.Dir = r.Dir
	cmd.WaitDelay = waitDelay
	stderr := &capped{max: 64 << 10, truncate: true}
	cmd.Stderr = stderr
	return cmd, stderr, nil
}

// Pipe executes name with args, feeding it stdin, and hands its stdout to read as it streams, so the
// output is never held whole. read sees at most limit bytes; past them its reader fails with
// ErrOutputTooLarge. read must consume the whole output: a byte left unread is an error. When read
// fails the command is stopped, so it cannot stay blocked on a full pipe. On failure the error carries
// the trimmed stderr.
func (r *Runner) Pipe(ctx context.Context, stdin io.Reader, limit int64, read func(io.Reader) error, name string, args ...string) error {
	run, stop := context.WithCancel(ctx)
	defer stop()
	cmd, stderr, err := r.command(run, name, args)
	if err != nil {
		return err
	}
	cmd.Stdin = stdin
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	out := &limited{r: pipe, n: limit}
	rerr := read(out)
	if rerr == nil {
		if n, _ := pipe.Read(make([]byte, 1)); n > 0 {
			rerr = ErrUnreadOutput
		}
	}
	if rerr != nil {
		stop()
	}
	werr := cmd.Wait()
	if rerr == nil && werr == nil {
		// Everything was read, even if ctx ended just after.
		return nil
	}
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("%s: %w", name, ctx.Err())
	case out.over:
		return fmt.Errorf("%s: %w", name, ErrOutputTooLarge)
	case rerr != nil:
		return fmt.Errorf("%s: %w: %s", name, rerr, strings.TrimSpace(stderr.buf.String()))
	}
	return fmt.Errorf("%s: %w: %s", name, werr, strings.TrimSpace(stderr.buf.String()))
}

// ErrUnreadOutput is output that Pipe's reader left unread.
var ErrUnreadOutput = errors.New("output left unread")

// limited reads at most n bytes from r. A byte past them fails the read with ErrOutputTooLarge.
type limited struct {
	r    io.Reader
	n    int64
	over bool
}

func (l *limited) Read(p []byte) (int, error) {
	if l.n <= 0 {
		k, err := l.r.Read(make([]byte, 1))
		if k > 0 {
			l.over = true
			return 0, ErrOutputTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	k, err := l.r.Read(p)
	l.n -= int64(k)
	return k, err
}
