package lang

import (
	"context"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/python"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func TestSet(t *testing.T) {
	s := Open(context.Background())
	defer s.Close()
	if !s.Status("go").Analysed {
		t.Error("go is not analysed")
	}
	// Recognised by the walk but not analysed in this build.
	for _, name := range []string{"typescript", "javascript", "rust", ""} {
		if st := s.Status(name); st.Analysed || st.Reason != "" {
			t.Errorf("%q: %+v", name, st)
		}
	}
	f := facts.File{Path: "a.go", Language: "go"}
	if !s.Fill(0, &f, []byte("package a\n\nfunc F() {}\n")) || !f.Analysed || f.Functions != 1 {
		t.Errorf("go file: %+v", f)
	}
	r := facts.File{Path: "a.rs", Language: "rust"}
	if !s.Fill(1, &r, []byte("fn main() {}\n")) || r.Analysed {
		t.Errorf("rust file: %+v", r)
	}
	// python3 is started only for a Python file, and listed only then.
	if st := s.Status("python"); st != (Status{}) || len(s.Tools()) != 0 {
		t.Errorf("python before any Python file: %+v, tools %v", st, s.Tools())
	}
}

// Without python3, Python is counted but not analysed, and says why.
func TestSetWithoutPython(t *testing.T) {
	defer func(p string) { python.Interpreter = p }(python.Interpreter)
	python.Interpreter = "githealthcheck-no-such-python"
	s := Open(context.Background())
	defer s.Close()
	f := facts.File{Path: "tests/test_a.py", Language: "python"}
	if !s.Fill(0, &f, []byte("def f():\n    pass\n")) || f.Analysed || !f.Test || f.Package != "tests" {
		t.Errorf("python file without python3: %+v", f)
	}
	if st := s.Status("python"); st.Analysed || st.Reason != report.ToolNotFound || len(s.Tools()) != 0 {
		t.Errorf("python: %+v, tools %v", st, s.Tools())
	}
	if got := s.Wait(); len(got) != 0 {
		t.Errorf("results without python3: %v", got)
	}
}
