package tests

import (
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	// The output of go test -coverprofile on a module with a tested, an untested and an empty package
	// (Go 1.27), followed by -coverpkg's repeated blocks and a second, concatenated profile.
	p, err := parseProfile([]byte("mode: set\r\n" +
		"example.com/probe/a/a.go:4.2,4.11 1 1\r\n" +
		"example.com/probe/a/a.go:5.3,6.1 1 1\n" +
		"example.com/probe/a/a.go:7.2,7.10 1 0\n" +
		"example.com/probe/c/c.go:3.11,3.11 0 0\n" +
		"example.com/probe/b/b.go:3.16,3.26 1 0\n" +
		"example.com/probe/b/b.go:3.16,3.26 1 1\n" +
		"example.com/probe/c/c.go:3.11,3.11 0 0\n" +
		"example.com/probe/a/a.go:4.2,4.11 1 0\n" + // a copy that did not run keeps the block covered
		"mode: set\n" +
		"C:/odd:name/d.go:1.1,2.2 3 0\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.mode != "set" || len(p.files) != 4 {
		t.Fatalf("mode %q, files %v", p.mode, p.files)
	}
	a := p.files["example.com/probe/a/a.go"]
	if len(a) != 3 || !a[blockKey{4, 2, 4, 11}].covered || a[blockKey{7, 2, 7, 10}].covered {
		t.Errorf("a.go blocks = %v", a)
	}
	// A block repeated by -coverpkg is covered when any copy ran, and counted once.
	if b := p.files["example.com/probe/b/b.go"]; len(b) != 1 || !b[blockKey{3, 16, 3, 26}].covered || b[blockKey{3, 16, 3, 26}].stmts != 1 {
		t.Errorf("b.go blocks = %v", b)
	}
	// The file name is everything before the last colon.
	if d := p.files["C:/odd:name/d.go"]; len(d) != 1 || d[blockKey{1, 1, 2, 2}].stmts != 3 {
		t.Errorf("d.go blocks = %v", d)
	}

	for _, mode := range []string{"count", "atomic"} {
		p, err := parseProfile([]byte("mode: " + mode + "\nm/a.go:1.1,1.5 2 4294967296\n"))
		if err != nil || p.mode != mode || !p.files["m/a.go"][blockKey{1, 1, 1, 5}].covered {
			t.Errorf("mode %s: %v %+v", mode, err, p)
		}
	}
	if p, err := parseProfile([]byte("mode: set\n")); err != nil || len(p.files) != 0 {
		t.Errorf("an empty profile: %v %+v", err, p)
	}
}

func TestParseProfileRejects(t *testing.T) {
	for name, src := range map[string]string{
		"empty":                  "",
		"no mode":                "m/a.go:1.1,1.5 1 1\n",
		"block before mode":      "m/a.go:1.1,1.5 1 1\nmode: set\n",
		"unknown mode":           "mode: branch\n",
		"second mode differs":    "mode: set\nm/a.go:1.1,1.5 1 1\nmode: count\n",
		"plain text":             "mode: set\nok  example.com/m 0.1s coverage: 50.0% of statements\n",
		"missing count":          "mode: set\nm/a.go:1.1,1.5 1\n",
		"negative count":         "mode: set\nm/a.go:1.1,1.5 1 -1\n",
		"no file name":           "mode: set\n:1.1,1.5 1 1\n",
		"line zero":              "mode: set\nm/a.go:0.1,1.5 1 1\n",
		"column zero":            "mode: set\nm/a.go:1.0,1.5 1 1\n",
		"ends before it starts":  "mode: set\nm/a.go:5.1,4.9 1 1\n",
		"ends before its column": "mode: set\nm/a.go:5.9,5.1 1 1\n",
		"overflowing line":       "mode: set\nm/a.go:99999999999999999999.1,1.5 1 1\n",
		"overflowing count":      "mode: set\nm/a.go:1.1,1.5 1 99999999999999999999\n",
		"statements disagree":    "mode: set\nm/a.go:1.1,1.5 1 1\nm/a.go:1.1,1.5 2 1\n",
		"trailing junk":          "mode: set\nm/a.go:1.1,1.5 1 1 x\n",
		"binary":                 "\x00\x01\x02",
		"html":                   "<html><body>coverage</body></html>\n",
	} {
		if p, err := parseProfile([]byte(src)); err == nil {
			t.Errorf("%s: parsed as %+v", name, p)
		}
	}
	// The error names the line, so a user can find it.
	if _, err := parseProfile([]byte("mode: set\nm/a.go:1.1,1.5 1 1\nbad\n")); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error = %v, want it to name line 3", err)
	}
}
