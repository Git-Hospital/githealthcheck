package report

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Rule describes a rule for the renderers: its ID, one-line title and family.
type Rule struct {
	ID     string
	Title  string
	Family Family
}

// RulesURL is where each rule's page lives; a SARIF rule links to RulesURL + ID + ".md".
const RulesURL = "https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/"

// sarifLevel is the level of a family's findings: what complexity and tests find is in the code and
// is a warning; what history finds is about who wrote the code, a note.
func sarifLevel(f Family) string {
	if f == History {
		return "note"
	}
	return "warning"
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string       `json:"id"`
	ShortDescription sarifText    `json:"shortDescription"`
	HelpURI          string       `json:"helpUri"`
	Default          sarifDefault `json:"defaultConfiguration"`
}

type sarifDefault struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool                `json:"executionSuccessful"`
	Notifications       []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string    `json:"level"`
	Message sarifText `json:"message"`
}

type sarifResult struct {
	RuleID       string             `json:"ruleId"`
	RuleIndex    int                `json:"ruleIndex"`
	Level        string             `json:"level"`
	Message      sarifText          `json:"message"`
	Locations    []sarifLocation    `json:"locations"`
	Suppressions []sarifSuppression `json:"suppressions,omitempty"`
}

type sarifLocation struct {
	Physical sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	Artifact sarifArtifact `json:"artifactLocation"`
	Region   *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

type sarifSuppression struct {
	Kind          string `json:"kind"`
	Justification string `json:"justification,omitempty"`
}

// SARIF renders r as SARIF 2.1.0 for code scanning. A result needs a file: findings on a directory or
// on the whole repository (isFile false) stay in the JSON and Markdown reports, and a notification
// says how many. Paths are relative to the scanned root (%SRCROOT%).
func SARIF(r *Report, rules []Rule, isFile func(path string) bool) ([]byte, error) {
	index := map[string]int{}
	driver := sarifDriver{Name: "githealthcheck", Version: r.Tool.Version, InformationURI: "https://github.com/Git-Hospital/githealthcheck"}
	for _, rl := range rules {
		index[rl.ID] = len(driver.Rules)
		driver.Rules = append(driver.Rules, sarifRule{ID: rl.ID, ShortDescription: sarifText{rl.Title},
			HelpURI: RulesURL + rl.ID + ".md", Default: sarifDefault{sarifLevel(rl.Family)}})
	}
	run := sarifRun{Tool: sarifTool{driver}, Results: []sarifResult{}}
	elsewhere := 0
	for _, f := range r.Families {
		for _, fd := range f.Findings {
			i, known := index[fd.RuleID]
			if !known {
				return nil, fmt.Errorf("sarif: finding of unknown rule %s", fd.RuleID)
			}
			if fd.Path == "" || !isFile(fd.Path) {
				elsewhere++
				continue
			}
			loc := sarifPhysical{Artifact: sarifArtifact{URI: uriPath(fd.Path), URIBaseID: "%SRCROOT%"}}
			if fd.Line > 0 {
				loc.Region = &sarifRegion{StartLine: fd.Line}
			}
			res := sarifResult{RuleID: fd.RuleID, RuleIndex: i, Level: sarifLevel(f.Family),
				Message: sarifText{fd.Message}, Locations: []sarifLocation{{loc}}}
			if fd.Suppressed {
				res.Suppressions = []sarifSuppression{{Kind: "external", Justification: fd.SuppressReason}}
			}
			run.Results = append(run.Results, res)
		}
	}
	inv := sarifInvocation{ExecutionSuccessful: ExitCode(r, nil) != ExitIncomplete}
	if elsewhere > 0 {
		inv.Notifications = append(inv.Notifications, sarifNotification{Level: "note", Message: sarifText{
			fmt.Sprintf("%d findings on a directory or on the whole repository are not results here; they are in the JSON and Markdown reports.", elsewhere)}})
	}
	run.Invocations = []sarifInvocation{inv}
	body, err := json.MarshalIndent(sarifLog{Schema: "https://json.schemastore.org/sarif-2.1.0.json", Version: "2.1.0", Runs: []sarifRun{run}}, "", "  ")
	return append(body, '\n'), err
}

// uriPath escapes each segment of a slash-separated relative path for a URI reference, so a name
// with spaces, '%', '#' or '?' stays one path.
func uriPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	out := strings.Join(parts, "/")
	// A first segment with a colon would read as a scheme.
	if first, _, _ := strings.Cut(out, "/"); strings.Contains(first, ":") {
		out = "./" + out
	}
	return out
}
