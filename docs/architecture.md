# GitHealthCheck architecture

Status: proposed for the Go rewrite (v0.2.0 onward). Decisions are recorded in [`decisions/0001-go-rewrite.md`](decisions/0001-go-rewrite.md) and cited here as D1-D31. This document sets the boundaries the code is built inside. It changes when the code teaches us something.

## 1. What the system does

Given a path to a git repository:

1. Walk the tree once and classify every file: its language, whether it is a test, whether it is vendored or generated, and whether it is excluded.
2. For each supported language, extract facts once per file: functions with their complexity, imports and tokens.
3. Read the git history once: authorship and churn per file.
4. Run each metric family over those shared facts. Each family produces findings and a family score, or `null` with a reason.
5. Build one report and render it as JSON, SARIF, Markdown or a terminal table. Exit with a code CI can act on.

It never executes code from the scanned repository (D22) and never touches the network (D16) unless told to. Without `-o`, it writes nothing to disk.

### What it measures, and what it does not

It measures how maintainable and well-tested a codebase is, from four families of evidence: tests and coverage, git history, complexity and duplication, and architecture and dependencies.

It is not a linter. It does not report style, and it does not find bugs. It does not judge documentation in v1. A family it could not measure says so, and is left out of the overall score.

## 2. Principles

- **Measured or null.** Every number is computed from input that was actually read. No input means no number: the value is `null`, with a machine-readable reason (`unsupported_language`, `tool_not_found`, `offline`, `no_coverage_report`, `history_too_short`, `exec_not_allowed`, `timeout`, `not_calibrated`, `insufficient_input`, `low_analysed_share`, `insufficient_families`, `stale_coverage_report`, `too_large`). Default constants presented as measurements are a bug class, and the tests target it (§7).
- **Facts once, metrics many.** Files are parsed once into `FileFacts`, and analyzers read facts, not files. This is what makes "no file cap" (D23) affordable.
- **Explainable findings, comparable scores.** A finding cites a documented threshold and a location. A score is a percentile against a pinned corpus (D18, D19).
- **The scanned repository is untrusted input.** Repository code and configuration are never executed by default (D22). This includes git's own configuration in the scanned `.git/config`, which can run commands through `core.fsmonitor`, `diff.external` or textconv drivers.
  - `execx` runs git with these environment variables: `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_OPTIONAL_LOCKS=0` and `GIT_TERMINAL_PROMPT=0`.
  - It passes these options: `-c core.fsmonitor=false -c core.hooksPath=/dev/null -c diff.external= -c core.pager=cat`, plus `--no-ext-diff --no-textconv` on log and diff.
  - git is used only to read history. Commands that refresh the index, such as `status` or work-tree diffs, are never used.
  - Symlinks are never followed: files are checked with lstat, and symlinks are listed in `skipped[]`.
  - Source files over 1 MiB and coverage reports over 50 MiB are skipped with the reason `too_large`.
- **Same contract as the rest of Git-Hospital.** Exit codes 0/1/2/3, a versioned JSON report, and stable rule IDs (D25). Another tool can rely on these.

## 3. Package layout

```
cmd/githealthcheck/        CLI: flag parsing, wiring, exit codes. No logic.
internal/
  execx/                   run external commands: timeout, cancellation, captured stderr, no shell
  repo/                    tree walk, file classification, path safety, .githealthcheck.yml
  config/                  config schema, validation, suppressions (reason required)
  lang/                    LanguageSupport interface + registry
    golang/                go/parser + go/ast
    python/                lexer (tokens, imports) + optional ruff/radon adapter
                           (ruff --isolated; radon from a neutral cwd with explicit paths, so the
                           repository's pyproject/setup.cfg cannot exclude files or change limits)
    jsts/                  lexer (tokens, imports) + optional ESLint adapter
  gitx/                    git log --numstat parsing, .mailmap, bot list, shallow detection
  facts/                   FileFacts, RepoFacts: the shared model analyzers read
  analyze/                 Analyzer interface + registry
    tests/                 test files, test/code ratio, coverage report readers
    history/               DOA truck factor, churn, hotspots (churn x complexity), abandonment
    complexity/            cognitive + cyclomatic, long functions, deep nesting
    duplication/           token-window rolling hash (CPD method)
    architecture/          import cycles, fan-in/fan-out, instability; dependency vulns (--online):
                           pip-audit only as `-r <file> --no-deps --disable-pip` on fully pinned files,
                           otherwise a direct OSV query from the lockfile; govulncheck with GOTOOLCHAIN=local;
                           npm audit with --ignore-scripts. Any path that would build a package needs --allow-exec.
  score/                   thresholds, percentile lookup against calibration, overall score
  calibrate/               corpus fetch at pinned SHAs, distribution, corpus.lock writer
  report/                  Report model + JSON (schema), SARIF, Markdown, terminal renderers
calibration/corpus.lock    45 repos x SHA + derived thresholds (D19)
docs/rules/                one page per rule ID (also served by `explain`)
schema/report.v1.json      published JSON Schema
testdata/pairs/            defect/clean fixture repositories per rule (§7)
```

