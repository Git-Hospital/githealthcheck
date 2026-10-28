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

## The dev split

- **Agreement**: the two labellers agreed on 94 of 100 commits (Cohen's kappa 0.88). In all six disagreements the Opus labeller said `fix` and the Sonnet labeller `not_fix`. The maintainer resolved all six as `fix`; for the pandas commit, the project's own release note lists the bug it fixes (issue 55499). `labels-dev.tsv` holds both labels, both reasons and each resolution.
- **v0**, the experiment's rule: precision 0.84, recall 0.45, weighted (intervals 0.62-0.97 and 0.27-0.75 from the tuning script, which is not kept). Of its ten false matches, three were planned reverts, and the rest fixed style, type hints, an example or a script. It also missed "Fix panic when cloning with wrong ssh format", because "format" was excluded anywhere in the subject.
- **The frozen rule** (`internal/gitx/fix.go`): defect words, plus "incorrect", "wrong", "leak", "deadlock", "nil pointer" and a few others; exclusions only as a subject prefix (`TST:`, `chore:`, `docs:` ...) or as the object of "fix" ("fix typo", "fix the lint"); reverts not counted. On dev: precision 0.970 (0.940-0.991), recall 0.385 (0.219-0.699).
- `subjects.tsv` holds the 300 subjects, control characters replaced by spaces. `verdicts.tsv` holds the rule's verdict on each, written by `scripts/fixrule-eval.py verdicts`; `TestIsFixMatchesTheFrozenVerdicts` checks that Go's RE2 gives the same verdicts as Python's `re`.
- One labeller noted that a few large diffs reached it truncated by its file-reading tool; for the test split, each bundle is read on its own.

## The test split

Labelled after the rule was frozen, and scored once with `python3 -I scripts/fixrule-eval.py score test`.

- **Order**: the test commits' commitment was pushed as `9f272aa` (2026-10-27 23:29 UTC) and the frozen rule as `b4cf449` (2026-10-28 00:17 UTC), both before the first test label was written. The branch was later rebased, so on it they are `ff43f5d` and `e737f12`; GitHub still serves the original commits by hash.
- **Agreement**: 192 of 200 (96%, Cohen's kappa 0.92). Of the eight disagreements, the Opus labeller said `fix` in five and the Sonnet labeller in three. The maintainer took the Opus label for all eight; `labels-test.tsv` records this as the resolution.
- **The rule**: precision 0.979 (90% 0.968-0.989), recall 0.284 (90% 0.218-0.395), weighted to the population. This passes ADR 0002's bar: the lower end of the 90% interval is above 0.8. With the eight disagreements resolved the other way, precision is 0.975 (0.963-0.986) and recall 0.289, so the result does not hang on them.
- **Per repository**: the weighted figure pools all ten repositories, and home-assistant/core alone holds 5,328 of the matched commits, where the rule made no false match. A tool runs on one repository at a time, so the average of the per-repository precisions is the fairer figure for a user: 0.881 (90% 0.830-0.929, bootstrap over each repository's matches), also above 0.8. It ranges from 1.0 (gitea, ollama, pandas, home-assistant, poetry-core) to 0.6 in kubernetes/minikube, whose maintainers also write "fix" for chores ("fix boilerplate whitespace", "Fix usage string for minikube node commands"); containerd (0.67) and ansible (0.78) follow. Each of these rests on 7 to 11 matches.

| repository | matched | fixes among them | fixes missed |
|---|---|---|---|
| kubernetes/minikube | 10 | 6 | 1 |
| hashicorp/consul | 11 | 10 | 1 |
| containerd/containerd | 9 | 6 | 3 |
| go-gitea/gitea | 10 | 10 | 3 |
| ollama/ollama | 8 | 8 | 4 |
| numpy/numpy | 7 | 6 | 2 |
| pandas-dev/pandas | 10 | 10 | 0 |
| home-assistant/core | 9 | 9 | 7 |
| ansible/ansible | 9 | 7 | 3 |
| python-poetry/poetry-core | 10 | 10 | 2 |

- **False matches** (11): "fix" for a build, a godoc, a usage string, whitespace, a linter finding (`go fix "inline"`, staticcheck QF1004), error-message wording, and one labelled `not_fix` by the maintainer's resolution ("Fix ClusterLoadAssignment timeouts dropping endpoints").
- **Recall is low (0.28)**, lower than on dev. Most fixes name no defect in their subject; home-assistant/core's subjects rarely do. ADR 0002's amendment explains why precision is the bar. Recall differs between repositories, so the rule's fix counts compare files within one repository, not repositories with each other.
- The test split is now spent: a later change to the rule needs a new labelled set.
