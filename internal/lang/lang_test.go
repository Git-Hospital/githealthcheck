package lang

import "testing"

func TestRegistry(t *testing.T) {
	if s := For("go"); s == nil || s.Name() != "go" || !Analysed("go") {
		t.Errorf("go is not registered: %v", s)
	}
	// Recognised by the walk but not analysed in this build.
	for _, name := range []string{"python", "typescript", "javascript", "rust", ""} {
		if For(name) != nil || Analysed(name) {
			t.Errorf("%q is reported as analysed", name)
		}
	}
}
