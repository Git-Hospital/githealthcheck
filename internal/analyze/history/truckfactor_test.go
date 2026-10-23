package history

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// history builds commits from lines oldest first, "<email> <change>...", where a change is A:path,
// M:path, D:path or R:old>new. The result is newest first, as git log prints it; commit i is at
// time i days.
func hist(lines ...string) []facts.Commit {
	var out []facts.Commit
	for i, l := range lines {
		f := strings.Fields(l)
		c := facts.Commit{Hash: fmt.Sprint(i), Email: f[0], Time: int64(i) * 86400}
		for _, ch := range f[1:] {
			status, path, _ := strings.Cut(ch, ":")
			old, newPath, renamed := strings.Cut(path, ">")
			if renamed {
				c.Changes = append(c.Changes, facts.Change{Status: status[0], Old: old, Path: newPath})
			} else {
				c.Changes = append(c.Changes, facts.Change{Status: status[0], Path: path})
			}
		}
		out = append(out, c)
	}
	slices.Reverse(out)
	return out
}

func byEmail(c *facts.Commit) string { return c.Email }

func tf(t *testing.T, commits []facts.Commit, paths ...string) *Result {
	t.Helper()
	r, err := TruckFactor(context.Background(), commits, paths, byEmail)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func authors(r *Result) map[string][]string {
	out := map[string][]string{}
	for _, f := range r.files {
		out[f.path] = f.authors
	}
	return out
}

// The degree of authorship of Avelino et al. (ICPC 2016), with the thresholds of their tool.
func TestAuthorship(t *testing.T) {
	r := tf(t, hist(
		"a A:f.py A:g.py A:h.py",
		// f: b's three deliveries give 3.785 against a's 4.391-0.321*ln(4) = 3.946; normalized 0.959.
		"b M:f.py", "b M:f.py", "b M:f.py",
		// g: c's one delivery gives 3.457 against a's 4.391-0.321*ln(2) = 4.168; normalized 0.829.
		"c M:g.py",
		// h: after a's six deliveries, d's one gives 3.457-0.321*ln(7) = 2.832 against a's
		// 5.375-0.321*ln(2) = 5.152; normalized 0.550.
		"a M:h.py", "a M:h.py", "a M:h.py", "a M:h.py", "a M:h.py", "a M:h.py", "d M:h.py",
	), "f.py", "g.py", "h.py")
	want := map[string][]string{"f.py": {"a", "b"}, "g.py": {"a", "c"}, "h.py": {"a"}}
	if got := authors(r); !reflect.DeepEqual(got, want) {
		t.Errorf("authors %v, want %v", got, want)
	}

	// A file added again by another developer gives them first authorship too; the first author's
	// own re-add counts for nothing, and deletes are not part of the history.
	r = tf(t, hist(
		"a A:f.py",
		"b D:f.py",
		"b A:f.py",
		"a D:f.py",
		"a A:f.py",
		"c M:f.py", "c M:f.py", "c M:f.py", "c M:f.py", "c M:f.py", "c M:f.py", "c M:f.py", "c M:f.py",
	), "f.py")
	// a and b: 4.391-0.321*ln(9) = 3.686; c: 3.293+8*0.164 = 4.605; normalized 0.800, both authors.
	if got := authors(r)["f.py"]; !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("re-added file authors %v", got)
	}

	// The absolute threshold: a developer without an add needs a DOA of at least 3.293. Here e's
	// delivery gives 3.293+0.164-0.321*ln(2) = 3.234 against a's 4.391-0.321*ln(3) = 4.038: e passes
	// the normalized threshold with 0.801 and is still not an author.
	r = tf(t, hist("a A:f.py", "e M:f.py", "x M:f.py"), "f.py")
	if got := authors(r)["f.py"]; !slices.Equal(got, []string{"a"}) {
		t.Errorf("absolute threshold: authors %v", got)
	}
	if v := 3.293 + 0.164 - 0.321*math.Log(2); v >= absoluteDOA {
		t.Fatalf("the case above assumes %f < %f", v, absoluteDOA)
	}

	// The normalized threshold, from both sides: after 9 deliveries by b, a's 4.391-0.321*ln(10) =
	// 3.652 is 0.766 of b's 4.769; after 10, a's 3.621 is 0.734 of b's 4.933.
	for n, want := range map[int][]string{9: {"a", "b"}, 10: {"b"}} {
		lines := []string{"a A:f.py"}
		for range n {
			lines = append(lines, "b M:f.py")
		}
		if got := authors(tf(t, hist(lines...), "f.py"))["f.py"]; !slices.Equal(got, want) {
			t.Errorf("%d deliveries: authors %v, want %v", n, got, want)
		}
	}

	// Whoever added the file is an author below the absolute threshold too, the tool's rule rather
	// than the paper's: with 30 single deliveries by others, a and b (who added it again) are at
	// 4.391-0.321*ln(31) = 3.289, the best DOA of the file, and the others at 2.365.
	lines := []string{"a A:f.py", "x D:f.py", "b A:f.py"}
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("m%02d M:f.py", i))
	}
	if got := authors(tf(t, hist(lines...), "f.py"))["f.py"]; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("adders under 3.293: authors %v", got)
	}

	// A rename is a delivery: b only renamed the file and authors it (3.457 against a's 4.168).
	r = tf(t, hist("a A:x.py", "b R:x.py>y.py"), "y.py")
	if got := authors(r)["y.py"]; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("renamer: authors %v", got)
	}

	// A copy is not part of a file's history, as in the tool: a file that only arrived as a copy
	// has none.
	if r := tf(t, hist("a A:x.py", "b C:x.py>y.py"), "y.py"); r.Files != 0 {
		t.Errorf("copied file counted: %+v", r)
	}

	// A file no commit produced is left out of the count.
	if r := tf(t, hist("a A:f.py"), "f.py", "copied.py"); r.Files != 1 {
		t.Errorf("files %d, want 1", r.Files)
	}
}

