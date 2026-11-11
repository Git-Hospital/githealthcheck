package golang

import (
	"slices"
	"strings"
	"testing"
)

// A scanned repository's go.mod is untrusted input, and ModulePath, ModuleRequires and ModuleGo read
// it by hand. The target runs all three on the same bytes.
func FuzzModuleReaders(f *testing.F) {
	for _, seed := range []string{
		"",
		// The cases of TestModulePath.
		"module example.com/m\n\ngo 1.22\n",
		"// header\nmodule example.com/m // trailing\n",
		"module \"example.com/quoted\"\n",
		"module `example.com/raw`\n",
		"module example.com/crlf\r\n",
		"  module   example.com/spaces  \n",
		"go 1.22\n",
		"module (\n\texample.com/block\n)\n",
		"module \"unterminated\n",
		"module a b\n",
		"// module example.com/commented\n",
		"require example.com/x v1.0.0\nmodule example.com/late\n",
		// A require block, a replace block and single lines of both.
		"module example.com/m\n\ngo 1.24\n\nrequire golang.org/x/mod v0.20.0 // indirect\nrequire (\n\tgithub.com/a/b v1.0.0\n\t\"github.com/quoted/c\" v1.2.0\n\t// github.com/commented/out v1.0.0\n)\n",
		"replace (\n\tgithub.com/a/b => ../b\n\tgithub.com/c/d v1.0.0 => github.com/e/f v1.1.0\n)\nreplace github.com/g/h => ./h\n",
		"require(\n\tgithub.com/tight/paren v1.0.0\n)\n",
		// Blocks that never close, and a close with no block open.
		"require (\n\tgithub.com/a/b v1.0.0\n",
		"replace (\n",
		")\nrequire github.com/after/close v1.0.0\n",
		// go directives that are not versions.
		"go\n",
		"go 1.22rc1\ngo not-a-version\n",
		"go 99999999999999999999.1\n",
		"toolchain go1.23.4\ngo 1.23\n",
		"module \xff\xfe\nrequire \"\\xzz\" v1\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path, requires, goVersion := ModulePath(data), ModuleRequires(data), ModuleGo(data)

		if goVersion != "" && !strings.HasPrefix(goVersion, "go") {
			t.Fatalf("ModuleGo = %q, want \"\" or a string that starts with go", goVersion)
		}
		// The same bytes read twice give the same answer: nothing depends on map order or on state
		// kept between calls.
		if again := ModulePath(data); again != path {
			t.Fatalf("ModulePath = %q, then %q", path, again)
		}
		if again := ModuleRequires(data); !slices.Equal(again, requires) {
			t.Fatalf("ModuleRequires = %q, then %q", requires, again)
		}
		if again := ModuleGo(data); again != goVersion {
			t.Fatalf("ModuleGo = %q, then %q", goVersion, again)
		}
	})
}
