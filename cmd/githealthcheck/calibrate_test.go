package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/report"
	"github.com/Git-Hospital/githealthcheck/internal/score"
)

// The lock built into the binary loads, and lists the corpus of corpus.txt at its commits.
func TestBuiltInCorpus(t *testing.T) {
	if corpusErr != nil {
		t.Fatal(corpusErr)
	}
	data, err := os.ReadFile("../../calibration/corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := readCorpus(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Repos) == 0 {
		t.Skip("no lock yet")
	}
	if len(entries) != len(corpus.Repos) {
		t.Fatalf("%d repositories in corpus.txt, %d in the lock", len(entries), len(corpus.Repos))
	}
	for i, e := range entries {
		if r := corpus.Repos[i]; r.Name != e.name || r.Commit != e.commit || r.Language != e.language {
			t.Errorf("line %d: %v, lock has %s %s %s", i+1, e, r.Language, r.Name, r.Commit)
		}
	}
}

func TestReadCorpus(t *testing.T) {
	ok := "# comment\n\ngo   a/b  " + strings.Repeat("a", 40) + "  # trailing\npython c/d.e " + strings.Repeat("0", 40) + "\n"
	if e, err := readCorpus([]byte(ok)); err != nil || len(e) != 2 || e[1].name != "c/d.e" {
		t.Errorf("%v %v", e, err)
	}
	sha := strings.Repeat("a", 40)
	for name, line := range map[string]string{
		"language":  "rust a/b " + sha,
		"fields":    "go a/b",
		"name":      "go a/b/c " + sha,
		"dot dot":   "go ../b " + sha,
		"option":    "go -u/b " + sha,
		"short sha": "go a/b abc123",
		"upper sha": "go a/b " + strings.Repeat("A", 40),
		"duplicate": "go a/b " + sha + "\ngo a/b " + sha,
		"url":       "go https://github.com/a/b " + sha,
	} {
		if _, err := readCorpus([]byte(line)); err == nil {
			t.Errorf("%s: accepted %q", name, line)
		}
	}
}

func TestLockDiff(t *testing.T) {
	m := func(v float64) report.Measurement { return report.Measured(v, 1) }
	was := &score.Lock{Tool: "a", Repos: []score.Repo{
		{Language: "go", Name: "a/b", Commit: "1", Metrics: map[string]report.Measurement{"x": m(1), "y": report.Null(report.HistoryTooShort)}},
		{Language: "go", Name: "gone/repo", Commit: "1"},
	}}
	now := &score.Lock{Tool: "b", Repos: []score.Repo{
		{Language: "go", Name: "a/b", Commit: "1", Metrics: map[string]report.Measurement{"x": m(2), "y": report.Null(report.HistoryTooShort)}},
		{Language: "go", Name: "new/repo", Commit: "1"},
	}}
	got := strings.Join(lockDiff(was, now), "\n")
	for _, want := range []string{"a/b x: 1 in the lock, 2 now", "new/repo: not in the lock", "gone/repo: in the lock, not in the corpus"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, " y:") || strings.Count(got, "\n") != 2 {
		t.Errorf("unexpected differences:\n%s", got)
	}
	if d := lockDiff(now, now); len(d) != 0 {
		t.Errorf("a lock differs from itself: %v", d)
	}
}

// --keep without --work would keep clones in a directory that is removed: a usage error.
func TestCalibrateKeepNeedsWork(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := calibrate([]string{"--keep", "--corpus", "../../calibration/corpus.txt"}, &stdout, &stderr); code != report.ExitError ||
		!strings.Contains(stderr.String(), "--keep needs --work") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}
