# 0003: An evidence layer for changes written by agents

**Status:** accepted, 28 October 2026; v0.5.0 to v0.7.0; A6 and A9 amended by [ADR 0004](0004-trust-ledger.md), which settles A6's labelled set as agents' commits, not pull requests. **Decided by:** the maintainer. **Builds on** [ADR 0002](0002-evidence.md): every new capability serves evidence that the engine has measured. **Amends** ADR 0002's E8 (JS/TS moves from v0.5.0 to v0.7.0) and ADR 0001's D25 (`impact`, `verify` and `mcp` join its commands; `verify` has its own rule for exit code 1, A6). `verify` compares a change with its base; it is not the scan's `--diff-base` (D27), which stays in v2.

Each decision gives the option chosen, then the options considered and rejected, as in ADR 0002.

## Why

Coding agents (Claude Code, Cursor, Codex) now write part of the code in many repositories. An agent decides where to work and what to change from what it reads, and a person can no longer read every line it writes. Two questions are left without an independent answer:

- **Before a change:** which files of this repository attract fixes, and what else tends to change when a given file does?
- **After a change:** what did it touch, what did it weaken, and what is still unproven?

Tools already serve agents. CodeScene documents a Code Health MCP server (in beta for opt-in customers, read 28 October 2026). repowise documents an MCP server whose risk tool reports hotspots and a change's "blast radius". We read their documentation and did not run them. We found no statement in it that the tool tells the agent how well its risk or impact estimates did on this repository's past. ADR 0002 adds that for risk, and this record extends it to impact and to changes.

## Decisions

#### A1. What githealthcheck is
**githealthcheck is an evidence layer for changes, most of them written by agents.** It measures; it shows, on the repository's own history, how far each measure can be believed; and it reports what a change touched and what it left unproven. It writes no code and runs none of the repository's code.
- Not chosen: an orchestrator that coordinates many coding agents. That is the agent platforms' own ground, and it needs code execution and network access.
- Not chosen: a "digital twin" that simulates a change's consequences months ahead. Outcomes that far ahead cannot be measured in advance, which breaks "measured or null". Its measurable core, impact, is A4.
- Not chosen: a code-health tool only, without the agent layer. It would sit beside SonarQube and CodeScene, with less to show.

#### A2. The order of releases
| Version | Contents |
|---|---|
| v0.4.0 | Evidence (ADR 0002), unchanged |
| v0.5.0 | Impact from co-change (A4) |
| v0.6.0 | The agent layer: `mcp` (A5) and `verify` (A6) |
| v0.7.0 | JS/TS analysis and its corpus |

Impact reads only git, so it works on JS/TS repositories before their code is parsed; risk evidence does too, for churn and authors.
- Not chosen: JS/TS in v0.5.0, as E8 planned. The agent layer would come two releases later, and its strongest tool would be missing.
- Not chosen: the agent layer before impact. Its first release would lack the answer an agent needs before editing.

#### A3. One binary
**Every capability is a subcommand of `githealthcheck`**: the scan, `impact`, `verify` and `mcp`. They share one release, one report contract and one hardened git layer (`execx`, `gitx`).
- Not chosen: an umbrella `githospital` binary with githealthcheck as its diagnosis module. It means a rename of the repository and module, a new release line and a moved Action, for a name.
- Not chosen: a repository per capability. Go's `internal/` packages cannot be imported from outside, so the shared code would become a public API with its own compatibility to keep.

#### A4. Impact: co-change first, imports second
**`githealthcheck impact <path>` lists the files that changed together with the given file in its history**, with how often.
- `impact` looks the path up in the history already read in memory, and only if it is in HEAD's tree; the path is never passed to git.
- Co-change is read from the same `git log` as the history family, so it works in every language, and it finds links that no parser sees, such as a Go file and the YAML it reads.
- **It is backtested like risk.** At past points in the history, the files it named for a file are compared with the files that actually changed with that file afterwards. The report gives the share it named correctly next to a simple baseline, and its estimates are `null` with `history_too_short` where there is too little history.
- A later release adds the import graph for Go and Python, shown as a separate source next to co-change.
- Not chosen: a call graph first. It is precise inside one language, misses interfaces and reflection, sees nothing outside the code, and is hard to backtest.
- Not chosen: co-change, imports and calls in one release. It would delay the release and triple the review.

#### A5. Agents connect through `githealthcheck mcp`
**`githealthcheck mcp` is an MCP server on standard input and output.** It opens no port.
- **Read-only and offline.** It does not accept `--online`, `--allow-exec` or `-o`. No tool writes a file, runs the repository's code (D22) or opens a connection (D16), and no tool call can change this.
- **One repository.** The repository is fixed when the server starts. A tool call names a path that must be in HEAD's tree (`Repo.Paths`). It is looked up in what was already read and never passed to git or opened by name. A path outside the tree is an error with a fixed text that does not echo the path.
- **One contract.** Each tool returns a part of the JSON report (`schema/report.v1.json`), so the CLI and the agent see the same numbers.
- **The repository's text is untrusted.** Commit subjects, paths and file contents are written by the repository's authors, and an agent may read an instruction in them as its own. They appear only in data fields, escaped as in E3 and D36 without control characters, and never in a tool's name, description or error text. Each tool's description says that its results are data from the repository.
- **Bounded.** Each call has the scan's timeout and an output cap, and returns `null` with `timeout` or `too_large` beyond them. Only protocol messages go to standard output; diagnostics go to standard error and never contain repository text.
- **The library is the official Go SDK** (`github.com/modelcontextprotocol/go-sdk`), pinned to an exact version, so an API change reaches us only when we upgrade. It is githealthcheck's first dependency outside the standard library: its version is pinned, govulncheck covers it, and only the `mcp` package imports it.
- Not chosen: a JSON-RPC server of our own. It keeps zero dependencies, but every change to the protocol would be ours to follow.
- Not chosen: JSON from the CLI only. Agents could run it from a shell, but could not discover what it offers.

