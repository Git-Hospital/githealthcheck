# 0004: Sealed predictions, and tests an agent may not silently weaken

**Status:** accepted, 28 October 2026; v0.4.0 (after its atlas) and v0.6.0. **Decided by:** the maintainer. **Builds on** [ADR 0002](0002-evidence.md) and [ADR 0003](0003-evidence-layer.md). **Amends** ADR 0003's A6 (protected tests; its open question on the labelled set for weakened tests is settled with commits, not pull requests) and A9 (the atlas seals its predictions).

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
| Merge paradox: changes that pass alone but fail together | Evidence from history only, without running a merge | Not now; a candidate after v0.5.0 (open questions) |
| Proof half-life: when a verification stops holding | Little; build systems key results by their inputs | A field of `verify`'s report (open questions) |
| Counterfactual tests: do the tests fail on a planted fault? | Little; this is mutation testing | Rejected as an engine: it runs the tests (D22). Reading a mutation report the user's CI wrote stays possible |

On 28 October 2026 we found research benchmarks on agents that edit tests instead of solving the task, among them [SpecBench](https://arxiv.org/abs/2605.21384) and the [Reward Hacking Benchmark](https://arxiv.org/abs/2605.02964) (read 28 October 2026). We did not find a tool that checks pull requests for weakened tests; the search was brief and not systematic.

## Decisions

#### P1. The atlas seals its predictions and scores them six months later
**Each atlas run (A9) writes, for every repository, the ranked list of E1 at its pinned commit, and seals it before any outcome exists.**
- **The seal.** The atlas workflow writes the predictions file (`atlas/predictions/<date>.tsv`: repository, pinned commit, githealthcheck version, rank, path) and its SHA-256, and they reach main through a pull request. A commit's date can be set by its author, so the proof of order is the merge time GitHub records for that pull request; main refuses force-pushes and has no bypass, so the file cannot be replaced afterwards. Scoring reads only the sealed file and the pinned commits.
- **The score.** Six months later a script in the atlas workflow, not a subcommand, fetches each repository's later history and counts the fixes (E3's rule, `gitx.IsFix`) that each listed file received after the pinned commit, and gives the scorecard of E2 (the share of fixes caught by reading 20% of the code lines) next to its two baselines, random and largest files first, with a 90% interval.
- **The comparison.** The backtest is rerun with six-month outcome windows, a deviation from E2's one year used only for this comparison, pooled over windows starting up to three years before the pinned commit. Its interval is a bootstrap 90% interval over files.
- **The kill condition.** Only repositories with at least 30 fix touches in the six months count (E4's judgement). If the prospective scorecard lies below the backtest's interval in more than a third of them, the backtest is overstating the method; chance alone puts about a tenth below a 90% interval. That result is published, the scorecard is marked as not confirmed, and E7's test is rerun on the prospective data.
- **The binary stays offline.** Only the workflow fetches; githealthcheck itself makes no request (D16) and sends nothing (A9).
- **Cost.** One script and one sealed file per run. The atlas has no release yet, so neither has the first score: it is due six months after the first atlas run.
- Not chosen: backtests only. They cannot rule out that the method was tuned on the history it is judged by.
- Not chosen: predictions kept by users and sent to us. The tool sends nothing (A9).

#### P2. Protected tests, read from the base
**A repository can list the tests a person approved as protected, and `verify` reports any change to them.**
- **The list** is a file in the repository (its name is an open question). Each line names a test file, or a test by its file and name, since a bare name is ambiguous across packages: `pkg/x_test.go TestX`, `tests/test_x.py test_y`, `tests/test_x.py TestZ.test_y`.
- **It is read from the base, never from the change.** `verify` reads the list from the base commit's tree (E5). This holds only when the change's author does not choose the base: in CI, the base is the target branch's tip or the merge-base with it, and an agent that passes its own `--base` can pick a commit after it edited the list. The report always names the resolved base. A base without a list protects nothing: the field is `null` with the reason `no_list_at_base`, never read as "no protected test changed". A change that adds or edits the list is reported as its own event.
- **What is reported** is a fact, not a judgement: a protected test deleted, renamed or edited, with the lines that changed. "Edited" means a change to the test's tokens; a change to comments alone is not one. `--fail-on protected_test_changed` makes it exit 1 (A6); by default it only reports. Not covered: helpers, fixtures and `conftest.py` files that a protected test uses but the list does not name.
- **Weakened tests outside the list** stay A6's observations. Each rule ships only if the lower end of its precision's 90% interval is at least 0.8 on the labelled set below, the bar ADR 0002's E3 applies to the fix rule as amended. A label means: after the change, the test accepts an implementation it rejected before, and no behaviour change in the same commit explains it. The candidate rules:
  - R1: an assertion removed without a replacement, or loosened (an equality turned into a weaker check).
  - R2: a skip added: `t.Skip`, `t.Skipf`, `t.SkipNow`, `pytest.mark.skip`, `pytest.mark.skipif`, `unittest.skip`, `xfail`.
  - R3: an expected value changed while the code under test did not change in the same change. The code under test is found statically, as the non-test files of the same Go package or the modules a Python test imports; where none is found, the rule does not match. A test changed together with the behaviour it tests does not count.
  - R4: a test deleted, unless the symbol it tests is deleted in the same change.
- **The labelled set** (A6's open question): about 300 commits from public repositories, outside the experiment and the corpus, that touch a test and carry an agent's mark (a `Co-Authored-By` trailer naming an agent, or a known agent account as author). It is stratified per rule, split one third dev and two thirds test, and the test split is sealed before labelling. Each rule needs at least 40 matches in the test split, so the set grows where a rule is rare; a rule with fewer is reported as unmeasured and does not ship. Two labellers (Opus and Sonnet) label each commit independently, and the maintainer resolves the disagreements, as in `testdata/fixrule`.
- **A planted check** measures recall the labels cannot: in 50 commits labelled as not weakening, a known weakening of each rule's kind is planted, and the share each rule finds is published.
- **A published number** follows from the set: how often agents' commits weaken a test, next to the same rate for people's commits in the same repositories, with intervals.
- Not chosen: inferring invariants by running the code, as Daikon does. It runs the repository's code (D22).
- Not chosen: protecting files through CODEOWNERS or branch rules only. They say who may change a file, not that a test still checks what it checked.
- Not chosen: reading the list from the change. The agent under check could edit it.

## Open questions

- **The list's name and format**, and whether it can name a single assertion rather than a whole test.
- **A user's own ledger.** Whether a later command scores an old JSON report against the history written since it, so any user can check the tool's past predictions on their own repository.
- **Merge paradox, a candidate after v0.5.0's impact engine, with no milestone yet.** Whether parallel changes to files that usually change together are followed by more fixes after they merge, measured on history alone. Only merge-based repositories keep the parallel branches; squash-merged ones do not.
- **Proof half-life.** Whether `verify`'s report names the base it holds for and the paths whose change makes it stale.
- **Mutation reports.** Whether, after v0.7.0, a mutation-testing report the user's CI already wrote is read like a coverage report, with `null` when there is none.
