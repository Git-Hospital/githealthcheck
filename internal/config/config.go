// Package config reads .githealthcheck.yml (ADR 0001, D21). The file comes from the scanned
// repository, so it is untrusted input: it is read with the walk's guards, capped in size, and
// parsed by a strict reader of a small YAML subset rather than a YAML library, which keeps
// githealthcheck free of dependencies outside the standard library.
//
// This version knows suppressions only:
//
//	version: 1
//	suppress:
//	  - rule: GHC-TEST-001
//	    path: cmd/tool
//	    reason: main only wires the other packages together
//
// A suppression needs a rule and a non-empty reason; path is a file or a directory, and without it
// the suppression covers every finding of the rule, including those on the whole repository. A
// suppressed finding stays in every report, marked with its reason, and does not count in the exit
// code. Suppression never changes a metric or a score (architecture section 5).
package config

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// FileName is the configuration file at the root of the work tree.
const FileName = ".githealthcheck.yml"

// Limits. A real file is a few hundred bytes; these bound the work a crafted one can cause.
const (
	MaxBytes        = 64 << 10 // the file
	MaxSuppressions = 1000
	maxValueBytes   = 1024 // one value: a rule, a path or a reason
)

// Config is a parsed .githealthcheck.yml.
type Config struct {
	Version  int
	Suppress []Suppression
}

// Suppression marks the findings of one rule, under one path or everywhere, as accepted.
type Suppression struct {
	Rule   string
	Path   string // a file or a directory, relative to the root; empty for every path
	Reason string
	Line   int // where the entry starts, for messages
}

// Error is a problem in the file, at a line.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line == 0 {
		return FileName + ": " + e.Msg
	}
	return fmt.Sprintf("%s line %d: %s", FileName, e.Line, e.Msg)
}