// A rename carries the history of the old path, and of the paths before it. Two files that share a
// rename chain both see all of it: the reference tool marks a rename as expanded on the shared record,
// so the second file in path order loses the old history (Flask's src/flask/sansio/app.py).
func TestRenameChains(t *testing.T) {
	r := tf(t, hist(
		"a A:flask/app.py",
		"a M:flask/app.py", "a M:flask/app.py", "a M:flask/app.py", "a M:flask/app.py",
		"b R:flask/app.py>src/flask/app.py",
		"c R:src/flask/app.py>src/flask/sansio/app.py",
		"c A:src/flask/app.py",
	), "src/flask/app.py", "src/flask/sansio/app.py")
	for path, as := range authors(r) {
		if !slices.Contains(as, "a") {
			t.Errorf("%s lost a's history: authors %v", path, as)
		}
	}
	// A rename back to an old name ends instead of looping.
	r = tf(t, hist("a A:x.py", "b R:x.py>y.py", "c R:y.py>x.py"), "x.py")
	if r.Files != 1 {
		t.Errorf("rename cycle: %+v", r)
	}
}

func TestGreedyAndPruning(t *testing.T) {
	// t authors all 20 files; each of eleven others co-authors one. Removing t leaves 11 of 20
	// covered, so two of the others go too before coverage drops under half, and pruning removes
	// them: they author 1 file, under 10% of t's 20.
	lines := []string{}
	var paths []string
	for i := range 20 {
		p := fmt.Sprintf("f%02d.py", i)
		paths = append(paths, p)
		lines = append(lines, "t A:"+p)
	}
	for i := range 11 {
		lines = append(lines, fmt.Sprintf("s%02d M:f%02d.py", i, i))
	}
	r := tf(t, hist(lines...), paths...)
	if r.TF != 1 || !slices.Equal(r.TFDevs, []string{"t"}) || r.CoveredAfter != 9 || r.Files != 20 {
		t.Errorf("got TF %d %v, covered after %d of %d", r.TF, r.TFDevs, r.CoveredAfter, r.Files)
	}

	// Two developers who each author half the files: removing one leaves exactly half, which is not
	// under half, so both go. Ties on files and changes fall to the smaller key.
	r = tf(t, hist("b A:1.py A:2.py", "a A:3.py A:4.py"), "1.py", "2.py", "3.py", "4.py")
	if r.TF != 2 || !slices.Equal(r.TFDevs, []string{"a", "b"}) || r.CoveredAfter != 0 {
		t.Errorf("halves: TF %d %v covered %d", r.TF, r.TFDevs, r.CoveredAfter)
	}
	// More changes break a tie on files.
	r = tf(t, hist("b A:1.py A:2.py", "a A:3.py A:4.py", "b M:1.py"), "1.py", "2.py", "3.py", "4.py")
	if !slices.Equal(r.TFDevs, []string{"b", "a"}) {
		t.Errorf("tie on files: %v", r.TFDevs)
	}
	// No file has history: no truck factor to speak of.
	if r := tf(t, nil, "a.py"); r.TF != 0 || r.Files != 0 {
		t.Errorf("no history: %+v", r)
	}
}

func TestTruckFactorCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TruckFactor(ctx, hist("a A:f.py"), []string{"f.py"}, byEmail); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
}

func TestDevelopers(t *testing.T) {
	commits := []facts.Commit{
		{Name: "Ann", Email: "ann@work.example"},
		{Name: "ann", Email: "ANN@home.example"}, // the same name, any case, joins the emails
		{Name: "Ann B", Email: "ann@home.example"},
		{Name: "Ann B", Email: "ann@third.example"}, // and the chain goes on
		{Name: "", Email: "anon@example.com"},
		{Name: "", Email: "anon2@example.com"}, // an empty name joins nothing
		{Name: "Zed", Email: ""},               // no email: the name is the identity
		{Name: "zed", Email: "zed@example.com"},
		{},
	}
	dev := Developers(commits)
	key := func(i int) string { return dev(&commits[i]) }
	same := [][]int{{0, 1, 2, 3}, {6, 7}}
	for _, g := range same {
		for _, i := range g[1:] {
			if key(i) != key(g[0]) {
				t.Errorf("commit %d is %q, commit %d is %q", i, key(i), g[0], key(g[0]))
			}
		}
	}
	if key(4) == key(5) || key(0) == key(4) || key(0) == key(6) || key(8) == key(4) {
		t.Errorf("distinct developers merged: %q %q %q %q", key(0), key(4), key(6), key(8))
	}
}
