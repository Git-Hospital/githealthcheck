// Package analyze is the contract between the metric families and the rest of the tool. An analyzer
// reads facts, never files, and returns metrics and findings; it never imports another analyzer.
package analyze

import (
	"context"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Rule is one finding an analyzer can produce. Each rule has a page at docs/rules/<ID>.md with its
// threshold and the source of the threshold, and a defect and a clean fixture under
// testdata/pairs/<ID>.
type Rule struct {
	ID    string
	Title string
}

// Analyzer is one metric family.
type Analyzer interface {
	Family() report.Family
	Rules() []Rule
	Run(ctx context.Context, rf *facts.Repo) Result
}

// Result is what one family produced. Measured is false when the family had nothing to read; Reason
// then says why.
type Result struct {
	Measured bool
	Reason   report.NullReason
	Metrics  map[string]report.Measurement
	Findings []report.Finding
	// Coverage describes the coverage report the family read, when there was one.
	Coverage *report.CoverageInput
}
