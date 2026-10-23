# 0001: Rebuild GitHealthCheck in Go, to the Helmbulance standard

**Status:** proposed, 23 October 2026. **Decided by:** Kadirhan. **Proposed by:** Kadirhan, with the Git-Hospital review committee.

Kadirhan ticked one option per decision in a working session. The rejected options are kept so the reasoning stays visible.

**Inputs:**
- the Git-Hospital committee review of 22-23 October 2026: eleven independent reviews of the four org tools. Its GitHealthCheck findings are summarised under "Why".
- [`docs/architecture.md`](../architecture.md), which turns these decisions into boundaries for the code.
- Helmbulance's practices as the target: reviewed PRs, fixture pairs, a versioned report, exit codes 0/1/2/3, and a pinned corpus.

## Why

Python GitHealthCheck v0.1.0 cannot be trusted today. The committee checked these findings against the code and by running it:

- **It does not read Go or Rust.** The default include list is `*.py *.js *.ts` (`models/simple_report.py:151-152`), so on a Go repo every analyzer reports "0 files". The tool still prints scores such as Code Quality 6.7/10 and Architecture 8.0/10. These are default constants, not measurements.
- **It reads at most 20 files** (`core/git_parser/repository.py:32`, used at line 85), and the report does not say so.
- **The smell score is inverted twice** (`cli/main.py:222`): a clean repo shows 0.0 and a smelly one about 10.
- **`test_success_rate` is at least 0.5 for every repo** (`core/analyzers/test_analyzer.py:434`), although no test is ever run.
- **The output is broken.**
  - The JSON holds Python `repr()` strings.
  - `--format yaml` crashes.
  - The README's CI example raises `AttributeError`.
- **The README claims are false.** It claims "100% test coverage"; measured line coverage is 66%, and the `rha` entry point has 0%.

The committee split on keeping the tool: one member said drop it, another said refocus or move it out. Kadirhan chose to keep the tool and its purpose, and to rebuild it so that every number it prints is a measurement.

## Decisions

### Product

#### D1. What is GitHealthCheck?
- [ ] An offline repository rule checker (a Repolinter successor).
- [x] A code health analyzer: tests, history, complexity and architecture of the code itself.
- [ ] Both: rules first, code metrics later.
- [ ] Only an internal gate for the Git-Hospital repos.

#### D2. How are results given?
- [ ] Findings and a verdict, no score.
- [x] Findings, with 0-10 scores next to them. A score is computed only from what was measured. A metric that could not be measured is `null` with a reason, never 0 or 10. *(This "measured or null" rule is not optional. It is the fix for every defect listed under "Why".)*
- [ ] Only a 0-10 score, as today.

#### D3. Language of the implementation
- [x] Rewrite in Go. It matches MeshMedic and Helmbulance: one static binary, `go install`, and the same CI and release pipeline.
- [ ] Stay in Python and clean up.
- [ ] Rust.

#### D4. Name
- [x] The product stays **GitHealthCheck**.
- [ ] `checkup`.
- [ ] `triage`.

#### D5. Repository name and module path
- [x] Rename the repository to `Git-Hospital/githealthcheck`. The module is `github.com/Git-Hospital/githealthcheck` and the binary is `githealthcheck`. GitHub redirects the old URL.
- [ ] Keep `GitHealthCheck-CLI` as the module path.

#### D6. The Python code and versioning
- [x] Same repository. The Python code is deleted in one PR, and its last state stays reachable at tag `v0.1.0`. The Go code starts at **v0.2.0**, and v1.0.0 is tagged only when the exit criteria at the end of this record are met. The conflicting `1.0.0` in `pyproject.toml` goes away with the Python code.
- [ ] Go as v2.0.0.
- [ ] Keep the Python code in `legacy/` for a while.
- [ ] A new repository, with the old one archived.

#### D7. License
- [x] Move to Apache-2.0, like the rest of the org. Every existing commit is Kadirhan's: the 21 commits by "Demo User <demo@example.com>" came from a misconfigured local git identity. Add a `.mailmap` entry so history attributes them correctly.
- [ ] Keep MIT.

### Analysis engine

#### D8. How code in different languages is read
- [x] **Hybrid.**
  - Language-independent facts are computed in pure Go: the file tree, test-file detection, git history, the import graph and tokens.
  - Go code is read with the standard `go/parser` and `go/ast`.
  - Python and JS/TS use specialist tools when they are installed. When a tool is missing, the dependent metric is `null` with the reason "tool not found".
- [ ] tree-sitter for every language. It needs cgo, which complicates cross-compiling and the single-binary release.
- [ ] Only aggregate external tools.
- [ ] Our own regex and line heuristics (the current approach, and the source of the defects).

#### D9. Languages fully supported in v1
- [x] Go, Python, JavaScript/TypeScript. Each gets defect/clean fixture repositories. Every other language is listed in the report as "present, not analysed", with its file count.
- [ ] Go only.
- [ ] Go and Python.
- [ ] Six or more languages from the start.

