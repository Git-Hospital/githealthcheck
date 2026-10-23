package history

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
)

// The truck factor of Avelino, Passos, Hora and Valente, "A Novel Approach for Estimating Truck
// Factors", ICPC 2016, computed as their tool does (github.com/aserg-ufmg/Truck-Factor, MIT, with its
// config.properties). Where the tool's result depends on Java HashMap order, ties are broken by the
// developer key instead, so the result is the same on every run.

const (
	normalizedDOA = 0.75 // an author's DOA over the file's best DOA must exceed this
	tfCoverage    = 0.5  // developers are removed until fewer than half of the files have an author
)

// ErrTooLarge is returned when following rename chains would read more than MaxVisits history
// entries in all: files that share an ancestor each read its whole history, so a crafted history of
// a few megabytes can cost files x entries.
var ErrTooLarge = errors.New("rename history too large")

// MaxVisits bounds the entries read over all files, far above real repositories: Flask reads about
// 30 thousand.
var MaxVisits = 50_000_000

var (
	// The tool keeps these thresholds as Java floats and compares doubles and floats against them, so
	// they are float32 values here too: 3.293 as a float is 3.2929999828...
	absoluteDOA   = float64(float32(3.293))
	minPercentage = float32(0.1) // a TF developer with under 10% of the top one's files is pruned
)

// authorship is one developer's record on one file.
type authorship struct {
	dev        string
	first      bool // made the first add of the file or of one of its former paths
	secondary  bool // made a later add of it, as a different developer
	deliveries int  // modifications and renames
}

// fileAuthors is the result for one file: its developers' records and the DOA authors among them.
type fileAuthors struct {
	path    string
	records map[string]*authorship
	authors []string // developers whose degree of authorship makes them authors of the file
}

// Result is a truck factor with what it was computed from.
type Result struct {
	TF    int
	Files int // files with at least one add, modification or rename in the history
	// CoveredAfter is the number of Files that still have an author once every developer the greedy
	// step removed is gone, before pruning: the tool's coverage. It is under half of Files.
	CoveredAfter int
	// CoveredWithoutTF is the number of Files that still have an author once only TFDevs are gone.
	// When pruning dropped developers, it can be half of Files or more.
	CoveredWithoutTF int
	TFDevs           []string // the TF developers after pruning, by key
	files            []fileAuthors
}

// entry is one add, modification or rename of a path; id makes it unique across the history.
type entry struct {
	id     int
	commit *facts.Commit
	status byte
	old    string
}

