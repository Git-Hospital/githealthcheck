# 0005: A checker that teams add beside their coding agents

**Status:** not pursued except C3, see [ADR 0006](0006-code-rules-in-maintenance.md); proposed 8 November 2026, v0.4.0 and v0.5.0. **Decided by:** the maintainer, 8 November 2026: the precision bar and the labelling (C3), and `verify` with the pull-request comment in v0.5.0, impact next (C7). The rest of C7 and the other decisions are proposed with this record. **Builds on** [ADR 0003](0003-evidence-layer.md) and ADR 0004 (sealed predictions and protected tests, PR #23). **Amends** ADR 0003's A2 and the release table in `docs/architecture.md` (`verify` and the pull-request comment move to v0.5.0, impact to v0.6.0, `mcp` to v0.7.0, JS/TS to v0.8.0, and the image, pre-commit, badge and `--online` to v0.9.0); ADR 0002's E3 and ADR 0004's P2 (a sealed test split is labelled by two people and a third resolves; the dev split stays with two models and the maintainer); ADR 0004's release references (`verify` and protected tests in v0.5.0, impact in v0.6.0); and ADR 0001's D36 (the pull-request comment, which D36 left for later, comes with `verify`).

Each decision gives the option chosen, then the options considered and rejected, as in ADR 0002.

## Why

ADR 0003 made githealthcheck an evidence layer for changes written by agents and put the agent layer, `mcp` and `verify` together, after impact. Most teams that write code with agents meet that code in one place: the pull request. The tool reaches most of them as a step in their CI, from the GitHub Action they already have, that checks each change and says what it found where they review. That needs no agent integration and no change to how they work.

Such a check is read by people who did not ask for it. If it is often wrong, they turn it off. So the rules it reports must have measured precision first, and that measurement comes before the comment.

## Decisions

#### C1. Who it serves
**Teams that write code with coding agents and review it in pull requests.** githealthcheck is the check they add beside the agent: the agent writes the change, `verify` reads it and the comment says, with evidence, what the change weakened and what is unproven. It writes no code and calls no language model (ADR 0003, A8).
- Not chosen: an agent integration first (`mcp`, ADR 0003 A5). It reaches the agents of people who configure them, not the reviewers of everyone else's changes. It stays, in v0.7.0.

#### C2. Every change, not only those an agent wrote
**`verify` checks every pull request the same way.** It does not try to tell an agent's code from a person's.
- Authorship marks (`Co-authored-by` trailers, bot accounts, labels) are optional and often missing, so a check limited to them would skip most agent changes and could not say how many.
- An option in the Action may limit the comment to pull requests with a given label or author, for teams that want it.
- Not chosen: an AI-code detector. Its precision cannot be measured on a repository's own history, so it breaks "measured or null", and a wrong guess about who wrote a change is unfair to the person who did.

#### C3. Only measured rules speak
**A rule is on by default only when its precision on a sealed, human-labelled test split has a 90% interval whose lower end is at least 0.8 (#67)**, and only rules on by default appear in the comment unless the repository turns on others. The figure, its interval and its false positives are on the rule's page. A rule below the bar is `experimental`: it stays in the JSON report, and the comment shows it only if the repository turns it on. The same bar sets the scan's defaults: an experimental rule's findings do not count in exit code 1 (D25), in SARIF or in the job summary.
- The labels: two models label the dev split, which rules are revised against, and the maintainer settles their disagreements; two people label the test split, which is sealed before any label exists, and a third settles theirs. The published figure comes from the test split. The fix rule's test split (#21) is relabelled by people the same way.
- Each round measures the rules again on repositories not used before, so a precision is not kept by a rule that later drifts.
- Not chosen: a bar of 0.9. Fewer rules would pass, and each would need a larger sample, for a difference a reviewer would rarely notice.
- Not chosen: labels by models only for the test split. Quick, but the published figure would rest on the same kind of judgement the tool is meant to check.

#### C4. What `verify` reports first
**The first `verify` (v0.5.0) reports, for the lines and files the change touches:**
- the findings that generated code tends to leave and the complexity family already measures: errors checked and then dropped, imports of packages the module does not provide, functions nothing references, commented-out code, placeholder bodies, duplicated blocks (GHC-CPLX-004 to 010);
- weakened tests (ADR 0003, A6; ADR 0004's R1-R4), each only after its precision is measured, and any change to a protected test, a fact read from the base (ADR 0004, P2);
- the evidence of ADR 0002 for the files touched: which of them attract fixes in this repository, and how far that can be believed here;
- a new package without test files (GHC-TEST-001), read from the change, never by running the tests.

Impact (co-change, ADR 0003 A4) joins in v0.6.0, with the "stable" section (A7).
- Not chosen: impact before `verify`, as ADR 0003 ordered it. The comment would wait a release for the part of it that is least about the change itself.

#### C5. The comment
**One comment per pull request, edited in place on each push**, not a new comment each time.
- It lists findings on lines the change added or modified, at most 20, each with its rule and a link to the rule's page. Findings elsewhere in the file are counted, not listed.
- It never shows a score, and a lower score is never a reason to fail (ADR 0002, E1; ADR 0003, A6).
- Repository text in it (paths, identifiers, commit subjects) is escaped as in D36, so a file name cannot add a link, an image or HTML.
- When there is nothing to report, it says so in one line and lists what it did not measure, with the reasons.
- Not chosen: a review comment on every line. It floods the review, and a reviewer cannot see the whole at once.

#### C6. Posting from a fork's pull request
**The Action checks the change in a `pull_request` workflow with a read-only token and uploads the report as an artifact; a second workflow, on `workflow_run`, posts the comment.** The second workflow never checks out or runs the change's code; it reads the report as untrusted data, checks its schema (`schema/report.v1.json`) and size, and writes only the comment. Its permissions are `actions: read`, to download the artifact, and `pull-requests: write`, to comment. The Action documents both workflows.
- **Where it posts comes from the event alone.** The pull request is the open one whose head commit and head repository equal `workflow_run.head_sha` and `workflow_run.head_repository`; for a fork, `workflow_run.pull_requests` is empty, so it is looked up this way. Nothing in the artifact names a pull request: a fork writes the whole artifact, and can edit the `pull_request` workflow itself, so a number there would let it post on any pull request or issue.
- **The comment edited in place** is found by a hidden marker and its author (`github-actions[bot]`), so a comment someone else wrote with the marker is never edited.
- Without a comment, the report still goes to the job summary, which needs no permission (D36).
- Not chosen: `pull_request_target`. It runs with a write token while handling a fork's change, a setting behind several published GitHub Actions compromises. The project's own CI follows the same rule (`pull_request`, never `pull_request_target`; `.github/workflows/ci.yml`).
- Not chosen: comments only on branches of the same repository. Contributions from forks are where a reviewer most needs the check.

#### C7. The order of releases
| Version | Contents |
|---|---|
| v0.4.0 | Evidence (ADR 0002), with the fix rule's precision on human labels, and every default rule's precision measured (C3) |
| v0.5.0 | `verify` (C4) and the pull-request comment in the Action (C5, C6) |
| v0.6.0 | Impact from co-change (ADR 0003, A4) and the "stable" section (A7) |
| v0.7.0 | `mcp` (ADR 0003, A5) |
| v0.8.0 | JS/TS analysis and its corpus |
| v0.9.0 | The container image, pre-commit, the badge and `--online` |

- Not chosen: the comment now, in parallel with the precision work. The first comments would come from rules whose precision nobody has measured, which is the reason people turn a check off.
- Not chosen: ADR 0003's order unchanged. The comment would come in v0.8.0, after the agent integration, though it is the way most teams would meet the tool.

## Open questions

- **How many findings per rule per round**, so the interval can clear 0.8 at the observed rate; and whether test files are sampled (#67).
- **Who labels.** The test split needs two people and a third for disagreements; the maintainers are three.
- **Which lines count as changed** when a change moves code: a moved function with an old finding is not the change's finding. Whether exact renames (#43) and moved blocks are followed in `verify`.
- **The labelled set for weakened tests** (ADR 0004, P2): whether its 40 matches per rule in the test split are enough for the lower end to clear 0.8 at the observed rate.
- **The comment's limits**: whether 20 findings is right, and how the comment is kept under GitHub's 65,536-character limit.
