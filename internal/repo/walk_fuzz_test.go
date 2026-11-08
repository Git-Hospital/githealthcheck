package repo

import "testing"

func FuzzParseTree(f *testing.F) {
	f.Add([]byte("100644 blob aaaa\ta b.go\x00120000 blob bbbb\tlink.go\x00160000 commit cccc\tsub\x00"))
	f.Add([]byte("garbage\x00"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		entries, err := ParseTree(data)
		if err != nil {
			return
		}
		for i, entry := range entries {
			if entry.Mode == "" || entry.OID == "" || entry.Path == "" {
				t.Fatalf("entry %d lacks mode, object name or path: %+v", i, entry)
			}
		}
		malformed := append(append([]byte(nil), data...), []byte("\x00malformed\x00")...)
		if _, err := ParseTree(malformed); err == nil {
			t.Fatal("accepted a valid prefix followed by a malformed record")
		}
	})
}
