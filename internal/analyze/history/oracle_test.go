package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/gitx"
	"github.com/Git-Hospital/githealthcheck/internal/repo"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// TestOracle computes the truck factor of a real repository for comparison with the reference tool.
// It runs only under scripts/tf-oracle.sh, which sets GHC_TF_ORACLE to the repository and
// GHC_TF_ORACLE_OUT to a directory. It writes there the files it counted (filelist.log, the reference
// tool's input) and its result (ghc.json). Bots are not left out, since the tool keeps them.
func TestOracle(t *testing.T) {
	root, out := os.Getenv("GHC_TF_ORACLE"), os.Getenv("GHC_TF_ORACLE_OUT")
	if root == "" || out == "" {
		t.Skip("set by scripts/tf-oracle.sh")
	}
	ctx := context.Background()
	git, err := execx.NewGit(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	head, err := git.Run(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	h := strings.TrimSpace(string(head))
	tree, err := git.Run(ctx, "ls-tree", "-r", "-z", "--full-tree", h)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repo.ParseTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	blobID, err := repo.BlobID(git.ObjectFormat())
	if err != nil {
		t.Fatal(err)
	}
	walked, err := repo.Walk(ctx, git.Root(), entries, blobID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range walked.Files {
		if !f.Vendored && !f.Generated {
			paths = append(paths, f.Path)
		}
	}
	slices.Sort(paths)

	commits, err := gitx.Log(ctx, git, h)
	if err != nil {
		t.Fatal(err)
	}
	src, skip := repo.ReadFile(git.Root(), ".mailmap", repo.MaxSourceBytes)
	switch {
	case skip == nil:
		mm := gitx.ParseMailmap(src)
		for i := range commits {
			commits[i].Name, commits[i].Email = mm.Map(commits[i].Name, commits[i].Email)
		}
	case skip.Reason != report.SkipMissing:
		t.Fatalf(".mailmap: %+v", skip)
	}
	res, err := TruckFactor(ctx, commits, paths, Developers(commits))
	if err != nil {
		t.Fatal(err)
	}

	type dev struct {
		Key   string `json:"key"`
		Files int    `json:"files"`
	}
	result := struct {
		TF           int   `json:"tf"`
		Files        int   `json:"files"`
		CoveredAfter int   `json:"coveredAfter"`
		Commits      int   `json:"commits"`
		Devs         []dev `json:"devs"`
	}{TF: res.TF, Files: res.Files, CoveredAfter: res.CoveredAfter, Commits: len(commits)}
	authored := map[string]int{}
	var perFile strings.Builder // path;number of authors, as the debug patch of the tool prints it
	for _, f := range res.files {
		for _, a := range f.authors {
			authored[a]++
		}
		fmt.Fprintf(&perFile, "%s;%d\n", f.path, len(f.authors))
	}
	for _, d := range res.TFDevs {
		result.Devs = append(result.Devs, dev{d, authored[d]})
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(
		os.WriteFile(filepath.Join(out, "filelist.log"), []byte(strings.Join(paths, "\n")+"\n"), 0o644),
		os.WriteFile(filepath.Join(out, "ghc.json"), body, 0o644),
		os.WriteFile(filepath.Join(out, "ghc-files.txt"), []byte(perFile.String()), 0o644),
	); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", body)
}