#### D10. How a new metric or language is added
- [x] A compiled-in registry.
  - Each analyzer implements a Go interface and registers itself in `init()`.
  - Each language implements `LanguageSupport`.
  - A new analyzer is one package plus its fixture pair.
- [ ] External plugins (exec plus JSON).
- [ ] A compiled core now, external plugins in v2.

#### D11. Metric families in v1
- [x] Tests and coverage.
- [x] Git history.
- [x] Complexity and code smells, including duplication (D17).
- [x] Architecture and dependencies.

Documentation analysis is out of scope for v1. The old README/docstring heuristics are dropped, not ported.

#### D12. Test coverage
- [x] **Read an existing coverage report; never run the repository's tests.**
  - Supported formats: Go coverprofile, Cobertura XML (coverage.py), LCOV (Istanbul/c8) and coverage.py JSON (`coverage.json`). A stale report is not a measurement (architecture §5).
  - With no report, coverage is `null` ("no coverage report found").
  - The test-to-code ratio is reported separately, under its own name. It never stands in for coverage.
- [ ] An optional `--run-tests`.
- [ ] Only an estimated ratio.

#### D13. Complexity
- [x] **Both measures.**
  - Findings come from **cognitive complexity**, as defined in the SonarSource white paper (G. Ann Campbell).
  - The report also gives **cyclomatic complexity** (McCabe), so the numbers can be compared with gocyclo, radon and ESLint.
- [ ] Only cyclomatic.
- [ ] Only cognitive.

#### D14. Bus factor
- [x] **The published degree-of-authorship (DOA) truck-factor algorithm** of Avelino et al., ICPC 2016, computed from `git log --no-merges -M --name-status`: DOA needs first authorship and rename tracking, which line counts cannot give. `--numstat` is used separately for churn.
  - `.mailmap` merges one person's addresses.
  - Bots are excluded by a documented list.
- [ ] Line ownership from `git blame`.
- [ ] Count of committers in the last year.

#### D15. Git access
- [x] Call the `git` binary through a small exec wrapper with a timeout and cancellation, the same pattern as Helmbulance's `internal/execx`. A shallow clone is detected and reported. History metrics are `null` when the history is too short to judge.
- [ ] The go-git library.

#### D16. Dependency vulnerabilities
- [x] **Offline by default.**
  - No network request is ever made unless `--online` is given.
  - Without it, the vulnerability metric is `null` ("offline").
  - With it, the tool runs govulncheck, pip-audit or `npm audit`, or queries OSV. `--online` never implies `--allow-exec`: any path that could build a package or fetch a toolchain is closed off (architecture §3, `architecture/`).
- [ ] Online by default.
- [ ] Download the OSV database and query it locally.

#### D17. Duplication
- [x] **Token-based.** Each file is lexed into tokens, with identifiers and literals normalised. Repeated runs of 50 or more tokens are found with rolling hashes, the PMD CPD method. One engine serves all three languages, each with its own small lexer.
- [ ] Line matching.
- [ ] Later.

### Scoring

#### D18. What "7/10" means
- [x] **Both kinds of threshold.**
  - **Findings use documented literature thresholds**, so they are explainable. Example: cognitive complexity above 15 in one function is a finding.
  - **Family scores are percentiles against a reference corpus**, so they are comparable. 7/10 means "at about the 70th percentile of well-known projects in this language".
- [ ] Only the corpus.
- [ ] Only literature thresholds.

#### D19. Calibration corpus
- [x] **45 repositories: 15 each for Go, Python and JS/TS,** in a mix of small, medium and large projects.
  - The list, each commit SHA and the derived thresholds are locked in `calibration/corpus.lock`.
  - `githealthcheck calibrate` reproduces the lock from scratch.
  - A changed lock is a reviewed PR with the before/after distribution.
- [ ] 15 repositories.
- [ ] 90 or more.

#### D20. The overall score
- [x] **Each family has its own 0-10 score.**
  - The overall score is the weighted mean of the **measured** families only, printed with its coverage, for example "overall 7.4 (3 of 4 families measured)".
  - The weights are documented and versioned with the schema, and users cannot change them, so scores stay comparable.
- [ ] Family scores only.
- [ ] Weights set by the user.

#### D21. Repository configuration
- [x] **`.githealthcheck.yml`.**
  - Users can exclude paths (vendored or generated code) and tune finding thresholds.
  - Suppressing a finding requires a `reason`.
  - A suppressed finding stays in the report, marked as suppressed, and does not count against the score.
  - Configuration never changes the calibration or the weights. Tuned thresholds change findings only. Family statistics always use the calibration thresholds, so a repository cannot raise its own score through its config.
- [ ] No configuration.
- [ ] Free-form configuration.

### Safety and performance

#### D22. Code inside the scanned repository
- [x] **Never executed by default.**
  - Tools that execute repository code or configuration are skipped, and the report gives the reason. Examples: ESLint loading the repository's `eslint.config.js`, and npm scripts.
  - `--allow-exec` turns them on, for your own repository or inside the container image.
  - Where a tool can run without the repository's config, that mode is preferred. Example: ESLint with `--no-config-lookup` and our own rule set.
