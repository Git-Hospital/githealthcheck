package report

import (
	"encoding/json"
	"testing"
)

func TestMeasurementJSON(t *testing.T) {
	cases := []struct {
		name    string
		m       Measurement
		want    string
		wantErr bool
	}{
		{"measured", Measured(7.5, 12), `{"value":7.5,"inputs":12}`, false},
		{"measured zero is still a value", Measured(0, 3), `{"value":0,"inputs":3}`, false},
		{"null with reason", Null(NotCalibrated), `{"value":null,"reason":"not_calibrated","inputs":0}`, false},
		{"null without reason", Measurement{}, "", true},
		{"value and reason", Measurement{Value: new(float64), Reason: Offline}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.m)
			if c.wantErr {
				if err == nil {
					t.Fatalf("encoded %s, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// A reasonless null nested deep in a report must fail the whole encoding, not vanish.
func TestReportRejectsReasonlessNull(t *testing.T) {
	r := Report{Families: []FamilyResult{{
		Family: Tests, Measured: true, Score: Null(NotCalibrated),
		Metrics: map[string]Measurement{"testRatio": {}},
	}}}
	if _, err := json.Marshal(r); err == nil {
		t.Fatal("report with a reasonless null metric encoded without error")
	}
}

func TestExitCode(t *testing.T) {
	measured := FamilyResult{Family: Tests, Measured: true, Score: Null(NotCalibrated)}
	finding := Finding{RuleID: "GHC-TEST-001", Message: "m"}
	withFinding := measured
	withFinding.Findings = []Finding{finding}
	suppressed := measured
	suppressed.Findings = []Finding{{RuleID: "GHC-TEST-001", Message: "m", Suppressed: true, SuppressReason: "generated"}}
	notRun := FamilyResult{Family: History, Reason: HistoryTooShort, Score: Null(HistoryTooShort)}
	timedOut := FamilyResult{Family: Complexity, Reason: Timeout, Score: Null(Timeout)}
	crashedMetric := measured
	crashedMetric.Metrics = map[string]Measurement{"coverage": Null(ToolCrashed), "testRatio": Measured(0.5, 2)}
	expectedNullMetric := measured
	expectedNullMetric.Metrics = map[string]Measurement{"coverage": Null(NoCoverageReport)}

	cases := []struct {
		name      string
		families  []FamilyResult
		overall   Measurement
		failUnder *float64
		want      int
	}{
		{"clean", []FamilyResult{measured}, Null(InsufficientFamily), nil, ExitClean},
		{"findings", []FamilyResult{withFinding}, Null(InsufficientFamily), nil, ExitFindings},
		{"suppressed findings only", []FamilyResult{suppressed}, Null(InsufficientFamily), nil, ExitClean},
		{"expected absence is not incomplete", []FamilyResult{measured, notRun}, Null(InsufficientFamily), nil, ExitClean},
		{"no family measured", []FamilyResult{notRun}, Null(InsufficientFamily), nil, ExitIncomplete},
		{"no families at all", nil, Null(InsufficientFamily), nil, ExitIncomplete},
		{"timeout beats findings", []FamilyResult{withFinding, timedOut}, Null(InsufficientFamily), nil, ExitIncomplete},
		{"crashed metric in a measured family", []FamilyResult{crashedMetric}, Null(InsufficientFamily), nil, ExitIncomplete},
		{"expected null metric", []FamilyResult{expectedNullMetric}, Null(InsufficientFamily), nil, ExitClean},
		{"timed-out overall", []FamilyResult{measured}, Null(Timeout), nil, ExitIncomplete},
		{"fail-under with null overall", []FamilyResult{measured}, Null(NotCalibrated), new(7.0), ExitIncomplete},
		{"below fail-under", []FamilyResult{measured}, Measured(6.9, 2), new(7.0), ExitFindings},
		{"at fail-under", []FamilyResult{measured}, Measured(7, 2), new(7.0), ExitClean},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &Report{Families: c.families, Overall: Overall{Score: c.overall}}
			if got := ExitCode(r, c.failUnder); got != c.want {
				t.Errorf("ExitCode = %d, want %d", got, c.want)
			}
		})
	}
}
