package python

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// TestOracle writes the cognitive and cyclomatic complexity of every Python function under
// $GHC_CPLX_ORACLE to $GHC_CPLX_ORACLE_OUT, one "<file>:<line> <cognitive> <cyclomatic>" per line,
// for scripts/py-oracle.sh to compare with radon's and SonarSource's.
func TestOracle(t *testing.T) {
	root, out := os.Getenv("GHC_CPLX_ORACLE"), os.Getenv("GHC_CPLX_ORACLE_OUT")
	if root == "" || out == "" {
		t.Skip("GHC_CPLX_ORACLE and GHC_CPLX_ORACLE_OUT are not set")
	}
	p, err := Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".py") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p.Submit(len(paths), facts.File{Path: path, Language: "python"}, src)
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range p.Wait() {
		for _, fn := range r.File.Funcs {
			lines = append(lines, fmt.Sprintf("%s:%d %d %d", paths[r.Index], fn.Line, fn.Cognitive, fn.Cyclomatic))
		}
	}
	sort.Strings(lines)
	if err := os.WriteFile(out, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
