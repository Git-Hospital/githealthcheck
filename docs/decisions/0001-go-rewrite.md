# 0001: Rebuild GitHealthCheck in Go, to the Helmbulance standard

**Status:** accepted, 23 October 2026; v0.2.0 (24 October 2026) implements it for Go. **Decided by:** the maintainer.

Each decision gives the option chosen, then the options considered and rejected, so the reasoning stays visible.

**Inputs:**
- an internal review of the four Git-Hospital tools (22-23 October 2026). Its GitHealthCheck findings are summarised under "Why".
- [`docs/architecture.md`](../architecture.md), which turns these decisions into boundaries for the code.
- Helmbulance's practices as the target: reviewed PRs, fixture pairs, a versioned report, exit codes 0/1/2/3, and a pinned corpus.

## Why

Python GitHealthCheck v0.1.0 could not be trusted. These findings were checked against its code and by running it:

- **It does not read Go or Rust.** The default include list is `*.py *.js *.ts` (`models/simple_report.py:151-152`), so on a Go repo every analyzer reports "0 files". The tool still prints scores such as Code Quality 6.7/10 and Architecture 8.0/10. These are default constants, not measurements.
- **It reads at most 20 files** (`core/git_parser/repository.py:32`, used at line 85), and the report does not say so.
- **The smell score is inverted twice** (`cli/main.py:222`): a clean repo shows 0.0 and a smelly one about 10.
- **`test_success_rate` is at least 0.5 for every repo** (`core/analyzers/test_analyzer.py:434`), although no test is ever run.
- **The output is broken.**
  - The JSON holds Python `repr()` strings.
  - `--format yaml` crashes.
  - The README's CI example raises `AttributeError`.
- **The README claims are false.** It claims "100% test coverage"; measured line coverage is 66%, and the `rha` entry point has 0%.

Dropping the tool and narrowing it were both considered. The decision was to keep the tool and its purpose, and to rebuild it so that every number it prints is a measurement.

## Decisions

### Product

#### D1. What is GitHealthCheck?
A code health analyzer: tests, history, complexity and architecture of the code itself.

Considered and rejected:

- An offline repository rule checker (a Repolinter successor).
- Both: rules first, code metrics later.
- Only an internal gate for the Git-Hospital repos.

#### D2. How are results given?
Findings, with 0-10 scores next to them. A score is computed only from what was measured. A metric that could not be measured is `null` with a reason, never 0 or 10. *(This "measured or null" rule is not optional. It is the fix for every defect listed under "Why".)*

Considered and rejected:

- Findings and a verdict, no score.
- Only a 0-10 score, as today.

#### D3. Language of the implementation
Rewrite in Go. It matches MeshMedic and Helmbulance: one static binary, `go install`, and the same CI and release pipeline.

Considered and rejected:

- Stay in Python and clean up.
- Rust.

#### D4. Name
The product stays **GitHealthCheck**.

Considered and rejected:

- `checkup`.
- `triage`.

#### D5. Repository name and module path
Rename the repository to `Git-Hospital/githealthcheck`. The module is `github.com/Git-Hospital/githealthcheck` and the binary is `githealthcheck`. GitHub redirects the old URL.

Considered and rejected:

- Keep `GitHealthCheck-CLI` as the module path.

#### D6. The Python code and versioning
Same repository. The Python code is deleted in one PR, and its last state stays reachable at tag `v0.1.0`. The Go code starts at **v0.2.0**, and v1.0.0 is tagged only when the exit criteria at the end of this record are met. The conflicting `1.0.0` in `pyproject.toml` goes away with the Python code.

Considered and rejected:

- Go as v2.0.0.
- Keep the Python code in `legacy/` for a while.
- A new repository, with the old one archived.

#### D7. License
Move to Apache-2.0, like the rest of the org. Every existing commit is the maintainer's: the 21 commits under a placeholder identity came from a misconfigured local git setup. Add a `.mailmap` entry so history attributes them correctly.

Considered and rejected:

- Keep MIT.

