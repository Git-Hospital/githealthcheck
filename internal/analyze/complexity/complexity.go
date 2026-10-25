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
	ComplexFunction    = "GHC-CPLX-001"
	LongFunction       = "GHC-CPLX-002"
	DeepNesting        = "GHC-CPLX-003"
	SwallowedError     = "GHC-CPLX-004"
	UnresolvedImport   = "GHC-CPLX-005"
	UnusedFunction     = "GHC-CPLX-006"
	CommentedCode      = "GHC-CPLX-007"
	Placeholder        = "GHC-CPLX-008"
	DuplicatedBlock    = "GHC-CPLX-009"
	IdenticalFunctions = "GHC-CPLX-010"
)

// Metric names in the report.
const (
	ComplexFunctionShare = "complex_function_share"
	LongFunctionShare    = "long_function_share"
	MeanCyclomatic       = "mean_cyclomatic_complexity"
	// SmellsPerKLOC counts the findings of GHC-CPLX-004 to 008 per 1000 lines of analysed code.
	SmellsPerKLOC = "smells_per_kloc"
	// DuplicatedLineShare is the share of code lines, outside tests, that are in a block of
	// GHC-CPLX-009, either copy.
	DuplicatedLineShare = "duplicated_line_share"
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

// minCodeLines is analyze.MinCodeLines, lowered by tests of the densities on small sources.
var minCodeLines = analyze.MinCodeLines

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
		{ID: DuplicatedBlock, Title: fmt.Sprintf("Block of %d tokens or more repeated", MinCloneTokens)},
		{ID: IdenticalFunctions, Title: "Function with the body of another"},
	}
}

func (Analyzer) Run(ctx context.Context, rf *facts.Repo) analyze.Result {
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	own, anyFile, err := ownFiles(ctx, rf)
	if err != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	res := analyze.Result{Metrics: map[string]report.Measurement{}}
	var sh shapes
	for _, f := range own {
		if !f.Test { // tests are judged for smells, not for their shape
			res.Findings = append(res.Findings, sh.add(f)...)
		}
	}
	found, err := smellFindings(ctx, rf, own)
	if err != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	smells, dropped := droppedSmells(own)
	for _, fd := range found {
		if fd.RuleID != UnusedFunction {
			smells++
		}
	}
	res.Findings = append(res.Findings, found...)
	dups, share, err := duplication(ctx, own)
	if err != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	res.Findings = append(res.Findings, dups...)
	res.Metrics[DuplicatedLineShare] = share
	if !anyFile {
		return analyze.Result{Reason: report.UnsupportedLanguage}
	}
	sh.metrics(res.Metrics)
	lines := 0
	for _, f := range own {
		lines += f.Lines
	}
	if lines > 0 && lines >= minCodeLines {
		res.Metrics[SmellsPerKLOC] = report.Measured(1000*float64(smells)/float64(lines), len(own))
	} else {
		res.Metrics[SmellsPerKLOC] = report.Null(report.InsufficientInput)
	}
	for _, m := range res.Metrics {
		res.Measured = res.Measured || !m.IsNull()
	}
	slices.SortFunc(res.Findings, func(a, b report.Finding) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	res.Findings = capPerFile(res.Findings, dropped)
	if !res.Measured {
		// Too little code for any number; each finding stands on its own and is still reported.
		res.Reason = report.InsufficientInput
	}
	return res
}

// ownFiles returns the repository's own analysed code: not vendored, generated, or ignored by the go
// command. anyFile is false when no file was analysed.
func ownFiles(ctx context.Context, rf *facts.Repo) (own []*facts.File, anyFile bool, err error) {
	for i := range rf.Files {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		f := &rf.Files[i]
		if !f.Analysed {
			continue
		}
		anyFile = true
		if !f.Vendored && !f.Generated && f.Package != "" {
			own = append(own, f)
		}
	}
	return own, anyFile, nil
}

// shapes counts the functions of the code and those too complex or too long.
type shapes struct{ measured, complex, long, cyclomatic int }

// add counts the functions of f and returns the findings on their shape.
func (sh *shapes) add(f *facts.File) []report.Finding {
	var out []report.Finding
	finding := func(rule string, fn facts.Function, format string, n int) {
		out = append(out, report.Finding{RuleID: rule, Path: f.Path, Line: fn.Line, Message: fmt.Sprintf(format, fn.Name, n)})
	}
	for _, fn := range f.Funcs {
		sh.measured++
		sh.cyclomatic += fn.Cyclomatic
		if fn.Cognitive > MaxCognitive {
			sh.complex++
			finding(ComplexFunction, fn, "%s has cognitive complexity %d", fn.Cognitive)
		}
		if fn.Lines > MaxLines {
			sh.long++
			finding(LongFunction, fn, "%s has %d lines of code", fn.Lines)
		}
		if fn.Nesting > MaxNesting {
			finding(DeepNesting, fn, "%s nests control flow %d deep", fn.Nesting)
		}
	}
	return out
}

// metrics sets the shares, which need enough functions to say something.
func (sh *shapes) metrics(m map[string]report.Measurement) {
	if sh.measured < MinFunctions {
		for _, name := range []string{ComplexFunctionShare, LongFunctionShare, MeanCyclomatic} {
			m[name] = report.Null(report.InsufficientInput)
		}
		return
	}
	n := float64(sh.measured)
	m[ComplexFunctionShare] = report.Measured(float64(sh.complex)/n, sh.measured)
	m[LongFunctionShare] = report.Measured(float64(sh.long)/n, sh.measured)
	m[MeanCyclomatic] = report.Measured(float64(sh.cyclomatic)/n, sh.measured)
}

// droppedSmells counts the smells files found but did not keep, in all and by rule and path.
func droppedSmells(own []*facts.File) (int, map[[2]string]int) {
	n := 0
	dropped := map[[2]string]int{}
	for _, f := range own {
		for kind, k := range f.SmellsDropped {
			n += k
			dropped[[2]string{smellRule[kind], f.Path}] += k
		}
	}
	return n, dropped
}

// MaxPerFile is the number of findings of one rule listed for one file; the rest are summed up in
// one more finding. A crafted file can otherwise make the report a hundred times its size.
const MaxPerFile = 50

// smellRule is the rule each kind of smell is reported under.
var smellRule = map[facts.SmellKind]string{
	facts.EmptyErrorCheck: SwallowedError,
	facts.ErrorDropped:    SwallowedError,
	facts.ExceptIgnored:   SwallowedError,
	facts.Placeholder:     Placeholder,
	facts.CommentedCode:   CommentedCode,
}

// capPerFile keeps the first MaxPerFile findings of each rule in each file, of findings sorted by
// rule and path, and replaces the rest with a count, which includes the smells the file did not keep
// (dropped, by rule and path).
func capPerFile(fs []report.Finding, dropped map[[2]string]int) []report.Finding {
	out := fs[:0:0]
	for i := 0; i < len(fs); {
		j := i
		for j < len(fs) && fs[j].RuleID == fs[i].RuleID && fs[j].Path == fs[i].Path {
			j++
		}
		more := max(j-i-MaxPerFile, 0) + dropped[[2]string{fs[i].RuleID, fs[i].Path}]
		out = append(out, fs[i:min(j, i+MaxPerFile)]...)
		if more > 0 {
			out = append(out, report.Finding{RuleID: fs[i].RuleID, Path: fs[i].Path,
				Message: fmt.Sprintf("%d more findings of this rule in this file are not listed", more)})
		}
		i = j
	}
	return out
}
