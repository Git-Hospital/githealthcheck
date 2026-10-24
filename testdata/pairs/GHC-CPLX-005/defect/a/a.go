package a

import "github.com/fastyaml/autoparse"

// F parses.
func F(s string) any { return autoparse.Parse(s) }
