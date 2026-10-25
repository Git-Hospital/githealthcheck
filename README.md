# GitHealthCheck

[![CI](https://github.com/Git-Hospital/githealthcheck/actions/workflows/ci.yml/badge.svg)](https://github.com/Git-Hospital/githealthcheck/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Git-Hospital/githealthcheck)](https://github.com/Git-Hospital/githealthcheck/releases)
[![License](https://img.shields.io/github/license/Git-Hospital/githealthcheck)](LICENSE)

A code health analyzer for Git repositories. It looks at tests and coverage, git history, complexity and duplication, and architecture and dependencies. Every number it prints is a measurement or `null` with a reason.

**Status: v0.2.0, the first release of the Go rewrite.** The plan and its reasons are in [docs/decisions/0001-go-rewrite.md](docs/decisions/0001-go-rewrite.md), the design is in [docs/architecture.md](docs/architecture.md), and the changes are in [CHANGELOG.md](CHANGELOG.md). Progress is tracked in [#1](https://github.com/Git-Hospital/githealthcheck/issues/1).

## Install

```sh
go install github.com/Git-Hospital/githealthcheck/cmd/githealthcheck@latest
```

or download an archive for Linux, macOS or Windows from the [releases](https://github.com/Git-Hospital/githealthcheck/releases) and check it with `gh attestation verify <archive> --repo Git-Hospital/githealthcheck`. It needs git 2.44 or later on `PATH`.

The earlier Python version is at tag [`v0.1.0`](https://github.com/Git-Hospital/githealthcheck/tree/v0.1.0). It is not maintained. On Go and Rust repositories it reads no files but still prints scores, so do not rely on its numbers.

## What works today

`githealthcheck scan [--format text|json] [--coverage <file>] [-o <file>] [--timeout <d>] <path>` scans the commit at HEAD. Go is analysed by every family and Python by complexity; other languages are counted. Python needs python3 3.10 or later on `PATH`, whose `ast` module parses the code (it is never run); without one, Python is reported as not analysed, with the reason. Of the four families, three are implemented:

- **tests**: the test-to-code line ratio, the share of packages without a test file, and statement coverage read from an existing Go coverage profile (the tool never runs your tests).
- **history**: the truck factor of Avelino et al. (ICPC 2016), checked against the authors' own tool, the share of files with a single author, and the share of files whose authors have all stopped committing. It works on every recognised language and needs the full history (not a shallow clone) and at least 50 commits.
- **complexity**: per function, cognitive complexity (SonarSource's count, checked against gocognit for Go and SonarSource's own test file for Python), cyclomatic complexity, length and nesting depth; and the patterns generated code leaves behind: errors checked and then dropped, imports of packages the module does not provide, functions nothing references, commented-out code and placeholder bodies; and duplication: blocks of 100 tokens or more repeated with renamed locals, and functions with the body of another.

Their rules are documented in [docs/rules](docs/rules). Architecture is `null` with the reason `not_implemented`. Scores stay `null` until the calibration corpus exists: each measured family's score with the reason `not_calibrated`, and the overall score with `not_calibrated` once two families are measured, `insufficient_families` before that.

```console
$ go test -coverprofile=coverage.out ./...
$ githealthcheck scan --coverage coverage.out .
Languages
  go              101 files    10604 lines  analysed
  shell             2 files       99 lines  not analysed

Families
  tests        score null (not_calibrated)
    statement_coverage       92.5%  (from 21)
    test_code_line_ratio     0.91  (from 48)
    untested_package_share   0.0%  (from 10)
  history      score null (history_too_short)
  complexity   score null (not_calibrated)
    complex_function_share   20.4%  (from 142)
    long_function_share      3.5%  (from 142)
    ...
  architecture score null (not_implemented)

Findings (37)
  GHC-CPLX-001  internal/analyze/tests/profile.go:58: parseProfile has cognitive complexity 54
  ...
```

That is this repository scanning itself at 005997c, shortened. History is `null` because the repository has fewer than 50 commits.

Exit codes: 0 clean, 1 findings, 2 error, 3 incomplete.

## License

Apache-2.0. See [LICENSE](LICENSE).
