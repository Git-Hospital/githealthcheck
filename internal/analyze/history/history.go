// Package history is the history family: who authored the code, read from git log, and whether
// those authors are still committing (D14, D15). It works on every recognised language (D8).
package history

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Rule IDs; each has a page under docs/rules.
const (
	TruckFactorOne = "GHC-HIST-001"
	AbandonedFile  = "GHC-HIST-002"
)

// Metric names in the report.
const (
	TruckFactorMetric     = "truck_factor"
	SingleAuthorFileShare = "single_author_file_share"
	AbandonedFileShare    = "abandoned_file_share"
)

const (
	// MinCommits is the number of non-merge commits by people, not bots, below which the history is
	// too short to say who owns the code (architecture §5).
	MinCommits = 50
	// AbandonedSeconds is how long before the latest commit a developer's last commit must be for
	// them to count as gone: one year, as in Avelino, Constantinou, Valente and Serebrenik, "On the
	// abandonment and survival of open source projects", ESEM 2019.
	AbandonedSeconds = 365 * 24 * 60 * 60
)

// Analyzer is the history family.
type Analyzer struct{}

func (Analyzer) Family() report.Family { return report.History }

func (Analyzer) Rules() []analyze.Rule {
	return []analyze.Rule{
		{ID: TruckFactorOne, Title: "Truck factor of one"},
		{ID: AbandonedFile, Title: "File whose authors have all stopped committing"},
	}
}

func (Analyzer) Run(ctx context.Context, rf *facts.Repo) analyze.Result {
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	h := rf.History
	switch {
	case h == nil || h.Shallow:
		return analyze.Result{Reason: report.HistoryTooShort}
	case h.TooLarge:
		return analyze.Result{Reason: report.TooLarge}
	case h.Failed:
		return analyze.Result{Reason: report.ToolCrashed}
	}
	commits := slices.DeleteFunc(slices.Clone(h.Commits), func(c facts.Commit) bool { return IsBot(c.Name, c.Email) })
	if len(commits) < MinCommits {
		return analyze.Result{Reason: report.HistoryTooShort}
	}
	var paths []string
	for _, f := range rf.Files {
		if !f.Vendored && !f.Generated {
			paths = append(paths, f.Path)
		}
	}
	slices.Sort(paths)

	developer := Developers(commits)
	tf, err := TruckFactor(ctx, commits, paths, developer)
	switch {
	case errors.Is(err, ErrTooLarge):
		return analyze.Result{Reason: report.TooLarge}
	case err != nil:
		return analyze.Result{Reason: report.Timeout}
	case tf.Files == 0:
		return analyze.Result{Reason: report.InsufficientInput}
	}

	res := analyze.Result{Measured: true, Metrics: map[string]report.Measurement{}}
	res.Metrics[TruckFactorMetric] = report.Measured(float64(tf.TF), tf.Files)
	if tf.TF == 1 {
		res.Findings = append(res.Findings, truckFactorOne(tf))
	}
	single := 0
	for _, f := range tf.files {
		if len(f.authors) == 1 {
			single++
		}
	}
	res.Metrics[SingleAuthorFileShare] = report.Measured(float64(single)/float64(tf.Files), tf.Files)
	share, findings := abandonedFiles(commits, developer, tf)
	res.Metrics[AbandonedFileShare] = share
	res.Findings = append(res.Findings, findings...)
	slices.SortFunc(res.Findings, func(a, b report.Finding) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Path, b.Path))
	})
	return res
}

func truckFactorOne(tf *Result) report.Finding {
	msg := fmt.Sprintf("one developer authors so much of the code that without them %d of the %d files with history keep an author",
		tf.CoveredWithoutTF, tf.Files)
	if 2*tf.CoveredWithoutTF >= tf.Files {
		// Pruning dropped the other developers the estimate removed: each authors under 10% as
		// many files as this one.
		msg = fmt.Sprintf("one developer is the truck factor: without them %d of the %d files with history keep an author, nearly all through developers who each author under 10%% as many files",
			tf.CoveredWithoutTF, tf.Files)
	}
	return report.Finding{RuleID: TruckFactorOne, Message: msg}
}

