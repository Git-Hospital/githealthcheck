// Command githealthcheck measures the code health of a git repository. Every number it prints is a
// measurement or null with a reason; see docs/architecture.md.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Git-Hospital/githealthcheck/internal/analyze"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/history"
	"github.com/Git-Hospital/githealthcheck/internal/analyze/tests"
	"github.com/Git-Hospital/githealthcheck/internal/execx"
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/gitx"
	"github.com/Git-Hospital/githealthcheck/internal/lang"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
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
	coverageFile := fs.String("coverage", "", "read this coverage report instead of looking for one at the repository root")
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
		// ParseFloat takes the whole string, so "5abc" and "7,5" are errors; NaN would compare false
		// against every score and pass any gate.
		v, err := strconv.ParseFloat(*failUnderText, 64)
		if err != nil || math.IsNaN(v) || v < 0 || v > 10 {
			fmt.Fprintf(stderr, "--fail-under must be a number from 0 to 10, got %q\n", *failUnderText)
			return report.ExitError
		}
		failUnder = &v
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()

	r, err := buildReport(ctx, fs.Arg(0), *coverageFile)
	if err != nil {
		fmt.Fprintln(stderr, "githealthcheck:", err)
		// Incomplete only when the user's --timeout ran out. A per-command timeout inside (a config
		// that blocks) means the repository could not be opened: an error.
		if ctx.Err() != nil {
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
		err = writeFile(*out, body)
	} else {
		_, err = stdout.Write(body)
	}
	if err != nil {
		// A report that was not written must not let a pipeline pass.
		fmt.Fprintln(stderr, "githealthcheck: writing the report:", err)
		return report.ExitError
	}
	return report.ExitCode(r, failUnder)
}

// writeFile refuses a symlink, FIFO or device at path, which would be written through or block, then
// writes the report next to path and renames it into place, so a failed write leaves no half-written
// report.
func writeFile(path string, body []byte) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s exists and is not a regular file", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// walk is repo.Walk; tests replace it to time out after the repository is open.
var walk = repo.Walk

// analyzers are the families implemented in this build. A family without one is null with the reason
// not_implemented.
var analyzers = []analyze.Analyzer{tests.Analyzer{}, history.Analyzer{}}

