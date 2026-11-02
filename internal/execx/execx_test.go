package execx

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCapped(t *testing.T) {
	c := &capped{max: 4}
	if n, err := c.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if _, err := c.Write([]byte("de")); !errors.Is(err, ErrOutputTooLarge) || !c.over {
		t.Errorf("write past the cap: err %v over %v", err, c.over)
	}

	tr := &capped{max: 4, truncate: true}
	if n, err := tr.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("truncating Write = %d, %v", n, err)
	}
	if tr.buf.String() != "abcd" || !tr.over {
		t.Errorf("kept %q over %v, want abcd and true", tr.buf.String(), tr.over)
	}
}

func TestRunNotFound(t *testing.T) {
	r := Runner{}
	if _, err := r.Run(context.Background(), "githealthcheck-no-such-tool"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPipe(t *testing.T) {
	r := Runner{}
	ctx := context.Background()
	readAll := func(got *string) func(io.Reader) error {
		return func(out io.Reader) error {
			b, err := io.ReadAll(out)
			*got = string(b)
			return err
		}
	}

	var got string
	if err := r.Pipe(ctx, strings.NewReader("abc"), 3, readAll(&got), "cat"); err != nil || got != "abc" {
		t.Errorf("cat within the limit: %q, %v", got, err)
	}
	if err := r.Pipe(ctx, strings.NewReader("abcd"), 3, readAll(&got), "cat"); !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("cat past the limit: %v", err)
	}
	if err := r.Pipe(ctx, strings.NewReader("abc"), 3, func(io.Reader) error { return nil }, "cat"); !errors.Is(err, ErrUnreadOutput) {
		t.Errorf("output left unread: %v", err)
	}
	if err := r.Pipe(ctx, nil, 3, readAll(&got), "false"); err == nil {
		t.Error("a failing command gave no error")
	}
	if err := r.Pipe(ctx, nil, 3, readAll(&got), "githealthcheck-no-such-tool"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}

	// A reader that fails, or a context that ends, stops a command that would print without end.
	stop := errors.New("stop")
	start := time.Now()
	err := r.Pipe(ctx, nil, 1<<40, func(out io.Reader) error {
		_, _ = io.ReadFull(out, make([]byte, 10))
		return stop
	}, "yes")
	if !errors.Is(err, stop) || time.Since(start) > waitDelay {
		t.Errorf("reader error: %v after %v", err, time.Since(start))
	}
	cctx, cancel := context.WithCancel(ctx)
	start = time.Now()
	err = r.Pipe(cctx, nil, 1<<40, func(out io.Reader) error {
		_, _ = io.ReadFull(out, make([]byte, 10))
		cancel()
		_, err := io.Copy(io.Discard, out)
		return err
	}, "yes")
	if !errors.Is(err, context.Canceled) || time.Since(start) > waitDelay {
		t.Errorf("cancelled: %v after %v", err, time.Since(start))
	}
}
