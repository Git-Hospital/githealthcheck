# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Until 1.0.0 the report schema and the rule set may change between minor versions; the report carries `schemaVersion`.

## [0.2.0] - unreleased

The first release of the Go rewrite (ADR 0001). Go is analysed; other languages are counted. Three of the four families are implemented; architecture is `null` with the reason `not_implemented`, and scores stay `null` (`not_calibrated`) until the calibration corpus exists.

### Added

- `githealthcheck scan [--format text|json] [--coverage <file>] [-o <file>] [--timeout <d>] <path>` scans the commit at HEAD, offline, without running the repository's code. Exit codes: 0 clean, 1 findings, 2 error, 3 incomplete.
- **tests**: test-to-code line ratio, packages without tests (GHC-TEST-001), package coverage under 60% (GHC-TEST-002) and files no test runs (GHC-TEST-003), from an existing Go coverage profile.
- **history**: the truck factor of Avelino et al. (ICPC 2016), equal to the authors' tool on six repositories (GHC-HIST-001), and files whose authors have all stopped committing (GHC-HIST-002).
- **complexity**:
  - cognitive complexity over 15 (GHC-CPLX-001), functions over 90 lines of code (GHC-CPLX-002) and nesting over 4 (GHC-CPLX-003); the counts equal gocognit's and gocyclo's on eight repositories;
  - the smells of generated code: errors checked and then dropped (GHC-CPLX-004), imports the module does not provide (GHC-CPLX-005), functions nothing references (GHC-CPLX-006), commented-out code (GHC-CPLX-007) and placeholder bodies (GHC-CPLX-008);
  - duplication: blocks of 100 tokens or more repeated with renamed locals (GHC-CPLX-009) and functions with the signature and body of another (GHC-CPLX-010), with the metric `duplicated_line_share`.
- Every rule has a page under `docs/rules/` with its threshold and source, and a defect/clean fixture pair.
- The scan treats the repository as hostile: git runs with its configuration neutralised and an allowlist of commands, files are read through `os.Root` without following symlinks, and every input has a size bound.

### Removed

- The Python version (0.1.0). It read no Go or Rust files yet printed scores.

[0.2.0]: https://github.com/Git-Hospital/githealthcheck/compare/v0.1.0...v0.2.0
