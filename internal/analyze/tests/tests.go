// Package tests is the tests family: test files, the test-to-code ratio, and statement coverage read
// from an existing coverage report. It never runs the repository's tests (D12).
package tests

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Rule IDs; each has a page under docs/rules.
const (
	UntestedPackage = "GHC-TEST-001"
	LowCoverage     = "GHC-TEST-002"
	UncoveredFile   = "GHC-TEST-003"
)

// Metric names in the report.
const (
	TestCodeLineRatio    = "test_code_line_ratio"
	UntestedPackageShare = "untested_package_share"
	StatementCoverage    = "statement_coverage"
)

const (
	// AcceptablePercent is the statement coverage Google calls acceptable ("Code Coverage Best
	// Practices", Google Testing Blog, 2020-08-07). A package below it is a finding.
	AcceptablePercent = 60
	// MinPercentAtHead is the share of a report's files that must exist in the commit for the report
	// to count as fresh (architecture §5).
	MinPercentAtHead = 90
	// MinPackages is the number of Go packages that declare functions below which the share of
	// untested packages says nothing (architecture §5).
	MinPackages = 5
)

// ReportNames are the coverage reports looked for at the repository root, in this order, when the
// user names none: Go profiles, then coverage.py's Cobertura XML.
var ReportNames = []string{"coverage.out", "cover.out", "coverage.txt", "c.out", "profile.cov", "coverage.xml"}

// Analyzer is the tests family.
type Analyzer struct{}

func (Analyzer) Family() report.Family { return report.Tests }

func (Analyzer) Rules() []analyze.Rule {
	return []analyze.Rule{
		{ID: UntestedPackage, Title: "Package without test files"},
		{ID: LowCoverage, Title: "Package statement coverage below 60%"},
		{ID: UncoveredFile, Title: "File with no statement covered"},
	}
}

// pkg is one Go package of the repository's own code, or a directory of Python.
type pkg struct {
	functions int
	tested    bool
}

func (Analyzer) Run(ctx context.Context, rf *facts.Repo) analyze.Result {
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	own, analysed := ownFiles(rf)
	switch {
	case !analysed:
		return analyze.Result{Reason: report.UnsupportedLanguage}
	case len(own) == 0:
		return analyze.Result{Reason: report.InsufficientInput}
	}

	res := analyze.Result{Measured: true, Metrics: map[string]report.Measurement{}}
	res.Metrics[TestCodeLineRatio] = lineRatio(own)
	res.Metrics[UntestedPackageShare], res.Findings = untestedPackages(own)

	m, input, findings := coverage(ctx, rf, own)
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout, Coverage: input}
	}
	res.Metrics[StatementCoverage] = m
	res.Coverage = input
	res.Findings = append(res.Findings, findings...)
	slices.SortFunc(res.Findings, func(a, b report.Finding) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Path, b.Path))
	})
	// A family with no measured number is not measured, and has no score; what it found it still
	// reports, since each finding stands on its own.
	if !anyMeasured(res.Metrics) {
		return analyze.Result{Reason: report.InsufficientInput, Metrics: res.Metrics, Findings: res.Findings, Coverage: input}
	}
	return res
}

func anyMeasured(metrics map[string]report.Measurement) bool {
	for _, m := range metrics {
		if !m.IsNull() {
			return true
		}
	}
	return false
}

// ownFiles returns the repository's own code in the languages the family reads: not vendored,
// generated or ignored by the go command. The family needs only a file's package and whether it is a
// test, which a Python file has without python3. analysed is false when no file is in such a language.
func ownFiles(rf *facts.Repo) (own map[string]*facts.File, analysed bool) {
	own = map[string]*facts.File{}
	for i := range rf.Files {
		f := &rf.Files[i]
		if !f.Analysed && f.Language != "python" {
			continue
		}
		analysed = true
		if !f.Vendored && !f.Generated && f.Package != "" {
			own[f.Path] = f
		}
	}
	return own, analysed
}

func lineRatio(own map[string]*facts.File) report.Measurement {
	var testLines, codeLines int
	for _, f := range own {
		if f.Test {
			testLines += f.Lines
		} else {
			codeLines += f.Lines
		}
	}
	if codeLines < analyze.MinCodeLines {
		return report.Null(report.InsufficientInput)
	}
	return report.Measured(float64(testLines)/float64(codeLines), len(own))
}

// untestedPackages measures the share of Go packages without a test file. A package without one
// says something in Go, whose tests sit beside the code; Python keeps them in a tests directory of
// its own. A package counts once it declares a function outside its tests; a package of only types,
// constants or a doc comment has nothing to test.
func untestedPackages(own map[string]*facts.File) (report.Measurement, []report.Finding) {
	pkgs := map[string]*pkg{}
	for _, f := range own {
		if f.Language != "go" {
			continue
		}
		p := pkgs[f.Package]
		if p == nil {
			p = &pkg{}
			pkgs[f.Package] = p
		}
		if f.Test {
			p.tested = true
		} else {
			p.functions += f.Functions
		}
	}
	var findings []report.Finding
	counted, untested := 0, 0
	for dir, p := range pkgs {
		if p.functions == 0 {
			continue
		}
		counted++
		if !p.tested {
			untested++
			findings = append(findings, report.Finding{
				RuleID:  UntestedPackage,
				Path:    dir,
				Message: fmt.Sprintf("package declares %d functions and has no _test.go file", p.functions),
			})
		}
	}
	switch {
	case counted >= MinPackages:
		return report.Measured(float64(untested)/float64(counted), counted), findings
	case len(pkgs) == 0:
		return report.Null(report.UnsupportedLanguage), findings // no Go
	}
	return report.Null(report.InsufficientInput), findings
}

