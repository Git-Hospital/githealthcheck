#!/usr/bin/env python3
"""Score the fix rule (ADR 0002, E3) on a labelled split of testdata/fixrule.

Usage:
  python3 -I scripts/fixrule-eval.py subjects CLONES_DIR   write subjects.tsv from the bare clones
  python3 -I scripts/fixrule-eval.py verdicts                write verdicts.tsv (the Go test reads it)
  python3 -I scripts/fixrule-eval.py score dev|test          weighted precision and recall, 90% intervals

The rule's expressions are read from internal/gitx/fix.go. Go turns control and non-ASCII characters
into spaces before matching, as subjects.tsv does with control characters, and re.ASCII then gives the
same \\b, \\s and case folding as RE2. Each sampled commit stands for its stratum's population divided by
the commits drawn from it (strata.tsv), and the bootstrap resamples within each repository's stratum.
"""

import csv
import random
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DIR = ROOT / "testdata" / "fixrule"
CONTROL = re.compile(r"[\x00-\x1f\x7f]")


def go_consts():
    src = (ROOT / "internal" / "gitx" / "fix.go").read_text()
    consts = {}
    for name, body in re.findall(r"^\t([A-Z]\w+) = ((?:`[^`]*`\s*\+?\s*)+)", src, re.M):
        consts[name] = "".join(re.findall(r"`([^`]*)`", body))
    return consts


def rule():
    c = go_consts()
    fix, prefix, obj = (re.compile(c[k], re.ASCII) for k in ("FixWords", "NotCodePrefix", "NotCodeObject"))
    return lambda s: not (prefix.match(s) or obj.search(s)) and bool(fix.search(s))


def sample():
    return list(csv.DictReader(open(DIR / "sample.tsv"), delimiter="\t"))


def subjects(clones):
    out = ["repo\tcommit\tsubject\n"]
    for r in sample():
        git = Path(clones) / (r["repo"].replace("/", "_") + ".git")
        s = subprocess.run(["git", "-C", str(git), "log", "-1", "--format=%s", r["commit"]],
                           check=True, capture_output=True, env={"GIT_NO_LAZY_FETCH": "1", "PATH": "/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin"}).stdout
        out.append(f"{r['repo']}\t{r['commit']}\t{CONTROL.sub(' ', s.decode('utf-8', 'replace').strip())}\n")
    (DIR / "subjects.tsv").write_text("".join(out))


def load_subjects():
    return {(r["repo"], r["commit"]): r["subject"] for r in csv.DictReader(open(DIR / "subjects.tsv"), delimiter="\t", quoting=csv.QUOTE_NONE)}


def verdicts():
    is_fix, subj = rule(), load_subjects()
    out = ["repo\tcommit\tfix\n"] + [f"{r}\t{c}\t{str(is_fix(s)).lower()}\n" for (r, c), s in subj.items()]
    (DIR / "verdicts.tsv").write_text("".join(out))


def score(split):
    is_fix, subj = rule(), load_subjects()
    strata = {(r["repo"], r["stratum"]): int(r["commits"]) for r in csv.DictReader(open(DIR / "strata.tsv"), delimiter="\t")}
    labels = list(csv.DictReader(open(DIR / f"labels-{split}.tsv"), delimiter="\t", quoting=csv.QUOTE_NONE))
    rows = [(r["repo"], r["stratum"], r["label"] == "fix", is_fix(subj[(r["repo"], r["commit"])])) for r in labels]
    # The same rows with each disagreement resolved for the other labeller.
    flipped = [(r["repo"], r["stratum"], (r["opus"] if r["label"] != r["opus"] else r["sonnet"]) == "fix", hit)
               for r, (_, _, _, hit) in zip(labels, rows)]
    drawn = {}
    for repo, st, _, _ in rows:
        drawn[(repo, st)] = drawn.get((repo, st), 0) + 1

    def pr(data):
        tp = fp = fn = 0.0
        for repo, st, fix, hit in data:
            w = strata[(repo, st)] / drawn[(repo, st)]
            tp += w * (hit and fix)
            fp += w * (hit and not fix)
            fn += w * (fix and not hit)
        return (tp / (tp + fp) if tp + fp else float("nan")), (tp / (tp + fn) if tp + fn else float("nan"))

    groups = {}
    for row in rows:
        groups.setdefault(row[:2], []).append(row)
    rng, ps, rs = random.Random(20261028), [], []
    for _ in range(5000):
        p, r = pr([x for g in groups.values() for x in rng.choices(g, k=len(g))])
        ps.append(p)
        rs.append(r)
    ps.sort()
    rs.sort()
    lo, hi = int(0.05 * len(ps)), int(0.95 * len(ps))
    p, r = pr(rows)
    hits = sum(h for *_, h in rows)
    print(f"{split}: {len(rows)} commits, rule matched {hits}, labelled fix {sum(f for _, _, f, _ in rows)}")
    print(f"precision {p:.3f} (90% {ps[lo]:.3f}-{ps[hi]:.3f}), recall {r:.3f} (90% {rs[lo]:.3f}-{rs[hi]:.3f}), weighted")
    p, r = pr(flipped)
    print(f"with each disagreement resolved the other way: precision {p:.3f}, recall {r:.3f}, weighted")

    # A tool runs on one repository, so the mean of the per-repository precisions, unweighted, with a
    # bootstrap over each repository's matches.
    hits = {}
    for repo, _, fix, hit in rows:
        if hit:
            hits.setdefault(repo, []).append(fix)
    mean = lambda h: sum(sum(v) / len(v) for v in h.values()) / len(h)
    ms = sorted(mean({k: rng.choices(v, k=len(v)) for k, v in hits.items()}) for _ in range(5000))
    print(f"mean of per-repository precisions {mean(hits):.3f} (90% {ms[lo]:.3f}-{ms[hi]:.3f})")
    for repo, v in hits.items():
        missed = sum(fix and not hit for rp, _, fix, hit in rows if rp == repo)
        print(f"  {repo}: matched {len(v)}, fixes among them {sum(v)}, fixes missed {missed}")


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "subjects":
        subjects(sys.argv[2])
    elif cmd == "verdicts":
        verdicts()
    elif cmd == "score":
        score(sys.argv[2])
    else:
        sys.exit(__doc__)
