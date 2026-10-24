// Package lang holds the languages this build analyses. A language without a Support is still
// recognised and counted by the walk, but reported as not analysed.
package lang

import (
	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
)

// Support extracts the language-specific facts of one file.
type Support interface {
	Name() string
	// Fill sets f's Test, Generated, Package and Functions from the file's source. It runs once per
	// file, during the walk.
	Fill(f *facts.File, src []byte)
}

var supports = map[string]Support{"go": golang.Support{}}

// For returns the support for a language name, or nil when the language is not analysed.
func For(name string) Support { return supports[name] }

// Analysed reports whether this build analyses a language.
func Analysed(name string) bool { return supports[name] != nil }
