package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// probe resolves example.com/probe/<rest> to <rest>; only the files in atHead are in the commit.
func probe(atHead ...string) resolver {
	return func(ip string) (string, bool, bool) {
		rel, ok := strings.CutPrefix(ip, "example.com/probe/")
		if !ok {
			return "", false, false
		}
		for _, p := range atHead {
			if p == rel {
				return rel, true, true
			}
		}
		return rel, true, false
	}
}

func parse(t *testing.T, src string, r resolver) *profile {
	t.Helper()
	p, err := parseProfile(context.Background(), []byte(src), r)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseProfile(t *testing.T) {
	// The output of go test -coverprofile on a module with a tested, an untested and an empty package
	// (Go 1.27), followed by -coverpkg's repeated blocks, a standard library file and a second,
	// concatenated profile.
	p := parse(t, "mode: set\r\n"+
		"example.com/probe/a/a.go:4.2,4.11 1 1\r\n"+
		"example.com/probe/a/a.go:5.3,6.1 1 1\n"+
		"example.com/probe/a/a.go:7.2,7.10 1 0\n"+
		"example.com/probe/c/c.go:3.11,3.11 0 0\n"+
		"example.com/probe/b/b.go:3.16,3.26 1 0\n"+
		"example.com/probe/b/b.go:3.16,3.26 1 1\n"+
		"example.com/probe/c/c.go:3.11,3.11 0 0\n"+
		"example.com/probe/a/a.go:4.2,4.11 1 0\n"+ // a copy that did not run keeps the block covered
		"strings/strings.go:10.2,12.3 2 1\n"+
		"mode: set\n"+
		"example.com/probe/gone/d.go:1.1,2.2 3 0\n\n",
		probe("a/a.go", "b/b.go", "c/c.go"))
	if p.mode != "set" || p.files != 5 || p.inModules != 4 || len(p.blocks) != 3 {
		t.Fatalf("mode %q, files %d, in modules %d, at HEAD %d", p.mode, p.files, p.inModules, len(p.blocks))
	}
	a := p.blocks["a/a.go"]
	if len(a) != 3 || !a[blockKey{4, 2, 4, 11}].covered || a[blockKey{7, 2, 7, 10}].covered {
		t.Errorf("a.go blocks = %v", a)
	}
	// A block repeated by -coverpkg is covered when any copy ran, and counted once.
	if b := p.blocks["b/b.go"]; len(b) != 1 || !b[blockKey{3, 16, 3, 26}].covered || b[blockKey{3, 16, 3, 26}].stmts != 1 {
		t.Errorf("b.go blocks = %v", b)
	}
	// Files outside the commit are counted but their blocks are not kept.
	if _, ok := p.blocks["gone/d.go"]; ok {
		t.Error("kept the blocks of a file that is not in the commit")
	}

	// The file name is everything before the last colon.
	odd := parse(t, "mode: set\nexample.com/probe/C:/odd:name/d.go:1.1,2.2 3 0\n", probe("C:/odd:name/d.go"))
	if d := odd.blocks["C:/odd:name/d.go"]; len(d) != 1 || d[blockKey{1, 1, 2, 2}].stmts != 3 {
		t.Errorf("d.go blocks = %v", d)
	}
	// //line directives give column 0 and blocks that run backwards; the parser keeps them, and the
	// freshness check decides what they mean.
	moved := parse(t, "mode: set\nexample.com/probe/y.go:5000.0,10.0 1 1\n", probe("y.go"))
	if len(moved.blocks["y.go"]) != 1 {
		t.Errorf("//line block = %v", moved.blocks)
	}
	for _, mode := range []string{"count", "atomic"} {
		p := parse(t, "mode: "+mode+"\nexample.com/probe/a.go:1.1,1.5 2 4294967296\n", probe("a.go"))
		if p.mode != mode || !p.blocks["a.go"][blockKey{1, 1, 1, 5}].covered {
			t.Errorf("mode %s: %+v", mode, p)
		}
	}
	if p := parse(t, "mode: set\n", probe()); p.files != 0 {
		t.Errorf("an empty profile: %+v", p)
	}
}

func TestParseProfileRejects(t *testing.T) {
	for name, src := range map[string]string{
		"empty":               "",
		"no mode":             "example.com/probe/a.go:1.1,1.5 1 1\n",
		"block before mode":   "example.com/probe/a.go:1.1,1.5 1 1\nmode: set\n",
		"unknown mode":        "mode: branch\n",
		"second mode differs": "mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nmode: count\n",
		"plain text":          "mode: set\nok  example.com/m 0.1s coverage: 50.0% of statements\n",
		"missing count":       "mode: set\nexample.com/probe/a.go:1.1,1.5 1\n",
		"negative count":      "mode: set\nexample.com/probe/a.go:1.1,1.5 1 -1\n",
		"no file name":        "mode: set\n:1.1,1.5 1 1\n",
		"overflowing line":    "mode: set\nexample.com/probe/a.go:99999999999999999999.1,1.5 1 1\n",
		"overflowing count":   "mode: set\nexample.com/probe/a.go:1.1,1.5 1 99999999999999999999\n",
		"huge block":          fmt.Sprintf("mode: set\nexample.com/probe/a.go:1.1,1.5 %d 1\n", maxBlockStmts+1),
		"statements disagree": "mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nexample.com/probe/a.go:1.1,1.5 2 1\n",
		"trailing junk":       "mode: set\nexample.com/probe/a.go:1.1,1.5 1 1 x\n",
		"binary":              "\x00\x01\x02",
		"html":                "<html><body>coverage</body></html>\n",
	} {
		if p, err := parseProfile(context.Background(), []byte(src), probe("a.go")); err == nil {
			t.Errorf("%s: parsed as %+v", name, p)
		}
	}
	// The error names the line, so a user can find it.
	if _, err := parseProfile(context.Background(), []byte("mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nbad\n"), probe()); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error = %v, want it to name line 3", err)
	}
}

// A long profile stops when the deadline passes instead of running past it.
func TestParseProfileCancelled(t *testing.T) {
	var b strings.Builder
	b.WriteString("mode: set\n")
	for i := range 10000 {
		fmt.Fprintf(&b, "example.com/probe/f%d.go:1.1,1.2 1 1\n", i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseProfile(ctx, []byte(b.String()), probe()); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