- [ ] Only inside a container.
- [ ] Always run.

#### D23. Large repositories
- [x] **No file cap.**
  - Files are processed in parallel, one worker per CPU.
  - Elapsed time and files analysed/total are always reported.
  - Targets: a medium repository under 10 s, and a Kubernetes-sized one under 3 min on a CI runner.
  - If `--timeout` expires, the run exits 3 (incomplete) and lists what was not measured.
- [ ] A cap that the report states.

### Output and integration

#### D24. Output formats
- [x] JSON with a `schemaVersion` and a published JSON Schema.
- [x] SARIF 2.1.0.
- [x] Markdown.
- [x] A coloured terminal table for people. It is the default and is plain when not attached to a TTY.

No file is written unless `-o` asks for one. Today's silent `health_report.json` in the working directory goes away.

#### D25. CLI and exit codes
- [x] **The Helmbulance contract.**
  - Commands: `githealthcheck scan <path>`, `explain <rule-id>`, `calibrate`, `version`.
  - Exit codes:
    - 0: clean.
    - 1: findings, or the score is below `--fail-under`.
    - 2: error.
    - 3: incomplete, meaning a family could not finish.
  - Rule IDs are stable, for example `GHC-CPLX-001`. Each one is documented in `docs/rules/` and printed by `explain`.
- [ ] A score threshold only.
- [ ] Always exit 0.

#### D26. Integrations in v1
- [x] A GitHub Action (`action.yml`) that uploads SARIF and posts the Markdown summary.
- [x] A container image on `ghcr.io/git-hospital/githealthcheck`, shipping ruff, radon, ESLint with typescript-eslint, and govulncheck. In the image, nothing is skipped because a tool is missing.
- [x] A pre-commit hook.
- [x] A README badge. The Action produces it, and it shows the overall score and the measured-families count.

#### D27. Changed-code-only mode for PRs
- [ ] `--diff-base` in v1.
- [x] Deferred to v2.

### Process

#### D28. How work starts
- [x] This decision record and `docs/architecture.md` first, checked by an independent reviewer and approved by Kadirhan, then code.
- [ ] Code first.

#### D29. Review
- [x] Every change goes through a PR, with an automated review (`/code-review`) and CI evidence. Kadirhan merges. The ruleset requires a PR but 0 approvals while the project has one maintainer, and moves to 1 required approval when a second maintainer joins.
- [ ] Ege as required reviewer from the start.
- [ ] Both, staged.

> **Known gap:** until a second person reviews, criterion C1 below ("reviewed by another person") is only partly met.

#### D30. Who builds it
- [x] Kadirhan with at most three AI agents at a time. The unit of parallel work is a package. Writers are Sonnet, and a skeptic reviewer (Opus) runs before each PR.
- [ ] One person, no agents.
- [ ] Kadirhan writes, the agent reviews.

#### D31. Scope of v0.2.0
- [x] **A vertical slice.**
  - **In v0.2.0:** Go only, all four families end to end, JSON and terminal output, and fixture pairs. The slice must be verified on the four Git-Hospital repositories, with results checked by hand.
  - **Later:** Python and JS/TS, SARIF and Markdown, the Action, the image and calibration arrive in v0.3 to v0.5.
- [ ] Horizontal: three languages, two families.
- [ ] Everything at once as v1.0.

## Exit criteria for v1.0.0

The criteria are the committee's "Helmbulance level" rubric (C1-C9), made concrete for this tool. v1.0.0 is tagged only when all of them are met.

| | Criterion | Evidence required |
|---|---|---|
| C1 | **Change process** | Every change since this record went through a PR with a linked issue and a review. A second human reviewer is in place (see D29). |
| C2 | **Behaviour verified for real** | Every PR states what it was run against, and what it does not cover. |
| C3 | **Fixture pairs in CI** | Every rule ID has a defect fixture and a clean fixture. CI asserts the exact finding IDs and the exit code, and fails on any rule ID without a pair (a coverage lock). |
| C4 | **Versioned report** | The JSON Schema is published, CI validates every fixture's output against it, and `schemaVersion` changes are documented. |
| C5 | **Exit codes** | 0/1/2/3 are tested. INCOMPLETE is never reported as PASS. |
| C6 | **Real-world corpus** | The 45-repository calibration corpus is locked and reproducible. A hand-checked sample of at least 30 findings from it has its precision published, including the false positives. |
| C7 | **External validation** | At least three findings were confirmed by the maintainers of corpus projects, or their PRs were accepted. These are reported only where the maintainers want them. |
| C8 | **Honesty mechanics** | Every `null` has a reason. The README claims only what a test proves, and each claim links to its test (`CLAIMS.md`). |
| C9 | **Release** | GoReleaser binaries, a signed container image, a CHANGELOG, and `go install github.com/Git-Hospital/githealthcheck/cmd/githealthcheck@latest` working. |
