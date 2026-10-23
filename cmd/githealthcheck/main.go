// Command githealthcheck measures the code health of a git repository. Every number it prints is a
// measurement or null with a reason; see docs/architecture.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/repo"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage:
  githealthcheck scan [flags] <path>   measure the repository whose root is <path>
  githealthcheck version               print the version

Exit codes: 0 clean, 1 findings or score below --fail-under, 2 error, 3 incomplete.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return report.ExitError
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "githealthcheck", version)
		return report.ExitClean
	case "scan":
		return scan(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return report.ExitClean
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return report.ExitError
	}
}

func scan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or json")
	out := fs.String("o", "", "write the report to this file instead of stdout")
	timeout := fs.Duration("timeout", 10*time.Minute, "give up after this long; the run then exits 3")
	failUnderText := fs.String("fail-under", "", "exit 1 when the overall score is below this value (0-10)")
	if err := fs.Parse(args); err != nil {
		return report.ExitError
	}
	if fs.NArg() != 1 {
		fmt.Fprint(stderr, usage)
		return report.ExitError
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "unknown format %q (text, json)\n", *format)
		return report.ExitError
	}
	var failUnder *float64
	if *failUnderText != "" {
		var v float64
		if _, err := fmt.Sscan(*failUnderText, &v); err != nil || v < 0 || v > 10 {
			fmt.Fprintf(stderr, "--fail-under must be a number from 0 to 10, got %q\n", *failUnderText)
			return report.ExitError
		}
		failUnder = &v
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()

	r, err := buildReport(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "githealthcheck:", err)
		if errors.Is(err, context.DeadlineExceeded) {
			return report.ExitIncomplete
		}
		return report.ExitError
	}
	r.Inputs.ElapsedMillis = time.Since(start).Milliseconds()

	var body []byte
	if *format == "json" {
		body, err = json.MarshalIndent(r, "", "  ")
		body = append(body, '\n')
	} else {
		body = []byte(text(r))
	}
	if err != nil {
		fmt.Fprintln(stderr, "githealthcheck:", err)
		return report.ExitError
	}
	if *out != "" {
		if err := os.WriteFile(*out, body, 0o644); err != nil {
			fmt.Fprintln(stderr, "githealthcheck:", err)
			return report.ExitError
		}
	} else {
		stdout.Write(body)
	}
	return report.ExitCode(r, failUnder)
}

func buildReport(ctx context.Context, path string) (*report.Report, error) {
	git, err := execx.NewGit(ctx, path)
	if err != nil {
		return nil, err
	}
	r := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Tool:          report.Tool{Name: "githealthcheck", Version: version},
		// Only the directory name: an absolute path would put the user's home directory into reports
		// that get uploaded and shared.
		Repository: report.Repo{Path: filepath.Base(git.Root())},
		Skipped:    []report.Skip{},
	}
	if head, err := git.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		r.Repository.Head = strings.TrimSpace(string(head))
	}
	if shallow, err := git.Run(ctx, "rev-parse", "--is-shallow-repository"); err == nil {
		r.Repository.Shallow = strings.TrimSpace(string(shallow)) == "true"
	}

	// A timed-out walk still produces a report: what was read is listed, every family is null with
	// reason timeout, and the run exits 3.
	familyReason := report.NotImplemented
	tree, err := repo.Walk(ctx, git.Root())
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		familyReason = report.Timeout
	case err != nil:
		return nil, err
	}
	r.Skipped = append(r.Skipped, tree.Skipped...)
	r.Languages = languages(tree)
	r.Inputs.FilesTotal = len(tree.Files)

	// No family is implemented yet. Each one says so instead of printing a number, and the run exits 3
	// because nothing was measured.
	for _, f := range report.Families {
		r.Families = append(r.Families, report.FamilyResult{
			Family: f, Reason: familyReason, Score: report.Null(familyReason),
		})
	}
	r.Overall = report.Overall{Score: report.Null(report.InsufficientFamily), Of: len(report.Families)}
	return r, nil
}

// languages counts the project's own code per language; vendored and generated files are left out.
func languages(t *repo.Tree) []report.Language {
	by := map[string]*report.Language{}
	for _, f := range t.Files {
		if f.Vendored || f.Generated {
			continue
		}
		l := by[f.Language]
		if l == nil {
			l = &report.Language{Name: f.Language, Analysed: repo.Analysed[f.Language]}
			by[f.Language] = l
		}
		l.Files++
		l.Lines += f.Lines
	}
	out := []report.Language{}
	for _, l := range by {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func text(r *report.Report) string {
	var b strings.Builder
	head := r.Repository.Head
	if len(head) > 12 {
		head = head[:12]
	}
	fmt.Fprintf(&b, "%s at %s", r.Repository.Path, head)
	if r.Repository.Shallow {
		b.WriteString(" (shallow clone)")
	}
	b.WriteString("\n\nLanguages\n")
	for _, l := range r.Languages {
		status := "not analysed"
		if l.Analysed {
			status = "analysed"
		}
		fmt.Fprintf(&b, "  %-12s %6d files %8d lines  %s\n", l.Name, l.Files, l.Lines, status)
	}
	b.WriteString("\nFamilies\n")
	for _, f := range r.Families {
		fmt.Fprintf(&b, "  %-12s %s\n", f.Family, scoreText(f.Score))
	}
	fmt.Fprintf(&b, "\nOverall  %s  (%d of %d families measured)\n", scoreText(r.Overall.Score), r.Overall.Measured, r.Overall.Of)
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nSkipped %d paths (see --format json for the list)\n", len(r.Skipped))
	}
	return b.String()
}

func scoreText(m report.Measurement) string {
	if m.IsNull() {
		return "null (" + string(m.Reason) + ")"
	}
	return fmt.Sprintf("%.1f/10", *m.Value)
}
