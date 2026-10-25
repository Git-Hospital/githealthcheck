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
	// ToolTooOld is a tool that is installed in a version too old to read the code, such as a
	// python3 before 3.10.
	ToolTooOld          NullReason = "tool_too_old"
	Offline             NullReason = "offline"
	NoCoverageReport    NullReason = "no_coverage_report"
	StaleCoverageReport NullReason = "stale_coverage_report"
	// InvalidCoverageReport is a report that was found by name but is not a valid report of a
	// supported format.
	InvalidCoverageReport NullReason = "invalid_coverage_report"
	HistoryTooShort       NullReason = "history_too_short"
	ExecNotAllowed        NullReason = "exec_not_allowed"
	NotCalibrated         NullReason = "not_calibrated"
	InsufficientInput     NullReason = "insufficient_input"
	LowAnalysedShare      NullReason = "low_analysed_share"
	InsufficientFamily    NullReason = "insufficient_families"
	NotImplemented        NullReason = "not_implemented"
	TooLarge              NullReason = "too_large"
	Timeout               NullReason = "timeout"
	ToolCrashed           NullReason = "tool_crashed"
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
	// MetricScores are the 0-10 scores of the metrics the family score is the mean of.
	MetricScores map[string]float64 `json:"metricScores,omitempty"`
	Findings     []Finding          `json:"findings,omitempty"`
}

// Language is one language present in the repository.
type Language struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Lines    int    `json:"lines"`
	Analysed bool   `json:"analysed"`
	// Reason is why a language this build supports was not analysed, such as tool_not_found.
	Reason NullReason `json:"reason,omitempty"`
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
	// Calibration is the corpus the scores are percentiles in; absent when the run did not score.
	Calibration *Calibration `json:"calibration,omitempty"`
}

// Calibration names the corpus a report was scored against.
type Calibration struct {
	// Language is the analysed language with the most code lines, whose corpus repositories the
	// metrics are compared with, and AnalysedShare the share of code lines in analysed languages.
	Language      string  `json:"language"`
	AnalysedShare float64 `json:"analysedShare"`
	// Repositories is the number of corpus repositories in the language.
	Repositories int `json:"repositories"`
	// Tool is the githealthcheck version that measured the corpus.
	Tool string `json:"tool"`
}

type Inputs struct {
	FilesAnalysed int `json:"filesAnalysed"`
	FilesTotal    int `json:"filesTotal"`
	// WalkComplete is false when the run timed out during the walk: filesTotal and languages then
	// count only the files read before the deadline.
	WalkComplete  bool  `json:"walkComplete"`
	Commits       int   `json:"commits"`
	ElapsedMillis int64 `json:"elapsedMillis"`
	// Coverage is the coverage report that was found, used or not; absent when there was none.
	Coverage *CoverageInput `json:"coverage,omitempty"`
	// Tools are the external tools code was read with, and their versions: the results of a
	// language read by an installed interpreter can depend on its version.
	Tools []Tool `json:"tools,omitempty"`
}

// CoverageInput describes a coverage report, so a reader can judge whether it belongs to the commit.
type CoverageInput struct {
	Path    string `json:"path"`             // relative to the root, or only the base name when outside it
	Tracked bool   `json:"tracked"`          // the report is a file in the scanned commit
	Format  string `json:"format,omitempty"` // absent when the report could not be parsed
	// Files is the number of source files the report lists, ModuleFiles those under one of the
	// repository's module paths (the rest are the standard library and dependencies; for a Cobertura
	// report, which has no module paths, every file), and FilesAtHead those that exist in the commit. Below 90% of ModuleFiles at HEAD, coverage is null with the
	// reason stale_coverage_report. All three are absent when the report was not parsed.
	Files       *int `json:"files,omitempty"`
	ModuleFiles *int `json:"moduleFiles,omitempty"`
	FilesAtHead *int `json:"filesAtHead,omitempty"`
}
