package golang

import (
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

func TestTokens(t *testing.T) {
	f := facts.File{Path: "a/a.go"}
	Support{}.Fill(&f, []byte(`package a

import (
	"fmt"
	"os"
)

// F prints.
func F(n int) {
	m := n + 1 // a comment is no token
	fmt.Println(m, "x", os.Args)
}
`))
	tk := f.Tokens
	// From func: func F ( n int ) { m := n + 1 fmt . Println ( m , "x" , os . Args ) }
	if len(tk.Kind) != 25 || tk.Line[0] != 9 || tk.Line[len(tk.Line)-1] != 12 {
		t.Fatalf("%d tokens, lines %v", len(tk.Kind), tk.Line)
	}
	if fn := f.Funcs[0]; fn.Body != [2]int32{7, 24} {
		t.Errorf("body %v, want the tokens between the braces", fn.Body)
	}
	kinds := map[int]string{}
	for i, name := range tk.Name {
		switch {
		case name == 0:
		case name&nameLiteral == nameLiteral:
			kinds[i] = "literal"
		case name&nameLocal != 0:
			kinds[i] = "local"
		case name&nameFixed != 0:
			kinds[i] = "fixed"
		}
	}
	want := map[int]string{1: "fixed", 3: "local", 4: "fixed", 7: "local", 9: "local", 11: "literal",
		12: "fixed", 14: "fixed", 16: "local", 18: "literal", 20: "fixed", 22: "fixed"}
	for i, k := range want {
		if kinds[i] != k {
			t.Errorf("token %d is %q, want %q", i, kinds[i], k)
		}
	}
	if len(kinds) != len(want) {
		t.Errorf("names %v", kinds)
	}
	if tk.Name[3] != tk.Name[9] || tk.Kind[3] != kindIdent || tk.Kind[11] != kindLiteral {
		t.Error("the same local has the same name; kinds of identifiers and literals")
	}

	// Tests, generated and ignored files carry no tokens.
	for _, path := range []string{"a/a_test.go", "testdata/a.go", "vendor/x/x.go"} {
		g := facts.File{Path: path, Vendored: path == "vendor/x/x.go"}
		Support{}.Fill(&g, []byte("package a\n\nfunc F() { println(1) }\n"))
		if len(g.Tokens.Kind) != 0 {
			t.Errorf("%s has tokens", path)
		}
	}
}
