package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOracle prints the paper's cognitive complexity and the cyclomatic complexity of every function under
// $GHC_CPLX_ORACLE as "<file>:<line> <cognitive> <cyclomatic>", for comparison with gocognit and
// gocyclo (scripts/cplx-oracle.sh).
func TestOracle(t *testing.T) {
	root, out := os.Getenv("GHC_CPLX_ORACLE"), os.Getenv("GHC_CPLX_ORACLE_OUT")
	if root == "" || out == "" {
		t.Skip("GHC_CPLX_ORACLE and GHC_CPLX_ORACLE_OUT are not set")
	}
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		file, _ := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if file == nil {
			return nil
		}
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
				// The paper's measure, without the error-guard exemption, is what gocognit computes.
				fmt.Fprintf(&b, "%s:%d %d %d\n", p, fset.Position(fd.Pos()).Line, cognitive(fd, false), cyclomatic(fd))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
