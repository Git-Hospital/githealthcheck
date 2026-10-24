package golang

import (
	"fmt"
	"reflect"
	"slices"
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
			return nil // a closure has its own results: not judged
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
