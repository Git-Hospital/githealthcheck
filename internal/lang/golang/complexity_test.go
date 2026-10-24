package golang

import (
	"reflect"
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
