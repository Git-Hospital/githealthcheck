package gitx

import (
	"strings"
	"testing"
)

func FuzzParseMailmap(f *testing.F) {
	for _, id := range mailmapIdentities {
		name, email, _ := strings.Cut(strings.TrimSuffix(id, ">"), " <")
		f.Add([]byte(mailmapSource), name, email)
	}
	f.Add([]byte{}, "", "")
	f.Add([]byte("No Close <broken@example.com\x00\xff\r\n<>\n"), "\x00\xffName", "\xfe@example.com")

	f.Fuzz(func(t *testing.T, src []byte, name, email string) {
		m := ParseMailmap(src)
		m.Map(name, email)
	})
}
