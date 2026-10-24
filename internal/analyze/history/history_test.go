package history

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

const day = 24 * 60 * 60

// repoWith gives a repository with one file per path and the commits, which hist makes one day apart.
func repoWith(commits []facts.Commit, paths ...string) *facts.Repo {
	rf := &facts.Repo{History: &facts.History{Commits: commits}}
	for _, p := range paths {
		rf.Files = append(rf.Files, facts.File{Path: p, Language: "python"})
	}
	return rf
}

// padding is n commits by p that touch only pad.py, to reach MinCommits.
func padding(p string, n int) []string {
	lines := []string{p + " A:pad.py"}
	for range n - 1 {
		lines = append(lines, p+" M:pad.py")
	}
	return lines
}

func TestRunNotMeasured(t *testing.T) {
	enough := hist(padding("a", MinCommits)...)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	bots := hist(append(padding("a", MinCommits-1),
		"dependabot[bot] M:pad.py", "49699333+dependabot[bot]@users.noreply.github.com M:pad.py")...)
	bots[0].Name, bots[0].Email = "x", "49699333+dependabot[bot]@users.noreply.github.com"
	bots[1].Name, bots[1].Email = "dependabot[bot]", "support@github.com"
	// Vendored and generated files are not the project's code, though commits produced them.
	vendored := repoWith(enough, "pad.py", "pad.py")
	vendored.Files[0].Vendored = true
	vendored.Files[1].Generated = true

	// pad.py's 50 entries are read for pad.py and again for p2.py, renamed from it: 101 in all. The
	// copy p1.py has no history of its own.
	chains := repoWith(hist(append(padding("a", MinCommits), "a C:pad.py>p1.py", "a R:pad.py>p2.py")...), "p1.py", "p2.py", "pad.py")
	defer func(n int) { MaxVisits = n }(MaxVisits)
	MaxVisits = 2*MinCommits - 1

	for name, c := range map[string]struct {
		ctx  context.Context
		rf   *facts.Repo
		want report.NullReason
	}{
		"no history":        {context.Background(), &facts.Repo{}, report.HistoryTooShort},
		"shallow":           {context.Background(), &facts.Repo{History: &facts.History{Commits: enough, Shallow: true}}, report.HistoryTooShort},
		"too large":         {context.Background(), &facts.Repo{History: &facts.History{TooLarge: true}}, report.TooLarge},
		"git log failed":    {context.Background(), &facts.Repo{History: &facts.History{Failed: true}}, report.ToolCrashed},
		"renames too large": {context.Background(), chains, report.TooLarge},
		"one short":         {context.Background(), repoWith(enough[1:], "pad.py"), report.HistoryTooShort},
		"bots do not count": {context.Background(), repoWith(bots, "pad.py"), report.HistoryTooShort},
		"no own file":       {context.Background(), vendored, report.InsufficientInput},
		"no file history":   {context.Background(), repoWith(enough, "other.py"), report.InsufficientInput},
		"cancelled":         {cancelled, repoWith(enough, "pad.py"), report.Timeout},
	} {
		res := Analyzer{}.Run(c.ctx, c.rf)
		if res.Measured || res.Reason != c.want {
			t.Errorf("%s: measured %v reason %q, want %q", name, res.Measured, res.Reason, c.want)
		}
	}
	if res := (Analyzer{}).Run(context.Background(), repoWith(enough, "pad.py")); !res.Measured {
		t.Errorf("exactly MinCommits: %+v", res)
	}
}

