package tests

import (
	"context"
	"os"
	"strings"
	"testing"
)

// in returns a resolver for a commit of the given paths.
func in(paths ...string) pathResolver {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(full string) (string, bool) { return full, set[full] }
}

func covered(p *profile, rel string) (stmts, cov int) {
	for _, b := range p.blocks[rel] {
		stmts += b.stmts
		if b.covered {
			cov += b.stmts
		}
	}
	return stmts, cov
}

// The report coverage.py 7.6.10 wrote for a project of two modules under src/, one test calling one
// function; its source is the absolute path of the machine that ran it.
func TestParseCoberturaFromCoveragePy(t *testing.T) {
	data, err := os.ReadFile("testdata/coverage.py.xml")
	if err != nil {
		t.Fatal(err)
	}
	if !isCobertura(data) || isCobertura([]byte("mode: set\n")) {
		t.Fatal("format detection")
	}
	p, err := parseCobertura(context.Background(), data, in("src/calc/__init__.py", "src/calc/ops.py", "src/calc/unused.py"))
	if err != nil {
		t.Fatal(err)
	}
	if p.files != 3 || p.inModules != 3 || len(p.blocks) != 3 {
		t.Errorf("files %d, in modules %d, at head %d", p.files, p.inModules, len(p.blocks))
	}
	for rel, want := range map[string][2]int{"src/calc/ops.py": {6, 3}, "src/calc/unused.py": {3, 0}, "src/calc/__init__.py": {0, 0}} {
		if s, c := covered(p, rel); s != want[0] || c != want[1] {
			t.Errorf("%s: %d of %d covered, want %d of %d", rel, c, s, want[1], want[0])
		}
	}
}

// A file name is relative to a source, an absolute path on another machine: the longest suffix of
// the two joined that is in the commit is the file.
func TestParseCoberturaPaths(t *testing.T) {
	report := func(sources, file string) string {
		return `<?xml version="1.0" ?>
<!DOCTYPE coverage SYSTEM "https://example.com/coverage-04.dtd">
<coverage><sources>` + sources + `</sources><packages><package><classes>
<class filename="` + file + `"><methods><method><lines><line number="9" hits="1"/></lines></method></methods>
<lines><line number="1" hits="1"/><line number="2" hits="0"/></lines></class>
</classes></package></packages></coverage>`
	}
	for name, c := range map[string]struct {
		sources, file string
		commit        []string
		want          string
	}{
		"source is the root":      {"<source>/home/ci/proj</source>", "pkg/mod.py", []string{"pkg/mod.py"}, "pkg/mod.py"},
		"source is a subdir":      {"<source>/home/ci/proj/src</source>", "pkg/mod.py", []string{"src/pkg/mod.py"}, "src/pkg/mod.py"},
		"longest suffix wins":     {"<source>/w/proj/src</source>", "pkg/mod.py", []string{"pkg/mod.py", "src/pkg/mod.py"}, "src/pkg/mod.py"},
		"second source":           {"<source>/a/b</source><source>/w/proj/lib</source>", "m.py", []string{"lib/m.py"}, "lib/m.py"},
		"windows":                 {`<source>C:\work\proj\src</source>`, `pkg\mod.py`, []string{"src/pkg/mod.py"}, "src/pkg/mod.py"},
		"absolute file name":      {"", "/home/ci/proj/pkg/mod.py", []string{"pkg/mod.py"}, "pkg/mod.py"},
		"relative files, no root": {"<source>.</source>", "pkg/mod.py", []string{"pkg/mod.py"}, "pkg/mod.py"},
		"not in the commit":       {"<source>/x</source>", "gone.py", []string{"pkg/mod.py"}, ""},
		"no escape upwards":       {"<source>/x</source>", "../../etc/passwd", []string{"etc/passwd"}, "etc/passwd"},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := parseCobertura(context.Background(), []byte(report(c.sources, c.file)), in(c.commit...))
			if err != nil {
				t.Fatal(err)
			}
			var got string
			for rel := range p.blocks {
				got = rel
			}
			if got != c.want {
				t.Fatalf("resolved to %q, want %q", got, c.want)
			}
			// The method's own lines repeat the class's in other writers and are not counted.
			if s, cov := covered(p, got); got != "" && (s != 2 || cov != 1) {
				t.Errorf("%d of %d covered, want 1 of 2", cov, s)
			}
		})
	}
}

func TestParseCoberturaRejects(t *testing.T) {
	for name, data := range map[string]string{
		"not xml":         "<coverage><unclosed>",
		"other root":      `<report><class filename="a.py"/></report>`,
		"no filename":     `<coverage><packages><package><classes><class><lines/></class></classes></package></packages></coverage>`,
		"bad number":      `<coverage><packages><package><classes><class filename="a.py"><lines><line number="x" hits="1"/></lines></class></classes></package></packages></coverage>`,
		"line zero":       `<coverage><packages><package><classes><class filename="a.py"><lines><line number="0" hits="1"/></lines></class></classes></package></packages></coverage>`,
		"negative hits":   `<coverage><packages><package><classes><class filename="a.py"><lines><line number="1" hits="-1"/></lines></class></classes></package></packages></coverage>`,
		"entity":          `<!DOCTYPE coverage [<!ENTITY x "a">]><coverage><sources><source>&x;</source></sources></coverage>`,
		"external entity": `<!DOCTYPE coverage [<!ENTITY x SYSTEM "file:///etc/passwd">]><coverage><sources><source>&x;</source></sources></coverage>`,
		"long source":     "<coverage><sources><source>" + strings.Repeat("a", maxLineBytes+1) + "</source></sources></coverage>",
		"many sources":    "<coverage><sources>" + strings.Repeat("<source>a</source>", maxSources+1) + "</sources></coverage>",
		"deep":            "<coverage>" + strings.Repeat("<a>", 20000) + strings.Repeat("</a>", 20000) + "</coverage>",
	} {
		if _, err := parseCobertura(context.Background(), []byte(data), in("a.py")); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseCoberturaCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseCobertura(ctx, []byte("<coverage/>"), in()); err == nil {
		t.Error("parsed after cancel")
	}
}
