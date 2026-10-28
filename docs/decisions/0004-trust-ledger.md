# 0004: Sealed predictions, and tests an agent may not weaken

**Status:** accepted, 28 October 2026; v0.4.0 (after its atlas) and v0.6.0. **Decided by:** the maintainer. **Builds on** [ADR 0002](0002-evidence.md) and [ADR 0003](0003-evidence-layer.md). **Amends** ADR 0003's A6 (protected tests, and the labelled set for weakened tests) and A9 (the atlas seals its predictions).

Each decision gives the option chosen, then the options considered and rejected, as in ADR 0002.

## Why

ADR 0003 makes githealthcheck an evidence layer for changes written by agents. That leaves two questions it cannot yet answer:

- **Can the tool itself be believed?** ADR 0002's backtest measures the method on the past, but the method was also chosen while looking at the past. Only predictions written down before the outcome exists, and scored after, rule out hindsight.
- **Can an agent move its own goalposts?** An agent asked to make the tests pass can make the tests easier instead. ADR 0003 (A6) reports weakened tests as observations; nothing yet stops the agent from editing the very list of what must not be weakened.

Five ideas were weighed against ADR 0001's principles (offline, no execution of the repository's code, measured or null) and a three-person team:

| Idea | What is new in it | Verdict |
|---|---|---|
| A self-falsifying engine: predictions scored against what happened later | Sealing per-repository predictions in advance and publishing their score | Accepted: P1 |
| An invariant vault: approved behaviour an agent may not change | Reading the list of protected tests from the base, so the change under check cannot edit it | Accepted: P2 |
| Merge paradox: changes that pass alone but fail together | Evidence from history only, without running a merge | Not now; a v0.5.0 candidate (open questions) |
| Proof half-life: when a verification stops holding | Little; build systems key results by their inputs | A field of `verify`'s report (open questions) |
| Counterfactual tests: do the tests fail on a planted fault? | Little; this is mutation testing | Rejected as an engine: it runs the tests (D22). Reading a mutation report the user's CI wrote stays possible |

On 28 October 2026 a search found research benchmarks that measure agents editing tests (SpecBench, EvilGenie, the Reward Hacking Benchmark) and prototypes, but no tool that checks a pull request for weakened tests. The search was not exhaustive.

## Decisions

#### P1. The atlas seals its predictions and scores them six months later
**Each atlas run (A9) writes, for every repository, the ranked list of E1 at its pinned commit, and seals it before any outcome exists.**
- **The seal.** The predictions file (`atlas/predictions/<date>.tsv`: repository, commit, rank, path) is committed with its SHA-256, as `testdata/fixrule/test.sha256` was. The commit's push time is the proof that the list came first.
- **The score.** Six months later a script counts the fixes (E3's rule, `gitx.IsFix`) that each listed file received after the pinned commit, and gives the scorecard of E2 (the share of fixes caught by reading 20% of the code lines) next to its two baselines, random and largest files first, with a 90% interval.
- **The comparison.** The same repositories are backtested with six-month windows, so the prospective score is compared with a retrospective one of the same horizon.
- **The kill condition.** If the prospective scorecard lies below the backtest's 90% interval in more than a third of the repositories with enough fixes, the backtest is overstating the method. That result is published, and E7's rule removes the measures concerned from the headline.
- **Cost.** One script, one sealed file per run; the first score is due six months after the first atlas run.
- Not chosen: backtests only. They cannot rule out that the method was tuned on the history it is judged by.
- Not chosen: predictions kept by users and sent to us. The tool sends nothing (A9).

#### P2. Protected tests, read from the base
**A repository can list the tests a person approved as protected, and `verify` reports any change to them.**
- **The list** is a file in the repository (its name is an open question), with one test file or one test function per line: a Go `TestX` in a `_test.go` file, a Python `test_x` function or `Test` class method.
- **It is read from the base, never from the change.** `verify` reads the list from the base commit's tree (E5), so a change cannot remove an entry and then change its test unseen. A change to the list itself is reported as its own event.
- **What is reported** is a fact, not a judgement: a protected test deleted, renamed or edited, with the lines that changed. `--fail-on protected_test_changed` makes it exit 1 (A6); by default it only reports.
- **Weakened tests outside the list** stay A6's observations. Each rule ships only if the lower end of its precision's 90% interval is at least 0.8 on the labelled set below, as for the fix rule. The candidate rules:
  - R1: an assertion removed without a replacement, or loosened (an equality turned into a weaker check).
  - R2: a skip added (`t.Skip`, `pytest.mark.skip`, `xfail`).
  - R3: an expected value changed while the code under test did not change in the same change. A test changed together with the behaviour it tests does not count.
  - R4: a test deleted.
- **The labelled set** (A6's open question): 300 commits from public repositories, outside the experiment and the corpus, that touch a test and carry an agent's mark (a `Co-Authored-By` trailer naming an agent, or a known agent account as author). It is stratified by whether a rule matches, split 100 dev and 200 test, and the test split is sealed before labelling. Two labellers (Opus and Sonnet) label each commit independently, and the maintainer resolves the disagreements, as in `testdata/fixrule`.
- **A planted check** measures recall the labels cannot: in 50 commits labelled as not weakening, a known weakening of each rule's kind is planted, and the share each rule finds is published.
- **A published number** follows from the set: how often agents' commits weaken a test, next to the same rate for people's commits in the same repositories, with intervals.
- Not chosen: inferring invariants by running the code, as Daikon does. It runs the repository's code (D22).
- Not chosen: protecting files through CODEOWNERS or branch rules only. They say who may change a file, not that a test still checks what it checked.
- Not chosen: reading the list from the change. The agent under check could edit it.

## Open questions

- **The list's name and format**, and whether it can name a single assertion rather than a whole test.
- **A user's own ledger.** Whether a later command scores an old JSON report against the history written since it, so any user can check the tool's past predictions on their own repository.
- **Merge paradox, as a v0.5.0 candidate.** Whether parallel changes to files that usually change together are followed by more fixes after they merge, measured on history alone. Only merge-based repositories keep the parallel branches; squash-merged ones do not.
- **Proof half-life.** Whether `verify`'s report names the base it holds for and the paths whose change makes it stale.
- **Mutation reports.** Whether, after v0.7.0, a mutation-testing report the user's CI already wrote is read like a coverage report, with `null` when there is none.