### Analysis engine

#### D8. How code in different languages is read
**Hybrid.**
- Language-independent facts are computed in pure Go: the file tree, test-file detection, git history, the import graph and tokens.
- Go code is read with the standard `go/parser` and `go/ast`.
- Python and JS/TS use specialist tools when they are installed. When a tool is missing, the dependent metric is `null` with the reason "tool not found".

Considered and rejected:

- tree-sitter for every language. It needs cgo, which complicates cross-compiling and the single-binary release.
- Only aggregate external tools.
- Our own regex and line heuristics (the current approach, and the source of the defects).

#### D9. Languages fully supported in v1
Go, Python, JavaScript/TypeScript. Each gets defect/clean fixture repositories. Every other language is listed in the report as "present, not analysed", with its file count.

Considered and rejected:

- Go only.
- Go and Python.
- Six or more languages from the start.

#### D10. How a new metric or language is added
A compiled-in registry.
- Each analyzer implements a Go interface and registers itself in `init()`.
- Each language implements `LanguageSupport`.
- A new analyzer is one package plus its fixture pair.

Considered and rejected:

- External plugins (exec plus JSON).
- A compiled core now, external plugins in v2.

#### D11. Metric families in v1
- Tests and coverage.
- Git history.
- Complexity and code smells, including duplication (D17).
- Architecture and dependencies.

Documentation analysis is out of scope for v1. The old README/docstring heuristics are dropped, not ported.

#### D12. Test coverage
**Read an existing coverage report; never run the repository's tests.**
- Supported formats: Go coverprofile, Cobertura XML (coverage.py), LCOV (Istanbul/c8) and coverage.py JSON (`coverage.json`). A stale report is not a measurement (architecture §5).
- With no report, coverage is `null` ("no coverage report found").
- The test-to-code ratio is reported separately, under its own name. It never stands in for coverage.

Considered and rejected:

- An optional `--run-tests`.
- Only an estimated ratio.

#### D13. Complexity
**Both measures.**
- Findings come from **cognitive complexity**, as defined in the SonarSource white paper (G. Ann Campbell).
- The report also gives **cyclomatic complexity** (McCabe), so the numbers can be compared with gocyclo, radon and ESLint.

Considered and rejected:

- Only cyclomatic.
- Only cognitive.

#### D14. Bus factor
**The published degree-of-authorship (DOA) truck-factor algorithm** of Avelino et al., ICPC 2016, computed from `git log --no-merges -M --name-status`: DOA needs first authorship and rename tracking, which line counts cannot give. `--numstat` is used separately for churn.
- `.mailmap` merges one person's addresses.
- Bots are excluded by a documented list.

Considered and rejected:

- Line ownership from `git blame`.
- Count of committers in the last year.

#### D15. Git access
Call the `git` binary through a small exec wrapper with a timeout and cancellation, the same pattern as Helmbulance's `internal/execx`. A shallow clone is detected and reported. History metrics are `null` when the history is too short to judge.

Considered and rejected:

- The go-git library.

#### D16. Dependency vulnerabilities
**Offline by default.**
- No network request is ever made unless `--online` is given.
- Without it, the vulnerability metric is `null` ("offline").
- With it, the tool runs govulncheck, pip-audit or `npm audit`, or queries OSV. `--online` never implies `--allow-exec`: any path that could build a package or fetch a toolchain is closed off (architecture §3, `architecture/`).

Considered and rejected:

- Online by default.
- Download the OSV database and query it locally.

#### D17. Duplication
**Token-based.** Each file is lexed into tokens, with identifiers and literals normalised. Repeated runs of 100 or more tokens (D34) are found with rolling hashes, the PMD CPD method. One engine serves all three languages, each with its own small lexer.

Considered and rejected:

- Line matching.
- Later.

