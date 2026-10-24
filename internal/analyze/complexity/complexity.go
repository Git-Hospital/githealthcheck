// Package complexity is the complexity family: how hard each function is to follow (D13), and the
// smells of generated code (D32). It reads the functions the language support measured.
package complexity

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Rule IDs; each has a page under docs/rules.
const (
	ComplexFunction  = "GHC-CPLX-001"
	LongFunction     = "GHC-CPLX-002"
	DeepNesting      = "GHC-CPLX-003"
	SwallowedError   = "GHC-CPLX-004"
	UnresolvedImport = "GHC-CPLX-005"
	UnusedFunction   = "GHC-CPLX-006"
	CommentedCode    = "GHC-CPLX-007"
	Placeholder      = "GHC-CPLX-008"
)

// Metric names in the report.
const (
	ComplexFunctionShare = "complex_function_share"
	LongFunctionShare    = "long_function_share"
	MeanCyclomatic       = "mean_cyclomatic_complexity"
	// SmellsPerKLOC counts the findings of GHC-CPLX-004 to 008 per 1000 lines of Go.
	SmellsPerKLOC = "smells_per_kloc"
)

const (
	// MaxCognitive is the cognitive complexity a function may have; above it is a finding. 15 is the
	// default of SonarSource's rule S3776 for Go, Python and TypeScript.
	MaxCognitive = 15
	// MaxLines is the number of code lines a function body may have: halfway between the defaults of
	// golangci-lint's funlen (60) and SonarSource's go:S138 (120), a choice of the project (D34).
	MaxLines = 90
	// MaxNesting is the depth of nested control flow a function may have: the default of
	// SonarSource's go:S134.
	MaxNesting = 4
	// MinFunctions is the number of functions below which the shares say nothing (architecture §5).
	MinFunctions = 20
)

// Analyzer is the complexity family.
type Analyzer struct{}

func (Analyzer) Family() report.Family { return report.Complexity }

func (Analyzer) Rules() []analyze.Rule {
	return []analyze.Rule{
		{ID: ComplexFunction, Title: fmt.Sprintf("Function with cognitive complexity over %d", MaxCognitive)},
		{ID: LongFunction, Title: fmt.Sprintf("Function with more than %d lines of code", MaxLines)},
		{ID: DeepNesting, Title: fmt.Sprintf("Control flow nested more than %d deep", MaxNesting)},
		{ID: SwallowedError, Title: "Error checked and then dropped"},
		{ID: UnresolvedImport, Title: "Import of a package the module does not provide"},
		{ID: UnusedFunction, Title: "Function nothing references"},
		{ID: CommentedCode, Title: "Commented-out code"},
		{ID: Placeholder, Title: "Function that only says it is not implemented"},
	}
}

func (Analyzer) Run(ctx context.Context, rf *facts.Repo) analyze.Result {
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	measured, anyFile := 0, false
	var complex, long, cyclomatic, lines, files, smells int
	res := analyze.Result{Metrics: map[string]report.Measurement{}}
	var own []*facts.File
	for i := range rf.Files {
		if i%256 == 0 && ctx.Err() != nil {
			return analyze.Result{Reason: report.Timeout}
		}
		f := &rf.Files[i]
		if f.Language != "go" {
			continue
		}
		anyFile = true
		// The repository's own code: not vendored, generated, or ignored by the go command.
		if f.Vendored || f.Generated || f.Package == "" {
			continue
		}
		own = append(own, f)
		files++
		lines += f.Lines
		if f.Test {
			continue // tests are judged for smells, not for their shape
		}
		for _, fn := range f.Funcs {
			measured++
			cyclomatic += fn.Cyclomatic
			if fn.Cognitive > MaxCognitive {
				complex++
				res.Findings = append(res.Findings, report.Finding{RuleID: ComplexFunction, Path: f.Path, Line: fn.Line,
					Message: fmt.Sprintf("%s has cognitive complexity %d", fn.Name, fn.Cognitive)})
			}
			if fn.Lines > MaxLines {
				long++
				res.Findings = append(res.Findings, report.Finding{RuleID: LongFunction, Path: f.Path, Line: fn.Line,
					Message: fmt.Sprintf("%s has %d lines of code", fn.Name, fn.Lines)})
			}
			if fn.Nesting > MaxNesting {
				res.Findings = append(res.Findings, report.Finding{RuleID: DeepNesting, Path: f.Path, Line: fn.Line,
					Message: fmt.Sprintf("%s nests control flow %d deep", fn.Name, fn.Nesting)})
			}
		}
	}
	found, err := smellFindings(ctx, rf, own)
	if err != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	for _, f := range own {
		smells += f.SmellsDropped
	}
	for _, fd := range found {
		if fd.RuleID != UnusedFunction {
			smells++
		}
		res.Findings = append(res.Findings, fd)
	}
	if !anyFile {
		return analyze.Result{Reason: report.UnsupportedLanguage}
	}
	// Each finding stands on its own; the shares need enough functions to say something.
	if measured >= MinFunctions {
		n := float64(measured)
		res.Metrics[ComplexFunctionShare] = report.Measured(float64(complex)/n, measured)
		res.Metrics[LongFunctionShare] = report.Measured(float64(long)/n, measured)
		res.Metrics[MeanCyclomatic] = report.Measured(float64(cyclomatic)/n, measured)
	} else {
		for _, m := range []string{ComplexFunctionShare, LongFunctionShare, MeanCyclomatic} {
			res.Metrics[m] = report.Null(report.InsufficientInput)
		}
	}
	if lines > 0 {
		res.Metrics[SmellsPerKLOC] = report.Measured(1000*float64(smells)/float64(lines), files)
	} else {
		res.Metrics[SmellsPerKLOC] = report.Null(report.InsufficientInput)
	}
	for _, m := range res.Metrics {
		res.Measured = res.Measured || !m.IsNull()
	}
	if !res.Measured {
		return analyze.Result{Reason: report.InsufficientInput, Metrics: res.Metrics}
	}
	slices.SortFunc(res.Findings, func(a, b report.Finding) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	res.Findings = capPerFile(res.Findings)
	return res
}

// MaxPerFile is the number of findings of one rule listed for one file; the rest are summed up in
// one more finding. A crafted file can otherwise make the report a hundred times its size.
const MaxPerFile = 50

// capPerFile keeps the first MaxPerFile findings of each rule in each file, of findings sorted by
// rule and path, and replaces the rest with a count.
func capPerFile(fs []report.Finding) []report.Finding {
	out := fs[:0:0]
	for i := 0; i < len(fs); {
		j := i
		for j < len(fs) && fs[j].RuleID == fs[i].RuleID && fs[j].Path == fs[i].Path {
			j++
		}
		if j-i <= MaxPerFile {
			out = append(out, fs[i:j]...)
		} else {
			out = append(out, fs[i:i+MaxPerFile]...)
			out = append(out, report.Finding{RuleID: fs[i].RuleID, Path: fs[i].Path,
				Message: fmt.Sprintf("%d more findings of this rule in this file are not listed", j-i-MaxPerFile)})
		}
		i = j
	}
	return out
}
