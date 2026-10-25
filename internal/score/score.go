// Package score turns a report's metrics into family and overall scores: percentiles against the
// calibration corpus of the repository's language (D18 to D20, docs/calibration.md).
package score

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/Git-Hospital/githealthcheck/internal/analyze/complexity"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/history"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/tests"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Format is the version of the lock file's layout.
const Format = 1

// MinAnalysedShare is the share of a repository's code lines that must be in analysed languages for
// its families to be scored: below it, a score would describe a minority of the code.
const MinAnalysedShare = 0.5

// MinCorpus is the number of corpus repositories of a language that must have measured a metric for
// it to be scored: with fewer, a percentile moves by a large step from one repository to the next.
const MinCorpus = 10

// higherIsBetter lists the calibrated metrics, and whether a higher value is the better one. A metric
// not listed here is reported but not scored.
var higherIsBetter = map[string]bool{
	tests.TestCodeLineRatio:         true,
	tests.UntestedPackageShare:      false,
	history.TruckFactorMetric:       true,
	history.SingleAuthorFileShare:   false,
	history.AbandonedFileShare:      false,
	complexity.ComplexFunctionShare: false,
	complexity.LongFunctionShare:    false,
	complexity.MeanCyclomatic:       false,
	complexity.SmellsPerKLOC:        false,
	complexity.DuplicatedLineShare:  false,
}

// Calibrated reports whether a metric is compared with the corpus.
func Calibrated(metric string) bool {
	_, ok := higherIsBetter[metric]
	return ok
}

// Weights are the weights of the families in the overall score, fixed for a schema version.
var Weights = map[report.Family]float64{report.Tests: 1, report.History: 1, report.Complexity: 1, report.Architecture: 1}

// Lock is calibration/corpus.lock: the corpus, what was measured on each repository, and the sorted
// values per language and metric that scores are percentiles in.
type Lock struct {
	Format int `json:"format"`
	// Tool is the githealthcheck version that measured the corpus, and Tools the external tools it read
	// code with.
	Tool  string        `json:"tool"`
	Tools []report.Tool `json:"tools,omitempty"`
	Repos []Repo        `json:"repos"`
	// Values holds, for each language and calibrated metric, the values the corpus repositories of the
	// language measured, sorted.
	Values map[string]map[string][]float64 `json:"values"`
}

// Repo is one corpus repository and its metrics.
type Repo struct {
	Language string                        `json:"language"`
	Name     string                        `json:"name"`
	Commit   string                        `json:"commit"`
	Metrics  map[string]report.Measurement `json:"metrics"`
}

// Load reads a lock file.
func Load(data []byte) (*Lock, error) {
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("calibration lock: %w", err)
	}
	if l.Format != Format {
		return nil, fmt.Errorf("calibration lock: format %d, want %d", l.Format, Format)
	}
	for lang, metrics := range l.Values {
		for m, vs := range metrics {
			if !slices.IsSorted(vs) || slices.ContainsFunc(vs, func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }) {
				return nil, fmt.Errorf("calibration lock: values of %s %s are not sorted finite numbers", lang, m)
			}
		}
	}
	return &l, nil
}

// Distributions computes Values from Repos.
func (l *Lock) Distributions() {
	l.Values = map[string]map[string][]float64{}
	for _, r := range l.Repos {
		for m, v := range r.Metrics {
			if v.IsNull() || !Calibrated(m) {
				continue
			}
			if l.Values[r.Language] == nil {
				l.Values[r.Language] = map[string][]float64{}
			}
			l.Values[r.Language][m] = append(l.Values[r.Language][m], *v.Value)
		}
	}
	for _, metrics := range l.Values {
		for _, vs := range metrics {
			slices.Sort(vs)
		}
	}
}

// Language returns the language a report is scored as, its analysed language with the most code
// lines, and the share of the code lines that are analysed.
func Language(langs []report.Language) (string, float64) {
	name, total, analysed := "", 0, 0
	for _, l := range langs { // ordered by lines, most first
		total += l.Lines
		if l.Analysed {
			analysed += l.Lines
			if name == "" {
				name = l.Name
			}
		}
	}
	if total == 0 {
		return name, 0
	}
	return name, float64(analysed) / float64(total)
}