// TruckFactor computes the truck factor over the given paths. dev returns a commit's developer key.
// It returns ctx.Err() when ctx ends first.
func TruckFactor(ctx context.Context, commits []facts.Commit, paths []string, dev func(*facts.Commit) string) (*Result, error) {
	// Every add, modification and rename, by the path it produced. Deletes, copies and type changes
	// are not part of a file's history in the tool.
	byPath := map[string][]entry{}
	id := 0
	for i := len(commits) - 1; i >= 0; i-- { // oldest first, so the first add is the earliest
		c := &commits[i]
		for _, ch := range c.Changes {
			switch ch.Status {
			case 'A', 'M', 'R':
				byPath[ch.Path] = append(byPath[ch.Path], entry{id: id, commit: c, status: ch.Status, old: ch.Old})
				id++
			}
		}
	}

	res := &Result{}
	visits := 0
	for _, p := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		entries := history(byPath, p)
		if visits += len(entries); visits > MaxVisits {
			return nil, ErrTooLarge
		}
		if len(entries) == 0 {
			continue // no commit produced this path: the tool leaves the file out
		}
		res.Files++
		res.files = append(res.files, doa(p, entries, dev))
	}

	// Greedy removal: take away the developer who authors the most files until fewer than half of the
	// files have an author left.
	authored := map[string]map[string]bool{} // developer -> files they author
	changes := map[string]int{}              // developer -> adds and deliveries over all files
	for _, f := range res.files {
		for _, a := range f.authors {
			if authored[a] == nil {
				authored[a] = map[string]bool{}
			}
			authored[a][f.path] = true
		}
		for _, r := range f.records {
			changes[r.dev] += r.deliveries
			if r.first || r.secondary {
				changes[r.dev]++
			}
		}
	}
	if res.Files == 0 {
		return res, nil
	}
	sizes := map[string]int{}
	for d, fs := range authored {
		sizes[d] = len(fs)
	}
	var removed []string
	for len(authored) > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if float32(covered(authored))/float32(res.Files) < tfCoverage {
			break
		}
		// "" is a developer key too (an author without name or email), so it cannot mark "none yet".
		top, chosen := "", false
		for d := range authored {
			if !chosen || cmp.Or(
				cmp.Compare(len(authored[d]), len(authored[top])),
				cmp.Compare(changes[d], changes[top]),
				cmp.Compare(top, d), // ties in the tool fall to HashMap order; here to the smaller key
			) > 0 {
				top, chosen = d, true
			}
		}
		removed = append(removed, top)
		delete(authored, top)
	}
	res.CoveredAfter = covered(authored)

	// Pruning: a TF developer who authors under 10% of the files of the top TF developer does not
	// count.
	topFiles := 0
	for _, d := range removed {
		topFiles = max(topFiles, sizes[d])
	}
	for _, d := range removed {
		if float32(sizes[d])/float32(topFiles) >= minPercentage {
			res.TFDevs = append(res.TFDevs, d)
		}
	}
	res.TF = len(res.TFDevs)
	gone := map[string]bool{}
	for _, d := range res.TFDevs {
		gone[d] = true
	}
	for _, f := range res.files {
		if slices.ContainsFunc(f.authors, func(a string) bool { return !gone[a] }) {
			res.CoveredWithoutTF++
		}
	}
	return res, nil
}

// history collects every add, modification and rename that produced path or, through renames, one
// of its former paths. A former path's whole history is taken, as in the tool.
func history(byPath map[string][]entry, path string) []entry {
	seen := map[int]bool{}
	var out []entry
	queue := []string{path}
	expanded := map[string]bool{path: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, e := range byPath[p] {
			if seen[e.id] {
				continue
			}
			seen[e.id] = true
			out = append(out, e)
			if e.status == 'R' && !expanded[e.old] {
				expanded[e.old] = true
				queue = append(queue, e.old)
			}
		}
	}
	slices.SortFunc(out, func(a, b entry) int { return cmp.Compare(a.id, b.id) })
	return out
}

// doa computes the degree of authorship of every developer of one file and picks its authors.
func doa(path string, entries []entry, dev func(*facts.Commit) string) fileAuthors {
	f := fileAuthors{path: path, records: map[string]*authorship{}}
	first := ""
	changes := 0
	for _, e := range entries {
		d := dev(e.commit)
		r := f.records[d]
		if r == nil {
			r = &authorship{dev: d}
			f.records[d] = r
		}
		switch {
		case e.status == 'A' && first == "":
			first = d
			r.first = true
		case e.status == 'A' && !r.first:
			r.secondary = true // a later add by someone else; a repeated add by the first author is ignored
		case e.status == 'M' || e.status == 'R':
			r.deliveries++
			changes++
		}
	}
	best := 0.0
	values := map[string]float64{}
	for d, r := range f.records {
		fa := 0.0
		if r.first || r.secondary {
			fa = 1
		}
		v := 3.293 + 1.098*fa + 0.164*float64(r.deliveries) - 0.321*math.Log(float64(1+changes-r.deliveries))
		values[d] = v
		best = max(best, v)
	}
	if best <= 0 {
		return f // the tool finds no best author when every DOA is negative
	}
	for d, r := range f.records {
		if values[d]/best > normalizedDOA && (values[d] >= absoluteDOA || r.first || r.secondary) {
			f.authors = append(f.authors, d)
		}
	}
	slices.Sort(f.authors)
	return f
}

// covered counts the files that have an author.
func covered(authored map[string]map[string]bool) int {
	files := map[string]bool{}
	for _, fs := range authored {
		for f := range fs {
			files[f] = true
		}
	}
	return len(files)
}
