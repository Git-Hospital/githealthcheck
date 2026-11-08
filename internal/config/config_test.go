package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/report"
)

func TestParse(t *testing.T) {
	c, err := Parse([]byte(`# accepted findings, each with its reason
version: 1
suppress:
  - rule: GHC-TEST-001
    path: cmd/tool/   # a directory
    reason: main only wires the other packages together
  -   # the keys can start on the next line
    rule: GHC-HIST-001
    reason: "a two-person team: the truck factor is #2 at best"
- rule: GHC-CPLX-006
  path: './internal/asm/stub.go'
  reason: 'called from assembly, which the rule''s reader cannot see'
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Suppression{
		{Rule: "GHC-TEST-001", Path: "cmd/tool", Reason: "main only wires the other packages together", Line: 4},
		{Rule: "GHC-HIST-001", Reason: "a two-person team: the truck factor is #2 at best", Line: 7},
		{Rule: "GHC-CPLX-006", Path: "internal/asm/stub.go", Reason: "called from assembly, which the rule's reader cannot see", Line: 10},
	}
	if c.Version != 1 || !reflect.DeepEqual(c.Suppress, want) {
		t.Fatalf("got %+v", c)
	}
	for _, src := range []string{"\uFEFFversion: 1\n", "version: 1\n", "version: 1\nsuppress: []\n", "version: \"1\"\r\nsuppress:\r\n"} {
		if c, err := Parse([]byte(src)); err != nil || len(c.Suppress) != 0 {
			t.Errorf("%q: %+v %v", src, c, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	const head = "version: 1\nsuppress:\n"
	for name, tc := range map[string]struct {
		src  string
		line int
		want string
	}{
		"no version":        {"suppress: []\n", 0, "version: 1 is required"},
		"version 2":         {"version: 2\n", 1, "not supported"},
		"unknown top key":   {"version: 1\nexclude:\n", 2, `unknown key "exclude"`},
		"unknown entry key": {head + "  - rule: GHC-TEST-001\n    reasn: typo\n", 4, `unknown key "reasn"`},
		"missing reason":    {head + "  - rule: GHC-TEST-001\n    path: a\n", 3, "needs a reason"},
		"blank reason":      {head + "  - rule: GHC-TEST-001\n    reason: \"  \"\n", 3, "needs a reason"},
		"missing rule":      {head + "  - reason: because\n", 3, "needs a rule"},
		"bad rule":          {head + "  - rule: CPLX-1\n    reason: x\n", 3, "not a rule ID"},
		"twice":             {head + "  - rule: GHC-TEST-001\n    rule: GHC-TEST-002\n", 4, "given twice"},
		"misaligned":        {head + "  - rule: GHC-TEST-001\n      reason: x\n", 4, "line up"},
		"tab":               {head + "\t- rule: GHC-TEST-001\n", 3, "tab"},
		"flow":              {head + "  - {rule: GHC-TEST-001, reason: x}\n", 3, "expected key: value"},
		"anchor":            {head + "  - rule: &r GHC-TEST-001\n", 3, "quote a value"},
		"block scalar":      {head + "  - rule: GHC-TEST-001\n    reason: |\n", 4, "quote a value"},
		"outside":           {head + "  - rule: GHC-TEST-001\n    path: ../x\n", 4, "outside the repository"},
		"absolute":          {head + "  - rule: GHC-TEST-001\n    path: /etc\n", 4, "outside the repository"},
		"backslash":         {head + "  - rule: GHC-TEST-001\n    path: a\\b\n", 4, "separator"},
		"second document":   {"version: 1\n---\n", 2, "one document"},
		"stray indent":      {"version: 1\n  rule: x\n", 2, "unexpected indented"},
		"unterminated":      {head + "  - rule: \"GHC-TEST-001\n", 3, "unterminated"},
		"too big":           {"version: 1\n# " + strings.Repeat("x", MaxBytes) + "\n", 0, "over 64 KiB"},
	} {
		_, err := Parse([]byte(tc.src))
		var e *Error
		if !errors.As(err, &e) || e.Line != tc.line || !strings.Contains(e.Msg, tc.want) {
			t.Errorf("%s: got %v, want line %d and %q", name, err, tc.line, tc.want)
		}
		if err != nil && tc.line > 0 && !strings.Contains(err.Error(), ".githealthcheck.yml line ") {
			t.Errorf("%s: the message does not name the file and line: %v", name, err)
		}
	}
	many := "version: 1\nsuppress:\n" + strings.Repeat("  - rule: GHC-TEST-001\n    reason: x\n", MaxSuppressions+1)
	if _, err := Parse([]byte(many)); err == nil || !strings.Contains(err.Error(), "more than 1000") {
		t.Errorf("too many suppressions: %v", err)
	}
}

func TestApply(t *testing.T) {
	c := &Config{Suppress: []Suppression{
		{Rule: "GHC-TEST-001", Path: "cmd/tool", Reason: "wiring only"},
		{Rule: "GHC-HIST-001", Reason: "two people"},
		{Rule: "GHC-CPLX-001", Path: "nothing/here", Reason: "unused"},
	}}
	fams := []report.FamilyResult{{Findings: []report.Finding{
		{RuleID: "GHC-TEST-001", Path: "cmd/tool"},
		{RuleID: "GHC-TEST-001", Path: "cmd/tool/sub"},
		{RuleID: "GHC-TEST-001", Path: "cmd/toolbox"}, // a sibling with the same prefix
		{RuleID: "GHC-TEST-002", Path: "cmd/tool"},    // another rule
	}}, {Findings: []report.Finding{{RuleID: "GHC-HIST-001"}}}}
	unused := c.Apply(fams)
	var got []string
	for _, f := range fams {
		for _, fd := range f.Findings {
			got = append(got, fd.RuleID+" "+fd.Path+" "+fd.SuppressReason)
		}
	}
	want := []string{"GHC-TEST-001 cmd/tool wiring only", "GHC-TEST-001 cmd/tool/sub wiring only",
		"GHC-TEST-001 cmd/toolbox ", "GHC-TEST-002 cmd/tool ", "GHC-HIST-001  two people"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if len(unused) != 1 || unused[0].Path != "nothing/here" {
		t.Errorf("unused %+v", unused)
	}
	if (*Config)(nil).Apply(fams) != nil {
		t.Error("a missing config must change nothing")
	}
}

// FuzzParse: the file comes from the scanned repository. Parse must not panic, and what it accepts
// must hold only well-formed suppressions.
func FuzzParse(f *testing.F) {
	f.Add("version: 1\nsuppress:\n  - rule: GHC-TEST-001\n    path: cmd/x\n    reason: why\n")
	f.Add("version: 1\nsuppress: []\n")
	f.Add("version: 1\nsuppress:\n- rule: 'GHC-CPLX-006'\n  reason: \"a \\\"quoted\\\" # reason\"\n")
	f.Add("version: 1\nsuppress:\n  -\n    rule: GHC-HIST-001\n    reason: x # y\n")
	f.Fuzz(func(t *testing.T, src string) {
		c, err := Parse([]byte(src))
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error of type %T: %v", err, err)
			}
			return
		}
		if c.Version != 1 || len(c.Suppress) > MaxSuppressions {
			t.Fatalf("accepted %+v", c)
		}
		for _, s := range c.Suppress {
			if !ruleID.MatchString(s.Rule) || strings.TrimSpace(s.Reason) == "" || s.Line < 1 ||
				strings.HasPrefix(s.Path, "/") || strings.HasPrefix(s.Path, "..") {
				t.Fatalf("accepted a bad suppression %+v from %q", s, src)
			}
		}
	})
}