func buildReport(ctx context.Context, path, coverageFile string) (*report.Report, error) {
	git, err := execx.NewGit(ctx, path)
	if err != nil {
		return nil, err
	}
	// Read before the walk: a report the user named that cannot be read is a usage error, and is
	// reported before any time is spent.
	var cov *facts.Coverage
	inside := false
	if coverageFile != "" {
		if cov, inside, err = readCoverage(git.Root(), coverageFile); err != nil {
			return nil, fmt.Errorf("--coverage: %w", err)
		}
	}
	r := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Tool:          report.Tool{Name: "githealthcheck", Version: version},
		// Only the directory name: an absolute path would put the user's home directory into reports
		// that get uploaded and shared.
		Repository: report.Repo{Path: filepath.Base(git.Root())},
		Skipped:    []report.Skip{},
	}
	// --verify --quiet exits 1 with no output only when HEAD does not resolve: an unborn branch. Any
	// other failure is an error, not an absent head.
	head, err := git.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		r.Repository.Head = strings.TrimSpace(string(head))
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && ctx.Err() == nil:
	default:
		return nil, err
	}
	shallow, err := git.Run(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, err
	}
	r.Repository.Shallow = strings.TrimSpace(string(shallow)) == "true"

	// Only the files in HEAD's tree are read: untracked and ignored files (.venv, build output) are not
	// part of the commit the report names. A branch with no commits has no files.
	var entries []repo.Entry
	if r.Repository.Head != "" {
		out, err := git.Run(ctx, "ls-tree", "-r", "-z", "--full-tree", r.Repository.Head)
		if err == nil {
			entries, err = repo.ParseTree(out)
		}
		if err != nil && ctx.Err() == nil {
			return nil, err
		}
	}
	blobID, err := repo.BlobID(git.ObjectFormat())
	if err != nil {
		return nil, err
	}

	// A timed-out walk still produces a report: what was read is listed, walkComplete is false, every
	// family is null with reason timeout, and the run exits 3.
	familyReason := report.NotImplemented
	tree := &repo.Tree{}
	if ctx.Err() == nil {
		tree, err = walk(ctx, git.Root(), entries, blobID)
	}
	if tree == nil {
		tree = &repo.Tree{}
	}
	switch {
	case ctx.Err() != nil:
		familyReason = report.Timeout
	case err != nil:
		return nil, err
	default:
		r.Inputs.WalkComplete = true
	}
	r.Skipped = append(r.Skipped, tree.Skipped...)
	r.Languages = languages(tree)
	r.Inputs.FilesTotal = len(tree.Files)
	for _, f := range tree.Files {
		if f.Modified {
			r.Repository.ModifiedFiles++
		}
		if lang.Analysed(f.Language) {
			r.Inputs.FilesAnalysed++
		}
	}
	missing := slices.ContainsFunc(tree.Skipped, func(s report.Skip) bool { return s.Reason == report.SkipMissing })
	r.Repository.Dirty = r.Repository.ModifiedFiles > 0 || missing

	rf := &facts.Repo{Files: tree.Files, Paths: map[string]bool{}, Coverage: cov}
	for _, e := range entries {
		rf.Paths[e.Path] = true
	}
	if inside {
		cov.Tracked = rf.Paths[cov.Name]
	}
	if familyReason != report.Timeout {
		var skipped []report.Skip
		rf.Modules, skipped = modules(ctx, git.Root(), entries)
		r.Skipped = append(r.Skipped, skipped...)
		if cov == nil {
			rf.Coverage, skipped = findCoverage(git.Root(), rf.Paths)
			r.Skipped = append(r.Skipped, skipped...)
		}
		rf.History, skipped, _ = readHistory(ctx, git, r.Repository.Head, r.Repository.Shallow) // nil only when ctx ended
		r.Skipped = append(r.Skipped, skipped...)
		if rf.History != nil {
			r.Inputs.Commits = len(rf.History.Commits)
		}
	}

	byFamily := map[report.Family]analyze.Analyzer{}
	for _, a := range analyzers {
		byFamily[a.Family()] = a
	}
	for _, f := range report.Families {
		fr := report.FamilyResult{Family: f, Reason: familyReason, Score: report.Null(familyReason)}
		if a := byFamily[f]; a != nil && familyReason != report.Timeout {
			res := a.Run(ctx, rf)
			fr.Measured, fr.Reason, fr.Metrics, fr.Findings = res.Measured, res.Reason, res.Metrics, res.Findings
			// Scores are percentiles against the calibration corpus, which does not exist before v0.4.0.
			fr.Score = report.Null(report.NotCalibrated)
			if !res.Measured {
				fr.Score = report.Null(res.Reason)
			}
			if res.Coverage != nil {
				r.Inputs.Coverage = res.Coverage
			}
		}
		if fr.Measured {
			r.Overall.Measured++
		}
		r.Families = append(r.Families, fr)
	}
	r.Overall.Of = len(report.Families)
	r.Overall.Score = report.Null(report.InsufficientFamily)
	if r.Overall.Measured >= 2 {
		r.Overall.Score = report.Null(report.NotCalibrated)
	}
	// A report the user named is listed even when no family read it, e.g. in a repository without Go.
	if r.Inputs.Coverage == nil && cov != nil {
		r.Inputs.Coverage = &report.CoverageInput{Path: cov.Name, Tracked: cov.Tracked}
	}
	return r, nil
}

