package tests

import (
	"bytes"
	"context"
	"fmt"
	"hash/maphash"
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
// number of statements and how often it ran. Positions follow //line directives, so a generated file
// can have blocks that end before they start, past its last line, or at column 0.

type blockKey struct{ startLine, startCol, endLine, endCol int }

type block struct {
	stmts   int
	covered bool
}

type profile struct {
	mode string
	// files counts the distinct files the profile lists, and inModules those under one of the
	// repository's module paths; the rest are the standard library and dependencies, which
	// -coverpkg=all adds.
	files, inModules int
	// blocks holds the blocks of the files that are in the commit, by repository path; the blocks of
	// other files are only counted. Memory stays linear in the profile, which is capped at 50 MiB.
	blocks map[string]map[blockKey]*block
}

// Bounds far above anything go test writes. maxBlockStmts keeps sums over a 50 MiB profile from
// overflowing; maxLineBytes and maxPathSegments bound the cost of resolving one file name, which
// looks up each parent of the import path.
const (
	maxBlockStmts   = 1 << 24
	maxLineBytes    = 4096
	maxPathSegments = 128
)

var profileLine = regexp.MustCompile(`^(.+):([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+) ([0-9]+) ([0-9]+)$`)

// resolver maps a file's import path to its repository path, reporting whether the file is under one
// of the repository's modules and whether it is in the commit.
type resolver func(importPath string) (rel string, inModule, atHead bool)

// parseProfile parses a Go coverage profile. Profiles that were concatenated, with a repeated mode line,
// and blocks that appear more than once, as -coverpkg writes them, are merged: a block is covered
// when any of its copies ran.
func parseProfile(ctx context.Context, data []byte, resolve resolver) (*profile, error) {
	p := &profile{blocks: map[string]map[blockKey]*block{}}
	seed := maphash.MakeSeed()
	seen := map[uint64]bool{} // distinct file names, by a hash an attacker cannot predict
	var last string
	var lastBlocks map[blockKey]*block
	n := 0
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		n++
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("line %d: longer than %d bytes", n, maxLineBytes)
		}
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
		stmts, covered := v[4], v[5] > 0
		if stmts > maxBlockStmts {
			return nil, fmt.Errorf("line %d: %d statements in one block", n, stmts)
		}
		if name := m[1]; name != last {
			if strings.Count(name, "/") > maxPathSegments {
				return nil, fmt.Errorf("line %d: file name has more than %d path segments", n, maxPathSegments)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last, lastBlocks = name, nil
			h := maphash.String(seed, name)
			rel, inModule, atHead := resolve(name)
			if !seen[h] {
				seen[h] = true
				p.files++
				if inModule {
					p.inModules++
				}
			}
			if atHead {
				if lastBlocks = p.blocks[rel]; lastBlocks == nil {
					lastBlocks = map[blockKey]*block{}
					p.blocks[rel] = lastBlocks
				}
			}
		}
		if lastBlocks == nil {
			continue
		}
		if b := lastBlocks[k]; b != nil {
			if b.stmts != stmts {
				return nil, fmt.Errorf("line %d: block has %d statements, earlier %d", n, stmts, b.stmts)
			}
			b.covered = b.covered || covered
			continue
		}
		lastBlocks[k] = &block{stmts: stmts, covered: covered}
	}
	if p.mode == "" {
		return nil, fmt.Errorf("no mode line")
	}
	return p, nil
}
