package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// commit makes one commit in dir with the given author and author time.
func commit(t *testing.T, dir, name, email, when string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_AUTHOR_DATE="+when,
		"GIT_COMMITTER_NAME=c", "GIT_COMMITTER_EMAIL=c@example.com", "GIT_COMMITTER_DATE="+when)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLog(t *testing.T) {
	dir := t.TempDir()
	commit(t, dir, "", "", "", "init", "-q", "-b", "main")
	body := strings.Repeat("a line that git can match across a rename\n", 20)
	odd := "dir/tab\there \"quoted\" ünï\ncode.py"
	write(t, dir, "a.py", body)
	write(t, dir, odd, "x\n")
	commit(t, dir, "Ann\x01 Newline", "ann@example.com", "1700000000 +0000", "add", "-A")
	commit(t, dir, "Ann\x01 Newline", "ann@example.com", "1700000000 +0000", "commit", "-q", "-m", "one")
	if err := os.Rename(filepath.Join(dir, "a.py"), filepath.Join(dir, "b.py")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "c.py", "y\n")
	// The zone does not move a Unix time.
	commit(t, dir, "Bob", "bob@example.com", "1700086400 +0200", "add", "-A")
	commit(t, dir, "Bob", "bob@example.com", "1700086400 +0200", "commit", "-q", "-m", "two")
	if err := os.Remove(filepath.Join(dir, "c.py")); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "Bob", "", "1700172800 +0000", "add", "-A")
	commit(t, dir, "Bob", "", "1700172800 +0000", "commit", "-q", "-m", "three")
	commit(t, dir, "Bob", "bob@example.com", "1700172801 +0000", "commit", "-q", "--allow-empty", "-m", "empty")
	// A merge has no changes of its own and is left out.
	commit(t, dir, "Bob", "bob@example.com", "1700172802 +0000", "checkout", "-q", "-b", "side")
	commit(t, dir, "Bob", "bob@example.com", "1700172802 +0000", "commit", "-q", "--allow-empty", "-m", "side")
	commit(t, dir, "Bob", "bob@example.com", "1700172802 +0000", "checkout", "-q", "main")
	commit(t, dir, "Bob", "bob@example.com", "1700172803 +0000", "commit", "-q", "--allow-empty", "-m", "main")
	commit(t, dir, "Bob", "bob@example.com", "1700172804 +0000", "merge", "-q", "--no-ff", "-m", "merge", "side")

	git, err := execx.NewGit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Log(context.Background(), git, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if len(got[i].Hash) != 40 {
			t.Fatalf("commit %d hash %q", i, got[i].Hash)
		}
		got[i].Hash = ""
	}
	want := []facts.Commit{
		{Name: "Bob", Email: "bob@example.com", Time: 1700172803},
		{Name: "Bob", Email: "bob@example.com", Time: 1700172802},
		{Name: "Bob", Email: "bob@example.com", Time: 1700172801},
		{Name: "Bob", Time: 1700172800, Changes: []facts.Change{{Status: 'D', Path: "c.py"}}},
		{Name: "Bob", Email: "bob@example.com", Time: 1700086400, Changes: []facts.Change{
			{Status: 'R', Old: "a.py", Path: "b.py"}, {Status: 'A', Path: "c.py"}}},
		{Name: "Ann\x01 Newline", Email: "ann@example.com", Time: 1700000000, Changes: []facts.Change{
			{Status: 'A', Path: "a.py"}, {Status: 'A', Path: odd}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseLogRejects(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for name, out := range map[string]string{
		"no marker":       hash + "\x00n\x00e\x001\n",
		"short hash":      "\x01abc\x00n\x00e\x001\n",
		"upper-case hash": "\x01" + strings.Repeat("A", 40) + "\x00n\x00e\x001\n",
		"cut short":       "\x01" + hash + "\x00n\x00e",
		"unknown status":  "\x01" + hash + "\x00n\x00e\x001\nZ\x00a.py\x00\x00",
		"rename cut":      "\x01" + hash + "\x00n\x00e\x001\nR100\x00a.py",
		"change cut":      "\x01" + hash + "\x00n\x00e\x001\nM",
	} {
		if c, err := ParseLog([]byte(out)); err == nil {
			t.Errorf("%s: parsed as %+v", name, c)
		}
	}
	if c, err := ParseLog(nil); err != nil || len(c) != 0 {
		t.Errorf("empty output: %v %v", c, err)
	}
}

// A commit object can hold any author time. git prints one beyond int64 as stored, and a negative or
// malformed one as nothing; the commit is kept, with its time marked unknown, instead of failing the
// whole history.
func TestLogCraftedTimes(t *testing.T) {
	dir := t.TempDir()
	commit(t, dir, "", "", "", "init", "-q", "-b", "main")
	write(t, dir, "a.py", "x\n")
	commit(t, dir, "Ann", "ann@example.com", "1700000000 +0000", "add", "-A")
	commit(t, dir, "Ann", "ann@example.com", "1700000000 +0000", "commit", "-q", "-m", "one")
	run := func(stdin string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tree, parent := run("", "rev-parse", "HEAD^{tree}"), run("", "rev-parse", "HEAD")
	for _, when := range []string{"99999999999999999999", "-5", "soon"} {
		obj := fmt.Sprintf("tree %s\nparent %s\nauthor Eve <eve@example.com> %s +0000\ncommitter Eve <eve@example.com> 1 +0000\n\nm\n", tree, parent, when)
		parent = run(obj, "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
	}
	git, err := execx.NewGit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Log(context.Background(), git, parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("%d commits", len(got))
	}
	for i, c := range got[:3] {
		if !c.NoTime || c.Time != 0 || c.Name != "Eve" {
			t.Errorf("crafted commit %d: %+v", i, c)
		}
	}
	if got[3].NoTime || got[3].Time != 1700000000 {
		t.Errorf("good commit: %+v", got[3])
	}
}
