# GHC-TEST-002: package statement coverage below 60%

**Family:** tests. **Languages:** Go, Python. **Needs:** a fresh coverage report.

The share of a package's statements that ran in the tests, according to the coverage report, is below
60%.

## Threshold and source

60%, Google's "acceptable" level: "Although there is no 'ideal code coverage number,' at Google we offer
the general guidelines of 60% as 'acceptable', 75% as 'commendable' and 90% as 'exemplary.'"
(Arguelles, Ivanković, Bender, "Code Coverage Best Practices", Google Testing Blog, 2020-08-07,
<https://testing.googleblog.com/2020/08/code-coverage-best-practices.html>). Exactly 60% is not a
finding.

## Input

The report is read, never produced: the repository's tests are not run (ADR 0001, D12). It is the file
named by `--coverage`, or else the first of `coverage.out`, `cover.out`, `coverage.txt`, `c.out`,
`profile.cov` and `coverage.xml` at the repository root, tracked or not. A name that is a symlink, a FIFO or larger than
50 MiB is not read and is listed in `skipped`.

Supported format: the Go coverage profile (`go test -coverprofile`, `go tool covdata textfmt`). Profiles
concatenated from several runs, and blocks repeated by `-coverpkg`, are merged; a block counts as covered
when any copy of it ran. Paths in the profile are import paths, mapped to files through the `go.mod`
files of the commit (not those under `testdata/` or other directories the go command ignores). Files
outside the repository's module paths, such as the standard library and dependencies that
`-coverpkg=all` adds, are counted in `files` and otherwise ignored.

The report is not used, and coverage is `null` with the reason `stale_coverage_report`, when fewer than
90% of the files it lists under the repository's module paths exist in the commit (a report with no
file under them is stale too), or when a block of
the repository's own code starts before line 1, ends before it starts, or ends past the last line of
its file as read from the work tree. Generated files are not checked, because `//line` directives (in
goyacc output, for example) move their positions; a `//line` directive in hand-written code makes the
report stale. A report from an older commit whose files kept their line counts is not detected.
`inputs.coverage` names the report, says whether it is tracked, and gives `files`, `moduleFiles` and
`filesAtHead`; the counts are absent when the report could not be parsed, and coverage is then `null`
with the reason `invalid_coverage_report`.

Vendored and generated files are left out of the statistics, and so is a file of the commit that was
not read (for example one over 1 MiB).

### Python

Supported format: Cobertura XML, as coverage.py writes it (`coverage xml`), told from a Go profile by
its first character. Each `line` of a class is one statement, as coverage.py counts them, so the
coverage equals coverage.py's `line-rate` for the same files; lines repeated under `methods`, as other
Cobertura writers list them, are not counted again. Branch data is not read. A file name is relative to
one of the report's `source` paths, which are absolute paths on the machine that ran the tests: the
source joined with the name ends with the repository path. Of its suffixes that keep the name whole,
over every source and the name on its own, the longest that is a path of the commit is the file, so
`/home/ci/work/proj/src` with `pkg/mod.py` is `src/pkg/mod.py`. The name itself is never cut: a file
`old/util.py` that is gone from the commit does not become `util.py`. An absolute name under a source
is that source and a relative name; one under no source keeps at least its directory and base name.
One file written several ways (with `\` or `/`, relative or absolute) is counted once. Every file the report lists counts in `moduleFiles`, since Cobertura has no module
paths, and the freshness checks are those above: 90% of them at HEAD, and no line past the end of its
file. Test files are left out of the statistics, as SonarSource leaves them out of coverage. A package
is a directory. None of this needs python3: without it, Python's complexity is not analysed, but its
coverage and test files are.

The XML is read with Go's `encoding/xml`, which expands no entities other than XML's own and reads no
DTD. Only `source` elements in `coverage/sources` are sources. A report is invalid when it uses another
entity, nests elements more than 32 deep, lists more than 64 sources, has a source or file name over 1024 bytes, or needs more than about two million
lookups to map its files to the commit (a real one needs a few per file).

## Fix

Add tests for the package's untested paths. Suppression with a reason (D21) is not implemented yet.
