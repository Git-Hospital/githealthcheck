package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func renderReport() *Report {
	v := 4.25
	return &Report{
		SchemaVersion: SchemaVersion,
		Tool:          Tool{Name: "githealthcheck", Version: "1.2.3"},
		Repository:    Repo{Path: "repo", Head: "0123456789abcdef"},
		Families: []FamilyResult{
			{Family: Tests, Measured: true, Score: Measured(v, 2), Metrics: map[string]Measurement{"untested_package_share": Measured(0.5, 6)},
				MetricScores: map[string]float64{"untested_package_share": 4.25},
				Findings:     []Finding{{RuleID: "GHC-TEST-001", Path: "pkg/a", Message: "package declares 2 functions and has no _test.go file"}}},
			{Family: History, Measured: true, Score: Measured(v, 3), Findings: []Finding{
				{RuleID: "GHC-HIST-001", Message: "one developer authors so much"},
				{RuleID: "GHC-HIST-002", Path: "dir with space/a#b.go", Message: "its only author left"},
			}},
			{Family: Complexity, Measured: true, Score: Null(NotCalibrated), Findings: []Finding{
				{RuleID: "GHC-CPLX-001", Path: "a:b/c.go", Line: 12, Message: "F has cognitive complexity 20"},
				{RuleID: "GHC-CPLX-007", Path: "x.go", Line: 3, Message: "line 3 is commented-out code", Suppressed: true, SuppressReason: "kept on purpose"},
			}},
			{Family: Architecture, Reason: NotImplemented, Score: Null(NotImplemented)},
		},
		Overall: Overall{Score: Measured(v, 2), Measured: 3, Of: 4, Calibration: &Calibration{Language: "go", AnalysedShare: 1, Repositories: 15}},
		Inputs:  Inputs{WalkComplete: true},
		Skipped: []Skip{},
	}
}

var renderRules = []Rule{
	{ID: "GHC-TEST-001", Title: "Package without tests", Family: Tests},
	{ID: "GHC-HIST-001", Title: "Truck factor of one", Family: History},
	{ID: "GHC-HIST-002", Title: "Abandoned file", Family: History},
	{ID: "GHC-CPLX-001", Title: "Cognitive complexity over 15", Family: Complexity},
	{ID: "GHC-CPLX-007", Title: "Commented-out code", Family: Complexity},
}

func TestSARIF(t *testing.T) {
	files := map[string]bool{"dir with space/a#b.go": true, "a:b/c.go": true, "x.go": true}
	body, err := SARIF(renderReport(), renderRules, func(p string) bool { return files[p] })
	if err != nil {
		t.Fatal(err)
	}
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Version string `json:"version"`
					Rules   []struct {
						ID      string `json:"id"`
						HelpURI string `json:"helpUri"`
						Default struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Invocations []struct {
				Successful    bool `json:"executionSuccessful"`
				Notifications []struct {
					Message struct{ Text string } `json:"message"`
				} `json:"toolExecutionNotifications"`
			} `json:"invocations"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				RuleIndex int    `json:"ruleIndex"`
				Level     string `json:"level"`
				Locations []struct {
					Physical struct {
						Artifact struct {
							URI    string `json:"uri"`
							BaseID string `json:"uriBaseId"`
						} `json:"artifactLocation"`
						Region *struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				Suppressions []struct {
					Kind string `json:"kind"`
				} `json:"suppressions"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &log); err != nil {
		t.Fatal(err)
	}
	if log.Version != "2.1.0" || len(log.Runs) != 1 {
		t.Fatalf("%s", body)
	}
	run := log.Runs[0]
	if run.Tool.Driver.Version != "1.2.3" || len(run.Tool.Driver.Rules) != len(renderRules) ||
		run.Tool.Driver.Rules[1].Default.Level != "note" || run.Tool.Driver.Rules[0].Default.Level != "warning" ||
		run.Tool.Driver.Rules[0].HelpURI != RulesURL+"GHC-TEST-001.md" {
		t.Errorf("driver %+v", run.Tool.Driver)
	}
	// The package directory and the repository-wide finding are not results.
	if len(run.Results) != 3 {
		t.Fatalf("results %+v", run.Results)
	}
	hist, cplx, supp := run.Results[0], run.Results[1], run.Results[2]
	if hist.RuleID != "GHC-HIST-002" || hist.Level != "note" || hist.RuleIndex != 2 || hist.Locations[0].Physical.Region != nil ||
		hist.Locations[0].Physical.Artifact.URI != "dir%20with%20space/a%23b.go" || hist.Locations[0].Physical.Artifact.BaseID != "%SRCROOT%" {
		t.Errorf("history result %+v", hist)
	}
	if cplx.Level != "warning" || cplx.Locations[0].Physical.Region.StartLine != 12 || cplx.Locations[0].Physical.Artifact.URI != "./a:b/c.go" {
		t.Errorf("complexity result %+v", cplx)
	}
	if len(supp.Suppressions) != 1 || supp.Suppressions[0].Kind != "external" {
		t.Errorf("suppressed result %+v", supp)
	}
	inv := run.Invocations[0]
	if !inv.Successful || len(inv.Notifications) != 1 || !strings.HasPrefix(inv.Notifications[0].Message.Text, "2 findings") {
		t.Errorf("invocation %+v", inv)
	}
	if _, err := SARIF(renderReport(), renderRules[:1], func(string) bool { return true }); err == nil {
		t.Error("rendered a finding of a rule it does not list")
	}
}