// modules reads every go.mod in the commit that the go command does not ignore. They are ordered by
// depth, so of two go.mod files that declare the same module path the shallower one is used. When ctx
// ends it stops; the analyzers then report the timeout.
func modules(ctx context.Context, root string, entries []repo.Entry) ([]facts.Module, []report.Skip) {
	var mods []facts.Module
	var skipped []report.Skip
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if filepath.Base(e.Path) != "go.mod" || (e.Mode != "100644" && e.Mode != "100755") || golang.Package(e.Path) == "" {
			continue
		}
		src, skip := repo.ReadFile(root, e.Path, repo.MaxSourceBytes)
		if skip != nil {
			skipped = append(skipped, *skip)
			continue
		}
		if p := golang.ModulePath(src); p != "" {
			mods = append(mods, facts.Module{Dir: golang.Package(e.Path), Path: p})
		}
	}
	slices.SortStableFunc(mods, func(a, b facts.Module) int {
		return cmp.Or(cmp.Compare(strings.Count(a.Dir, "/"), strings.Count(b.Dir, "/")), cmp.Compare(a.Dir, b.Dir))
	})
	return mods, skipped
}

// readHistory reads the non-merge commits reachable from head and applies the .mailmap at the root of
// the work tree, as git would. git's own mailmap support is off (execx): the file is read here, with
// the walk's guards against symlinks and FIFOs. A branch with no commits has an empty history. When
// ctx ends it returns nil; the analyzers then report the timeout.
func readHistory(ctx context.Context, git *execx.Git, head string, shallow bool) (*facts.History, []report.Skip, error) {
	h := &facts.History{Shallow: shallow}
	if head == "" {
		return h, nil, nil
	}
	commits, err := gitx.Log(ctx, git, head)
	switch {
	case errors.Is(err, execx.ErrOutputTooLarge) || errors.Is(err, gitx.ErrTooLarge):
		h.TooLarge = true
		return h, nil, nil
	case err != nil && ctx.Err() != nil:
		return nil, nil, err
	case err != nil:
		// The other families still report: the history is null with the reason tool_crashed. The
		// error text is not echoed into the report, which is shared.
		h.Failed = true
		return h, nil, nil
	}
	var skipped []report.Skip
	src, skip := repo.ReadFile(git.Root(), ".mailmap", repo.MaxSourceBytes)
	switch {
	case skip == nil:
		mm := gitx.ParseMailmap(src)
		for i := range commits {
			commits[i].Name, commits[i].Email = mm.Map(commits[i].Name, commits[i].Email)
		}
	case skip.Reason != report.SkipMissing:
		skipped = append(skipped, *skip)
	}
	h.Commits = commits
	return h, skipped, nil
}

// findCoverage looks for a coverage report under the names in tests.ReportNames at the root of the work
// tree, tracked or not: CI writes the report just before the scan and does not commit it. A name
// that exists but cannot be read, such as a symlink, is listed as skipped and the next name is tried.
func findCoverage(root string, tracked map[string]bool) (*facts.Coverage, []report.Skip) {
	var skipped []report.Skip
	for _, name := range tests.ReportNames {
		data, skip := repo.ReadFile(root, name, repo.MaxReportBytes)
		switch {
		case skip == nil:
			return &facts.Coverage{Name: name, Tracked: tracked[name], Data: data}, skipped
		case skip.Reason != report.SkipMissing:
			skipped = append(skipped, *skip)
		}
	}
	return nil, skipped
}

