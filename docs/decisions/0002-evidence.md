# 0002: Prove each finding on the repository's own history

**Status:** accepted, 27 October 2026; planned for v0.4.0. **Decided by:** the maintainer.

Each decision gives the option chosen, then the options considered and rejected, so the reasoning stays visible. ADR 0001 stays in force; this record changes what the tool puts first and adds one way of reading the repository.

## Why

Every code-health tool we found states a score and asks to be believed. The evidence behind the scores, where there is any, comes from other people's code:

- CodeScene's study ("Code Red", TechDebt 2022) ties its score to defects on 39 proprietary codebases. Its authors work for CodeScene, the data come from its customers, and the result is a correlation.
- repowise publishes a defect benchmark of its own (21 repositories, mean AUC 0.737). Its own data show the AUC falling to about 0.49 when files of the same size are compared. Most of the signal is file size.
- None of the tools we read (CodeScene, SonarQube, Qlty, Codacy, DeepSource, repowise, aislop and others, checked on 26 October 2026) shows how well its findings predicted defects in the user's own repository.

githealthcheck already measures each file's complexity, its changes and its authors. A repository's history records which files were fixed later. So the tool can test its own findings on the repository it scans: measure the code as it was a year ago, count the fixes of the year after, and show only what that test supports.

### The experiment

A throwaway script ran this test before the decision. Each repository was measured at five starting points (one, 1.5, 2, 2.5 and 3 years before its HEAD) with githealthcheck's own measurement code, in non-test, non-vendored Go and Python files. It counted the commits touching each file in the year before, and the fix commits touching it in the year after. Fix commits were recognised by their subject line, a rule whose precision was not measured. Files were compared within size quartiles, and a bootstrap over files gave 90% intervals.

| Repository (HEAD) | Changed 3+ times in the prior year | Has a function over cognitive 15 | Fixes caught reading 20% of the lines, by churn per line |
|---|---|---|---|
| psf/black (`cca20965615b`) | 7.5x (3.7-17) | 4.0x (1.3-60) | 79%, largest files first 11% |
| gin-gonic/gin (`0f09c3a9b462`) | 4.4x (2.3-13) | 6.1x (2.7-28) | 21%, largest files first 26% |
| junegunn/fzf (`b412a450538e`) | 3.8x (1.6-8.3) | 2.0x (0.6-12) | 22%, largest files first 10% |
| caddyserver/caddy (`71c5812cfeb7`) | 2.4x (1.6-3.4) | 1.6x (1.1-2.5) | 33%, largest files first 20% |
| pytest-dev/pytest (`41d12cdbb4a3`) | 2.3x (1.6-3.8) | 1.7x (1.03-3.7) | 30%, largest files first 13% |
| gohugoio/hugo (`8c1fdcac0b2f`) | 2.1x (1.7-2.7) | 1.8x (1.4-2.3) | 31%, largest files first 25% |
| python-poetry/poetry (`1fe5e0a57928`) | 1.6x (1.3-2.2) | 1.3x (0.98-1.8) | 23%, largest files first 16% |
| prometheus/client_golang (`db682b9fc5ff`) | 2.0x (0.6-4.1) | 1.4x (0.6-3.1) | 44%, largest files first 9% |
| spf13/viper (`528f7416c4b5`) | 7.6x (0.0-29) | 16x (0.0-40) | 20%, largest files first 20% |

Pallets/flask, psf/requests, Textualize/rich, pallets/click and spf13/cobra had fewer than 30 fixes on their source files in the five windows: no evidence either way.

What it showed:

- **The idea works where there is history.** Churn predicted fixes beyond file size in 7 of the 9 repositories with enough fixes; complexity did in 5.
- **The answer differs between repositories,** and that is the point. In fzf the ranked list barely beats a random pick; in gin, picking the largest files does as well. A tool that scores every repository the same way cannot say this.
- **Small, mature libraries have too little history** to prove anything on their own.
- **The fix rule is noisy.** Most matches in the active repositories are real fixes ("Fix crash on…", "panic"), but "fix typing", "fix link", "fix linter" and reverts of features are counted too.

## Decisions

