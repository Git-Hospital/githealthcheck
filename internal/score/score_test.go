package score

import (
	"math"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/analyze/complexity"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/history"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/tests"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func TestPercentile(t *testing.T) {
	vs := []float64{1, 2, 2, 2, 4}
	for _, c := range []struct{ x, want float64 }{
		{0, 0},       // below every value
		{1, 0},       // the lowest
		{1.5, 0.375}, // halfway from position 0 to the last 2, at 3: 1.5 of 4
		{2, 0.75},    // a tie is at the run's last position, 3 of 4
		{3, 0.875},   // halfway from 3 to 4
		{4, 1},       // the highest
		{9, 1},       // past it
	} {
		if got := Percentile(vs, c.x); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Percentile(%v) = %v, want %v", c.x, got, c.want)
		}
	}
	// Continuous where a run starts: just below 2 is just below 0.75.
	if got := Percentile(vs, 2-1e-9); math.Abs(got-0.75) > 1e-6 {
		t.Errorf("just below a run: %v", got)
	}
}

// For a metric where less is better, the best value of the corpus scores 10 however many repositories
// share it, and the worst 0.
func TestMetricScore(t *testing.T) {
	shares := []float64{0, 0, 0, 0, 0.1, 0.2, 0.4}
	for _, c := range []struct {
		x, want float64
	}{{0, 10}, {0.4, 0}, {0.5, 0}, {0.3, 10 * 0.5 / 6}, {0.1, 10 * 2.0 / 6}} {
		if got := metricScore(shares, c.x, false); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("less is better, %v: %v, want %v", c.x, got, c.want)
		}
	}
	truck := []float64{1, 1, 1, 2, 3, 5, 8}
	if got := metricScore(truck, 1, true); math.Abs(got-10*2.0/6) > 1e-9 {
		t.Errorf("truck factor 1: %v", got)
	}
	if got := metricScore(truck, 8, true); got != 10 {
		t.Errorf("truck factor 8: %v", got)
	}
}

func values(n int, f func(i int) float64) []float64 {
	vs := make([]float64, n)
	for i := range vs {
		vs[i] = f(i)
	}
	return vs
}

func lock() *Lock {
	share := values(11, func(i int) float64 { return float64(i) / 10 }) // 0, 0.1, ... 1
	return &Lock{Format: Format, Tool: "test", Repos: make([]Repo, 11), Values: map[string]map[string][]float64{
		"go": {
			complexity.ComplexFunctionShare: share,
			complexity.LongFunctionShare:    share,
			history.TruckFactorMetric:       values(11, func(i int) float64 { return float64(i + 1) }),
			tests.TestCodeLineRatio:         values(11, func(i int) float64 { return float64(i) / 5 }),
			// below MinCorpus: not scored
			complexity.SmellsPerKLOC: {1, 2, 3},
		},
	}}
}

func measured(v float64) report.Measurement { return report.Measured(v, 100) }

func repoReport(langs ...report.Language) *report.Report {
	return &report.Report{
		Languages: langs,
		Families: []report.FamilyResult{
			{Family: report.Tests, Measured: true, Metrics: map[string]report.Measurement{
				tests.TestCodeLineRatio:    measured(1),   // position 5 of 10: 5
				tests.StatementCoverage:    measured(0.8), // 8
				tests.UntestedPackageShare: report.Null(report.UnsupportedLanguage),
			}},
			{Family: report.History, Measured: true, Metrics: map[string]report.Measurement{
				history.TruckFactorMetric: measured(11), // 10
			}},
			{Family: report.Complexity, Measured: true, Metrics: map[string]report.Measurement{
				complexity.ComplexFunctionShare: measured(0.2), // 8
				complexity.LongFunctionShare:    measured(0.6), // 4
				complexity.SmellsPerKLOC:        measured(9),   // not scored
			}},
			{Family: report.Architecture, Reason: report.NotImplemented, Score: report.Null(report.NotImplemented)},
		},
		Overall: report.Overall{Measured: 3, Of: 4},
	}
}

