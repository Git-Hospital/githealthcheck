package golang

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

func measured(t *testing.T, src string) map[string]facts.Function {
	t.Helper()
	f := facts.File{Path: "a/a.go"}
	Support{}.Fill(&f, []byte(src))
	out := map[string]facts.Function{}
	for _, fn := range f.Funcs {
		fn.Line, fn.EndLine = 0, 0
		out[fn.Name] = fn
	}
	return out
}

func TestMeasure(t *testing.T) {
	got := measured(t, `package a

// The two examples of the white paper, in Go: 7 and 1.
func sumOfPrimes(max int) int {
	total := 0

	// a comment line does not count
OUT:
	for i := 1; i <= max; i++ { // +1
		for j := 2; j < i; j++ { // +2 (nesting 1)
			if i%j == 0 { // +3 (nesting 2)
				continue OUT // +1
			}
		}
		total += i
	}
	return total
}

func getWords(number int) string {
	switch number { // +1
	case 1:
		return "one"
	case 2:
		return "a couple"
	case 3:
		return "a few"
	default:
		return "lots"
	}
}

func chain(a, b bool) {
	for { // +1
		if a { // +2
		} else if b { // +1, no nesting
		} else { // +1
			if a { // +3: inside the else, nesting 2
			}
		}
		break
	}
}

func logic(a, b, c, d bool) bool {
	if a && b && c || d { // +1, +2 for two sequences
	}
	if a && (b || c) { // +1, +2: parentheses do not hide the change of operator
	}
	return a && b // +1
} // cyclomatic: 1, 2 ifs, 6 operators

func (t *T[P]) Walk(n int) int {
	f := func() {
		if n > 0 { // +2: a function literal nests
		}
	}
	f()
	if n <= 1 { // +1
		return 1
	}
	return t.Walk(n-1) + 1 // +1 for recursion, once
}

func jumps(ch chan int, x any) {
	select { // +1
	case <-ch:
	default:
	}
	switch x.(type) { // +1
	case int:
		goto end // +1
	}
end:
}

func guards(f func() error) error {
	if err := f(); err != nil { // a guard: nothing
		return err
	}
	if closeErr := f(); closeErr != nil { // nothing
		return closeErr
	}
	if err := f(); nil == err { // nothing either way round
		return nil
	}
	if err := f(); err != nil { // +1, and +1 for the else: not a guard
		return err
	} else {
	}
	if e := f(); e != nil { // +1: not named like an error
		return e
	}
	return nil
}
`)
	want := map[string]facts.Function{
		"sumOfPrimes": {Name: "sumOfPrimes", Lines: 11, Cognitive: 7, Cyclomatic: 4, Nesting: 3},
		"getWords":    {Name: "getWords", Lines: 10, Cognitive: 1, Cyclomatic: 4, Nesting: 1},
		"chain":       {Name: "chain", Lines: 9, Cognitive: 8, Cyclomatic: 5, Nesting: 3},
		"logic":       {Name: "logic", Lines: 5, Cognitive: 7, Cyclomatic: 9, Nesting: 1},
		"T.Walk":      {Name: "T.Walk", Lines: 9, Cognitive: 4, Cyclomatic: 3, Nesting: 1, Method: true, Exported: true},
		"guards":      {Name: "guards", Lines: 17, Cognitive: 3, Cyclomatic: 6, Nesting: 1},
		"jumps":       {Name: "jumps", Lines: 9, Cognitive: 3, Cyclomatic: 3, Nesting: 1},
	}
	for name, w := range want {
		if g := got[name]; !reflect.DeepEqual(g, w) {
			t.Errorf("%s:\n got  %+v\n want %+v", name, g, w)
		}
	}
}

// Cases from the review of the complexity family.
func TestMeasureEdges(t *testing.T) {
	one := func(src string) facts.Function {
		t.Helper()
		f := facts.File{Path: "a/a.go"}
		Support{}.Fill(&f, []byte(src))
		if len(f.Funcs) == 0 {
			t.Fatalf("no function in %q", src)
		}
		return f.Funcs[0]
	}
	body := strings.Repeat("\tx++\n", 100)
	if fn := one("package a\n\n//line other.go:5000\nfunc Long() {\n\tx := 0\n" + body + "\t_ = x\n}\n"); fn.Line != 4 || fn.Lines != 102 {
		t.Errorf("//line: line %d, %d lines; want 4, 102", fn.Line, fn.Lines)
	}
	if fn := one("package a\n\n//line other.go:1\nfunc Long() {\n\tx := 0\n" + body + "//line other.go:1\n\t_ = x\n}\n"); fn.Lines != 102 {
		t.Errorf("//line inside: %d lines; want 102", fn.Lines)
	}
	raw := "package a\r\n\r\nfunc F() string {\r\n\treturn `\r\n" + strings.Repeat("line\r\n", 100) + "`\r\n}\r\n"
	if fn := one(raw); fn.Lines != 102 {
		t.Errorf("CRLF raw string: %d lines; want 102", fn.Lines)
	}
	if fn := one("package a\n\nfunc f() {\n\tif func() bool { for { for { for { for { for { return true } } } } } }() {\n\t}\n}\n"); fn.Nesting != 5 {
		// The condition sits at the if's own depth, as in cognitive complexity: the loops nest from 0.
		t.Errorf("closure in a condition: nesting %d; want 5", fn.Nesting)
	}
	if fn := one("package a\n\nfunc Walk[T any](n *T, d int) int { return Walk[T](n, d-1) }\n"); fn.Cognitive != 1 {
		t.Errorf("generic recursion: cognitive %d; want 1", fn.Cognitive)
	}
	if fn := one("package a\n\nfunc run() {\n\trun := func() {}\n\trun()\n}\n"); fn.Cognitive != 0 {
		t.Errorf("a local named like the function: cognitive %d; want 0", fn.Cognitive)
	}
}
