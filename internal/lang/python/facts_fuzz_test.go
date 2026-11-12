package python

import (
	"testing"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// The helper is our own script, but its answer is parsed with encoding/json and its numbers are used
// as slice bounds. TestHostileInput covers hostile Python source; this covers a malformed answer.
// No python3 is needed: the bytes go straight to fill.
func FuzzFill(f *testing.F) {
	// What the helper answers for this file, asked with tokens:
	//
	//	import os
	//	def f(a):
	//	    return a and os.sep or eval("1")
	const real = `{"partial":false,"parsed":true,"i":[{"p":"os","l":1,"o":false}],"r":["a","eval","os","sep"],` +
		`"f":[{"n":"f","l":2,"e":3,"c":1,"s":1,"cog":2,"cyc":3,"nest":0,"m":false,"x":true,"sp":[2,18]}],"sm":[],` +
		`"tk":[111,4,147,4,148,151,250,130,4,103,4,163,4,127,4,147,9,148,251],` +
		`"tn":[0,2735482777,0,1678518572,0,0,0,0,1678518572,0,2654219031,0,2317815119,0,2295475727,0,3240785786,0,0],` +
		`"tl":[2,2,2,2,2,2,3,3,3,3,3,3,3,3,3,3,3,3,4]}`
	for _, seed := range []string{
		real,
		// The same answer cut short, in a string, in a number and in the token arrays.
		real[:len(real)/4],
		real[:len(real)/2],
		real[:len(real)-1],
		"",
		"null",
		"[]",
		`{"error":"MemoryError"}`,
		`{"error":"RecursionError","parsed":true}`,
		`{"parsed":false}`,
		`{"parsed":true}`,
		`{"parsed":true,"partial":true,"tk":[1,2,3],"tn":[0,0],"tl":[1,1,1]}`,
		// Spans that do not fit the token arrays: reversed, negative, past the end, with no tokens.
		`{"parsed":true,"f":[{"n":"a","sp":[2,1]},{"n":"b","sp":[-1,1]},{"n":"c","sp":[0,9]},{"n":"d","sp":[1,2]}],"tk":[1,2],"tn":[0,0],"tl":[1,1]}`,
		`{"parsed":true,"f":[{"n":"a","sp":[0,1]}]}`,
		`{"parsed":true,"f":[{"n":"a","sp":[2147483647,2147483647]}],"tk":[1],"tn":[0],"tl":[1]}`,
		`{"parsed":true,"sm":[{"k":"except_ignored","l":1,"e":2,"f":"a"},{"k":"","l":-1},{"k":"except_ignored"}]}`,
		// Values of the wrong type and out of range.
		`{"parsed":true,"tk":[256]}`,
		`{"parsed":true,"f":[{"l":99999999999999999999}]}`,
		`{"parsed":"yes"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, tokens := range []bool{false, true} {
			var file facts.File
			if _, err := fill(&file, data, tokens); err != nil {
				continue
			}
			if file.Functions != len(file.Funcs) {
				t.Fatalf("tokens=%t: Functions = %d with %d Funcs", tokens, file.Functions, len(file.Funcs))
			}
			tk := file.Tokens
			if len(tk.Kind) != len(tk.Name) || len(tk.Kind) != len(tk.Line) {
				t.Fatalf("tokens=%t: token arrays of %d, %d and %d", tokens, len(tk.Kind), len(tk.Name), len(tk.Line))
			}
			if !tokens && len(tk.Kind) != 0 {
				t.Fatalf("tokens=false kept %d tokens", len(tk.Kind))
			}
			for _, fn := range file.Funcs {
				sp := fn.Span
				if sp != [2]int32{} && (sp[0] < 0 || sp[0] > sp[1] || int(sp[1]) > len(tk.Kind)) {
					t.Fatalf("tokens=%t: function %q has span %v in %d tokens", tokens, fn.Name, sp, len(tk.Kind))
				}
			}
			kept := map[facts.SmellKind]int{}
			for _, s := range file.Smells {
				if kept[s.Kind]++; kept[s.Kind] > MaxSmells {
					t.Fatalf("tokens=%t: more than %d smells of kind %q kept", tokens, MaxSmells, s.Kind)
				}
			}
		}
	})
}