// Apply sets the score of each measured family of r, the overall score and the calibration it used.
// A family that was not measured keeps its null score.
func (l *Lock) Apply(r *report.Report) {
	lang, share := Language(r.Languages)
	values := l.Values[lang]
	r.Overall.Calibration = &report.Calibration{Language: lang, AnalysedShare: round(share, 3), Tool: l.Tool}
	for _, rp := range l.Repos {
		if rp.Language == lang {
			r.Overall.Calibration.Repositories++
		}
	}
	var scores []float64
	for i := range r.Families {
		fr := &r.Families[i]
		if !fr.Measured {
			continue
		}
		switch {
		case share < MinAnalysedShare:
			fr.Score = report.Null(report.LowAnalysedShare)
		case values == nil:
			fr.Score = report.Null(report.NotCalibrated)
		default:
			fr.Score, fr.MetricScores = familyScore(fr.Metrics, values)
		}
		if !fr.Score.IsNull() {
			scores = append(scores, *fr.Score.Value)
		}
	}
	r.Overall.Weights = Weights
	if len(scores) < 2 {
		// The overall score needs two families; when they were measured but not scored, it has their reason.
		if r.Overall.Measured >= 2 && share < MinAnalysedShare {
			r.Overall.Score = report.Null(report.LowAnalysedShare)
		} else if r.Overall.Measured >= 2 && len(scores) == 0 {
			r.Overall.Score = report.Null(report.NotCalibrated)
		} else {
			r.Overall.Score = report.Null(report.InsufficientFamily)
		}
		return
	}
	sum := 0.0
	for _, s := range scores {
		sum += s // every family weighs the same
	}
	r.Overall.Score = report.Measured(round(sum/float64(len(scores)), 2), len(scores))
}

// familyScore is the mean of the scores of the metrics that have a value: statement coverage as
// X% → X/10, every calibrated metric as its percentile in the corpus.
func familyScore(metrics map[string]report.Measurement, values map[string][]float64) (report.Measurement, map[string]float64) {
	names := make([]string, 0, len(metrics))
	for m := range metrics {
		names = append(names, m)
	}
	sort.Strings(names)
	scored := map[string]float64{}
	sum := 0.0
	for _, m := range names {
		v := metrics[m]
		if v.IsNull() {
			continue
		}
		var s float64
		switch corpus := values[m]; {
		case m == tests.StatementCoverage:
			s = 10 * math.Max(0, math.Min(1, *v.Value))
		case !Calibrated(m) || len(corpus) < MinCorpus:
			continue
		default:
			s = metricScore(corpus, *v.Value, higherIsBetter[m])
		}
		scored[m] = round(s, 2)
		sum += s
	}
	if len(scored) == 0 {
		return report.Null(report.NotCalibrated), nil
	}
	return report.Measured(round(sum/float64(len(scored)), 2), len(scored)), scored
}

// Percentile is how far x is up the sorted corpus values vs, from 0 to 1, for a metric where a higher
// value is better: the position of the last corpus value x is at least as good as, over n-1, linear
// between corpus values. A tie counts in the repository's favour: x equal to a run of corpus values is
// at the run's last position, so a repository tied with the best of the corpus is at 1. A value past
// the best is at 1, and one below every corpus value at 0.
func Percentile(vs []float64, x float64) float64 {
	n := len(vs)
	switch {
	case n < 2:
		return 0.5
	case x < vs[0]:
		return 0
	case x >= vs[n-1]:
		return 1
	}
	above := func(v float64) int { return sort.Search(n, func(i int) bool { return vs[i] > v }) }
	hi := above(x) // the first value above x: 1 <= hi <= n-1
	a, b := vs[hi-1], vs[hi]
	pa, pb := float64(hi-1), float64(above(b)-1) // the last positions of the runs of a and b
	return (pa + (pb-pa)*(x-a)/(b-a)) / float64(n-1)
}

// metricScore is a metric's score from 0 to 10 against its sorted corpus values.
func metricScore(vs []float64, x float64, higherBetter bool) float64 {
	if !higherBetter {
		// Less is better: the same on the negated values, which reverses their order.
		neg := make([]float64, len(vs))
		for i, v := range vs {
			neg[len(vs)-1-i] = -v
		}
		vs, x = neg, -x
	}
	return 10 * Percentile(vs, x)
}

func round(v float64, digits int) float64 {
	f := math.Pow(10, float64(digits))
	return math.Round(v*f) / f
}