func TestMarkdown(t *testing.T) {
	md := Markdown(renderReport())
	for _, want := range []string{
		"## githealthcheck: **4.2/10**",
		"scored as go against 15 corpus repositories",
		"| tests | **4.2/10** | untested_package_share 50.0% (4.2) |",
		"### Findings (5)",
		"- [GHC-CPLX-001](" + RulesURL + "GHC-CPLX-001.md) a\\:b\\/c\\.go:12: F has cognitive complexity 20",
		"(suppressed: kept on purpose)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
}

// A path or message from the repository cannot add a link, an image, HTML or a line to the page.
func TestMarkdownEscapes(t *testing.T) {
	r := renderReport()
	r.Families[2].Findings = []Finding{{RuleID: "GHC-CPLX-001",
		Path:    "![x](https://evil.example/p.png)<img src=x>|`a`.go",
		Message: "line\n## Injected [link](https://evil.example) *b* _c_ \u202e"}}
	md := Markdown(r)
	line := md[strings.Index(md, "- [GHC-CPLX-001]"):]
	line = line[:strings.IndexByte(line, '\n')]
	for _, bad := range []string{"![", "](https://evil", "\n## Injected", "\u202e", "|`", " *b*"} {
		if strings.Contains(line, bad) {
			t.Errorf("%q survives in %q", bad, line)
		}
	}
	for i := 1; i < len(line); i++ {
		if line[i] == '<' && line[i-1] != '\\' {
			t.Errorf("unescaped < at %d in %q", i, line)
		}
	}
	if !strings.Contains(line, `\!\[x\]\(https\:\/\/evil\.example\/p\.png\)\<img src\=x\>\|\`+"`"+`a\`+"`"+`\.go`) {
		t.Errorf("path not escaped as literal text: %q", line)
	}
	if !strings.Contains(line, `line\\n\#\# Injected`) || !strings.Contains(line, `\\u202e`) {
		t.Errorf("control characters not escaped: %q", line)
	}
}

func TestMarkdownCaps(t *testing.T) {
	r := renderReport()
	for range MaxMarkdownFindings + 7 {
		r.Families[2].Findings = append(r.Families[2].Findings, Finding{RuleID: "GHC-CPLX-001", Path: "x.go", Line: 1, Message: "m"})
	}
	md := Markdown(r)
	if n := strings.Count(md, "\n- ["); n != MaxMarkdownFindings {
		t.Errorf("%d findings listed", n)
	}
	if !strings.Contains(md, "12 more findings are in the JSON report.") {
		t.Errorf("no count of the rest:\n%s", md[len(md)-200:])
	}
}

// A byte that is not UTF-8, and U+FFFD itself, are escaped and do not stop the render.
func TestMarkdownEscapesInvalidUTF8(t *testing.T) {
	cases := map[string]string{
		"caf\xe9.go":   `caf\\xe9\.go`,
		"a�b":          `a\\ufffdb`,
		"tab\there":    `tab\\there`,
		"\xff\xfe\x00": `\\xff\\xfe\\u0000`,
	}
	for in, want := range cases {
		if got := mdEscape(in); got != want {
			t.Errorf("mdEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSARIFCaps(t *testing.T) {
	r := renderReport()
	for range MaxSARIFResults + 4 {
		r.Families[2].Findings = append(r.Families[2].Findings, Finding{RuleID: "GHC-CPLX-001", Path: "x.go", Line: 1, Message: "m"})
	}
	body, err := SARIF(r, renderRules, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var log struct {
		Runs []struct {
			Results     []json.RawMessage `json:"results"`
			Invocations []struct {
				Notifications []struct {
					Message struct{ Text string } `json:"message"`
				} `json:"toolExecutionNotifications"`
			} `json:"invocations"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &log); err != nil {
		t.Fatal(err)
	}
	n := log.Runs[0].Invocations[0].Notifications
	// Four findings of renderReport have a path (isFile is true for all, the directory too) and one
	// has none: 4 + 5004 on files, of which 5000 are results and 8 are counted.
	if len(log.Runs[0].Results) != MaxSARIFResults || len(n) != 2 || !strings.HasPrefix(n[1].Message.Text, "8 more findings") {
		t.Errorf("%d results, notifications %+v", len(log.Runs[0].Results), n)
	}
}
