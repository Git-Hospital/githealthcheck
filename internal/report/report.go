// Package report is the report model and the exit-code contract. JSON is the source of truth; every
// other format is a rendering of it.
package report

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion changes whenever a field changes meaning or disappears.
const SchemaVersion = 1

// NullReason says why a value is missing. A value is either measured or null with a reason; there is
// no third state, and a null without a reason fails to encode.
type NullReason string

const (
	UnsupportedLanguage NullReason = "unsupported_language"
	ToolNotFound        NullReason = "tool_not_found"
	Offline             NullReason = "offline"
	NoCoverageReport    NullReason = "no_coverage_report"
	StaleCoverageReport NullReason = "stale_coverage_report"
	HistoryTooShort     NullReason = "history_too_short"
	ExecNotAllowed      NullReason = "exec_not_allowed"
	NotCalibrated       NullReason = "not_calibrated"
	InsufficientInput   NullReason = "insufficient_input"
	LowAnalysedShare    NullReason = "low_analysed_share"
	InsufficientFamily  NullReason = "insufficient_families"
	NotImplemented      NullReason = "not_implemented"
	TooLarge            NullReason = "too_large"
	Timeout             NullReason = "timeout"
	ToolCrashed         NullReason = "tool_crashed"
)

// incomplete reasons mean a run did not finish what it set out to do. The others mean an input was
// absent in an expected way, and do not make the run incomplete on their own.
var incomplete = map[NullReason]bool{Timeout: true, ToolCrashed: true}

// Measurement is a measured value or null with a reason. Inputs counts the files, functions or
// commits the value was computed from, so thresholds on minimum input can be checked.
type Measurement struct {
	Value  *float64   `json:"value"`
	Reason NullReason `json:"reason,omitempty"`
	Inputs int        `json:"inputs"`
}

// Measured returns a measured value.
func Measured(v float64, inputs int) Measurement { return Measurement{Value: &v, Inputs: inputs} }

// Null returns a null value with its reason.
func Null(r NullReason) Measurement { return Measurement{Reason: r} }

// IsNull reports whether m has no value.
func (m Measurement) IsNull() bool { return m.Value == nil }

// MarshalJSON enforces the contract: a value and a reason never appear together, and a null always
// has a reason.
func (m Measurement) MarshalJSON() ([]byte, error) {
	switch {
	case m.Value == nil && m.Reason == "":
		return nil, fmt.Errorf("report: null measurement without a reason")
	case m.Value != nil && m.Reason != "":
		return nil, fmt.Errorf("report: measured value %v also has the null reason %q", *m.Value, m.Reason)
	}
	type plain Measurement
	return json.Marshal(plain(m))
}

// Family names a metric family.
type Family string

const (
	Tests        Family = "tests"
	History      Family = "history"
	Complexity   Family = "complexity"
	Architecture Family = "architecture"
)

// Families lists every family in report order.
var Families = []Family{Tests, History, Complexity, Architecture}

// Finding is one rule violation at one location.
type Finding struct {
	RuleID     string `json:"ruleId"`
	Message    string `json:"message"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Suppressed bool   `json:"suppressed,omitempty"`
	// SuppressReason is the reason given in .githealthcheck.yml; required when Suppressed.
	SuppressReason string `json:"suppressReason,omitempty"`
}

// FamilyResult is what one family produced. Measured is false when the family could not run at all;
// Reason then says why. A measured family can still have a null Score, e.g. before calibration.
type FamilyResult struct {
	Family   Family                 `json:"family"`
	Measured bool                   `json:"measured"`
	Reason   NullReason             `json:"reason,omitempty"`
	Score    Measurement            `json:"score"`
	Metrics  map[string]Measurement `json:"metrics,omitempty"`
	Findings []Finding              `json:"findings,omitempty"`
}

// Language is one language present in the repository.
type Language struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Lines    int    `json:"lines"`
	Analysed bool   `json:"analysed"`
}

// SkipReason says why a path was not read.
type SkipReason string

const (
	SkipSymlink     SkipReason = "symlink"
	SkipTooLarge    SkipReason = "too_large"
	SkipNestedRepo  SkipReason = "nested_repository"
	SkipUnreadable  SkipReason = "unreadable"
	SkipNotRegular  SkipReason = "not_regular_file"
	SkipMissing     SkipReason = "missing"      // in the commit, not in the work tree
	SkipInvalidPath SkipReason = "invalid_path" // a tree entry no checkout would create; the path is not echoed
)

// Skip is a path that was not read, with the reason.
type Skip struct {
	Path   string     `json:"path"`
	Reason SkipReason `json:"reason"`
	Detail string     `json:"detail,omitempty"`
}

// Report is the whole result of one scan.
type Report struct {
	SchemaVersion int            `json:"schemaVersion"`
	Tool          Tool           `json:"tool"`
	Repository    Repo           `json:"repository"`
	Languages     []Language     `json:"languages"`
	Families      []FamilyResult `json:"families"`
	Overall       Overall        `json:"overall"`
	Inputs        Inputs         `json:"inputs"`
	Skipped       []Skip         `json:"skipped"`
}

type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Repo struct {
	Path    string `json:"path"`
	Head    string `json:"head,omitempty"` // absent before the first commit
	Shallow bool   `json:"shallow"`
	// ModifiedFiles counts the source files read whose work-tree content differs from the commit's
	// blob, and Dirty is true when there are any, or when commit files are missing from the work tree.
	ModifiedFiles int  `json:"modifiedFiles"`
	Dirty         bool `json:"dirty"`
}

// Overall is the combined score over measured families only.
type Overall struct {
	Score    Measurement        `json:"score"`
	Measured int                `json:"measuredFamilies"`
	Of       int                `json:"families"`
	Weights  map[Family]float64 `json:"weights,omitempty"`
}

type Inputs struct {
	FilesAnalysed int `json:"filesAnalysed"`
	FilesTotal    int `json:"filesTotal"`
	// WalkComplete is false when the run timed out during the walk: filesTotal and languages then
	// count only the files read before the deadline.
	WalkComplete  bool  `json:"walkComplete"`
	Commits       int   `json:"commits"`
	ElapsedMillis int64 `json:"elapsedMillis"`
}
