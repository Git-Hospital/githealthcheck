package tests

import (
	"bytes"
	"context"
	"errors"
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
	g := &goProfile{ctx: ctx, resolve: resolve, p: &profile{blocks: map[string]map[blockKey]*block{}},
		seed: maphash.MakeSeed(), seen: map[uint64]bool{}}
	n := 0
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		n++
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("line %d: longer than %d bytes", n, maxLineBytes)
		}
		if err := g.line(strings.TrimSuffix(string(line), "\r")); err != nil {
			if errors.Is(err, ctx.Err()) {
				return nil, err
			}
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if g.p.mode == "" {
		return nil, fmt.Errorf("no mode line")
	}
	return g.p, nil
}

// goProfile is the state of parseProfile while it reads the lines.
type goProfile struct {
	ctx        context.Context
	resolve    resolver
	p          *profile
	seed       maphash.Seed
	seen       map[uint64]bool // distinct file names, by a hash an attacker cannot predict
	last       string
	lastBlocks map[blockKey]*block // the blocks of last, nil when it is not in the commit
}

func (g *goProfile) line(s string) error {
	if s == "" {
		return nil
	}
	if mode, ok := strings.CutPrefix(s, "mode: "); ok {
		return g.mode(mode)
	}
	if g.p.mode == "" {
		return fmt.Errorf("no mode line before the first block")
	}
	m := profileLine.FindStringSubmatch(s)
	if m == nil {
		return fmt.Errorf("not a coverage block")
	}
	var v [6]int
	for i := range v {
		x, err := strconv.Atoi(m[i+2])
		if err != nil {
			return err
		}
		v[i] = x
	}
	k := blockKey{v[0], v[1], v[2], v[3]}
	stmts, covered := v[4], v[5] > 0
	if stmts > maxBlockStmts {
		return fmt.Errorf("%d statements in one block", stmts)
	}
	if name := m[1]; name != g.last {
		if err := g.file(name); err != nil {
			return err
		}
	}
	if g.lastBlocks == nil {
		return nil
	}
	if b := g.lastBlocks[k]; b != nil {
		if b.stmts != stmts {
			return fmt.Errorf("block has %d statements, earlier %d", stmts, b.stmts)
		}
		b.covered = b.covered || covered
		return nil
	}
	g.lastBlocks[k] = &block{stmts: stmts, covered: covered}
	return nil
}

func (g *goProfile) mode(mode string) error {
	switch {
	case g.p.mode == "" && (mode == "set" || mode == "count" || mode == "atomic"):
		g.p.mode = mode
	case g.p.mode == "":
		return fmt.Errorf("unknown mode %q", mode)
	case mode != g.p.mode:
		return fmt.Errorf("mode %q after mode %q", mode, g.p.mode)
	}
	return nil
}

// file starts the blocks of another file: it is counted once, and its blocks are kept when it is in
// the commit.
func (g *goProfile) file(name string) error {
	if strings.Count(name, "/") > maxPathSegments {
		return fmt.Errorf("file name has more than %d path segments", maxPathSegments)
	}
	if g.ctx.Err() != nil {
		return g.ctx.Err()
	}
	g.last, g.lastBlocks = name, nil
	h := maphash.String(g.seed, name)
	rel, inModule, atHead := g.resolve(name)
	if !g.seen[h] {
		g.seen[h] = true
		g.p.files++
		if inModule {
			g.p.inModules++
		}
	}
	if atHead {
		if g.lastBlocks = g.p.blocks[rel]; g.lastBlocks == nil {
			g.lastBlocks = map[blockKey]*block{}
			g.p.blocks[rel] = g.lastBlocks
		}
	}
	return nil
}
