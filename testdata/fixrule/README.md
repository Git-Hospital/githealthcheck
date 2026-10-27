# The fix rule's labelled set

ADR 0002 (E3) ships the fix rule only if its precision on a labelled set is at least 0.8. This directory holds that set and how it was drawn.

## The repositories

Ten repositories that are in neither ADR 0002's experiment nor the calibration corpus (`calibration/corpus.txt`): five Go and five Python, with both squash-merged and merge-based histories. `repos.txt` pins each HEAD.

## The sample

`scripts/fixrule-sample.py` draws it from bare clones made with `--filter=blob:none --shallow-since=2023-10-28`; run it with `python3 -I`.

- **Population**: non-merge commits after 2023-11-28, not by an author ending in `[bot]`, that touch at least one source file (not a test, not vendored). The month after the shallow boundary keeps boundary commits out.
- **Strata**: commits the v0 rule matches, and commits it does not. v0 is the experiment's rule, copied into the script. `strata.tsv` gives each stratum's size, so precision and recall can be weighted back to the population.
- **Draw**: per repository, 15 commits from each stratum with a fixed seed; 5 of each go to `dev`, 10 to `test`. 300 commits in all, 100 dev and 200 test (`sample.tsv`).
- **Commitment**: `test.sha256` is the SHA-256 of the test commits, one `repo<TAB>commit` line each in `sample.tsv`'s order. It was committed before any commit was labelled. To check it: `tail -n +2 sample.tsv | awk -F'\t' '$4=="test"{print $1"\t"$2}' | shasum -a 256`.

## The labels

`GUIDELINE.md` defines the label: a fix corrects wrong behaviour in shipped code. Two reviewers label each commit independently: one Claude Opus agent and one Claude Sonnet agent, each without the other's label or the rule's verdict. The maintainer resolves each disagreement, and the reason is recorded.

1. The dev commits are labelled first. The rule may be revised while looking at them.
2. The rule is frozen in a commit.
3. Only then are the test commits labelled, and the frozen rule is scored on them once. The precision and recall published with the rule come from the test split.
