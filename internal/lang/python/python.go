// Package python extracts the facts of Python source files with the ast and tokenize modules of the
// installed python3 (D8). The repository's code is parsed, never run: a fixed helper, helper.py, runs
// in isolated mode (-I: no PYTHON* variables, no user site directory, not the working directory on
// sys.path) without the site module (-S), in a temporary directory, and reads only what it is sent
// on stdin.
package python

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

//go:embed helper.py
var helper string

// Interpreter is the command the helpers run, looked up on PATH; tests point it elsewhere.
var Interpreter = "python3"

// MinVersion is the oldest Python whose ast reads every syntax of current code: match statements
// arrived in 3.10.
var MinVersion = [2]int{3, 10}

const (
	// FileTimeout bounds the time a helper may spend on one file. A file of the walk's 1 MiB limit
	// parses in well under a second; past this the helper is killed and the file is partial.
	FileTimeout = 20 * time.Second
	// MaxResponse bounds the facts a helper may send back for one file: a 1 MiB file of one-character
	// tokens gives about 15 MiB.
	MaxResponse = 64 << 20
	// startTimeout bounds the wait for a new helper's greeting.
	startTimeout = 5 * time.Second
	// maxStartFailures is the number of times in a row a helper may fail to start before the
	// worker's files are left unread: python3 itself is broken. A helper that dies on a file is
	// replaced however often it happens, since that is the file's doing.
	maxStartFailures = 3
)

// fileTimeout is FileTimeout, lowered by tests.
var fileTimeout = FileTimeout

var (
	errNotFound = errors.New("python3 not found")
	errTooOld   = errors.New("python3 too old")
)

// Reason turns an error of Start into the reason Python is not analysed.
func Reason(err error) report.NullReason {
	switch {
	case errors.Is(err, errNotFound):
		return report.ToolNotFound
	case errors.Is(err, errTooOld):
		return report.ToolTooOld
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return report.Timeout
	}
	return report.ToolCrashed
}

// Result is one file filled by a helper, with the index it was submitted with.
type Result struct {
	Index int
	File  facts.File
}

type job struct {
	index int
	file  facts.File
	src   []byte
}

// Pool is a set of helper processes that fill files in the background.
type Pool struct {
	ctx     context.Context
	path    string
	version string
	jobs    chan job
	wg      sync.WaitGroup
	mu      sync.Mutex
	done    []Result
	closed  bool
}

// Start finds python3, checks its version with one helper, and starts the pool. The pool stops with
// ctx; files left then come back partial.
func Start(ctx context.Context) (*Pool, error) {
	path, err := exec.LookPath(Interpreter)
	if err != nil {
		return nil, errNotFound
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p := &Pool{ctx: ctx, path: path}
	first, err := p.spawn()
	if err != nil {
		return nil, err
	}
	p.version = first.version
	if !atLeast(first.version, MinVersion) {
		first.kill()
		return nil, fmt.Errorf("%w: %s", errTooOld, first.version)
	}
	n := min(runtime.NumCPU(), 4)
	p.jobs = make(chan job, n)
	for i := range n {
		var h *proc
		if i == 0 {
			h = first
		}
		p.wg.Add(1)
		go p.work(h)
	}
	return p, nil
}

// Version is the version of the python3 the helpers run, such as "3.12.4".
func (p *Pool) Version() string { return p.version }

// Submit queues a file, blocking while every helper is busy and the queue is full.
func (p *Pool) Submit(index int, f facts.File, src []byte) {
	p.jobs <- job{index, f, src}
}

// Done returns the files filled since the last call.
func (p *Pool) Done() []Result {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.done
	p.done = nil
	return out
}

// Wait waits for every submitted file and returns those Done has not.
func (p *Pool) Wait() []Result {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.jobs)
	}
	p.mu.Unlock()
	p.wg.Wait()
	return p.Done()
}

// Close stops the helpers.
func (p *Pool) Close() {
	if p != nil {
		p.Wait()
	}
}