#### A6. `verify` checks a change without running it
**`githealthcheck verify --base <revision>` compares the change with its base, statically.**
- **The base** is resolved once with `rev-parse --verify --end-of-options` to a full object name, through `execx.Git`. A value that starts with `-` or does not resolve is a usage error (exit 2). The change is the commit at HEAD against its base. Both trees are read from git objects as in E5, so nothing is written to disk. A git subcommand that is not on the allowlist yet joins it only with a security review, as in E5.
- **What it reports** for the files the change touches is drawn from the measures, the evidence (ADR 0002) and impact (A4); the first set is an open question. Its report says what it measured and what it did not.
- **Weakened tests.** It reports, as observations, test changes that make tests pass more easily. It does not call them cheating. Which changes count is set rule by rule, and each rule ships only after its precision is measured on a labelled set, as E3 does for fixes.
- **Test results** come only from a report that already exists. Without one, the field is `null`, never a guess. The tests are never run.
- **Exit code.** By default `verify` reports and exits 0, even with findings; exit codes 2 and 3 keep D25's meaning. `--fail-on` names concrete events that make it exit 1, for example a weakened test, or a new finding in the complexity or tests family. A lower score is never one of them, so E1's rejection of a score guard stands.
- Not chosen: running the tests with `--allow-exec` in a container. It adds a container runtime and the largest security surface in the tool, for a check the repository's CI already runs.
- Not chosen: gating by default. A rule whose precision is below 1 would fail correct changes.

#### A7. Files the evidence says to leave alone
**The report has a "stable" section**: files the evidence says to leave alone, because changing them is a risk without a measured benefit. It is shown only where the repository's own history supports it; otherwise it is `null` with `history_too_short`. Which files qualify is an open question, settled with v0.4.0's measurements.
- Not chosen: listing only risky files. An agent told only what is bad tends to rewrite whatever looks bad.

#### A8. No language model, and no code generation
**githealthcheck contains no language model and edits no code.** The same input gives the same output, and no key or connection is needed. Reasoning about the evidence and writing the change are the user's own agent's work.
- Generating and selecting candidate changes by running them (an "evolution" engine) is not githealthcheck's work. Such a system can use `verify` as its judge.
- Not chosen: an optional `--llm` flag that writes a plan as prose. The plan would differ between runs and models, and the tool would need a key and a network connection.

#### A9. Learning across repositories uses public repositories only
**githealthcheck sends no measurements of the scanned repository anywhere.** The only requests it makes are D16's, and only with `--online`: the dependency queries. What it learns across repositories comes from public repositories, measured by us and embedded as a pinned lock file, as D35's `corpus.lock` is.
- **The open atlas.** After v0.4.0, a workflow run by hand runs the backtest on public repositories at pinned SHAs, one GitHub Actions job per repository, and publishes the table. It can be reproduced, like the Calibration workflow.
- Not chosen: opt-in sharing of anonymous measures from users. It needs a privacy review, a server and a policy, and it would make the tool send measurements.
- Not chosen: a hosted service that collects data.

## Open questions

- **Uncommitted changes.** Agents often leave their edits uncommitted. Whether `verify` also reads the work tree, or only commits (the report says `dirty: true` when there are uncommitted changes, architecture §6).
- **What `verify` reports first.** A candidate set: measures before and after, findings added or removed, ADR 0002's evidence and A4's impact for the touched files, and whether the files that usually change with them did.
- **Which test changes count as weakened.** Candidates: removed or loosened assertions, added skips, deleted tests, expected values changed while the code under test did not change; a test changed together with the behaviour it tests would not count.
- **The "stable" section's rule**, for example complex files with no fixes and few changes, in a repository where complexity does not predict fixes.
- **The atlas's size** and its relation to E4's corpus evidence.
- **Test-result formats.** D12's formats are coverage, which does not say whether tests passed. Whether `verify` reads a test-result report (for example JUnit XML), and its `null` reason when there is none.
- **The labelled set for weakened tests**: where its changes come from (agents' pull requests in public repositories), and its size.
- **The MCP tools.** A first set: the ranked list with its scorecard (E1), one file's measures, evidence, past fixes and impact, and `verify`. More are added from how agents use the first set.
- **Two changes at once.** Whether `verify` compares two branches with each other, for agents working in parallel. Overlapping files and co-change links between them can be measured; their order of integration cannot.
- **Impact's method**: how often two files must change together to be listed, how bulk commits (formatting, renames) are left out, and which baseline its backtest is compared with (for example, files of the same directory).