func TestApply(t *testing.T) {
	l := lock()
	l.Repos[0].Language = "go"
	r := repoReport(report.Language{Name: "go", Lines: 900, Analysed: true}, report.Language{Name: "rust", Lines: 100})
	l.Apply(r)
	want := map[report.Family]float64{report.Tests: 6.5, report.History: 10, report.Complexity: 6}
	for _, f := range r.Families {
		if w, ok := want[f.Family]; ok && (f.Score.IsNull() || math.Abs(*f.Score.Value-w) > 1e-9) {
			t.Errorf("%s: %+v, want %v (metric scores %v)", f.Family, f.Score, w, f.MetricScores)
		}
	}
	if s := r.Families[2].MetricScores; len(s) != 2 {
		t.Errorf("complexity scored %v: smells have too few corpus values", s)
	}
	if a := r.Families[3]; a.Score.Reason != report.NotImplemented {
		t.Errorf("architecture %+v", a.Score)
	}
	if o := r.Overall; o.Score.IsNull() || math.Abs(*o.Score.Value-7.5) > 1e-9 || o.Score.Inputs != 3 {
		t.Errorf("overall %+v", o.Score)
	}
	if c := r.Overall.Calibration; c == nil || c.Language != "go" || c.AnalysedShare != 0.9 || c.Repositories != 1 || c.Tool != "test" {
		t.Errorf("calibration %+v", c)
	}
}

func TestApplyNull(t *testing.T) {
	// Most of the code is in a language that is not analysed.
	r := repoReport(report.Language{Name: "rust", Lines: 600}, report.Language{Name: "go", Lines: 400, Analysed: true})
	lock().Apply(r)
	if r.Families[0].Score.Reason != report.LowAnalysedShare || r.Overall.Score.Reason != report.LowAnalysedShare {
		t.Errorf("low share: %+v, overall %+v", r.Families[0].Score, r.Overall.Score)
	}
	// A language without a corpus.
	r = repoReport(report.Language{Name: "python", Lines: 600, Analysed: true})
	lock().Apply(r)
	if r.Families[1].Score.Reason != report.NotCalibrated || r.Overall.Score.Reason != report.NotCalibrated {
		t.Errorf("no corpus: %+v, overall %+v", r.Families[1].Score, r.Overall.Score)
	}
	// One family scored is not enough for an overall score.
	r = repoReport(report.Language{Name: "go", Lines: 600, Analysed: true})
	r.Families[0].Measured, r.Families[0].Score = false, report.Null(report.InsufficientInput)
	r.Families[1].Measured, r.Families[1].Score = false, report.Null(report.HistoryTooShort)
	r.Overall.Measured = 1
	lock().Apply(r)
	if r.Families[0].Score.Reason != report.InsufficientInput || r.Overall.Score.Reason != report.InsufficientFamily {
		t.Errorf("one family: %+v, overall %+v", r.Families[0].Score, r.Overall.Score)
	}
}

func TestLoad(t *testing.T) {
	if _, err := Load([]byte(`{"format": 1, "values": {"go": {"x": [2, 1]}}}`)); err == nil {
		t.Error("loaded unsorted values")
	}
	if _, err := Load([]byte(`{"format": 2}`)); err == nil {
		t.Error("loaded another format")
	}
	l := &Lock{Format: Format, Repos: []Repo{
		{Language: "go", Metrics: map[string]report.Measurement{complexity.MeanCyclomatic: measured(3), "unknown": measured(1)}},
		{Language: "go", Metrics: map[string]report.Measurement{complexity.MeanCyclomatic: measured(2), history.TruckFactorMetric: report.Null(report.HistoryTooShort)}},
	}}
	l.Distributions()
	if vs := l.Values["go"]; len(vs) != 1 || len(vs[complexity.MeanCyclomatic]) != 2 || vs[complexity.MeanCyclomatic][0] != 2 {
		t.Errorf("distributions %v", l.Values)
	}
}
