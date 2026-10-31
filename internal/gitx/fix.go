package gitx

import (
	"regexp"
	"strings"
)

// The fix rule (ADR 0002, E3): a commit is a fix when its subject names a defect, unless the subject
// says the commit is about something other than shipped code. It was tuned on the dev split of
// testdata/fixrule and frozen before the test split was labelled; scripts/fixrule-eval.py reads the
// same expressions and scores them. Reverts are not counted (ADR 0002, E3 amendment): in the dev split
// three of seven were planned. The expressions are RE2, so their cost is linear in the subject.
const (
	// FixWords are the words that name a defect.
	FixWords = `(?i)\b(fix(e[sd]|ing)?|bug|bugfix|hotfix|regression|crash(es|ed|ing)?|broken|panic(s|ked)?|` +
		`incorrect(ly)?|wrong(ly)?|leak(s|ing)?|deadlock(s)?|segfault|race condition|(nil|null) pointer|` +
		`out of bounds|regress(ed|es)?)\b`
	// NotCodePrefix is a subject prefix that marks tests, docs, style, CI, build or chores.
	NotCodePrefix = `(?i)^\s*\[?(tst|test|tests|doc|docs|sty|style|ci|bld|build|chore|deps|typo|lint|maint|refactor)\]?` +
		`(\([^)]*\))?\s*[:\]]`
	// NotCodeObject is "fix" followed by something that is not shipped code: "fix typo", "fix the lint".
	NotCodeObject = `(?i)\bfix(e[sd]|ing)?\s+(the\s+|a\s+|some\s+|more\s+|minor\s+|broken\s+|failing\s+|flaky\s+)*` +
		`(typos?|docs?|documentation|readme|changelog|comments?|spelling|links?|examples?|lint(er|ing|s)?|` +
		`format(ting)?|style|typing|type hints?|types|mypy|pylint|ruff|flake8|golangci(-lint)?|tests?|ci|build|scripts?)\b`
)

var (
	fixWords      = regexp.MustCompile(FixWords)
	notCodePrefix = regexp.MustCompile(NotCodePrefix)
	notCodeObject = regexp.MustCompile(NotCodeObject)
)

// IsFix reports whether a commit subject names a fix to shipped code. The subject is the repository
// author's text: it is only matched, never printed from here.
func IsFix(subject string) bool {
	subject = asciiOnly(subject)
	if notCodePrefix.MatchString(subject) || notCodeObject.MatchString(subject) {
		return false
	}
	return fixWords.MatchString(subject)
}

// asciiOnly turns every control and non-ASCII character into a space. The rule was measured with
// Python's re in ASCII mode on subjects whose control characters were spaces; RE2's (?i) would also
// fold U+017F to s, and its \s leaves out \v. Without them, both engines see the
// same text and give the same verdict.
func asciiOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f {
			return ' '
		}
		return r
	}, s)
}