Dependency direction: `cmd -> analyze, score, report -> facts <- lang, gitx, repo`. Analyzers never import each other. Renderers never compute.

## 4. Core interfaces

These are a sketch. The exact signatures are settled in the first PR.

```go
// lang
type LanguageSupport interface {
    Name() string                         // "go", "python", "jsts"
    Claims(path string) bool              // extension + shebang rules
    IsTest(path string, src []byte) bool
    Facts(ctx context.Context, path string, src []byte) (facts.FileFacts, error)
}

// analyze
type Analyzer interface {
    Family() Family                       // Tests, History, Complexity, Architecture
    Run(ctx context.Context, rf *facts.RepoFacts, env Env) Result
}

type Result struct {
    Findings []Finding
    Metrics  map[string]Measurement       // name -> value or null+reason
}

type Measurement struct {
    Value  *float64
    Reason NullReason                     // empty when Value != nil
    Inputs int                            // how many files/commits it was computed from
}
```

`Env` carries the resolved options: `--online`, `--allow-exec`, the tool lookup, the clock and the timeout. An analyzer that needs a disallowed capability returns `null` with the matching reason. It must not fall back to a guess.

## 5. Scoring

- **Findings.** Each rule has a literature threshold, documented in `docs/rules/<ID>.md` with its source. Examples: cognitive complexity per function > 15; a function longer than 80 logical lines; an import cycle of any length; a duplicated block of 50 or more tokens; a file with DOA owners = 1 inside a hotspot.
- **Family score.** Each family reduces its metrics to one statistic per repository, for example the share of functions over the threshold, or the truck factor relative to the number of active files. The statistic is converted to 0-10 by its percentile in the calibration distribution for that language. Mixed-language repositories weight the per-language results by analysed lines.
- **Overall score.** The weighted mean of the measured families. The weights are fixed per schema version and printed in the report. The report always shows the families measured out of 4.
- **Suppressed findings** (D21) are shown but do not count against the score.

Guards against the "measured or null" principle being bypassed:

- **Minimum input.** Each statistic has a documented minimum input, for example 20 functions or 50 commits. Below it, the value is `null` with the reason `insufficient_input`, decided from `Measurement.Inputs`. Without this guard, a repository with 2 functions would score 10/10.
- **Analysed share.** Each language result prints its share of the repository's code lines. A family score is `null` (`low_analysed_share`) when the analysed lines are under 50%. Without this guard, a repository that is 95% Rust would be scored from its 5% Python.
- **Enough families.** The overall score is `null` (`insufficient_families`) when fewer than 2 families are measured.
- **No calibration, no score.** Until the corpus exists (v0.4.0), every family score and the overall score is `null` with the reason `not_calibrated`. v0.2.0 and v0.3.0 report findings and raw metrics only.
- **Thresholds stay fixed.** Family statistics always use the calibration thresholds. Thresholds tuned in `.githealthcheck.yml` change findings only. Excluded paths are listed with their share of the code lines.
- **Fresh coverage only.** `inputs` names the coverage report that was used. It also records whether the report is tracked by git and how many of the files it lists exist at HEAD. If fewer than 90% of its files match, coverage is `null` with the reason `stale_coverage_report`.

## 6. Report contract

`schemaVersion: 1`. Top level:

- `tool`
- `repository`: path, HEAD SHA, whether the clone is shallow
- `languages`: analysed and present-not-analysed, with file counts
- `families[]`: score or null, reason, metrics, findings
- `overall`: score, measured count, weights
- `inputs`: files analysed/total, commits read, elapsed time
- `skipped[]`: what was not done, and why

Every `null` carries a `reason`. JSON is the source of truth. SARIF, Markdown and the terminal table are pure renderings of it, tested by golden files.

