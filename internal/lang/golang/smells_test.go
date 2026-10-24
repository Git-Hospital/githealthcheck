package golang

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

func filled(t *testing.T, src string) facts.File {
	t.Helper()
	f := facts.File{Path: "a/a.go"}
	Support{}.Fill(&f, []byte(src))
	return f
}

func TestSmells(t *testing.T) {
	f := filled(t, `package a

import (
	"errors"
	"fmt"
)

func emptyCheck(f func() error) {
	if err := f(); err != nil {
	}
	if err := f(); err != nil {
		// deliberately ignored: a comment says so
	}
	if err := f(); err == nil {
	}
}

func dropped(f func() error) error {
	if err := f(); err != nil {
		return nil
	}
	if err := f(); err != nil {
		fmt.Println(err)
		return nil // logged first: not a single return
	}
	g := func() error {
		if err := f(); err != nil {
			return nil // a callback may return nil to skip: not judged
		}
		return nil
	}
	return g()
}

func noError(f func() error) *int {
	if err := f(); err != nil {
		return nil // the function returns no error: nil is a pointer here
	}
	return new(int)
}

func todo() error {
	return errors.New("not implemented")
}

func later() {
	panic("TODO: implement me")
}

func real() {
	panic("unreachable")
}

// Prose is not code. Neither is a note: like this.
// fmt.Println("debug")
// x := compute(y)
func commented() {
	x := 1 // y = x: a comment after code is about the code
	_ = x
	//   indented(example)
	// Wrap (2)
	// 4;2 (semicolon) = underline + dim
	// return
	//	example := inADocComment()
	//go:noinline
	// TODO(someone): fix
	// func old() {
	// 	// a comment inside the code
	// 	doIt()
	// }
}
`)
	var got []string
	for _, s := range f.Smells {
		got = append(got, string(s.Kind)+" "+s.Func+" "+itoa(s.Line)+"-"+itoa(s.EndLine))
	}
	want := []string{
		"empty_error_check emptyCheck 9-10",
		"error_dropped dropped 19-21",
		"placeholder todo 42-44",
		"placeholder later 46-48",
		"commented_code  55-56",
		"commented_code  63-63",
		"commented_code  67-70",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	if !reflect.DeepEqual(f.Imports, []facts.Import{{Path: "errors", Line: 4}, {Path: "fmt", Line: 5}}) {
		t.Errorf("imports %+v", f.Imports)
	}
}

func itoa(n int) string { return fmt.Sprint(n) }

func TestRefs(t *testing.T) {
	f := filled(t, `package a

//export exported
func exported() {}

//go:linkname linked runtime.x
func linked()

func used() {}

func selfOnly(n int) int { return selfOnly(n - 1) }

func caller() { used(); var f = helper; _ = f }

func helper() {}
`)
	for _, name := range []string{"exported", "linked", "used", "helper"} {
		if !slices.Contains(f.Refs, name) {
			t.Errorf("%s not referenced in %v", name, f.Refs)
		}
	}
	for _, name := range []string{"selfOnly", "caller"} {
		if slices.Contains(f.Refs, name) {
			t.Errorf("%s referenced in %v", name, f.Refs)
		}
	}
}

// Cases from the review of the complexity family.
func TestSmellsEdges(t *testing.T) {
	kinds := func(f facts.File) []string {
		var out []string
		for _, s := range f.Smells {
			out = append(out, fmt.Sprintf("%s %s %d", s.Kind, s.Func, s.Line))
		}
		return out
	}
	for name, c := range map[string]struct {
		src  string
		want []string
	}{
		"closure in a goroutine": {"package a\n\nfunc f(g func() error) {\n\tgo func() {\n\t\tif err := g(); err != nil {\n\t\t}\n\t}()\n}\n",
			[]string{"empty_error_check f 5"}},
		"package-level function literal": {"package a\n\nvar h = func(g func() error) {\n\tif err := g(); err != nil {\n\t}\n}\n",
			[]string{"empty_error_check a function literal 4"}},
		"an error about a to-do item": {"package a\n\nimport \"errors\"\n\nfunc f() error { return errors.New(\"todo not found\") }\n\nfunc g() error { return errors.New(\"TODO\") }\n",
			[]string{"placeholder g 7"}},
		"expected output of an example": {"package a\n\nfunc ExampleF() {\n\tprintln(\"x = 1\")\n\t// output:\n\t// x = 1\n\t// p.Close()\n}\n", nil},
		"output: elsewhere hides nothing": {"package a\n\nfunc F() {\n\t// Output:\n\t// x := compute()\n\t// os.Remove(path)\n}\n",
			[]string{"commented_code  5"}},
		"labelled prose": {"package a\n\n// New makes a client.\n//\n// Example: c := New(cfg)\n// Usage: client.Do(req)\nfunc New(cfg int) int { return cfg }\n", nil},
		"a //line directive does not move lines": {"package a\n\n//line other.go:5000\nfunc f(g func() error) {\n\tif err := g(); err != nil {\n\t}\n}\n",
			[]string{"empty_error_check f 5"}},
	} {
		f := filled(t, c.src)
		if got := kinds(f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}

	f := filled(t, "package a\n\n//go:wasmexport add\nfunc add(a, b int32) int32 { return a + b }\n")
	if !slices.Contains(f.Refs, "add") {
		t.Errorf("wasmexport: refs %q", f.Refs)
	}

	// The smells kept per file are bounded; the rest are counted.
	var b strings.Builder
	b.WriteString("package a\n\n")
	for range MaxSmells + 7 {
		b.WriteString("//f()\n\n")
	}
	f = filled(t, b.String())
	if len(f.Smells) != MaxSmells || f.SmellsDropped[facts.CommentedCode] != 7 {
		t.Errorf("kept %d, dropped %v", len(f.Smells), f.SmellsDropped)
	}
	// The bound is per kind: a flood of one kind does not hide another.
	b.WriteString("func h(g func() error) {\n\tif err := g(); err != nil {\n\t}\n}\n")
	f = filled(t, b.String())
	if !slices.ContainsFunc(f.Smells, func(s facts.Smell) bool { return s.Kind == facts.EmptyErrorCheck }) {
		t.Error("the empty error check after the flood was dropped")
	}
}
