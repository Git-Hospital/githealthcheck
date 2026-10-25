package complexity

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// parsed fills a Go file as the walk does.
func parsed(path, src string) facts.File {
	f := facts.File{Path: path, Language: "go", Analysed: true, Lines: strings.Count(src, "\n")}
	golang.Support{}.Fill(&f, []byte(src))
	return f
}

// work is a function of 23 lines and about 150 tokens, with its locals and its callee named by
// the arguments.
func work(name, total, v, call string) string {
	return fmt.Sprintf(`func %[1]s(values []int, weight int) int {
	%[2]s := 0
	for _, %[3]s := range values {
		if %[3]s < 0 {
			continue
		}
		%[2]s += %[3]s * weight
		if %[2]s > 1000 {
			%[2]s = %[4]s(%[2]s, 1000)
		}
	}
	if %[2]s%%2 == 0 {
		%[2]s /= 2
	} else {
		%[2]s = %[4]s(%[2]s, weight) + len(values)
	}
	for i := range values {
		if values[i] > %[2]s {
			values[i] = %[4]s(values[i]-%[2]s, weight*2)
		} else if values[i] < -%[2]s {
			values[i] = %[4]s(values[i]+%[2]s, weight*3)
		}
	}
	return %[2]s
}
`, name, total, v, call)
}

func dupRun(t *testing.T, files ...facts.File) analyze2 {
	t.Helper()
	res := Analyzer{}.Run(context.Background(), &facts.Repo{Files: files})
	var out analyze2
	for _, f := range res.Findings {
		if f.RuleID == DuplicatedBlock || f.RuleID == IdenticalFunctions {
			out.got = append(out.got, fmt.Sprintf("%s %s:%d %s", f.RuleID, f.Path, f.Line, f.Message))
		}
	}
	out.share = res.Metrics[DuplicatedLineShare]
	return out
}

type analyze2 struct {
	got   []string
	share report.Measurement
}