type tally struct{ stmts, covered int }

func (t *tally) add(o tally) {
	t.stmts += o.stmts
	t.covered += o.covered
}

// coverage reads the coverage report. Only files under one of the repository's module paths count:
// -coverpkg=all also lists the standard library and dependencies. A report is used only when at least
// MinPercentAtHead of those files exist in the commit, and every block of the repository's own code
// fits inside its file as read: a block past a file's last line means the file changed after the
// report was written. Generated files are not checked, since //line directives move their positions.
func coverage(ctx context.Context, rf *facts.Repo, own map[string]*facts.File) (report.Measurement, *report.CoverageInput, []report.Finding) {
	if rf.Coverage == nil {
		return report.Null(report.NoCoverageReport), nil, nil
	}
	in := &report.CoverageInput{Path: rf.Coverage.Name, Tracked: rf.Coverage.Tracked}
	p, format, err := readReport(ctx, rf)
	switch {
	case ctx.Err() != nil:
		return report.Null(report.Timeout), in, nil
	case err != nil:
		return report.Null(report.InvalidCoverageReport), in, nil
	}
	in.Format = format
	in.Files, in.ModuleFiles, in.FilesAtHead = &p.files, &p.inModules, new(len(p.blocks))
	switch {
	case p.files == 0:
		return report.Null(report.InsufficientInput), in, nil
	case p.inModules == 0 || len(p.blocks)*100 < MinPercentAtHead*p.inModules:
		return report.Null(report.StaleCoverageReport), in, nil
	}
	files, pkgs, ok := tallies(p, own)
	if !ok {
		return report.Null(report.StaleCoverageReport), in, nil
	}
	var total tally
	for _, t := range files {
		total.add(*t)
	}
	if total.stmts == 0 {
		return report.Null(report.InsufficientInput), in, nil
	}
	return report.Measured(float64(total.covered)/float64(total.stmts), len(files)), in, coverageFindings(files, pkgs)
}

// readReport parses the coverage report, a Cobertura XML report or a Go profile.
func readReport(ctx context.Context, rf *facts.Repo) (*profile, string, error) {
	if isCobertura(rf.Coverage.Data) {
		p, err := parseCobertura(ctx, rf.Coverage.Data, func(full string) (string, bool) {
			return full, rf.Paths[full]
		})
		return p, "cobertura", err
	}
	mods := indexModules(rf.Modules)
	p, err := parseProfile(ctx, rf.Coverage.Data, func(ip string) (string, bool, bool) {
		rel, ok := mods.resolve(ip)
		return rel, ok, ok && rf.Paths[rel]
	})
	return p, "go-coverprofile", err
}

// tallies sums the statements of the repository's own code by file and by package. ok is false when a
// block does not fit inside its file.
func tallies(p *profile, own map[string]*facts.File) (files, pkgs map[string]*tally, ok bool) {
	files, pkgs = map[string]*tally{}, map[string]*tally{}
	for rel, blocks := range p.blocks {
		f := own[rel]
		if f == nil || f.Test {
			continue // vendored, generated, ignored by the go command, not read, or a test
		}
		ft := &tally{}
		for k, b := range blocks {
			if k.startLine < 1 || k.endLine < k.startLine || k.endLine > f.Lines {
				return nil, nil, false
			}
			ft.stmts += b.stmts
			if b.covered {
				ft.covered += b.stmts
			}
		}
		files[rel] = ft
		pt := pkgs[f.Package]
		if pt == nil {
			pt = &tally{}
			pkgs[f.Package] = pt
		}
		pt.add(*ft)
	}
	return files, pkgs, true
}

func coverageFindings(files, pkgs map[string]*tally) []report.Finding {
	var findings []report.Finding
	for dir, t := range pkgs {
		if t.stmts > 0 && t.covered*100 < AcceptablePercent*t.stmts {
			findings = append(findings, report.Finding{
				RuleID:  LowCoverage,
				Path:    dir,
				Message: fmt.Sprintf("statement coverage is %s, below %d%%", percent(t), AcceptablePercent),
			})
		}
	}
	for rel, t := range files {
		if t.stmts > 0 && t.covered == 0 {
			findings = append(findings, report.Finding{
				RuleID:  UncoveredFile,
				Path:    rel,
				Message: fmt.Sprintf("none of its %d statements ran in the tests", t.stmts),
			})
		}
	}
	return findings
}

func percent(t *tally) string {
	return fmt.Sprintf("%.1f%%", 100*float64(t.covered)/float64(t.stmts))
}

// moduleIndex maps module paths to modules. Of two go.mod files that declare the same path, the first
// in the caller's order is kept.
type moduleIndex map[string]facts.Module

func indexModules(mods []facts.Module) moduleIndex {
	idx := moduleIndex{}
	for _, m := range mods {
		if _, ok := idx[m.Path]; !ok {
			idx[m.Path] = m
		}
	}
	return idx
}

// resolve maps a file's import path in a profile to its path in the repository, through the module
// with the longest matching path. It looks up each parent of the import path, so its cost does not
// grow with the number of modules.
func (idx moduleIndex) resolve(importPath string) (string, bool) {
	for i := strings.LastIndexByte(importPath, '/'); i > 0; i = strings.LastIndexByte(importPath[:i], '/') {
		m, ok := idx[importPath[:i]]
		if !ok {
			continue
		}
		rest := importPath[i+1:]
		if m.Dir == "." {
			return rest, true
		}
		return m.Dir + "/" + rest, true
	}
	return "", false
}
