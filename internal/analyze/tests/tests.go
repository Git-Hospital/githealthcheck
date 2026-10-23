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
)

// ReportNames are the coverage reports looked for at the repository root, in this order, when the
// user names none.
var ReportNames = []string{"coverage.out", "cover.out", "coverage.txt", "c.out", "profile.cov"}

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

// pkg is one Go package of the repository's own code.
type pkg struct {
	functions int
	tested    bool
}

func (Analyzer) Run(ctx context.Context, rf *facts.Repo) analyze.Result {
	if ctx.Err() != nil {
		return analyze.Result{Reason: report.Timeout}
	}
	anyGo := false
	read := map[string]*facts.File{} // every Go file read, for the report's line check
	own := map[string]*facts.File{}  // the repository's own Go code: not vendored, generated or ignored
	for i := range rf.Files {
		f := &rf.Files[i]
		if f.Language != "go" {
			continue
		}
		anyGo = true
		read[f.Path] = f
		if !f.Vendored && !f.Generated && f.Package != "" {
			own[f.Path] = f
		}
	}
	switch {
	case !anyGo:
		return analyze.Result{Reason: report.UnsupportedLanguage}
	case len(own) == 0:
		return analyze.Result{Reason: report.InsufficientInput}
	}

	res := analyze.Result{Measured: true, Metrics: map[string]report.Measurement{}}
	var testLines, codeLines int
	pkgs := map[string]*pkg{}
	for _, f := range own {
		p := pkgs[f.Package]
		if p == nil {
			p = &pkg{}
			pkgs[f.Package] = p
		}
		if f.Test {
			testLines += f.Lines
			p.tested = true
		} else {
			codeLines += f.Lines
			p.functions += f.Functions
		}
	}
	if codeLines > 0 {
		res.Metrics[TestCodeLineRatio] = report.Measured(float64(testLines)/float64(codeLines), len(own))
	} else {
		res.Metrics[TestCodeLineRatio] = report.Null(report.InsufficientInput)
	}

	// A package counts once it declares a function outside its tests; a package of only types,
	// constants or a doc comment has nothing to test.
	counted, untested := 0, 0
	for dir, p := range pkgs {
		if p.functions == 0 {
			continue
		}
		counted++
		if !p.tested {
			untested++
			res.Findings = append(res.Findings, report.Finding{
				RuleID:  UntestedPackage,
				Path:    dir,
				Message: fmt.Sprintf("package declares %d functions and has no _test.go file", p.functions),
			})
		}
	}
	if counted > 0 {
		res.Metrics[UntestedPackageShare] = report.Measured(float64(untested)/float64(counted), counted)
	} else {
		res.Metrics[UntestedPackageShare] = report.Null(report.InsufficientInput)
	}

	m, input, findings := coverage(rf, read, own)
	res.Metrics[StatementCoverage] = m
	res.Coverage = input
	res.Findings = append(res.Findings, findings...)
	slices.SortFunc(res.Findings, func(a, b report.Finding) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Path, b.Path))
	})
	return res
}

type tally struct{ stmts, covered int }

// coverage reads the coverage report. A report is used only when at least MinPercentAtHead of the
// files it lists exist in the commit, and every block it records fits inside the file as read: a
// block past a file's last line means the file changed after the report was written.
func coverage(rf *facts.Repo, read, own map[string]*facts.File) (report.Measurement, *report.CoverageInput, []report.Finding) {
	if rf.Coverage == nil {
		return report.Null(report.NoCoverageReport), nil, nil
	}
	in := &report.CoverageInput{Path: rf.Coverage.Name, Tracked: rf.Coverage.Tracked}
	p, err := parseProfile(rf.Coverage.Data)
	if err != nil {
		return report.Null(report.InvalidCoverageReport), in, nil
	}
	in.Format = "go-coverprofile"
	in.Files = len(p.files)
	if len(p.files) == 0 {
		return report.Null(report.InsufficientInput), in, nil
	}

	atHead := map[string]string{} // import path -> repository path
	for ip := range p.files {
		if rel, ok := resolve(rf.Modules, ip); ok && rf.Paths[rel] {
			atHead[ip] = rel
		}
	}
	in.FilesAtHead = len(atHead)
	if len(atHead)*100 < MinPercentAtHead*len(p.files) {
		return report.Null(report.StaleCoverageReport), in, nil
	}
	for ip, rel := range atHead {
		f := read[rel]
		if f == nil {
			continue // in the commit but not read, e.g. too large: nothing to check against
		}
		for k := range p.files[ip] {
			if k.endLine > f.Lines {
				return report.Null(report.StaleCoverageReport), in, nil
			}
		}
	}

	files, pkgs := map[string]*tally{}, map[string]*tally{}
	var total tally
	for ip, rel := range atHead {
		f := own[rel]
		if f == nil {
			continue // vendored, generated, ignored by the go command, or not read
		}
		ft, pt := &tally{}, pkgs[f.Package]
		if pt == nil {
			pt = &tally{}
			pkgs[f.Package] = pt
		}
		for _, b := range p.files[ip] {
			ft.stmts += b.stmts
			if b.covered {
				ft.covered += b.stmts
			}
		}
		files[rel] = ft
		pt.stmts += ft.stmts
		pt.covered += ft.covered
		total.stmts += ft.stmts
		total.covered += ft.covered
	}
	if total.stmts == 0 {
		return report.Null(report.InsufficientInput), in, nil
	}

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
	return report.Measured(float64(total.covered)/float64(total.stmts), len(files)), in, findings
}

func percent(t *tally) string {
	return fmt.Sprintf("%.1f%%", 100*float64(t.covered)/float64(t.stmts))
}

// resolve maps a file's import path in a profile to its path in the repository, through the module
// with the longest matching path. Modules come in the order the caller chose, which breaks ties
// between two go.mod files that declare the same path.
func resolve(mods []facts.Module, importPath string) (string, bool) {
	best := -1
	for i, m := range mods {
		if strings.HasPrefix(importPath, m.Path+"/") && (best < 0 || len(m.Path) > len(mods[best].Path)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	rest := importPath[len(mods[best].Path)+1:]
	if mods[best].Dir == "." {
		return rest, true
	}
	return mods[best].Dir + "/" + rest, true
}
