package gitx

import (
	"strings"
	"testing"
)

func FuzzParseLog(f *testing.F) {
	hash := strings.Repeat("a", 40)
	head := "\x01" + hash + "\x00n\x00e\x001\n"
	twoChanges := head + "A\x00x\x00M\x00x\x00\x00"
	for _, seed := range []string{
		"",
		// The rejection cases from TestParseLogRejects.
		hash + "\x00n\x00e\x001\n",
		"\x01abc\x00n\x00e\x001\n",
		"\x01" + strings.Repeat("A", 40) + "\x00n\x00e\x001\n",
		"\x01" + hash + "\x00n\x00e",
		head + "Z\x00a.py\x00\x00",
		head + "R100\x00a.py",
		head + "M",
		// Both histories from TestParseLogTooManyChanges, below the normal limit.
		twoChanges,
		twoChanges + head + "D\x00x\x00\x00",
		head + "R100\x00old.py\x00new.py\x00\x00",
		head + "C100\x00old.py\x00copy.py\x00\x00",
		"\x01" + strings.Repeat("b", 64) + "\x00n\x00e\x001\nA\x00x\x00\x00",
		"\x01" + hash + "\x00Ann\x01 Newline\nünï\x00odd\x01address\x001\nA\x00dir/tab\there \"quoted\" ünï\ncode\xff.py\x00\x00",
		// Missing and overflowing times keep the commit, with NoTime set.
		"\x01" + hash + "\x00\x00\x00\nA\x00x\x00\x00",
		"\x01" + hash + "\x00n\x00e\x0099999999999999999999\nA\x00x\x00\x00",
		// A final field need not end with NUL, including a timestamp or rename path.
		"\x01" + hash + "\x00n\x00e\x001",
		head + "A\x00x",
		head + "R100\x00old.py\x00new.py",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		commits, err := ParseLog(data)
		if err != nil {
			if len(commits) != 0 {
				t.Fatal("failed parse returned a partial history")
			}
			return
		}
		for _, c := range commits {
			if c.NoTime && c.Time != 0 {
				t.Fatal("unknown author time has a nonzero value")
			}
		}
		// Close an unterminated final field before adding an invalid status or
		// commit head. Even a valid prefix must not escape as partial history.
		malformed := append(append([]byte(nil), data...), "\x00malformed\x00"...)
		partial, err := ParseLog(malformed)
		if err == nil || len(partial) != 0 {
			t.Fatal("malformed suffix returned a complete or partial history")
		}
	})
}