#### E1. One question, answered with evidence
**githealthcheck answers "where will this repository's next bug come from, and why should I believe it?"** The headline output is a short list of the riskiest files. Each entry carries the evidence for it. A scorecard says how well the same method did on the repository's past. The 0-10 score of D35 stays in the JSON report but is no longer the headline.
- Not chosen: the 0-10 score as the headline, with hotspots added. It would be a free copy of CodeScene, with less evidence.
- Not chosen: an agent guard that blocks each AI edit that lowers the score. aislop and others already do this, and it answers a different question.

#### E2. The method: a backtest within size bands
- **Windows**: one-year windows, starting up to three years before HEAD, every six months.
- **Measures at the start of a window**: from the existing families. These are churn and authors (history), cognitive complexity, length and nesting (complexity), and coverage where a report exists.
- **Outcome**: commits that fix the file in the year after.
- **The comparison**: files are compared with files of the same size. The result is a rate ratio pooled over size quartiles, with a bootstrap 90% interval.
- **What is shown**: a measure is shown only when its interval lies above 1 in this repository.
- **The scorecard**: the share of the fixes the method would have caught by reading 20% of the code lines. It sits next to two baselines, a random pick and the largest files first.

Considered and rejected:
- Not chosen: AUC over all files, without a size control. As repowise's numbers show, it mostly measures size.
- Not chosen: a model with learned weights. It is harder to explain than "files like this got 2.4x more fixes", and harder to check by hand.

#### E3. Fixes are recognised from git alone, and the rule is measured first
**A fix is recognised from git alone**: the commit subject, and reverts. Nothing is fetched over the network, so the scan stays offline (D16) and the scanned repository's code is never run (D22).
- **The rule is measured before the engine ships.**
  - The labelled set: about 300 commits from ten repositories, each labelled from its diff by two independent reviewers. Disagreements are resolved and recorded, and the set is kept in `testdata`.
  - The rule ships only if its precision on that set is at least 0.8.
  - Its precision and recall are published.
- Not chosen: GitHub issues labelled as bugs (`--online`). This needs the network and a token, and works only on one forge.

#### E4. Without enough history, the corpus's evidence, labelled as such
**A repository with too little history gets no evidence of its own.** "Too little" means fewer than 30 fix touches on its source files across the windows. Its own evidence is `null` with the reason `insufficient_history`.
- The report then shows what the same test found across the calibration corpus. An example: "changed 3+ times: 2.4x, in 7 of 9 repositories".
- The list it ranks with that evidence is marked as general, not this repository's.
- The corpus's histories are pinned, like D35's lock, and reproduced in CI.
- Not chosen: own evidence only. Small repositories would get nothing.

#### E5. The past is read from git objects, not checked out
**The tree at a window's start is read from git objects** (`git cat-file --batch`), in memory, with the same size bounds as the walk. Nothing is written to disk.
- `cat-file` joins the git allowlist of D15's exec wrapper, with fixed arguments.
- That change gets its own security review.
- Not chosen: a temporary checkout per window, as in the experiment. It writes the repository's files to disk, is slow, and lets the repository's content reach the file system.

#### E6. The unit is the file
- **Files are the unit.** Commits list files directly, there are enough fixes per file to compare, and an engineer acts on a file.
- Function-level evidence comes later, if it proves itself. It needs each commit's diff matched to functions, and the data per function are sparser.

#### E7. A measure that proves nothing in the corpus leaves the headline
- **A measure that shows evidence in no corpus repository leaves the headline output.** Its findings stay in the JSON report.
- The tool keeps its own complexity family score above 7: new code passes the tool's own complexity rules.
- Not chosen: keeping every family and rule as it is and adding the evidence on top.

#### E8. v0.4.0 is the evidence release
**The order of work:**
1. The fix rule and its labelled set (E3).
2. Reading the past from git objects (E5).
3. The backtest (E2), the corpus evidence (E4), and the output (E1).

JS/TS moves to v0.5.0.
- Not chosen: JS/TS in parallel. It would split the work, and the release would come later.

## Open questions

- **Renamed files.** The experiment ignored renames (`--no-renames`), so a renamed file lost its history. The engine should follow exact renames.
- **Squash-merged repositories.** These put a whole PR into one commit, while merge-based repositories keep many small commits. Both were in the experiment; whether the fix rule works as well on both is part of E3's measurement.
- **The window length** for very active repositories, where six months may already be enough.
