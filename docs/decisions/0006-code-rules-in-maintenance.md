# 0006: The code rules go into maintenance mode

**Status:** accepted, 12 November 2026. **Decided by:** the maintainer. **Builds on** [ADR 0005](0005-checker-beside-agents.md) and its precision method (C3, PR #81). **Amends** [ADR 0005](0005-checker-beside-agents.md): its `verify`, the agent interface (`mcp`) and the releases planned after v0.3 (C7) are not pursued for now. ADR 0005's status line says so, and the release table in `docs/architecture.md` follows.

Each decision gives the option chosen, then the options considered and rejected, as in ADR 0002.

## Why

ADR 0005 planned `verify`, a comment on pull requests, and later an agent interface, all built on rules whose precision would be measured first. The first measurement is done, and the working papers of 2026-11-11 (kept by the maintainer: a brief, a codebook, the sample method and the coded objections) checked what else the tool could do for people who write Go with coding agents. The measurements below are from 2026-11-11. This record asks whether the plan still holds.

## Evidence

- **Precision round 1 (PR #81), dev split.** Labels by two models, with the maintainer settling three disagreements. Weighted precision and 90% Wilson interval: GHC-CPLX-004 10 of 45 true, 0.092 (0.036 to 0.214); GHC-CPLX-006 36 of 41, 0.923 (0.804 to 0.972); GHC-CPLX-008 5 of 42, 0.136 (0.068 to 0.254). With the known false-positive classes removed, 004 stays near 0.56 and 008 near 0.38, under the 0.8 bar of ADR 0005, C3. 006 can pass, but its rule page says it is the dead code that staticcheck reports as U1000 (golangci-lint `unused`).
- **The organization's own Go repositories** (helmbulance, meshmedic, boards). A scan with a binary built from main reports no findings from 004, 006 and 008 there, also on 14 snapshots across 60 days of helmbulance and meshmedic. A diff mode built on the measured rules would be silent on them. What does fire: GHC-CPLX-001 55, 002 16, 003 2, 009 4, 010 10, GHC-HIST-001 2, GHC-TEST-001 4. None of these has a measured precision yet.
- **What reviewers object to.** A sample of 119 pull requests written by coding agents (Jules 33, Copilot 32, Devin 19, Claude 17, Cursor 15, Codex 4) in 70 Go repositories, 2026. Human reviewers made 434 objections, coded by three models with a written codebook. Agreement on 12 pull requests coded by all three was about 73% on class, so the counts are rough. 54% of the objections (236) need to know intent; 106 need the rest of the repository, 50 need a run, and 42 are decidable from the diff alone. Each class a program can decide is small. The coders named some of them as reported by no Go tool; from the tools' documentation, we judge that these cover most of them:
  - golangci-lint, with its diff mode (`new-from-rev`) and `unused`;
  - pre-commit-hooks, for large files, conflict markers and line endings;
  - Codecov patch coverage, for changed lines without tests;
  - mutation testing limited to changed lines: quality-gates/mutago (statement removal, `--git-diff-lines`, `--per-test`, a report for agents) and gremlins (`--diff`);
  - agent wrappers such as aislop.
- **Limits of the sample.** 153 of the 434 objections come from a repository owner reviewing their own agent. 48 are in three repositories whose owners paste AI-written reviews from human accounts. Only 55 of the pull requests are in repositories with 50 or more stars. Few of the pull requests are closed (12 of 119). The "needs the repository" class was judged by a model and is optimistic: many such items ("use the existing helper") need judgement, not a syntactic rule. The column naming an existing tool is weak. We read the tools' documentation and did not run them.
- **Three directions failed a check.** (1) A few measured rules plus a diff mode: the two points above. (2) Change hygiene and "existing constant ignored": 0 real findings on the organization's repositories, and pre-commit-hooks and goconst overlap. (3) A per-hunk check that the tests depend on the change: it is available already as changed-line mutation testing, and about 4 of the 434 objections are left to it.
- **Who uses it.** One person has written nearly all the code (a second member opened four pull requests on 11 November 2026, three of them hardening and fixes), and there are no outside users yet (7 stars, all from members of the organization).

## Decisions

#### M1. What to do with the code rules
**The code rules go into maintenance mode (B).** ADR 0005's `verify`, the agent interface and the releases planned after v0.3 are not pursued for now. The precision method (sealed stratified sample, two labellers, Wilson interval, bar 0.8) stays.

What continues:
- bug and security fixes, the scan, the GitHub Action and the released report formats (JSON schema, SARIF);
- dependency updates;
- a v0.3.1 from main, with what has landed since v0.3.0.

What stops:
- new rules;
- `verify` and the pull-request comment (ADR 0005, C4 to C6), `mcp`, and `impact`;
- v0.4 to v0.9 as planned in ADR 0005, C7. Evidence (ADR 0002) and impact (ADR 0003, A4) are not scheduled either.

What happens to ADR 0005: its status line now reads "not pursued except C3, see ADR 0006", and the release table in `docs/architecture.md` was changed in the same change. ADR 0005's text stays as the record of what was planned.

What happens to PR #81: the method and the dev-split results are kept as the evidence for this record. The sealed test split stays sealed and unlabelled, so a future round can still use it. The rule pages of GHC-CPLX-004 and 008 gain a note of their dev-split precision, in a separate change.

- Not chosen, A: continue ADR 0005 as written. Cost avoided: months on `verify` whose first rules stay under the bar on the dev split (004 near 0.56 at best, 008 near 0.38), report nothing on the organization's three repositories, or repeat a tool teams may already run (006). Cost of rejecting A: the pull-request comment, the part of the plan most teams would have met, is not built.
- Not chosen, C: become a thin agent-facing wrapper around existing Go tools. It would make agents' use easy, which was the goal in the brief, but it adds an install and a name over tools that already have diff modes and reports, and wrappers such as aislop exist. It is the likeliest thing to reopen (see below).
- Not chosen, D: add a language model. 54% of the objections need intent, which is the one thing a model might judge. But ADR 0003, A8 rejects it: runs would differ, and a key and a connection would be needed. Whether a model's judgement of intent is reliable is not measured, and the tool would no longer be the measured, deterministic thing it is.
- Not chosen, E: archive the repository. The scan, the Action and the released formats work, and the precision method has value. Archiving would also close the way back at no saving in effort, since maintenance is small.

## Consequences

- Gains: the maintainer's time goes elsewhere; no comment is built on rules known to be below the bar.
- Costs: ADR 0005's goal, that engineers who write Go with an agent run githealthcheck by default, is not met. With no outside users, little is lost, but none is gained either.
- What was measured stays checkable: the rule pages carry the dev-split figures once the separate change lands.

## What would reopen this

Either of these:
- a concrete request from an outside user; or
- a class of defect that a program can decide without a language model, that no existing tool reports, and that appears in at least one in ten pull requests of a fresh sample written by coding agents, coded the same way.

## Open questions

- Whether the other rules (GHC-CPLX-001 to 003, 005, 007, 009, 010, GHC-HIST, GHC-TEST) should get a precision round before any change to their defaults; this record does not decide it.
- Whether a v0.3.1 should wait for the rule-page notes.