var (
	ruleID = regexp.MustCompile(`^GHC-[A-Z]+-[0-9]{3}$`)
	keyRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Parse reads the file's content. Anything outside the subset (another key, a tab, a flow
// collection, an anchor, a block scalar, a second document) is an error with its line.
func Parse(src []byte) (*Config, error) {
	if len(src) > MaxBytes {
		return nil, &Error{Msg: fmt.Sprintf("over %d KiB", MaxBytes>>10)}
	}
	src = bytes.TrimPrefix(src, []byte("\uFEFF")) // a BOM some editors write
	c := &Config{}
	var item *Suppression
	seen := map[string]bool{} // keys of the current item
	itemIndent := -1          // column of the current item's "- "
	inList, sawVersion, sawSuppress := false, false, false

	finish := func() error {
		if item == nil {
			return nil
		}
		defer func() { item = nil }()
		switch {
		case item.Rule == "":
			return &Error{item.Line, "a suppression needs a rule"}
		case strings.TrimSpace(item.Reason) == "":
			return &Error{item.Line, "a suppression needs a reason"}
		}
		c.Suppress = append(c.Suppress, *item)
		return nil
	}

	for n, raw := range strings.Split(string(src), "\n") {
		line := n + 1
		text := strings.TrimSuffix(raw, "\r")
		if i := strings.IndexByte(text, '\t'); i >= 0 && strings.TrimSpace(text[:i]) == "" {
			return nil, &Error{line, "a tab in the indentation; use spaces"}
		}
		content, err := stripComment(text)
		if err != nil {
			return nil, &Error{line, err.Error()}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		indent := len(content) - len(strings.TrimLeft(content, " "))
		body := strings.TrimSpace(content)
		if body == "---" || body == "..." {
			return nil, &Error{line, "one document only"}
		}

		if indent == 0 && !strings.HasPrefix(body, "- ") && body != "-" {
			if err := finish(); err != nil {
				return nil, err
			}
			inList = false
			key, value, err := keyValue(body)
			if err != nil {
				return nil, &Error{line, err.Error()}
			}
			switch key {
			case "version":
				if sawVersion {
					return nil, &Error{line, "version is given twice"}
				}
				sawVersion = true
				if value != "1" {
					return nil, &Error{line, fmt.Sprintf("version %s is not supported; this build reads version 1", strconv.Quote(value))}
				}
				c.Version = 1
			case "suppress":
				if sawSuppress {
					return nil, &Error{line, "suppress is given twice"}
				}
				sawSuppress = true
				switch value {
				case "":
					inList = true
				case "[]":
				default:
					return nil, &Error{line, "suppress is a list of entries, one per line starting with \"- \""}
				}
			default:
				return nil, &Error{line, fmt.Sprintf("unknown key %s (this version knows version and suppress)", strconv.Quote(key))}
			}
			continue
		}

		if !inList {
			return nil, &Error{line, "unexpected indented line"}
		}
		rest := body
		if strings.HasPrefix(body, "- ") || body == "-" {
			if err := finish(); err != nil {
				return nil, err
			}
			if len(c.Suppress) >= MaxSuppressions {
				return nil, &Error{line, fmt.Sprintf("more than %d suppressions", MaxSuppressions)}
			}
			item, itemIndent, seen = &Suppression{Line: line}, indent, map[string]bool{}
			rest = strings.TrimSpace(strings.TrimPrefix(body, "-"))
			if rest == "" {
				continue
			}
		} else if item == nil || indent != itemIndent+2 {
			return nil, &Error{line, "an entry's keys line up two spaces in from its \"- \""}
		}
		key, value, err := keyValue(rest)
		if err != nil {
			return nil, &Error{line, err.Error()}
		}
		if seen[key] {
			return nil, &Error{line, fmt.Sprintf("%s is given twice in one entry", key)}
		}
		seen[key] = true
		switch key {
		case "rule":
			if !ruleID.MatchString(value) {
				return nil, &Error{line, fmt.Sprintf("rule %s is not a rule ID such as GHC-CPLX-001", strconv.Quote(value))}
			}
			item.Rule = value
		case "path":
			p, err := cleanPath(value)
			if err != nil {
				return nil, &Error{line, err.Error()}
			}
			item.Path = p
		case "reason":
			item.Reason = value
		default:
			return nil, &Error{line, fmt.Sprintf("unknown key %s in a suppression (rule, path, reason)", strconv.Quote(key))}
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	if !sawVersion {
		return nil, &Error{Msg: "version: 1 is required"}
	}
	return c, nil
}

// keyValue splits "key: value" and decodes the value.
func keyValue(s string) (key, value string, err error) {
	key, rest, ok := strings.Cut(s, ":")
	if !ok || !keyRe.MatchString(key) {
		return "", "", fmt.Errorf("expected key: value, got %s", short(s))
	}
	if rest != "" && rest[0] != ' ' {
		return "", "", fmt.Errorf("expected a space after %s:", key)
	}
	value, err = scalar(strings.TrimSpace(rest))
	return key, value, err
}

// scalar decodes a plain, single-quoted or double-quoted value. Plain values that YAML would read as
// something else (a flow collection, an anchor, an alias, a tag, a block scalar) are refused rather
// than guessed at.
func scalar(s string) (string, error) {
	if len(s) > maxValueBytes {
		return "", fmt.Errorf("a value over %d bytes", maxValueBytes)
	}
	switch {
	case s == "":
		return "", nil
	case s[0] == '"':
		if len(s) < 2 || s[len(s)-1] != '"' {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		v, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("a double-quoted value with an escape this reader does not take: %s", short(s))
		}
		return v, nil
	case s[0] == '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		inner := s[1 : len(s)-1]
		if strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
			return "", fmt.Errorf("a single quote inside a single-quoted value is written ''")
		}
		return strings.ReplaceAll(inner, "''", "'"), nil
	case s == "[]":
		return s, nil
	case strings.ContainsRune("[]{}&*!|>%@`", rune(s[0])):
		return "", fmt.Errorf("%s: quote a value that starts with %q", short(s), s[0])
	case strings.Contains(s, ": "):
		return "", fmt.Errorf("%s: quote a value that holds \": \"", short(s))
	}
	return s, nil
}

// stripComment removes a comment: a # at the start or after a space, outside quotes.
func stripComment(s string) (string, error) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\'') && (i == 0 || strings.ContainsRune(" :-", rune(s[i-1]))):
			quote = c
		case quote == 0 && c == '#' && (i == 0 || s[i-1] == ' '):
			return s[:i], nil
		}
	}
	return s, nil
}

// cleanPath turns a path into the slash-separated form findings use. It must stay inside the
// repository.
func cleanPath(p string) (string, error) {
	if strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("path %s: use / as the separator", short(p))
	}
	c := path.Clean(p)
	switch {
	case p == "" || c == ".":
		return "", nil
	case path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../"):
		return "", fmt.Errorf("path %s is outside the repository", short(p))
	}
	return c, nil
}

func short(s string) string {
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return strconv.Quote(s)
}

// Apply marks each finding that a suppression covers, with the first matching suppression's reason.
// It changes nothing else in the families. It returns the suppressions that matched no finding.
func (c *Config) Apply(families []report.FamilyResult) (unused []Suppression) {
	if c == nil {
		return nil
	}
	byRule := map[string][]int{}
	for i, s := range c.Suppress {
		byRule[s.Rule] = append(byRule[s.Rule], i)
	}
	used := make([]bool, len(c.Suppress))
	for fi := range families {
		for di := range families[fi].Findings {
			fd := &families[fi].Findings[di]
			for _, i := range byRule[fd.RuleID] {
				if covers(c.Suppress[i].Path, fd.Path) {
					fd.Suppressed, fd.SuppressReason = true, c.Suppress[i].Reason
					used[i] = true
					break
				}
			}
		}
	}
	for i, s := range c.Suppress {
		if !used[i] {
			unused = append(unused, s)
		}
	}
	return unused
}

// covers reports whether a suppression path covers a finding's path: the same file or directory, or
// a path under the directory. An empty suppression path covers everything.
func covers(suppress, finding string) bool {
	return suppress == "" || finding == suppress || strings.HasPrefix(finding, suppress+"/")
}
