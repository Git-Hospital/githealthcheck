package execx

import (
	"context"
	"errors"
	"testing"
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