// work fills the files of the queue with one helper, replacing it when it dies, hangs or runs out of
// memory. A file the helper did not answer for is partial and not analysed: it counts in no
// measurement.
func (p *Pool) work(h *proc) {
	defer p.wg.Done()
	startFailures := 0
	for j := range p.jobs {
		f := j.file
		Prepare(&f, j.src)
		if strings.HasSuffix(f.Path, ".pyi") {
			// A type stub holds signatures with ... for bodies: it is counted, not measured.
			p.publish(j.index, f)
			continue
		}
		tokens := wantTokens(&f)
		if h == nil && startFailures < maxStartFailures && p.ctx.Err() == nil {
			var err error
			if h, err = p.spawn(); err != nil {
				startFailures++
			} else {
				startFailures = 0
			}
		}
		if h == nil {
			f.Partial = true
			p.publish(j.index, f)
			continue
		}
		resp, err := h.ask(p.ctx, j.src, tokens)
		if err != nil {
			// The helper died, hung or answered past the limit: it is replaced for the next file.
			h.kill()
			h = nil
			f.Partial = true
			p.publish(j.index, f)
			continue
		}
		restart, err := fill(&f, resp, tokens)
		f.Analysed, f.Partial = err == nil, f.Partial || err != nil
		if restart {
			h.kill()
			h = nil
		}
		p.publish(j.index, f)
	}
	if h != nil {
		h.stop()
	}
}

func (p *Pool) publish(index int, f facts.File) {
	p.mu.Lock()
	p.done = append(p.done, Result{Index: index, File: f})
	p.mu.Unlock()
}

// proc is one running helper.
type proc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan line
	version string
	wait    func() // cmd.Wait, once, whoever calls it first
}

type line struct {
	data []byte
	err  error
}

// spawn starts a helper and reads its greeting, which names the Python version.
func (p *Pool) spawn() (*proc, error) {
	cmd := exec.Command(p.path, "-I", "-S", "-B", "-X", "utf8", "-c", helper)
	cmd.Env = execx.Env(os.Environ())
	cmd.Dir = os.TempDir()
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotFound, err)
	}
	h := &proc{cmd: cmd, stdin: stdin, lines: make(chan line, 1), wait: sync.OnceFunc(func() { cmd.Wait() })}
	go h.read(bufio.NewReaderSize(stdout, 64<<10))
	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	select {
	case l := <-h.lines:
		v, ok := strings.CutPrefix(strings.TrimSpace(string(l.data)), "githealthcheck-python ")
		if l.err != nil || !ok {
			h.kill()
			return nil, fmt.Errorf("python3 helper did not start")
		}
		h.version = v
		return h, nil
	case <-timer.C:
		h.kill()
		return nil, fmt.Errorf("python3 helper did not start")
	case <-p.ctx.Done():
		h.kill()
		return nil, p.ctx.Err()
	}
}

// read sends each line of the helper's stdout, at most MaxResponse bytes, to lines.
func (h *proc) read(r *bufio.Reader) {
	defer close(h.lines)
	for {
		var buf []byte
		for {
			chunk, err := r.ReadSlice('\n')
			if len(buf)+len(chunk) > MaxResponse {
				h.lines <- line{err: errors.New("response too large")}
				return
			}
			buf = append(buf, chunk...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				h.lines <- line{err: err}
				return
			}
			break
		}
		h.lines <- line{data: buf}
	}
}

// ask sends one file and waits for its facts. The header is the length of the source and whether
// its tokens are wanted.
func (h *proc) ask(ctx context.Context, src []byte, tokens bool) ([]byte, error) {
	flag := "-"
	if tokens {
		flag = "t"
	}
	sent := make(chan error, 1)
	go func() {
		_, err := io.WriteString(h.stdin, strconv.Itoa(len(src))+" "+flag+"\n")
		if err == nil {
			_, err = h.stdin.Write(src)
		}
		sent <- err
	}()
	timer := time.NewTimer(fileTimeout)
	defer timer.Stop()
	for {
		select {
		case err := <-sent:
			if err != nil {
				return nil, err
			}
			sent = nil
		case l, ok := <-h.lines:
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			return l.data, l.err
		case <-timer.C:
			return nil, errors.New("helper timed out")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// stop ends a helper by closing its stdin, and kills it if it does not exit.
func (h *proc) stop() {
	h.stdin.Close()
	done := make(chan struct{})
	go func() { h.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		h.kill()
	}
}

func (h *proc) kill() {
	h.cmd.Process.Kill()
	h.stdin.Close()
	go h.wait()
	go func() {
		for range h.lines {
		}
	}()
}

// atLeast reports whether a version such as "3.12.4" is at least min.
func atLeast(v string, min [2]int) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > min[0] || major == min[0] && minor >= min[1]
}
