package golang

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

func BenchmarkSupportFill(b *testing.B) {
	for _, functions := range []int{100, 1000} {
		b.Run(fmt.Sprintf("%d-functions", functions), func(b *testing.B) {
			src := benchmarkGoSource(functions)
			check := facts.File{Path: "bench/large.go", Language: "go"}
			Support{}.Fill(&check, src)
			if check.Partial || check.Generated || check.Test || check.Package == "" ||
				check.Functions != functions || len(check.Tokens.Kind) == 0 {
				b.Fatalf("incomplete Go fixture: functions=%d, partial=%v, tokens=%d", check.Functions, check.Partial, len(check.Tokens.Kind))
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				f := facts.File{Path: "bench/large.go", Language: "go"}
				Support{}.Fill(&f, src)
				if f.Partial || f.Functions != functions {
					b.Fatalf("functions = %d, partial = %v", f.Functions, f.Partial)
				}
			}
		})
	}
}

// benchmarkGoSource creates ordinary source, without the generated-code marker that skips tokens.
func benchmarkGoSource(functions int) []byte {
	var src strings.Builder
	src.WriteString("package bench\n")
	for i := range functions {
		fmt.Fprintf(&src, `func f%d(x int) int {
 total := x
 for i := 0; i < 32; i++ {
  if i %% 2 == 0 {
   total += i
  } else {
   total -= i
  }
 }
 return total
}
`, i)
	}
	return []byte(src.String())
}
