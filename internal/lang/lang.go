// Package lang holds the languages this build analyses. A language without a support is still
// recognised and counted by the walk, but reported as not analysed.
package lang

import (
	"context"

	"github.com/Git-Hospital/githealthcheck/internal/facts"
	"github.com/Git-Hospital/githealthcheck/internal/lang/golang"
	"github.com/Git-Hospital/githealthcheck/internal/lang/python"
	"github.com/Git-Hospital/githealthcheck/internal/report"
)

// Status says whether a language is analysed in a scan, and with which tool.
type Status struct {
	Analysed bool
	// Reason is why a language that has a support is not analysed, such as tool_not_found.
	Reason report.NullReason
	// Tool is the external tool the language is read with, if any: report.Tool{Name, Version}.
	Tool *report.Tool
}

// Filled is a file whose facts were extracted in another process, with its index in the walk.
type Filled struct {
	Index int
	File  facts.File
}

// Set is the language supports of one scan. Go is read in this process; Python by a pool of python3
// helpers, started for the first Python file and stopped by Close.
type Set struct {
	ctx    context.Context
	py     *python.Pool
	status map[string]Status
}

// Open prepares the supports of a scan.
func Open(ctx context.Context) *Set {
	return &Set{ctx: ctx, status: map[string]Status{"go": {Analysed: true}}}
}

// Status returns the status of a language: not analysed, with no reason, for a language this build
// has no support for or that the scan has not met. A language whose tool could not be started is not
// analysed, with the reason.
func (s *Set) Status(name string) Status { return s.status[name] }

// Tools lists the external tools the scan reads code with.
func (s *Set) Tools() []report.Tool {
	var out []report.Tool
	for _, name := range []string{"go", "python"} {
		if t := s.status[name].Tool; t != nil {
			out = append(out, *t)
		}
	}
	return out
}

// Fill extracts the facts of f, the file at index i of the walk, from src. It returns true when f is
// filled on return. Otherwise the file goes to another process and comes back from Done or Wait, and
// Fill may block until a helper is free.
func (s *Set) Fill(i int, f *facts.File, src []byte) bool {
	switch {
	case f.Language == "go":
		golang.Support{}.Fill(f, src)
		f.Analysed = true
		return true
	case f.Language == "python":
		// Its package and whether it is a test need no python3: the tests family counts the file
		// even when python3 is missing.
		python.Prepare(f, src)
		if _, met := s.status["python"]; !met {
			py, err := python.Start(s.ctx)
			if err != nil {
				s.status["python"] = Status{Reason: python.Reason(err)}
			} else {
				s.py = py
				s.status["python"] = Status{Analysed: true, Tool: &report.Tool{Name: "python3", Version: py.Version()}}
			}
		}
		if s.py != nil {
			s.py.Submit(i, *f, src)
			return false
		}
	}
	return true
}

// Done returns the files filled since the last call, without waiting.
func (s *Set) Done() []Filled { return s.convert(s.py.Done()) }

// Wait waits for every file submitted and returns those not returned before.
func (s *Set) Wait() []Filled { return s.convert(s.py.Wait()) }

func (s *Set) convert(in []python.Result) []Filled {
	out := make([]Filled, len(in))
	for i, r := range in {
		out[i] = Filled{Index: r.Index, File: r.File}
	}
	return out
}

// Close stops the helpers.
func (s *Set) Close() { s.py.Close() }