*Amended 2026-10-24 (D34):* literals are masked, but of the identifiers only a function's own
parameters and locals may be renamed, consistently (Baker's parameterised match); other names must be
equal. Masking every identifier made a getter look like every other getter. Blocks start at a line, and
test files are left out, as in SonarSource. Whole functions with one body are their own rule
(GHC-CPLX-010).

### Scoring

#### D18. What "7/10" means
**Both kinds of threshold.**
- **Findings use documented literature thresholds**, so they are explainable. Example: cognitive complexity above 15 in one function is a finding.
- **Family scores are percentiles against a reference corpus**, so they are comparable. 7/10 means "at about the 70th percentile of well-known projects in this language".

Considered and rejected:

- Only the corpus.
- Only literature thresholds.

#### D19. Calibration corpus
**45 repositories: 15 each for Go, Python and JS/TS,** in a mix of small, medium and large projects.
- The list, each commit SHA and the derived thresholds are locked in `calibration/corpus.lock`.
- `githealthcheck calibrate` reproduces the lock from scratch.
- A changed lock is a reviewed PR with the before/after distribution.

Considered and rejected:

- 15 repositories.
- 90 or more.

#### D20. The overall score
**Each family has its own 0-10 score.**
- The overall score is the weighted mean of the **measured** families only, printed with its coverage, for example "overall 7.4 (3 of 4 families measured)".
- The weights are documented and versioned with the schema, and users cannot change them, so scores stay comparable.

Considered and rejected:

- Family scores only.
- Weights set by the user.

#### D21. Repository configuration
**`.githealthcheck.yml`.**
- Users can exclude paths (vendored or generated code) and tune finding thresholds.
- Suppressing a finding requires a `reason`.
- A suppressed finding stays in the report, marked as suppressed, and does not count against the score.
- Configuration never changes the calibration or the weights. Tuned thresholds change findings only. Family statistics always use the calibration thresholds, so a repository cannot raise its own score through its config.

Considered and rejected:

- No configuration.
- Free-form configuration.

### Safety and performance

#### D22. Code inside the scanned repository
**Never executed by default.**
- Tools that execute repository code or configuration are skipped, and the report gives the reason. Examples: ESLint loading the repository's `eslint.config.js`, and npm scripts.
- `--allow-exec` turns them on, for your own repository or inside the container image.
- Where a tool can run without the repository's config, that mode is preferred. Example: ESLint with `--no-config-lookup` and our own rule set.

Considered and rejected:

- Only inside a container.
- Always run.

#### D23. Large repositories
**No file cap.**
- Files are processed in parallel, one worker per CPU.
- Elapsed time and files analysed/total are always reported.
- Targets: a medium repository under 10 s, and a Kubernetes-sized one under 3 min on a CI runner.
- If `--timeout` expires, the run exits 3 (incomplete) and lists what was not measured.

Considered and rejected:

- A cap that the report states.

### Output and integration

#### D24. Output formats
- JSON with a `schemaVersion` and a published JSON Schema.
- SARIF 2.1.0.
- Markdown.
- A coloured terminal table for people. It is the default and is plain when not attached to a TTY.

No file is written unless `-o` asks for one. Today's silent `health_report.json` in the working directory goes away.

#### D25. CLI and exit codes
**The Helmbulance contract.**
- Commands: `githealthcheck scan <path>`, `explain <rule-id>`, `calibrate`, `version`.
- Exit codes:
  - 0: clean.
  - 1: findings, or the score is below `--fail-under`.
  - 2: error.
  - 3: incomplete, meaning a family could not finish.
- Rule IDs are stable, for example `GHC-CPLX-001`. Each one is documented in `docs/rules/` and printed by `explain`.

Considered and rejected:

- A score threshold only.
- Always exit 0.

#### D26. Integrations in v1
- A GitHub Action (`action.yml`) that uploads SARIF and posts the Markdown summary.
- A container image on `ghcr.io/git-hospital/githealthcheck`, shipping ruff, radon, ESLint with typescript-eslint, and govulncheck. In the image, nothing is skipped because a tool is missing.
- A pre-commit hook.
- A README badge. The Action produces it, and it shows the overall score and the measured-families count.

#### D27. Changed-code-only mode for PRs
Deferred to v2.

Considered and rejected:

- `--diff-base` in v1.

### Process

#### D28. How work starts
This decision record and `docs/architecture.md` first, checked by an independent reviewer and approved by the maintainer, then code.

Considered and rejected:

- Code first.

#### D29. Review
Every change goes through a PR, with an automated review and CI evidence. The maintainer merges. The ruleset requires a PR but 0 approvals while the project has one maintainer, and moves to 1 required approval when a second maintainer joins.

Considered and rejected:

- A second maintainer as required reviewer from the start.
- Both, staged.

> **Known gap:** until a second person reviews, criterion C1 below ("reviewed by another person") is only partly met.

*Amended 2026-10-24:* until the tool meets the exit criteria below, a PR whose CI is green
and whose review findings are fixed is merged without waiting for the maintainer.

#### D30. Who builds it
**One AI-assisted author, agents only where they fit.**
- The code is written by one AI-assisted coding session at a time. No parallel agent writes production code.
- Agents do only work suited to agents: independent reviews, documentation lookups and mechanical checks. At most three run at a time.
- Every review checks security explicitly: untrusted repository input, exec paths, git config, network use and secrets in output.
- Agents get read-only access to the work tree. They are never given tokens and never act on GitHub.

Considered and rejected:

- Up to three AI agents writing packages in parallel, with an adversarial review before each PR.
- One person, no agents.
- A person writes, an agent reviews.

#### D31. Scope of v0.2.0
**A vertical slice.**
- **In v0.2.0:** Go only, all four families end to end, JSON and terminal output, and fixture pairs. The slice must be verified on the four Git-Hospital repositories, with results checked by hand.
- **Later:** Python and JS/TS, SARIF and Markdown, the Action, the image and calibration arrive in v0.3 to v0.5.

Considered and rejected:

- Horizontal: three languages, two families.
- Everything at once as v1.0.

*Amended 2026-10-24:* v0.2.0 is released once tests, history and complexity (with
duplication and the D32 smells) work on Go; the architecture family follows in a later release.

#### D32. Smells of generated code (2026-10-24)
Code written by language models has failure patterns of its own. The complexity family also reports:
- **Swallowed errors**: an error that is caught or returned and then dropped, such as an empty
  `except`/`catch`, `except Exception: pass`, or a Go error assigned to `_` or never checked.
- **Imports that do not resolve**: an import that is neither in the repository nor declared in
  its dependency files (`go.mod`, requirements, `package.json`). A model can invent a package name,
  and an attacker can register it. Checked offline.
- **Dead code**: functions nothing calls, blocks of commented-out code, and placeholder bodies
  (`pass`, `panic("not implemented")`, `throw new Error("not implemented")`).
- **Near-identical functions**: functions with different names and the same body after
  normalisation, reported per function from the D17 duplication engine.

#### D33. Language order (2026-10-24)
Each rule lands on Go first (read exactly with `go/ast`), then Python, then JS/TS.

Considered and rejected:

- Python first.
- All three at once.

#### D34. Complexity thresholds (2026-10-24)
- **Cognitive complexity as SonarSource counts it**, over 15 (S3776). An error check without an
  `else` adds nothing, as in sonar-go; the paper's count is kept for the gocognit oracle.
- **Long function: over 90 lines of code**, halfway between funlen's 60 and go:S138's 120.
  - Not chosen: 60 (funlen).
  - Not chosen: 120 (go:S138).
- **Deep nesting: over 4** (go:S134).
- **Duplication: at least 100 tokens over at least 10 lines**, SonarSource's rule for a
  duplicated block. It replaces the unsourced 50 tokens of D17.

*Amended 2026-10-24 (D32, Go):* `if err != nil {}` and `if err != nil { return nil }` are
reported; an error assigned to `_` is not, since telling an error result from any other needs types.

## Exit criteria for v1.0.0

The criteria are the Git-Hospital release rubric (C1-C9), the bar Helmbulance set, made concrete for this tool. v1.0.0 is tagged only when all of them are met.

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
