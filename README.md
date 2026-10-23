# GitHealthCheck

A code health analyzer for Git repositories. It looks at tests and coverage, git history, complexity and duplication, and architecture and dependencies. Every number it prints is a measurement or `null` with a reason.

**Status: being rebuilt in Go.** Nothing is released from the new code yet. The plan and its reasons are in [docs/decisions/0001-go-rewrite.md](docs/decisions/0001-go-rewrite.md), and the design is in [docs/architecture.md](docs/architecture.md). Progress is tracked in [#1](https://github.com/Git-Hospital/githealthcheck/issues/1).

The earlier Python version is at tag [`v0.1.0`](https://github.com/Git-Hospital/githealthcheck/tree/v0.1.0). It is not maintained. On Go and Rust repositories it reads no files but still prints scores, so do not rely on its numbers.

## What works today

`githealthcheck scan [--format text|json] [--coverage <file>] [-o <file>] <path>` scans the commit at HEAD. Go is analysed; other languages are counted. Of the four families, **tests** is implemented: the test-to-code line ratio, the share of packages without a test file, and statement coverage read from an existing Go coverage profile (the tool never runs your tests). Its rules are documented in [docs/rules](docs/rules). The other three families are `null` with the reason `not_implemented`, and scores stay `null` (`not_calibrated`) until the calibration corpus exists.

```sh
go test -coverprofile=coverage.out ./...
githealthcheck scan .
```

Exit codes: 0 clean, 1 findings, 2 error, 3 incomplete.

## License

Apache-2.0. See [LICENSE](LICENSE).