func TestRun(t *testing.T) {
	// a writes the code and stays; b wrote old.py and left more than a year before the latest commit;
	// c wrote recent.py and stopped less than a year before it.
	lines := []string{"b A:old.py", "c A:recent.py"}
	lines = append(lines, padding("a", MinCommits)...)
	commits := hist(lines...)
	latest := commits[0].Time
	commits[len(commits)-1].Time = latest - 400*day // b, before 1970
	commits[len(commits)-2].Time = latest - 364*day // c
	res := Analyzer{}.Run(context.Background(), repoWith(commits, "old.py", "pad.py", "recent.py"))
	if !res.Measured {
		t.Fatalf("not measured: %q", res.Reason)
	}
	// Each developer authors one file: removing one leaves two of three, removing a second leaves
	// one, under half. TF 2, every file has a single author, one of three is abandoned.
	wantMetrics := map[string]report.Measurement{
		TruckFactorMetric:     report.Measured(2, 3),
		SingleAuthorFileShare: report.Measured(1, 3),
		AbandonedFileShare:    report.Measured(1.0/3, 3),
	}
	if got := values(res.Metrics); !reflect.DeepEqual(got, values(wantMetrics)) {
		t.Errorf("metrics %v, want %v", got, values(wantMetrics))
	}
	want := []report.Finding{{RuleID: AbandonedFile, Path: "old.py",
		Message: "its only author made no commit in the year before the latest commit; their last was 400 days before it"}}
	if !reflect.DeepEqual(res.Findings, want) {
		t.Errorf("findings %+v", res.Findings)
	}

	// One developer who authors everything is a truck factor of one. Bots are left out of the
	// authorship as well as the count.
	lines = append(padding("a", MinCommits), "dependabot[bot] A:bot.py", "dependabot[bot] M:pad.py")
	commits = hist(lines...)
	commits[0].Name, commits[1].Name = "dependabot[bot]", "dependabot[bot]"
	res = Analyzer{}.Run(context.Background(), repoWith(commits, "pad.py", "bot.py"))
	want = []report.Finding{{RuleID: TruckFactorOne,
		Message: "one developer authors so much of the code that without them 0 of the 1 files with history keep an author"}}
	if !reflect.DeepEqual(res.Findings, want) || *res.Metrics[TruckFactorMetric].Value != 1 {
		t.Errorf("findings %+v metrics %v", res.Findings, res.Metrics)
	}
}

func TestIsBot(t *testing.T) {
	for id, want := range map[string]bool{
		"dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>":         true,
		"Renovate <29139614+renovate[bot]@users.noreply.github.com>":                  true,
		"github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>": true,
		"Ann <ann@example.com>":        false,
		"Bot Botson <bot@example.com>": false,
		"[bot] fan <fan@example.com>":  false,
	} {
		name, email := splitID(id)
		if got := IsBot(name, email); got != want {
			t.Errorf("%s: %v", id, got)
		}
	}
}

func splitID(id string) (string, string) {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '<' {
			return id[:i-1], id[i+1 : len(id)-1]
		}
	}
	return id, ""
}

// values prints measurements for comparison: DeepEqual on them compares pointers.
func values(ms map[string]report.Measurement) map[string]string {
	out := map[string]string{}
	for k, m := range ms {
		if m.IsNull() {
			out[k] = "null " + string(m.Reason)
		} else {
			out[k] = fmt.Sprintf("%.4f from %d", *m.Value, m.Inputs)
		}
	}
	return out
}

// At least a year, as Avelino et al. put it: 365 days is gone, a second less is not. Times far apart
// do not overflow, and commits without a time do not count as a last commit.
func TestAbandonedBoundary(t *testing.T) {
	run := func(gap int64, mutate func([]facts.Commit)) []report.Finding {
		t.Helper()
		lines := append([]string{"b A:old.py"}, padding("a", MinCommits)...)
		commits := hist(lines...)
		commits[len(commits)-1].Time = commits[0].Time - gap
		if mutate != nil {
			mutate(commits)
		}
		res := Analyzer{}.Run(context.Background(), repoWith(commits, "old.py", "pad.py"))
		if !res.Measured {
			t.Fatalf("not measured: %q", res.Reason)
		}
		var out []report.Finding
		for _, f := range res.Findings {
			if f.RuleID == AbandonedFile {
				out = append(out, f)
			}
		}
		return out
	}
	if f := run(AbandonedSeconds, nil); len(f) != 1 || f[0].Path != "old.py" {
		t.Errorf("exactly a year: %+v", f)
	}
	if f := run(AbandonedSeconds-1, nil); len(f) != 0 {
		t.Errorf("a second short of a year: %+v", f)
	}
	huge := func(c []facts.Commit) {
		c[0].Time = math.MaxInt64
		c[len(c)-1].Time = math.MinInt64
	}
	if f := run(0, huge); len(f) != 1 || !strings.Contains(f[0].Message, fmt.Sprint(uint64(math.MaxUint64)/day)) {
		t.Errorf("far apart: %+v", f)
	}
	// b's only commit has no time: old.py is not judged. A newest commit without a time does not
	// move the latest commit.
	noTime := func(c []facts.Commit) {
		for i := range c {
			c[i].Time += 1000 * day // far from 0, so an unknown time read as 0 would count as gone
		}
		c[len(c)-1].NoTime = true
		c[0].NoTime, c[0].Time = true, math.MaxInt64
	}
	if f := run(10*AbandonedSeconds, noTime); len(f) != 0 {
		t.Errorf("unknown times: %+v", f)
	}
}

