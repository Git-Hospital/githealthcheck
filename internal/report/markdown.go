package report

import (
	"fmt"
	"sort"
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
	fmt.Fprintf(&b, "%s at `%s`, %d of %d families measured", mdEscape(r.Repository.Path), mdEscape(head), r.Overall.Measured, r.Overall.Of)
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
		names := make([]string, 0, len(f.Metrics))
		for n := range f.Metrics {
			names = append(names, n)
		}
		sort.Strings(names)
		var ms []string
		for _, n := range names {
			m := f.Metrics[n]
			s := n + " " + valueMD(n, m)
			if sc, ok := f.MetricScores[n]; ok {
				s += fmt.Sprintf(" (%.1f)", sc)
			}
			ms = append(ms, s)
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", f.Family, scoreMD(f.Score), strings.Join(ms, "<br>"))
	}

	total := 0
	for _, f := range r.Families {
		total += len(f.Findings)
	}
	if total > 0 {
		fmt.Fprintf(&b, "\n### Findings (%d)\n\n", total)
		listed := 0
		for _, f := range r.Families {
			for _, fd := range f.Findings {
				if listed == MaxMarkdownFindings {
					break
				}
				listed++
				where := ""
				switch {
				case fd.Path != "" && fd.Line > 0:
					where = fmt.Sprintf("%s:%d: ", mdEscape(fd.Path), fd.Line)
				case fd.Path != "":
					where = mdEscape(fd.Path) + ": "
				}
				rule := fmt.Sprintf("[%s](%s%s.md)", fd.RuleID, RulesURL, fd.RuleID)
				line := fmt.Sprintf("- %s %s%s", rule, where, mdEscape(fd.Message))
				if fd.Suppressed {
					line += " (suppressed: " + mdEscape(fd.SuppressReason) + ")"
				}
				b.WriteString(line + "\n")
			}
		}
		if listed < total {
			fmt.Fprintf(&b, "\n%d more findings are in the JSON report.\n", total-listed)
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "\n%d paths were not read; the JSON report lists them.\n", len(r.Skipped))
	}
	return b.String()
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