Exit codes (D25):
- **0:** no unsuppressed findings, and the score is not below `--fail-under`.
- **1:** there are unsuppressed findings, or the score is below `--fail-under`.
- **2:** usage or runtime error.
- **3:** incomplete. This covers three cases:
  - a family timed out, or a tool crashed;
  - no family was measured at all;
  - `--fail-under` is set but the overall score is `null`.

Precedence is 2 > 3 > 1 > 0. Some nulls only mean that an input was expected to be absent: `no_coverage_report`, `offline`, `tool_not_found`, `exec_not_allowed`, `unsupported_language`, `history_too_short`, `not_calibrated` and `insufficient_input`. These do not cause exit 3 on their own.

## 7. Testing

- **Fixture pairs.** Every rule ID has `testdata/pairs/<ID>/defect` and `.../clean`, two small git repositories built by a script, so the history is deterministic. CI asserts:
  - the defect fixture yields exactly that ID;
  - the clean fixture yields none of it;
  - the exit code of each.

  A coverage lock fails CI when a rule ID has no pair.
- **Null tests.** For every family and language there is a fixture where the input is absent: no coverage report, a shallow clone, the tool not installed, offline. CI asserts `null` and the exact reason. This is the regression test for the Python version's main defect.
- **Golden reports.** These run on the four Git-Hospital repositories at pinned SHAs. A change in the output is a reviewed diff.
- **Calibration reproducibility.** A weekly CI job re-derives `corpus.lock` and fails on drift.
- **Schema.** Every fixture's JSON is validated against `schema/report.v1.json`.

## 8. Milestones

| Version | Contents | Done when |
|---|---|---|
| v0.2.0 | Go only; all four families; JSON and terminal output; fixture pairs; offline only. Findings and raw metrics only: scores are `null` (`not_calibrated`) | Run on the four Git-Hospital repos, with the results checked by hand and written up in the release notes |
| v0.3.0 | Python and JS/TS (lexers + optional tool adapters), `--allow-exec` | Fixture pairs per language; null tests for missing tools |
| v0.4.0 | Calibration corpus (45 repos), percentile scores, `calibrate` | `corpus.lock` reproduced in CI |
| v0.5.0 | SARIF, Markdown, GitHub Action, container image, pre-commit, badge, `--online` | The Action is used in the CI of all four Git-Hospital repositories |
| v1.0.0 | The exit criteria C1-C9 in ADR 0001 | Every row has its evidence linked |
| v2 | `--diff-base`, external plugins | Not designed yet |

## 9. Open questions, to settle by spikes before the code depends on them

1. **ESLint without the repository's config (D22).** Does `--no-config-lookup` with our own flat config and the typescript-eslint parser give cyclomatic complexity on TS without loading anything from the repository? If not, JS/TS complexity comes from our own lexer-level approximation, and is labelled as such.
2. **Python cognitive complexity.** radon gives cyclomatic complexity, and ruff gives McCabe complexity. Neither gives cognitive complexity (from memory, to verify). The options are a small indentation-aware Python lexer that is enough for the cognitive rules, or reporting cognitive complexity for Python as `null` in v0.3.
3. **The DOA constants** (Avelino et al.) and the threshold for "author" must be taken from the paper, not from memory. Before `history/` is merged, they are checked against the paper and the paper's own example results.
4. **Performance target (D23).** Measure on kubernetes/kubernetes at a pinned SHA on a GitHub runner. The 3-minute figure is a goal, not a measurement.
5. **Size of the lexer work.** A JS/TS lexer has to handle regex versus division, nested template literals, JSX and TS generics. Mitigations:
   - In v0.3, JS/TS imports resolve relative specifiers only. Every other import is listed as unresolved.
   - For Go, gocognit and gocyclo serve as reference oracles in the tests. Their licences are to be checked before use.
6. **Calibration drift check.** `corpus.lock` records the githealthcheck version and the version of each external tool. The weekly drift check runs only when those versions are unchanged. It uses shallow clones wherever no history metric is calibrated.
7. **`safe.directory` in the container and the Action.** The repository owner's uid differs from the uid the tool runs as, so git refuses to read the repository. The tool must handle this without trusting every directory globally.
8. **Unverified, from memory.** These claims come from memory and need checking against the tools' documentation:
   - whether `git log` applies textconv by default;
   - whether pip-audit builds sdists for unpinned input;
   - whether govulncheck obeys the `toolchain` line in go.mod.

   The mitigations above are applied regardless of the answers.