// The documented constants: 50 commits (architecture §5) and one year (ESEM 2019).
func TestConstants(t *testing.T) {
	if MinCommits != 50 || AbandonedSeconds != 365*86400 {
		t.Errorf("MinCommits %d, AbandonedSeconds %d", MinCommits, AbandonedSeconds)
	}
}

// When most files have no author, no developer is a truck factor: TF 0, which is not GHC-HIST-001.
// A bot added three files that two people then changed once each: 3.457-0.321*ln(2) = 3.234, under
// 3.293, and neither added the file.
func TestTruckFactorZero(t *testing.T) {
	lines := padding("a", MinCommits)
	for _, f := range []string{"x.py", "y.py", "z.py"} {
		lines = append(lines, "bot A:"+f, "p M:"+f, "q M:"+f)
	}
	commits := hist(lines...)
	for i := range commits {
		if commits[i].Email == "bot" {
			commits[i].Name = "renovate[bot]"
		}
	}
	res := Analyzer{}.Run(context.Background(), repoWith(commits, "pad.py", "x.py", "y.py", "z.py"))
	want := map[string]string{
		TruckFactorMetric:     "0.0000 from 4",
		SingleAuthorFileShare: "0.2500 from 4", // only pad.py: a file without authors has no single one
		AbandonedFileShare:    "0.0000 from 1",
	}
	if got := values(res.Metrics); !reflect.DeepEqual(got, want) || len(res.Findings) != 0 {
		t.Errorf("metrics %v findings %+v", got, res.Findings)
	}
}

func TestAbandonedMessageNamesTheAuthors(t *testing.T) {
	// b added old.py and c changed it three times (3.785 against b's 4.391-0.321*ln(4) = 3.946):
	// two authors, both gone; c, the later, 400 days before the latest commit.
	lines := append([]string{"b A:old.py", "c M:old.py", "c M:old.py", "c M:old.py"}, padding("a", MinCommits)...)
	commits := hist(lines...)
	latest := commits[0].Time
	for i := len(commits) - 4; i < len(commits); i++ {
		commits[i].Time = latest - int64(400+len(commits)-i)*day
	}
	commits[len(commits)-4].Time = latest - 400*day
	res := Analyzer{}.Run(context.Background(), repoWith(commits, "old.py", "pad.py"))
	want := []report.Finding{{RuleID: AbandonedFile, Path: "old.py",
		Message: "none of its 2 authors committed in the year before the latest commit; the last of them stopped 400 days before it"}}
	if !reflect.DeepEqual(res.Findings, want) {
		t.Errorf("findings %+v", res.Findings)
	}
}

// When pruning leaves one developer, losing them alone can leave half the files with an author or
// more; the finding says so instead of claiming under half.
func TestTruckFactorOneAfterPruning(t *testing.T) {
	// a adds 60 files; 13 others add 5 each. Without a, 65 of 125 keep an author, so the greedy step
	// also removes one of the others and stops at 60; pruning drops that one (5/60 < 0.1).
	var lines []string
	paths := []string{}
	for i := range 60 {
		lines = append(lines, fmt.Sprintf("a A:a%02d.py", i))
		paths = append(paths, fmt.Sprintf("a%02d.py", i))
	}
	for d := range 13 {
		for i := range 5 {
			lines = append(lines, fmt.Sprintf("d%02d A:d%02d_%d.py", d, d, i))
			paths = append(paths, fmt.Sprintf("d%02d_%d.py", d, i))
		}
	}
	res := Analyzer{}.Run(context.Background(), repoWith(hist(lines...), paths...))
	want := []report.Finding{{RuleID: TruckFactorOne, Message: "one developer is the truck factor: without them 65 of the 125 files with history keep an author, nearly all through developers who each author under 10% as many files"}}
	if !reflect.DeepEqual(res.Findings, want) {
		t.Errorf("findings %+v", res.Findings)
	}
}
