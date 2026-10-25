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

`githealthcheck scan [--format text|json|sarif|markdown] [--coverage <file>] [-o <file>] [--timeout <d>] <path>` scans the commit at HEAD. Go is analysed by every family, and Python by tests and complexity; other languages are counted. Python needs python3 3.10 or later on `PATH`, whose `ast` module parses the code (it is never run); without one, Python is reported as not analysed, with the reason. Of the four families, three are implemented:

- **tests**: the test-to-code line ratio, the share of Go packages without a test file, and statement coverage read from an existing Go coverage profile or coverage.py XML report (the tool never runs your tests).
- **history**: the truck factor of Avelino et al. (ICPC 2016), checked against the authors' own tool, the share of files with a single author, and the share of files whose authors have all stopped committing. It works on every recognised language and needs the full history (not a shallow clone) and at least 50 commits.
- **complexity**: per function, cognitive complexity (SonarSource's count, checked against gocognit for Go and SonarSource's own test file for Python), cyclomatic complexity, length and nesting depth; and the patterns generated code leaves behind: errors checked and then dropped, imports of packages the module does not provide, functions nothing references, commented-out code and placeholder bodies; and duplication: blocks of 100 tokens or more repeated with renamed locals, and functions with the body of another.

Their rules are documented in [docs/rules](docs/rules). Architecture is `null` with the reason `not_implemented`.

**Scores.** Each metric scores 0 to 10 by its percentile among 15 well-known repositories of the language ([docs/calibration.md](docs/calibration.md)): 7 means about the 70th percentile. Statement coverage scores X% as X/10. A family scores the mean of its metrics, and the overall score the mean of the scored families. Go and Python are calibrated; JS/TS gets `not_calibrated`. A metric below its minimum input (20 functions, 1000 code lines, 5 Go packages or 50 commits) is `null` and not scored.

```console
$ go test -coverprofile=coverage.out ./...
$ githealthcheck scan --coverage coverage.out .
Languages
  go              112 files    13336 lines  analysed
  python           62 files     1497 lines  analysed
  shell             3 files      166 lines  not analysed

Families
  tests        score 7.1/10
    statement_coverage       88.5%  (from 26)  scores 8.8
    test_code_line_ratio     0.73  (from 121)  scores 2.4
    untested_package_share   0.0%  (from 12)  scores 10.0
  history      score null (history_too_short)
  complexity   score 1.5/10
    complex_function_share   16.8%  (from 268)  scores 0.0
    mean_cyclomatic_complexity 7.21  (from 268)  scores 0.1
    ...
  architecture score null (not_implemented)

Findings (67)
  GHC-CPLX-001  internal/analyze/tests/cobertura.go:55: parseCobertura has cognitive complexity 98
  ...

Overall  4.3/10  (2 of 4 families measured)
  scored as go against 15 corpus repositories, 99% of the code analysed
```

That is this repository scanning itself, shortened: its own complexity is far below the corpus. History is `null` because the repository has fewer than 50 commits.

`githealthcheck calibrate [--check]` measures the corpus at its pinned commits and writes, or checks, `calibration/corpus.lock`.

Exit codes: 0 clean, 1 findings, 2 error, 3 incomplete.

`--json`, `--sarif` and `--markdown <file>` also write those renderings of the same scan. SARIF 2.1.0 is for code scanning: findings that are not on one file (a repository's truck factor, a Go package without tests) are counted there but listed only in the JSON and Markdown reports.

## GitHub Action

```yaml
jobs:
  health:
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      security-events: write # to upload the SARIF
      attestations: read # to check the release's provenance
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0 # history needs the full history
          persist-credentials: false
      - uses: Git-Hospital/githealthcheck@v0.3.0
        with:
          fail-under: "5"
```

The Action downloads the release it is pinned to, checks it against the release's checksums and build provenance, scans the checkout, uploads the findings to code scanning and writes the Markdown report to the job summary. Pinned to a commit SHA, it needs `version: v0.3.0` as well. It fails on errors and incomplete runs; `fail-on: findings` also fails on findings or a score under `fail-under`, and `fail-on: never` leaves the decision to the `exit-code`, `verdict` and `score` outputs. A pull request from a fork gets no `security-events: write`; set `upload-sarif: "false"` there. The inputs are in [action.yml](action.yml). Linux and macOS runners.

## License

Apache-2.0. See [LICENSE](LICENSE).
