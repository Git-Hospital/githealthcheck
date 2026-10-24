package complexity

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// parsed fills a Go file as the walk does.
func parsed(path, src string) facts.File {
	f := facts.File{Path: path, Language: "go", Lines: strings.Count(src, "\n")}
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
	if want := []string{"GHC-CPLX-009 b/b.go:3 lines 3-25 repeat lines 3-25 of a/a.go (140 tokens)"}; !reflect.DeepEqual(r.got, want) {
		t.Errorf("renamed copy: %q", r.got)
	}
	// 23 duplicated lines of each copy, over 27 + 28 lines.
	if r.share.Inputs != 2 || fmt.Sprintf("%.4f", *r.share.Value) != fmt.Sprintf("%.4f", 46.0/55.0) {
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
	if r := dupRun(t, a, b, e); len(r.got) != 2 || !strings.Contains(r.got[1], "e/e.go:3 lines 3-25 repeat lines 3-25 of a/a.go") {
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
		"GHC-CPLX-010 a/a.go:29 Add has the body of Sum at a/a.go:3, up to the names of its locals"}) {
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

	// Past the token limit duplication is not looked for.
	defer func(n int) { dupLimit = n }(dupLimit)
	dupLimit = 100
	if r := dupRun(t, parsed("a/a.go", same)); r.got != nil || r.share.Reason != report.TooLarge {
		t.Errorf("too large: %+v", r)
	}
}