// readCoverage reads the report the user named with --coverage. It may be outside the repository; the
// report then names it by its base name only, so no absolute path reaches the output. inside is true
// when the report is in the work tree, and its name is then relative to the root.
func readCoverage(root, name string) (c *facts.Coverage, inside bool, err error) {
	data, err := repo.ReadPath(name, repo.MaxReportBytes)
	if err != nil {
		return nil, false, err
	}
	c = &facts.Coverage{Name: filepath.Base(name), Data: data}
	abs, err1 := filepath.Abs(name)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	realRoot, err2 := filepath.EvalSymlinks(root)
	if err1 == nil && err2 == nil {
		if rel, err := filepath.Rel(realRoot, abs); err == nil && filepath.IsLocal(rel) {
			c.Name, inside = filepath.ToSlash(rel), true
		}
	}
	return c, inside, nil
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
			l = &report.Language{Name: f.Language, Analysed: lang.Analysed(f.Language)}
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
	fmt.Fprintf(&b, "%s at %s", printable(r.Repository.Path), head)
	if r.Repository.Shallow {
		b.WriteString(" (shallow clone)")
	}
	if r.Repository.Dirty {
		fmt.Fprintf(&b, " (work tree differs from it: %d files modified)", r.Repository.ModifiedFiles)
	}
	b.WriteString("\n\nLanguages")
	if !r.Inputs.WalkComplete {
		b.WriteString(" (partial: the run timed out during the walk)")
	}
	b.WriteString("\n")
	for _, l := range r.Languages {
		status := "not analysed"
		if l.Analysed {
			status = "analysed"
		}
		fmt.Fprintf(&b, "  %-12s %6d files %8d lines  %s\n", l.Name, l.Files, l.Lines, status)
	}
	b.WriteString("\nFamilies\n")
	findings := 0
	for _, f := range r.Families {
		fmt.Fprintf(&b, "  %-12s score %s\n", f.Family, scoreText(f.Score))
		names := make([]string, 0, len(f.Metrics))
		for name := range f.Metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "    %-24s %s\n", name, metricText(name, f.Metrics[name]))
		}
		findings += len(f.Findings)
	}
	if c := r.Inputs.Coverage; c != nil {
		tracked := "untracked"
		if c.Tracked {
			tracked = "tracked"
		}
		fmt.Fprintf(&b, "\nCoverage report %s (%s)", printable(c.Path), tracked)
		if c.Files != nil {
			fmt.Fprintf(&b, ": %d of the %d files it lists under the repository's modules are in the commit (%d files listed)",
				*c.FilesAtHead, *c.ModuleFiles, *c.Files)
		}
		b.WriteString("\n")
	}
	if findings > 0 {
		fmt.Fprintf(&b, "\nFindings (%d)\n", findings)
		for _, f := range r.Families {
			for _, fd := range f.Findings {
				if fd.Path == "" {
					fmt.Fprintf(&b, "  %s  %s\n", fd.RuleID, fd.Message)
				} else {
					fmt.Fprintf(&b, "  %s  %s: %s\n", fd.RuleID, printable(fd.Path), fd.Message)
				}
			}
		}
	}
	fmt.Fprintf(&b, "\nOverall  %s  (%d of %d families measured)\n", scoreText(r.Overall.Score), r.Overall.Measured, r.Overall.Of)
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\nSkipped %d paths (see --format json for the list)\n", len(r.Skipped))
	}
	return b.String()
}

// printable quotes a string that comes from the scanned repository when it holds anything but graphic
// characters (ESC, CR, a newline, a bidi control, invalid UTF-8), so a path cannot move the cursor or
// forge lines of the report in a terminal or a CI log. JSON output escapes control characters below
// U+0020 but not bidi or C1 controls; it is meant for programs, not terminals.
func printable(s string) string {
	if utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsGraphic(r) }) < 0 {
		return s
	}
	return strconv.QuoteToGraphic(s)
}

// metricText prints a share or a coverage as a percentage, a factor as a whole number and any other
// value as a plain number.
func metricText(name string, m report.Measurement) string {
	if m.IsNull() {
		return "null (" + string(m.Reason) + ")"
	}
	switch {
	case strings.HasSuffix(name, "_share") || strings.HasSuffix(name, "_coverage"):
		return fmt.Sprintf("%.1f%%  (from %d)", 100**m.Value, m.Inputs)
	case strings.HasSuffix(name, "_factor"):
		return fmt.Sprintf("%.0f  (from %d)", *m.Value, m.Inputs)
	}
	return fmt.Sprintf("%.2f  (from %d)", *m.Value, m.Inputs)
}

func scoreText(m report.Measurement) string {
	if m.IsNull() {
		return "null (" + string(m.Reason) + ")"
	}
	return fmt.Sprintf("%.1f/10", *m.Value)
}
