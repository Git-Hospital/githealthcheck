# GitHealthCheck

A code health analyzer for Git repositories. It looks at tests and coverage, git history, complexity and duplication, and architecture and dependencies. Every number it prints is a measurement or `null` with a reason.

**Status: being rebuilt in Go.** Nothing is released from the new code yet. The plan and its reasons are in [docs/decisions/0001-go-rewrite.md](docs/decisions/0001-go-rewrite.md), and the design is in [docs/architecture.md](docs/architecture.md). Progress is tracked in [#1](https://github.com/Git-Hospital/githealthcheck/issues/1).

The earlier Python version is at tag [`v0.1.0`](https://github.com/Git-Hospital/githealthcheck/tree/v0.1.0). It is not maintained. On Go and Rust repositories it reads no files but still prints scores, so do not rely on its numbers.

## What works today

`githealthcheck scan [--format text|json] [--coverage <file>] [-o <file>] <path>` scans the commit at HEAD. Go is analysed; other languages are counted. Of the four families, three are implemented:

- **tests**: the test-to-code line ratio, the share of packages without a test file, and statement coverage read from an existing Go coverage profile (the tool never runs your tests).
- **history**: the truck factor of Avelino et al. (ICPC 2016), checked against the authors' own tool, the share of files with a single author, and the share of files whose authors have all stopped committing. It works on every recognised language and needs the full history (not a shallow clone) and at least 50 commits.

- **complexity**: per function, cognitive complexity (SonarSource's count, checked against gocognit), cyclomatic complexity, length and nesting depth; and the patterns generated code leaves behind: errors checked and then dropped, imports of packages the module does not provide, functions nothing references, commented-out code and placeholder bodies. Duplication is not implemented yet.

Their rules are documented in [docs/rules](docs/rules). Architecture is `null` with the reason `not_implemented`. Scores stay `null` until the calibration corpus exists: each measured family's score with the reason `not_calibrated`, and the overall score with `not_calibrated` once two families are measured, `insufficient_families` before that.

```sh
go test -coverprofile=coverage.out ./...
githealthcheck scan .
```

Exit codes: 0 clean, 1 findings, 2 error, 3 incomplete.

## License

Apache-2.0. See [LICENSE](LICENSE).