func TestDuplication(t *testing.T) {
	head := "package a\n\n"
	// A copy that renames its locals, with a statement more so the functions differ: a block.
	a := parsed("a/a.go", head+work("Sum", "total", "v", "min"))
	b := parsed("b/b.go", head+strings.Replace(work("Add", "acc", "x", "min"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1))
	r := dupRun(t, a, b)
	if want := []string{"GHC-CPLX-009 b/b.go:4 lines 4-25 repeat lines 4-25 of a/a.go (128 tokens)"}; !reflect.DeepEqual(r.got, want) {
		t.Errorf("renamed copy: %q", r.got)
	}
	// 22 duplicated lines of each copy (the signature differs by its name), over 27 + 28 lines.
	if r.share.Inputs != 2 || fmt.Sprintf("%.4f", *r.share.Value) != fmt.Sprintf("%.4f", 44.0/55.0) {
		t.Errorf("share %+v", r.share)
	}

	// A copy that calls another function, or renames one local as two, is not the same code.
	c := parsed("c/c.go", head+strings.Replace(work("Add", "acc", "x", "max"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1))
	if r := dupRun(t, a, c); r.got != nil {
		t.Errorf("another callee: %q", r.got)
	}
	inconsistent := strings.Replace(work("Add", "acc", "x", "min"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1)
	inconsistent = strings.Replace(inconsistent, "acc /= 2", "weight /= 2", 1)
	if r := dupRun(t, a, parsed("d/d.go", head+inconsistent)); len(r.got) != 0 {
		t.Errorf("inconsistent renaming: %q", r.got)
	}

	// Three copies: each later one against the first.
	e := parsed("e/e.go", head+strings.Replace(work("More", "n", "y", "min"), "\treturn n\n", "\tn--\n\treturn n\n", 1))
	if r := dupRun(t, a, b, e); len(r.got) != 2 || !strings.Contains(r.got[1], "e/e.go:4 lines 4-25 repeat lines 4-25 of a/a.go") {
		t.Errorf("three copies: %q", r.got)
	}

	// Tests are not looked at.
	at := parsed("b/b_test.go", head+strings.Replace(work("Add", "acc", "x", "min"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1))
	if r := dupRun(t, a, at); r.got != nil {
		t.Errorf("test file: %q", r.got)
	}
}

func TestDuplicationEdges(t *testing.T) {
	head := "package a\n\n"
	// A table of literals is data, not code.
	var table strings.Builder
	table.WriteString(head + "var A = map[int]string{\n")
	for i := range 60 {
		fmt.Fprintf(&table, "\t%d: %q,\n", i, fmt.Sprint(i))
	}
	table.WriteString("}\n")
	tab := table.String()
	if r := dupRun(t, parsed("a/a.go", tab), parsed("b/b.go", strings.Replace(tab, "var A", "var B", 1))); r.got != nil {
		t.Errorf("literal table: %q", r.got)
	}

	// A block that repeats right after itself is reported once, without overlap.
	var rep strings.Builder
	rep.WriteString(head + "func F(x int) int {\n")
	for range 80 {
		rep.WriteString("\tx = x*3 + g(x, x)\n")
	}
	rep.WriteString("\treturn x\n}\n")
	r := dupRun(t, parsed("a/a.go", rep.String()))
	for _, g := range r.got {
		var from, to, ofrom, oto int
		fmt.Sscanf(g[strings.Index(g, "lines"):], "lines %d-%d repeat lines %d-%d", &from, &to, &ofrom, &oto)
		if ofrom > to || from <= oto {
			t.Errorf("overlapping copies: %s", g)
		}
	}
	if len(r.got) == 0 {
		t.Error("a repeated run was not found")
	}

	// Functions with the same body up to their locals; not with another literal, nor when short.
	same := head + work("Sum", "total", "v", "min") + "\n" + work("Add", "acc", "x", "min")
	if r := dupRun(t, parsed("a/a.go", same)); !reflect.DeepEqual(r.got, []string{
		"GHC-CPLX-010 a/a.go:29 Add has the signature and body of Sum at a/a.go:3, up to the names of its parameters and locals"}) {
		t.Errorf("same bodies (and no block for them): %q", r.got)
	}
	lit := head + work("Sum", "total", "v", "min") + "\n" + strings.Replace(work("Add", "acc", "x", "min"), "1000)", "999)", 1)
	if r := dupRun(t, parsed("a/a.go", lit)); len(r.got) != 1 || !strings.HasPrefix(r.got[0], "GHC-CPLX-009") {
		t.Errorf("another literal: a block, not the same body: %q", r.got)
	}
	short := head + "func A(x int) int { return x + 1 }\n\nfunc B(y int) int { return y + 1 }\n"
	if r := dupRun(t, parsed("a/a.go", short)); r.got != nil {
		t.Errorf("short functions: %q", r.got)
	}

	// One function per platform, in files with different build constraints, is meant to repeat.
	unix := parsed("a/term_unix.go", "//go:build linux\n\npackage a\n\n"+work("Sum", "total", "v", "min"))
	bsd := parsed("a/term_bsd.go", "//go:build darwin\n\npackage a\n\n"+work("Sum", "n", "v", "min"))
	if r := dupRun(t, unix, bsd); r.got != nil {
		t.Errorf("platform variants: %q", r.got)
	}
	other := parsed("b/b.go", "package b\n\n"+work("Sum", "n", "v", "min"))
	if r := dupRun(t, unix, other); len(r.got) != 1 || !strings.HasPrefix(r.got[0], "GHC-CPLX-010") {
		t.Errorf("same name in another package: %q", r.got)
	}

	// Review cases. A later copy is compared with the block it repeats, not only with the first
	// block of the same shape.
	foo := parsed("a/a.go", head+work("Sum", "total", "v", "foo"))
	bar := parsed("b/b.go", head+strings.Replace(work("Add", "acc", "x", "bar"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1))
	bar2 := parsed("c/c.go", head+strings.Replace(work("More", "acc", "x", "bar"), "\treturn acc\n", "\tacc--\n\treturn acc\n", 1))
	if r := dupRun(t, foo, bar, bar2); len(r.got) != 1 || !strings.Contains(r.got[0], "c/c.go:4 lines 4-25 repeat lines 4-25 of b/b.go") {
		t.Errorf("same shape, other callee first: %q", r.got)
	}
	// A block inside a reported pair is not reported again, also when other declarations follow.
	followed := head + work("Sum", "total", "v", "min") + "\nfunc Other() {}\n\n" + work("Add", "acc", "x", "min") + "\nfunc Another() {}\n"
	if r := dupRun(t, parsed("a/a.go", followed)); len(r.got) != 1 || !strings.HasPrefix(r.got[0], "GHC-CPLX-010") {
		t.Errorf("pair followed by declarations: %q", r.got)
	}
	// Struct keys are fields, not locals; parameters bind by position; a global used before a local
	// of its name stays the global.
	for name, src := range map[string]string{
		"fields":  "type R struct{ lo, hi, min, max int }\n\nfunc A(lo, hi int) R {\n\tr := R{lo: lo, hi: hi}\n\treturn r\n}\n\nfunc B(min, max int) R {\n\tr := R{min: min, max: max}\n\treturn r\n}\n",
		"swapped": "func Lt(a, b int) bool {\n\tx := a\n\treturn x < b\n}\n\nfunc Gt(a, b int) bool {\n\tx := b\n\treturn x < a\n}\n",
		"globals": "var limit, capacity int\n\nfunc A(n int) int {\n\tif n > limit {\n\t\treturn limit\n\t}\n\tfor limit := 0; limit < n; limit++ {\n\t\tn += limit\n\t}\n\treturn n\n}\n\nfunc B(n int) int {\n\tif n > capacity {\n\t\treturn capacity\n\t}\n\tfor capacity := 0; capacity < n; capacity++ {\n\t\tn += capacity\n\t}\n\treturn n\n}\n",
	} {
		if r := dupRun(t, parsed("a/a.go", head+src)); r.got != nil {
			t.Errorf("%s: %q", name, r.got)
		}
	}
	// Semicolons and type parameter names do not hide a copy; two init functions are compared.
	for name, src := range map[string]string{
		"semicolons": "func A(n int) int {\n\tx := n + 1\n\ty := x * 2\n\treturn y\n}\n\nfunc B(n int) int {\n\tx := n + 1; y := x * 2\n\treturn y\n}\n",
		"generics":   "func Map[T, U any](s []T, f func(T) U) []U {\n\tr := make([]U, 0, len(s))\n\tfor _, v := range s {\n\t\tr = append(r, f(v))\n\t}\n\treturn r\n}\n\nfunc Map2[K, V any](s []K, f func(K) V) []V {\n\tr := make([]V, 0, len(s))\n\tfor _, v := range s {\n\t\tr = append(r, f(v))\n\t}\n\treturn r\n}\n",
	} {
		if r := dupRun(t, parsed("a/a.go", head+src)); len(r.got) != 1 || !strings.HasPrefix(r.got[0], "GHC-CPLX-010") {
			t.Errorf("%s: %q", name, r.got)
		}
	}
	initA := parsed("a/a.go", head+"func init() {\n\tx := 1\n\tprintln(x)\n}\n")
	initB := parsed("a/b.go", head+"func init() {\n\ty := 1\n\tprintln(y)\n}\n")
	if r := dupRun(t, initA, initB); len(r.got) != 1 {
		t.Errorf("init functions: %q", r.got)
	}
	// Functions written once per platform are not counted in the share either.
	if r := dupRun(t, parsed("a/x_linux.go", "//go:build linux\n\n"+head+work("Sum", "total", "v", "min")),
		parsed("a/x_darwin.go", "//go:build darwin\n\n"+head+work("Sum", "n", "v", "min"))); r.got != nil || *r.share.Value != 0 {
		t.Errorf("platform share: %+v", r)
	}

	// Re-check cases. A near-copy that fails the comparison hides nothing after it; decoys that are
	// the same shape but rename inconsistently cannot use up the places a block is compared with.
	z := parsed("z/z.go", head+work("A1", "total", "v", "min")+"\n"+work("A2", "total", "v", "max"))
	inconsistent := func(src string) string {
		n := 0
		return regexp.MustCompile(`\btotal\b`).ReplaceAllStringFunc(src, func(string) string {
			if n++; n%2 == 0 {
				return "weight"
			}
			return "total"
		})
	}
	d := parsed("d/d.go", head+inconsistent(work("A1", "total", "v", "min")+"\n"+work("A2", "total", "v", "max")))
	o := parsed("o/o.go", head+strings.Replace(work("A2", "acc", "x", "max"), "\treturn acc\n", "\tacc++\n\treturn acc\n", 1))
	if r := dupRun(t, d, z, o); len(r.got) != 1 || !strings.Contains(r.got[0], "o/o.go") || !strings.Contains(r.got[0], "of z/z.go") {
		t.Errorf("after a failed comparison: %q", r.got)
	}
	var decoys []facts.File
	for k := range candidates + 1 {
		src := work("Sum", "total", "v", "min")
		m := 0
		src = regexp.MustCompile(`\btotal\b`).ReplaceAllStringFunc(src, func(string) string {
			if m++; m == k+2 {
				return "other"
			}
			return "total"
		})
		decoys = append(decoys, parsed(fmt.Sprintf("a%d/d.go", k), head+src))
	}
	real := append(decoys, parsed("m/m.go", head+work("Sum", "total", "v", "min")), parsed("z/z.go", head+work("Add", "acc", "x", "min")))
	if r := dupRun(t, real...); !slices.ContainsFunc(r.got, func(s string) bool {
		return strings.Contains(s, "z/z.go:3 Add has the signature and body of Sum at m/m.go:3")
	}) {
		t.Errorf("behind decoys: %q", r.got)
	}
	// Platform files: nothing between them is reported or counted, also across two functions.
	two := head + "const limit = 3\n\n" + work("Sum", "total", "v", "min") + "\n" + work("Max", "best", "v", "max")
	if r := dupRun(t, parsed("a/x_linux.go", "//go:build linux\n\n"+two), parsed("a/x_darwin.go", "//go:build darwin\n\n"+two)); r.got != nil || *r.share.Value != 0 {
		t.Errorf("platform files: %+v", r)
	}
	// A local declared in a closed block does not make a later use of the name local; map keys are
	// expressions, so a local there may be renamed.
	for name, c := range map[string]struct {
		src  string
		want int
	}{
		"closed block": {"func g(n int) int { return n * 2 }\n\nfunc h(n int) int { return n - 7 }\n\nfunc A(n int) int {\n\tif n > 9 {\n\t\tg := n / 2\n\t\tn = g\n\t}\n\treturn g(n)\n}\n\nfunc B(n int) int {\n\tif n > 9 {\n\t\th := n / 2\n\t\tn = h\n\t}\n\treturn h(n)\n}\n", 0},
		"map keys":     {"func A(n int) map[int]bool {\n\tk := n + 1\n\tm := map[int]bool{k: true}\n\treturn m\n}\n\nfunc B(n int) map[int]bool {\n\tj := n + 1\n\tm := map[int]bool{j: true}\n\treturn m\n}\n", 1},
	} {
		if r := dupRun(t, parsed("a/a.go", head+c.src)); len(r.got) != c.want {
			t.Errorf("%s: %q", name, r.got)
		}
	}

	// When the walk dropped the token streams, duplication is not looked for.
	big := parsed("a/a.go", same)
	big.Tokens, big.TokensDropped = facts.Tokens{}, true
	if r := dupRun(t, big); r.got != nil || r.share.Reason != report.TooLarge {
		t.Errorf("too large: %+v", r)
	}
}
