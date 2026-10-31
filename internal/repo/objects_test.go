package repo

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// gitIn runs plain git in dir, with the user's own config out of the way.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(execx.Env(os.Environ()),
		"HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// treeOf opens root and lists the tree of rev.
func treeOf(t *testing.T, root, rev string) (*execx.Git, []Entry) {
	t.Helper()
	git, err := execx.NewGit(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := git.Run(context.Background(), "ls-tree", "-r", "-z", "--full-tree", gitIn(t, root, "rev-parse", rev))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ParseTree(out)
	if err != nil {
		t.Fatal(err)
	}
	return git, entries
}

// sample makes a repository whose commit holds every kind of entry the walk tells apart.
func sample(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	write(t, root, "main.go", "package main\n\nfunc main() {\n\tif true {\n\t\tprintln(1)\n\t}\n}\n")
	write(t, root, "same.go", "package main\n\nfunc main() {\n\tif true {\n\t\tprintln(1)\n\t}\n}\n")
	write(t, root, "empty.go", "")
	write(t, root, "lib/no_newline.py", "def f(x):\n    return x")
	write(t, root, "vendor/dep/dep.go", "package dep\n")
	write(t, root, "README.md", "# readme\n")
	write(t, root, "big.go", "package big\n"+strings.Repeat("// pad\n", MaxSourceBytes/7+1))
	if err := os.Symlink("main.go", filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("ab", 20)+",sub")
	gitIn(t, root, "commit", "-q", "-m", "one")
	return root
}

func TestWalkObjectsMatchesWalk(t *testing.T) {
	root := sample(t)
	git, entries := treeOf(t, root, "HEAD")
	// git.Root() has its symbolic links resolved, as the scan passes it to both.
	fromDisk, err := Walk(context.Background(), git.Root(), entries, sha1ID(t))
	if err != nil {
		t.Fatal(err)
	}
	fromObjects, err := WalkObjects(context.Background(), git, entries, sha1ID(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromObjects, fromDisk) {
		t.Errorf("objects:\n%+v\ndisk:\n%+v", fromObjects, fromDisk)
	}
	// The sample reaches every branch the two must agree on.
	got := map[report.SkipReason]bool{}
	for _, s := range fromDisk.Skipped {
		got[s.Reason] = true
	}
	for _, r := range []report.SkipReason{report.SkipTooLarge, report.SkipSymlink, report.SkipNestedRepo} {
		if !got[r] {
			t.Errorf("no file skipped as %s: %+v", r, fromDisk.Skipped)
		}
	}
	if len(fromDisk.Files) != 5 {
		t.Errorf("%d files read, want 5: %+v", len(fromDisk.Files), fromDisk.Files)
	}
}

// A past commit is read as it was, whatever the work tree holds now.
func TestWalkObjectsReadsAPastCommit(t *testing.T) {
	root := sample(t)
	first := gitIn(t, root, "rev-parse", "HEAD")
	write(t, root, "main.go", "package main\n")
	gitIn(t, root, "rm", "-q", "lib/no_newline.py")
	gitIn(t, root, "commit", "-q", "-am", "two")
	write(t, root, "main.go", "package main // not committed\n")
	if err := os.Remove(filepath.Join(root, "same.go")); err != nil {
		t.Fatal(err)
	}

	git, entries := treeOf(t, root, first)
	past, err := WalkObjects(context.Background(), git, entries, sha1ID(t))
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	gitIn(t, checkout, "clone", "-q", "--no-checkout", root, ".")
	gitIn(t, checkout, "checkout", "-q", first)
	want, err := Walk(context.Background(), checkout, entries, sha1ID(t))
	if err != nil {
		t.Fatal(err)
	}
	want.Root = git.Root()
	if !reflect.DeepEqual(past, want) {
		t.Errorf("past commit:\n%+v\nits checkout:\n%+v", past, want)
	}
}

func TestWalkObjectsSkipsMissingAndInvalidObjects(t *testing.T) {
	root := sample(t)
	git, entries := treeOf(t, root, "HEAD")
	var mainOID string
	for _, e := range entries {
		if e.Path == "main.go" {
			mainOID = e.OID
		}
	}
	if err := os.Remove(filepath.Join(root, ".git", "objects", mainOID[:2], mainOID[2:])); err != nil {
		t.Fatal(err)
	}
	// A name that is not a full hexadecimal name is never sent to cat-file, where "HEAD:main.go" or
	// a second line would be read as another request.
	entries = append(entries,
		Entry{Mode: "100644", OID: "HEAD:main.go", Path: "a.go"},
		Entry{Mode: "100644", OID: mainOID[:39] + "\n" + mainOID, Path: "b.go"},
		Entry{Mode: "100644", OID: strings.ToUpper(mainOID), Path: "c.go"},
	)
	tree, err := WalkObjects(context.Background(), git, entries, sha1ID(t))
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]report.Skip{}
	for _, s := range tree.Skipped {
		skipped[s.Path] = s
	}
	// same.go has the same content, so the same missing blob.
	for _, p := range []string{"main.go", "same.go", "a.go", "b.go", "c.go"} {
		if s, ok := skipped[p]; !ok || s.Reason != report.SkipUnreadable {
			t.Errorf("%s: skipped as %+v, want unreadable", p, s)
		}
	}
	if d := skipped["main.go"].Detail; d != "does not exist" {
		t.Errorf("missing blob detail %q", d)
	}
	if len(tree.Files) != 3 {
		t.Errorf("%d files read, want 3", len(tree.Files))
	}
}

func TestWalkObjectsCancelled(t *testing.T) {
	root := sample(t)
	git, entries := treeOf(t, root, "HEAD")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WalkObjects(ctx, git, entries, sha1ID(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestReadCheck(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, tc := range []struct {
		in   string
		want object
		ok   bool
	}{
		{oid + " blob 12\n", object{"blob", 12}, true},
		{oid + " tree 0\n", object{"tree", 0}, true},
		{oid + " missing\n", object{}, true},
		{strings.Repeat("b", 40) + " blob 12\n", object{}, false}, // answers another name
		{oid + " blob -1\n", object{}, false},
		{oid + " blob 1x\n", object{}, false},
		{oid + " weird 1\n", object{}, false},
		{oid + " blob 12", object{}, false}, // cut short
		{oid + " ambiguous\n", object{}, false},
	} {
		got, err := readCheck(bufio.NewReader(strings.NewReader(tc.in)), oid)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("readCheck(%q) = %+v, %v", tc.in, got, err)
		}
	}
}

func TestReadObject(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{oid + " blob 3\nabc\n", true},
		{oid + " blob 4\nabc\n", false},                     // the size --batch-check gave differs
		{strings.Repeat("b", 40) + " blob 3\nabc\n", false}, // another object
		{oid + " blob 3\nabcd", false},                      // no newline after the content
		{oid + " blob 3\nab", false},                        // cut short
		{oid + " tree 3\nabc\n", false},
		{oid + " missing\n", false}, // not a blob
	} {
		src, err := readObject(bufio.NewReader(strings.NewReader(tc.in)), oid, 3)
		if (err == nil) != tc.ok || (tc.ok && string(src) != "abc") {
			t.Errorf("readObject(%q) = %q, %v", tc.in, src, err)
		}
	}
}

func TestReadObjectMissing(t *testing.T) {
	oid := strings.Repeat("a", 40)
	if _, err := readObject(bufio.NewReader(strings.NewReader(oid+" missing\n")), oid, 3); !errors.Is(err, errObjectMissing) {
		t.Errorf("err = %v, want errObjectMissing", err)
	}
	if err := drained(bufio.NewReader(strings.NewReader(""))); err != nil {
		t.Errorf("drained at EOF: %v", err)
	}
	if err := drained(bufio.NewReader(strings.NewReader("x"))); !errors.Is(err, errProtocol) {
		t.Errorf("drained with a byte left: %v", err)
	}
}
