package report

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxMarkdownFindings is the number of findings the Markdown report lists; the rest are counted. A job
// summary holds 1 MiB, and a list past a few hundred lines is not read.
const MaxMarkdownFindings = 200

// Markdown renders r for a job summary or a PR. Every string that comes from the repository (paths,
// messages) is escaped, so a crafted file name cannot add a link, an image or HTML to the page.
func Markdown(r *Report) string {
	var b strings.Builder
	head := r.Repository.Head
	if len(head) > 12 {
		head = head[:12]
	}
	fmt.Fprintf(&b, "## githealthcheck: %s\n\n", scoreMD(r.Overall.Score))
	if head == "" {
		fmt.Fprintf(&b, "%s, no commits yet, %d of %d families measured", mdEscape(r.Repository.Path), r.Overall.Measured, r.Overall.Of)
	} else {
		fmt.Fprintf(&b, "%s at `%s`, %d of %d families measured", mdEscape(r.Repository.Path), mdEscape(head), r.Overall.Measured, r.Overall.Of)
	}
	if c := r.Overall.Calibration; c != nil && c.Repositories > 0 {
		fmt.Fprintf(&b, ", scored as %s against %d corpus repositories (%.0f%% of the code analysed)", mdEscape(c.Language), c.Repositories, 100*c.AnalysedShare)
	}
	b.WriteString(".")
	if r.Repository.Shallow {
		b.WriteString(" Shallow clone: history needs the full history (`fetch-depth: 0`).")
	}
	if !r.Inputs.WalkComplete {
		b.WriteString(" **The run timed out during the walk; counts are partial.**")
	}
	b.WriteString("\n\n| Family | Score | Metrics |\n|---|---|---|\n")
	for _, f := range r.Families {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", f.Family, scoreMD(f.Score), metricsMD(f))
	}
	if c := r.Inputs.Coverage; c != nil {
		coverageMD(&b, c)
	}
	for _, tool := range r.Inputs.Tools {
		fmt.Fprintf(&b, "\nCode read with %s %s.\n", mdEscape(tool.Name), mdEscape(tool.Version))
	}
	mdFindings(&b, r)
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\n%d paths were not read; the JSON report lists them.\n", len(r.Skipped))
	}
	return b.String()
}

// coverageMD names the report and the commit files its coverage can describe.
func coverageMD(b *strings.Builder, c *CoverageInput) {
	tracked := "untracked"
	if c.Tracked {
		tracked = "tracked"
	}
	fmt.Fprintf(b, "\nCoverage report %s (%s", mdEscape(c.Path), tracked)
	if c.Format != "" {
		fmt.Fprintf(b, ", %s", mdEscape(c.Format))
	}
	b.WriteString(")")
	if c.Files != nil && c.FilesAtHead != nil {
		if c.Format == "cobertura" {
			fmt.Fprintf(b, ": %d of the %d files it lists are in the commit", *c.FilesAtHead, *c.Files)
		} else if c.ModuleFiles != nil {
			fmt.Fprintf(b, ": %d of the %d files it lists under the repository's modules are in the commit (%d files listed)",
				*c.FilesAtHead, *c.ModuleFiles, *c.Files)
		}
	}
	b.WriteString(".\n")
}

// metricsMD is a family's metrics, one per line of its table cell.
func metricsMD(f FamilyResult) string {
	var ms []string
	for _, n := range slices.Sorted(maps.Keys(f.Metrics)) {
		s := n + " " + valueMD(n, f.Metrics[n])
		if sc, ok := f.MetricScores[n]; ok {
			s += fmt.Sprintf(" (%.1f)", sc)
		}
		ms = append(ms, s)
	}
	return strings.Join(ms, "<br>")
}

// mdFindings lists up to MaxMarkdownFindings findings and counts the rest.
func mdFindings(b *strings.Builder, r *Report) {
	total := 0
	for _, f := range r.Families {
		total += len(f.Findings)
	}
	if total == 0 {
		return
	}
	fmt.Fprintf(b, "\n### Findings (%d)\n\n", total)
	listed := 0
	for _, f := range r.Families {
		for _, fd := range f.Findings[:min(len(f.Findings), MaxMarkdownFindings-listed)] {
			b.WriteString(findingMD(fd) + "\n")
			listed++
		}
	}
	if listed < total {
		fmt.Fprintf(b, "\n%d more findings are in the JSON report.\n", total-listed)
	}
}

func findingMD(fd Finding) string {
	where := ""
	switch {
	case fd.Path != "" && fd.Line > 0:
		where = fmt.Sprintf("%s:%d: ", mdEscape(fd.Path), fd.Line)
	case fd.Path != "":
		where = mdEscape(fd.Path) + ": "
	}
	line := fmt.Sprintf("- [%s](%s%s.md) %s%s", fd.RuleID, RulesURL, fd.RuleID, where, mdEscape(fd.Message))
	if fd.Suppressed {
		line += " (suppressed: " + mdEscape(fd.SuppressReason) + ")"
	}
	return line
}

func scoreMD(m Measurement) string {
	if m.IsNull() {
		return "null (" + string(m.Reason) + ")"
	}
	return fmt.Sprintf("**%.1f/10**", *m.Value)
}

func valueMD(name string, m Measurement) string {
	switch {
	case m.IsNull():
		return "null (" + string(m.Reason) + ")"
	case strings.HasSuffix(name, "_share") || strings.HasSuffix(name, "_coverage"):
		return fmt.Sprintf("%.1f%%", 100**m.Value)
	case strings.HasSuffix(name, "_factor"):
		return fmt.Sprintf("%.0f", *m.Value)
	}
	return fmt.Sprintf("%.2f", *m.Value)
}

// mdEscape makes s literal text in CommonMark and GitHub's Markdown: every ASCII punctuation character
// is backslash-escaped (which CommonMark allows for all of them), so no link, image, emphasis, table
// cell or HTML tag can start. A character that is not graphic, a line break among them, becomes an
// escape such as \\n or \\u202e, and a byte that is not UTF-8 one such as \\xff, each with its
// backslash escaped. A bare URL is not turned into a link, since ':' and '/' are escaped.
func mdEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "\\\\x%02x", s[i])
		case r == '\n':
			b.WriteString("\\\\n")
		case r == '\t':
			b.WriteString("\\\\t")
		case r < 0x80 && (unicode.IsPunct(r) || unicode.IsSymbol(r)):
			b.WriteByte('\\')
			b.WriteRune(r)
		case !unicode.IsGraphic(r) || r == utf8.RuneError:
			fmt.Fprintf(&b, "\\\\u%04x", r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
