package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func FuzzParseProfile(f *testing.F) {
	// Reuse the valid and rejected profiles from profile_test.go, including repeated blocks,
	// concatenated profiles, and positions produced by //line directives.
	for _, src := range []string{
		"mode: set\r\n" +
			"example.com/probe/a/a.go:4.2,4.11 1 1\r\n" +
			"example.com/probe/a/a.go:5.3,6.1 1 1\n" +
			"example.com/probe/a/a.go:7.2,7.10 1 0\n" +
			"example.com/probe/c/c.go:3.11,3.11 0 0\n" +
			"example.com/probe/b/b.go:3.16,3.26 1 0\n" +
			"example.com/probe/b/b.go:3.16,3.26 1 1\n" +
			"example.com/probe/c/c.go:3.11,3.11 0 0\n" +
			"example.com/probe/a/a.go:4.2,4.11 1 0\n" +
			"strings/strings.go:10.2,12.3 2 1\n" +
			"mode: set\n" +
			"example.com/probe/gone/d.go:1.1,2.2 3 0\n\n",
		"mode: set\nexample.com/probe/C:/odd:name/d.go:1.1,2.2 3 0\n",
		"mode: set\nexample.com/probe/y.go:5000.0,10.0 1 1\n",
		"mode: count\nexample.com/probe/a.go:1.1,1.5 2 4294967296\n",
		"mode: atomic\nexample.com/probe/a.go:1.1,1.5 2 4294967296\n",
		"mode: set\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 1",
		"",
		"example.com/probe/a.go:1.1,1.5 1 1\n",
		"example.com/probe/a.go:1.1,1.5 1 1\nmode: set\n",
		"mode: branch\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nmode: count\n",
		"mode: set\nok  example.com/m 0.1s coverage: 50.0% of statements\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 -1\n",
		"mode: set\n:1.1,1.5 1 1\n",
		"mode: set\nexample.com/probe/a.go:99999999999999999999.1,1.5 1 1\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 99999999999999999999\n",
		fmt.Sprintf("mode: set\nexample.com/probe/a.go:1.1,1.5 %d 1\n", maxBlockStmts+1),
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nexample.com/probe/a.go:1.1,1.5 2 1\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 1 x\n",
		"\x00\x01\x02",
		"<html><body>coverage</body></html>\n",
		"mode: set\nexample.com/probe/a.go:1.1,1.5 1 1\nbad\n",
		"mode: set\nexample.com/probe/" + strings.Repeat("a/", maxPathSegments) + "x.go:1.1,1.2 1 1\n",
		"mode: set\nexample.com/probe/" + strings.Repeat("a", maxLineBytes) + ".go:1.1,1.2 1 1\n",
		"mode: set\nexample.com/probe/" + strings.Repeat("a/", maxPathSegments-2) + "x.go:1.1,1.2 1 1\n",
	} {
		f.Add(src)
	}
	var many strings.Builder
	many.WriteString("mode: set\n")
	for i := range 100 {
		fmt.Fprintf(&many, "example.com/probe/f%d.go:1.1,1.2 1 1\n", i)
	}
	f.Add(many.String())

	resolve := probe("a.go", "a/a.go", "b/b.go", "c/c.go", "C:/odd:name/d.go", "y.go",
		strings.Repeat("a/", maxPathSegments-2)+"x.go")
	f.Fuzz(func(t *testing.T, src string) {
		p, err := parseProfile(context.Background(), []byte(src), resolve)
		if err != nil {
			if p != nil {
				t.Fatal("failed parse returned a partial coverage profile")
			}
			return
		}
		if p == nil {
			t.Fatal("successful parse returned no coverage profile")
		}
		switch p.mode {
		case "set", "count", "atomic":
		default:
			t.Fatalf("successful parse returned unknown mode %q", p.mode)
		}
		// The newline separates the invalid line even when the original input has no trailing
		// newline. A parser must not silently ignore it and report the valid prefix as coverage.
		if p, err := parseProfile(context.Background(), []byte(src+"\nnot a coverage block\n"), resolve); err == nil || p != nil {
			t.Fatal("malformed suffix returned a complete or partial coverage profile")
		}
	})
}