// lastCommits returns when each developer last committed, and the latest commit of all. A commit
// whose author time git could not print, in a corrupt or crafted object, has no time and is left out.
func lastCommits(commits []facts.Commit, developer func(*facts.Commit) string) (last map[string]int64, latest int64) {
	last = map[string]int64{}
	timed := false
	for i := range commits {
		c := &commits[i]
		if c.NoTime {
			continue
		}
		d := developer(c)
		if t, ok := last[d]; !ok || c.Time > t {
			last[d] = c.Time
		}
		if !timed || c.Time > latest {
			latest, timed = c.Time, true
		}
	}
	return last, latest
}

// abandonedFiles measures the share of files whose authors all stopped committing a year or more
// before the latest commit. A file is judged when it has authors and the last commit of each is known.
func abandonedFiles(commits []facts.Commit, developer func(*facts.Commit) string, tf *Result) (report.Measurement, []report.Finding) {
	last, latest := lastCommits(commits, developer)
	var findings []report.Finding
	judged := 0
	for _, f := range tf.files {
		recent, known := lastOfAuthors(f.authors, last)
		if !known {
			continue
		}
		judged++
		// latest >= recent; the difference of two int64 fits in a uint64.
		if gone := uint64(latest) - uint64(recent); gone >= AbandonedSeconds {
			findings = append(findings, abandonedFile(f, gone/(24*60*60)))
		}
	}
	if judged == 0 {
		return report.Null(report.InsufficientInput), findings
	}
	return report.Measured(float64(len(findings))/float64(judged), judged), findings
}

// lastOfAuthors returns the latest last commit of a file's authors; known is false when the file has
// no authors or one of them has no timed commit.
func lastOfAuthors(authors []string, last map[string]int64) (recent int64, known bool) {
	known = len(authors) > 0
	for i, a := range authors {
		t, ok := last[a]
		known = known && ok
		if i == 0 || t > recent {
			recent = t
		}
	}
	return recent, known
}

func abandonedFile(f fileAuthors, days uint64) report.Finding {
	msg := fmt.Sprintf("none of its %d authors committed in the year before the latest commit; the last of them stopped %d days before it",
		len(f.authors), days)
	if len(f.authors) == 1 {
		msg = fmt.Sprintf("its only author made no commit in the year before the latest commit; their last was %d days before it", days)
	}
	return report.Finding{RuleID: AbandonedFile, Path: f.path, Message: msg}
}

// Developers groups the authors of commits into developers, as the truck-factor tool does with its
// alias step: commits with the same email are one developer, and so are commits with the same
// non-empty name, both compared case-insensitively. The groups chain: an email used under two names
// joins every email used under either. It returns each commit's developer key, the smallest member of
// its group, so keys do not depend on map order.
func Developers(commits []facts.Commit) func(*facts.Commit) string {
	parent := map[string]string{}
	// find is iterative: a crafted history can chain many names, and recursion would go as deep.
	find := func(x string) string {
		if _, ok := parent[x]; !ok {
			parent[x] = x
			return x
		}
		root := x
		for parent[root] != root {
			root = parent[root]
		}
		for x != root {
			x, parent[x] = parent[x], root
		}
		return root
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		switch {
		case ra < rb:
			parent[rb] = ra
		case rb < ra:
			parent[ra] = rb
		}
	}
	for i := range commits {
		user, name := identity(&commits[i])
		find(user)
		if name != "" {
			union(user, name)
		}
	}
	return func(c *facts.Commit) string {
		user, _ := identity(c)
		return find(user)
	}
}

// identity returns the node a commit's author is known by, its email or, without one, its name, and
// the node of its name. The prefixes keep an email and a name with the same text apart.
func identity(c *facts.Commit) (user, name string) {
	if c.Name != "" {
		name = "n:" + strings.ToUpper(c.Name)
	}
	switch {
	case c.Email != "":
		return "e:" + strings.ToUpper(c.Email), name
	case name != "":
		return name, name
	default:
		return "", ""
	}
}

// IsBot reports whether an author is a GitHub App, which GitHub names "<app>[bot]" with the email
// "<id>+<app>[bot]@users.noreply.github.com". Other bots can be given such a name in .mailmap.
func IsBot(name, email string) bool {
	local, _, _ := strings.Cut(email, "@")
	return strings.HasSuffix(name, "[bot]") || strings.HasSuffix(local, "[bot]")
}
