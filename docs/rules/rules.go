// Package rules holds the page of each rule, built into the binary so githealthcheck explain can
// print it without the repository (ADR 0001, D25). It lives next to the pages because go:embed
// cannot reach a parent directory.
package rules

import (
	"embed"
	"sort"
	"strings"
)

//go:embed GHC-*.md
var pages embed.FS

// IDs returns the ID of every rule that has a page, sorted.
func IDs() []string {
	entries, _ := pages.ReadDir(".")
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(ids)
	return ids
}

// Page returns the page of the rule id, whose case does not matter, and the ID as the page spells
// it. ok is false when no rule has that ID.
func Page(id string) (canonical string, page []byte, ok bool) {
	for _, known := range IDs() {
		if strings.EqualFold(known, strings.TrimSpace(id)) {
			page, err := pages.ReadFile(known + ".md")
			return known, page, err == nil
		}
	}
	return "", nil, false
}

// Title returns the one-line title of a page: its first heading, "# GHC-CPLX-001: function with
// cognitive complexity over 15", without the mark and the ID. It is "" when the page does not start
// with such a heading.
func Title(id string) string {
	canonical, page, ok := Page(id)
	if !ok {
		return ""
	}
	first, _, _ := strings.Cut(string(page), "\n")
	title, found := strings.CutPrefix(strings.TrimSpace(first), "# "+canonical+":")
	if !found {
		return ""
	}
	return strings.TrimSpace(title)
}
