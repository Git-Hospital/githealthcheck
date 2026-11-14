package gitx

import (
	"bytes"
	"fmt"
	"testing"
)

func BenchmarkParseLog(b *testing.B) {
	for _, commits := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("%d-commits", commits), func(b *testing.B) {
			input := benchmarkLog(commits)
			check, err := ParseLog(input)
			if err != nil || len(check) != commits {
				b.Fatalf("fixture commits = %d, error = %v", len(check), err)
			}
			if len(check[0].Changes) != 3 || len(check[commits-1].Changes) != 3 {
				b.Fatal("fixture needs three changes per commit")
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				parsed, err := ParseLog(input)
				if err != nil || len(parsed) != commits {
					b.Fatalf("commits = %d, error = %v", len(parsed), err)
				}
			}
		})
	}
}

// benchmarkLog builds valid log records with a bounded pool of paths and authors. It includes
// ordinary changes and renames, so parsing also exercises path interning and the two-path form.
func benchmarkLog(commits int) []byte {
	var input bytes.Buffer
	for i := range commits {
		fmt.Fprintf(&input, "\x01%040x\x00Author %d\x00author%d@example.test\x00%d\n", i+1, i%16, i%16, 1700000000+i)
		fmt.Fprintf(&input, "M\x00src/file%d.go\x00A\x00src/file%d.go\x00R100\x00old/file%d.go\x00src/file%d.go\x00\x00",
			i%128, (i+1)%128, (i+2)%128, (i+2)%128)
	}
	return input.Bytes()
}
