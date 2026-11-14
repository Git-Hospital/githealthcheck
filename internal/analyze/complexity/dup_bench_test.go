package complexity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
)

func BenchmarkClones(b *testing.B) {
	for _, fields := range []int{32, 256} {
		for _, mode := range []struct {
			name   string
			copied bool
		}{{"distinct", false}, {"renamed", true}} {
			b.Run(fmt.Sprintf("%s/%d-fields", mode.name, fields), func(b *testing.B) {
				files := cloneBenchmarkFiles(b, fields, mode.copied)
				ctx := b.Context()
				check, err := clones(ctx, files)
				if err != nil || (len(check) > 0) != mode.copied {
					b.Fatalf("fixture clones = %d, error = %v; copied = %v", len(check), err, mode.copied)
				}
				b.ReportAllocs()
				for b.Loop() {
					found, err := clones(ctx, files)
					if err != nil || (len(found) > 0) != mode.copied {
						b.Fatalf("clones = %d, error = %v", len(found), err)
					}
				}
			})
		}
	}
}

// cloneBenchmarkFiles parses four Go files before timing. Shared fields allow copies with renamed
// parameters; distinct selectors prevent copies, since field names cannot be renamed by the finder.
func cloneBenchmarkFiles(b *testing.B, fields int, copied bool) []*facts.File {
	b.Helper()
	var files []*facts.File
	for fi := range 4 {
		offset := fi * fields
		if copied {
			offset = 0
		}
		var src strings.Builder
		fmt.Fprintf(&src, "package bench\ntype record%d struct {\n", fi)
		for j := range fields {
			fmt.Fprintf(&src, "field%d int\n", offset+j)
		}
		fmt.Fprintf(&src, "}\nfunc process%d(r%d *record%d) int {\n", fi, fi, fi)
		for j := range fields {
			fmt.Fprintf(&src, "r%d.field%d += %d\n", fi, offset+j, j+1)
		}
		fmt.Fprintf(&src, "return r%d.field%d\n}\n", fi, offset)
		f := &facts.File{Path: fmt.Sprintf("bench/file%d.go", fi), Language: "go"}
		golang.Support{}.Fill(f, []byte(src.String()))
		if f.Partial || len(f.Tokens.Kind) < MinCloneTokens {
			b.Fatalf("incomplete clone fixture: %s", f.Path)
		}
		files = append(files, f)
	}
	return files
}
