package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsFix(t *testing.T) {
	for subject, want := range map[string]bool{
		"Fix panic when cloning with wrong ssh format. (#32076)": true,
		"routes: fix nil pointer on empty body":                  true,
		"Handle incorrectly parsed DST offsets":                  true,
		"Add a new storage backend":                              false,
		"Fix typo in README":                                     false,
		"fix the lint errors":                                    false,
		"STY: fix PERF102 ruff error":                            false,
		"chore(deps): fix broken lockfile":                       false,
		"TST: regression test for #123":                          false,
		`Revert "Add booleanization strategy option"`:            false,
		"prefix fixture loading":                                 false,
		"\u017ftyle: fix crash":                                  true,
		"docs\v: fix crash":                                      false,
	} {
		if got := IsFix(subject); got != want {
			t.Errorf("IsFix(%q) = %v, want %v", subject, got, want)
		}
	}
}

// The labelled set's verdicts were written by scripts/fixrule-eval.py from the same expressions with
// Python's re; RE2 must give the same verdict on every subject.
func TestIsFixMatchesTheFrozenVerdicts(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "fixrule")
	subjects, verdicts := readTSV(t, filepath.Join(dir, "subjects.tsv")), readTSV(t, filepath.Join(dir, "verdicts.tsv"))
	if len(subjects) != len(verdicts) || len(subjects) == 0 {
		t.Fatalf("%d subjects, %d verdicts", len(subjects), len(verdicts))
	}
	for i, s := range subjects {
		v := verdicts[i]
		if s[0] != v[0] || s[1] != v[1] {
			t.Fatalf("row %d: %v against %v", i, s[:2], v[:2])
		}
		if got := IsFix(s[2]); got != (v[2] == "true") {
			t.Errorf("%s %s: IsFix(%q) = %v, the frozen verdict is %s", s[0], s[1], s[2], got, v[2])
		}
	}
}

func readTSV(t *testing.T, path string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")[1:] {
		rows = append(rows, strings.SplitN(line, "\t", 3))
	}
	return rows
}
