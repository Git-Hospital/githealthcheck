package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rulepages "github.com/Git-Hospital/githealthcheck/docs/rules"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Every rule an analyzer registers has a page built into the binary, with the title explain lists, so
// a rule cannot ship without one.
func TestEveryRuleHasAPage(t *testing.T) {
	for _, rl := range rules() {
		if _, page, ok := rulepages.Page(rl.ID); !ok || len(page) == 0 {
			t.Errorf("%s has no page in docs/rules", rl.ID)
			continue
		}
		if rulepages.Title(rl.ID) == "" {
			t.Errorf("docs/rules/%s.md does not start with \"# %s: <title>\"", rl.ID, rl.ID)
		}
	}
}

func TestExplainPrintsThePage(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "docs", "rules", "GHC-CPLX-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"GHC-CPLX-001", "ghc-cplx-001", "Ghc-Cplx-001"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"explain", id}, &stdout, &stderr); code != report.ExitClean {
			t.Fatalf("explain %s = %d, want %d; stderr: %s", id, code, report.ExitClean, stderr.String())
		}
		if stdout.String() != string(want) || stderr.Len() != 0 {
			t.Errorf("explain %s did not print docs/rules/GHC-CPLX-001.md as it is (stderr %q)", id, stderr.String())
		}
	}
}

func TestExplainUnknownRuleListsTheKnownOnes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"explain", "GHC-NOPE-001"}, &stdout, &stderr); code != report.ExitError {
		t.Fatalf("explain of an unknown rule = %d, want %d", code, report.ExitError)
	}
	if stdout.Len() != 0 {
		t.Errorf("an unknown rule printed to stdout: %q", stdout.String())
	}
	for _, rl := range rules() {
		if !strings.Contains(stderr.String(), rl.ID) {
			t.Errorf("the error does not list %s: %s", rl.ID, stderr.String())
		}
	}
	if code := run([]string{"explain", "GHC-CPLX-001", "GHC-CPLX-002"}, &stdout, &stderr); code != report.ExitError {
		t.Errorf("explain with two IDs = %d, want %d", code, report.ExitError)
	}
}

func TestExplainWithoutAnIDListsEveryRule(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"explain"}, &stdout, &stderr); code != report.ExitClean {
		t.Fatalf("explain = %d, want %d; stderr: %s", code, report.ExitClean, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != len(rulepages.IDs()) {
		t.Fatalf("%d lines for %d pages:\n%s", len(lines), len(rulepages.IDs()), stdout.String())
	}
	for _, rl := range rules() {
		if !strings.Contains(stdout.String(), rl.ID+"  "+rulepages.Title(rl.ID)+"\n") {
			t.Errorf("no line with the ID and title of %s:\n%s", rl.ID, stdout.String())
		}
	}
}
