package tests

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A Go coverage profile, as written by go test -coverprofile and go tool covdata textfmt:
//
//	mode: set
//	example.com/m/pkg/file.go:4.2,6.3 2 1
//
// Each line is one block: the file's import path, the block's start and end as line.column, its
// number of statements and how often it ran.

type blockKey struct{ startLine, startCol, endLine, endCol int }

type block struct {
	stmts   int
	covered bool
}

type profile struct {
	mode  string
	files map[string]map[blockKey]*block // import path of the file -> its blocks
}

var profileLine = regexp.MustCompile(`^(.+):([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+) ([0-9]+) ([0-9]+)$`)

// parseProfile parses a Go coverage profile. Profiles that were concatenated, with a repeated mode line,
// and blocks that appear more than once, as -coverpkg writes them, are merged: a block is covered
// when any of its copies ran.
func parseProfile(data []byte) (*profile, error) {
	p := &profile{files: map[string]map[blockKey]*block{}}
	n := 0
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		n++
		s := strings.TrimSuffix(string(line), "\r")
		if s == "" {
			continue
		}
		if mode, ok := strings.CutPrefix(s, "mode: "); ok {
			switch {
			case p.mode == "" && (mode == "set" || mode == "count" || mode == "atomic"):
				p.mode = mode
			case p.mode == "":
				return nil, fmt.Errorf("line %d: unknown mode %q", n, mode)
			case mode != p.mode:
				return nil, fmt.Errorf("line %d: mode %q after mode %q", n, mode, p.mode)
			}
			continue
		}
		if p.mode == "" {
			return nil, fmt.Errorf("line %d: no mode line before the first block", n)
		}
		m := profileLine.FindStringSubmatch(s)
		if m == nil {
			return nil, fmt.Errorf("line %d: not a coverage block", n)
		}
		var v [6]int
		for i := range v {
			x, err := strconv.Atoi(m[i+2])
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", n, err)
			}
			v[i] = x
		}
		k := blockKey{v[0], v[1], v[2], v[3]}
		if k.startLine < 1 || k.startCol < 1 || k.endLine < k.startLine || (k.endLine == k.startLine && k.endCol < k.startCol) {
			return nil, fmt.Errorf("line %d: block ends before it starts", n)
		}
		// Atoi has already refused a count that overflows int.
		stmts, covered := v[4], v[5] > 0
		blocks := p.files[m[1]]
		if blocks == nil {
			blocks = map[blockKey]*block{}
			p.files[m[1]] = blocks
		}
		if b := blocks[k]; b != nil {
			if b.stmts != stmts {
				return nil, fmt.Errorf("line %d: block has %d statements, earlier %d", n, stmts, b.stmts)
			}
			b.covered = b.covered || covered
			continue
		}
		blocks[k] = &block{stmts: stmts, covered: covered}
	}
	if p.mode == "" {
		return nil, fmt.Errorf("no mode line")
	}
	return p, nil
}
