package rules

import (
	"strings"
	"testing"
)

func TestPages(t *testing.T) {
	ids := IDs()
	if len(ids) == 0 {
		t.Fatal("no page is embedded")
	}
	for _, id := range ids {
		canonical, page, ok := Page(strings.ToLower(id))
		if !ok || canonical != id || len(page) == 0 {
			t.Errorf("Page(%q) = %q, %d bytes, %t; want the page of %s", strings.ToLower(id), canonical, len(page), ok, id)
		}
		if Title(id) == "" {
			t.Errorf("%s.md does not start with \"# %s: <title>\"", id, id)
		}
	}
	if _, _, ok := Page("GHC-NOPE-001"); ok {
		t.Error("an unknown ID has a page")
	}
	if _, _, ok := Page("../rules"); ok {
		t.Error("a path has a page")
	}
	if Title("GHC-NOPE-001") != "" {
		t.Error("an unknown ID has a title")
	}
}
