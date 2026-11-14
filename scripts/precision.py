#!/usr/bin/env python3
"""Measure rules' precision on a sealed labelled sample of their findings, round by round (#67, ADR 0005 C3).

Usage:
  python3 -I scripts/precision.py scan ROUND CLONES_DIR BINARY [OUT]  write findings.tsv from the round's repositories
  python3 -I scripts/precision.py sample ROUND                   draw the dev and test splits, write test.sha256
  python3 -I scripts/precision.py show ROUND SPLIT               print each sampled finding with its code, for labelling
  python3 -I scripts/precision.py label ROUND SPLIT NAME         label a split by hand, blind, as NAME
  python3 -I scripts/precision.py merge ROUND SPLIT A B          write labels-SPLIT.tsv from A's and B's labels
  python3 -I scripts/precision.py resolve ROUND SPLIT NAME       settle the disagreements as NAME, a third person
  python3 -I scripts/precision.py score ROUND SPLIT [REVISED]    each rule's weighted precision and its 90% interval

A round is a directory testdata/precision/roundN holding repos.tsv: one repository per line, as language,
GitHub owner/name and the commit scanned. `scan` fetches each commit with depth 1 into CLONES_DIR and runs
BINARY (a githealthcheck build) on it; only the rules in RULES are kept. The population of a rule is every
finding of it in the round's repositories. githealthcheck lists at most 50 findings of a rule per file and
counts the rest; those are written to unlisted.tsv, counted in the population, and cannot be drawn. Such a
file is its own stratum, so its listed findings stand for all of its findings. With OUT, scan writes there
instead of the round, for a re-scan with a revised rule.

`sample` draws about PER_RULE findings per rule, stratified by repository, each repository's share in
proportion to the square root of its findings: a large repository gets a smaller share than its findings, and the weights stay
even enough for the sample to carry its size. TEST_SHARE of each stratum's draw goes to the test split,
which rules are never revised against, and the rest to dev; a stratum with one draw goes to test, the split
the published figure comes from. A drawn finding stands for its stratum's findings divided by those drawn in
its split. test.sha256 commits the round's files (SEALED) before any label exists.

Labels: two models label the dev split and the maintainer settles their disagreements; two people label
the test split and a third settles theirs (ADR 0005, C3). Each labeller writes human/NAME-SPLIT.tsv with
repo, rule, path, line, label (true or false) and reason. A finding that cannot be judged from the code is
labelled false with a reason starting "undecidable:", and score counts those apart.

`score` gives the weighted precision with the Wilson interval on the Kish effective sample size, the number
of equally weighted findings the weighted sample is worth. REVISED is the directory of a re-scan with a
revised rule (scan ... OUT): it may only remove findings, and the rule is scored on the sampled findings it
still reports, each with the weight it was drawn with.
"""

import csv
import hashlib
import json
import math
import os
import random
import re
import subprocess
import sys
import unicodedata
import urllib.parse
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RULES = ("GHC-CPLX-004", "GHC-CPLX-006", "GHC-CPLX-008")
SEED = 20261109
PER_RULE, TEST_SHARE = 120, 0.6
BAR, Z90 = 0.8, 1.6449  # ADR 0005 C3: the lower end of the 90% interval
CONTEXT = 12  # lines shown on each side of a finding
NAME = re.compile(r"^[a-z][a-z0-9-]{0,31}$")
RESERVED = {"repo", "rule", "path", "line", "message", "split", "label", "reason", "resolution", "resolved"}
LABELS = ("true", "false")
SEALED = ("repos.tsv", "scanned-with.txt", "findings.tsv", "unlisted.tsv", "sample.tsv", "strata.tsv")
UNLISTED = re.compile(r"^(\d+) more findings of this rule in this file are not listed")
GIT_ENV = {"PATH": "/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin", "GIT_TERMINAL_PROMPT": "0",
           "GIT_CONFIG_NOSYSTEM": "1", "HOME": "/nonexistent"}


def round_dir(name):
    d = ROOT / "testdata" / "precision" / name
    if not (d / "repos.tsv").exists():
        sys.exit(f"{d}/repos.tsv: no such round")
    return d


def repos(d):
    return [line.split("\t") for line in (d / "repos.tsv").read_text().splitlines() if line and not line.startswith("#")]


def read_tsv(path):
    if not path.exists():
        return []
    return list(csv.DictReader(open(path), delimiter="\t", quoting=csv.QUOTE_NONE))


def merged_header(a, b):
    return ["repo", "rule", "path", "line", a, f"{a}_reason", b, f"{b}_reason", "label", "resolution", "resolved_by"]


def read_merged(path):
    """labels-SPLIT.tsv as merge writes it, or exit with one line if its header is not that."""
    header = open(path).readline().rstrip("\n").split("\t")
    if len(header) != 11 or header != merged_header(header[4], header[6]):
        sys.exit(f"{path.name}: the header is not the one merge writes for two labellers")
    return read_tsv(path)


def read_human(path):
    """A labeller's file, or exit with one line if its header is not the one label writes."""
    if open(path).readline().rstrip("\n").split("\t") != ["repo", "rule", "path", "line", "label", "reason"]:
        sys.exit(f"{path}: the header is not the one label writes")
    return read_tsv(path)


def write_tsv(path, fields, rows):
    tmp = path.with_suffix(".tmp")
    with open(tmp, "w") as f:
        f.write("\t".join(fields) + "\n")
        for r in rows:
            f.write("\t".join(str(r[k]) for k in fields) + "\n")
    os.replace(tmp, path)


def one_line(s):
    return " ".join(s.replace("\t", " ").split())


def safe(s):
    # Repository text: control and format characters (escape sequences, bidi overrides, zero-width
    # characters) are shown as escapes, so they cannot drive the terminal or reorder what is on screen.
    return "".join(c if c == "\t" or unicodedata.category(c) not in ("Cc", "Cf") else f"\\u{ord(c):04x}" for c in s)


def key(r):
    return (r["repo"], r["rule"], r["path"], r["line"])


def clone(clones, repo, sha):
    dest = clones / repo.replace("/", "_")
    git = lambda *a: subprocess.run(["git", "-C", str(dest), *a], check=True, capture_output=True, env=GIT_ENV)
    if not (dest / ".git").exists():
        dest.mkdir(parents=True, exist_ok=True)
        git("init", "-q")
        git("remote", "add", "origin", f"https://github.com/{repo}.git")
    head = subprocess.run(["git", "-C", str(dest), "rev-parse", "-q", "--verify", "HEAD"], capture_output=True, env=GIT_ENV)
    if head.stdout.decode().strip() != sha:
        git("fetch", "-q", "--depth", "1", "origin", sha)
        git("checkout", "-q", "--detach", sha)
    return dest


def scan(name, clones, binary, out_dir=None):
    d = round_dir(name)
    out_dir = Path(out_dir) if out_dir else d
    if out_dir.resolve() == d.resolve() and (d / "test.sha256").exists():
        sys.exit(f"{d.name} is sealed; scan into another directory (OUT) for a re-scan")
    out_dir.mkdir(parents=True, exist_ok=True)
    clones.mkdir(parents=True, exist_ok=True)
    version = subprocess.run([binary, "version"], capture_output=True, text=True).stdout.strip()
    rows, unlisted = [], []
    for _, repo, sha in repos(d):
        dest = clone(clones, repo, sha)
        out = subprocess.run([binary, "scan", "--format", "json", "--timeout", "30m", str(dest)], capture_output=True)
        if out.returncode not in (0, 1):
            sys.exit(f"{repo}: githealthcheck exited {out.returncode}: {out.stderr.decode('utf-8', 'replace')[-500:]}")
        report = json.loads(out.stdout)
        found = [fd for fam in report["families"] for fd in fam.get("findings") or [] if fd["ruleId"] in RULES]
        for fd in found:
            more = UNLISTED.match(fd["message"]) if not fd.get("line") else None
            if more:
                unlisted.append({"repo": repo, "rule": fd["ruleId"], "path": fd["path"], "count": int(more.group(1))})
            else:
                rows.append({"repo": repo, "rule": fd["ruleId"], "path": fd["path"], "line": fd.get("line", 0),
                             "message": one_line(fd["message"])})
        print(f"{repo}: " + ", ".join(f"{r} {sum(f['ruleId'] == r for f in found)}" for r in RULES), file=sys.stderr)
    rows.sort(key=lambda r: (r["rule"], r["repo"], r["path"], int(r["line"])))
    write_tsv(out_dir / "findings.tsv", ["repo", "rule", "path", "line", "message"], rows)
    write_tsv(out_dir / "unlisted.tsv", ["repo", "rule", "path", "count"], unlisted)
    (out_dir / "scanned-with.txt").write_text(version + "\n")
    print(f"{len(rows)} findings listed and {sum(u['count'] for u in unlisted)} counted only, "
          f"in {out_dir}, scanned with {version}")


def stratum_of(unlisted):
    """A function giving a finding's stratum: its repository, or the file, when githealthcheck capped it."""
    capped = {(u["repo"], u["rule"], u["path"]) for u in unlisted}
    return lambda f: (f["repo"], f["rule"], f["path"] if (f["repo"], f["rule"], f["path"]) in capped else "")


def populations(findings, unlisted, of=None):
    """{stratum: findings}, the listed and the counted ones, by the strata of `of` (by default unlisted's)."""
    of, pop = of or stratum_of(unlisted), {}
    for f in findings + unlisted:
        pop[of(f)] = pop.get(of(f), 0) + int(f.get("count") or 1)
    return pop


def sample(name):
    d = round_dir(name)
    if (d / "test.sha256").exists():
        sys.exit(f"{d.name}/test.sha256 exists: the split is sealed, and drawing it again would unseal it")
    findings, unlisted = read_tsv(d / "findings.tsv"), read_tsv(d / "unlisted.tsv")
    keys = [key(f) for f in findings]
    if len(set(keys)) != len(keys):
        sys.exit("findings.tsv has two findings on one line of one file; labels could not tell them apart")
    pop, of = populations(findings, unlisted), stratum_of(unlisted)
    rng = random.Random(SEED)
    drawn, strata = [], []
    for rule in RULES:
        groups = {}
        for f in findings:
            if f["rule"] == rule:
                groups.setdefault(of(f), []).append(f)
        root = {g: math.sqrt(pop[g]) for g in groups}
        for g in sorted(groups):
            n = min(len(groups[g]), max(1, round(PER_RULE * root[g] / sum(root.values()))))
            picked = rng.sample(groups[g], n)
            n_test = max(1, round(n * TEST_SHARE))
            for i, f in enumerate(picked):
                drawn.append({**f, "split": "test" if i < n_test else "dev"})
            strata.append({"repo": g[0], "rule": rule, "file": g[2], "findings": pop[g], "dev": n - n_test, "test": n_test})
    drawn.sort(key=lambda r: (r["split"], r["rule"], r["repo"], r["path"], int(r["line"])))
    write_tsv(d / "sample.tsv", ["repo", "rule", "path", "line", "message", "split"], drawn)
    write_tsv(d / "strata.tsv", ["repo", "rule", "file", "findings", "dev", "test"], strata)
    (d / "test.sha256").write_text(seal(d))
    for rule in RULES:
        n = {s: sum(r["rule"] == rule and r["split"] == s for r in drawn) for s in ("dev", "test")}
        total = sum(s["findings"] for s in strata if s["rule"] == rule)
        print(f"{rule}: {total} findings, drew {n['dev']} dev and {n['test']} test, "
              f"test worth {kish([s for s in strata if s['rule'] == rule], 'test'):.0f} equally weighted findings")


def seal(d):
    return "".join(f"{hashlib.sha256((d / f).read_bytes()).hexdigest()}  {f}\n" for f in SEALED)


def kish(strata, split):
    w = [(int(s["findings"]) / int(s[split]), int(s[split])) for s in strata if int(s[split])]
    return sum(x * n for x, n in w) ** 2 / sum(x * x * n for x, n in w) if w else 0.0


def code(clones, r, sha):
    """The finding's file around its line, from the clone, or from GitHub when there is no clone."""
    text = None
    if clones:
        root = (clones / r["repo"].replace("/", "_")).resolve()
        path = (root / r["path"]).resolve()
        if root in path.parents and not (root / r["path"]).is_symlink() and path.is_file():
            text = path.read_text("utf-8", "replace")
    if text is None:
        url = f"repos/{r['repo']}/contents/{urllib.parse.quote(r['path'])}?ref={sha}"
        out = subprocess.run(["gh", "api", "-H", "Accept: application/vnd.github.raw", url], capture_output=True)
        if out.returncode:
            return f"(could not fetch {safe(r['path'])}: {safe(out.stderr.decode('utf-8', 'replace').strip())})"
        text = out.stdout.decode("utf-8", "replace")
    # Lines as githealthcheck counts them: split at \n only.
    lines, n = [l.removesuffix("\r") for l in text.split("\n")], int(r["line"])
    lo, hi = max(1, n - CONTEXT), min(len(lines), n + CONTEXT)
    return "\n".join(f"{'>' if i == n else ' '}{i:6} {safe(lines[i - 1])}" for i in range(lo, hi + 1))


def shown(name, split):
    d = round_dir(name)
    heads = {repo: sha for _, repo, sha in repos(d)}
    return d, heads, [r for r in read_tsv(d / "sample.tsv") if r["split"] == split]


def show(name, split, clones):
    _, heads, rows = shown(name, split)
    for i, r in enumerate(rows, 1):
        print(f"\n{'=' * 78}\n[{i}] {r['rule']}  {r['repo']}@{heads[r['repo']][:12]}  {safe(r['path'])}:{r['line']}")
        print(f"finding: {safe(r['message'])}\n{'-' * 78}\n{code(clones, r, heads[r['repo']])}")


def check_name(name):
    if not NAME.match(name) or name in RESERVED:
        sys.exit(f"{name!r}: a name is lowercase letters, digits and -, for example kadirhan, and not one of {sorted(RESERVED)}")


def label(name, split, who, clones):
    check_name(who)
    d, heads, rows = shown(name, split)
    (d / "human").mkdir(exist_ok=True)
    path = d / "human" / f"{who}-{split}.tsv"
    done = {key(r) for r in read_tsv(path)}
    todo = [r for r in rows if key(r) not in done]
    random.Random(f"{name}/{split}/{who}").shuffle(todo)
    if not path.exists():
        path.write_text("repo\trule\tpath\tline\tlabel\treason\n")
    print(f"{len(done)} of {len(rows)} labelled. Read GUIDELINE.md first. Answers: t = true, f = false, "
          "s = skip for now, q = quit.")
    for i, r in enumerate(todo, len(done) + 1):
        print(f"\n{'=' * 78}\n{r['rule']}  {r['repo']}  {safe(r['path'])}:{r['line']}\nfinding: {safe(r['message'])}\n{'-' * 78}")
        print(code(clones, r, heads[r["repo"]]))
        while (a := input(f"\n[{i}/{len(rows)}] t / f / s / q: ").strip().lower()) not in ("t", "f", "s", "q"):
            pass
        if a == "q":
            return
        if a == "s":
            continue
        reason = ""
        while not reason:
            reason = one_line(input("one sentence: why (start with \"undecidable:\" if the code cannot tell): "))
        with open(path, "a") as f:
            f.write(f"{r['repo']}\t{r['rule']}\t{r['path']}\t{r['line']}\t{'true' if a == 't' else 'false'}\t{reason}\n")
    print(f"done: {path.relative_to(ROOT)}")


def merge(name, split, a, b):
    check_name(a)
    check_name(b)
    if a == b:
        sys.exit("merge needs two different labellers")
    d, _, rows = shown(name, split)
    out_path = d / f"labels-{split}.tsv"
    if out_path.exists() and any(r["resolution"] for r in read_merged(out_path)):
        sys.exit(f"{out_path.name} already holds resolutions; move it away first if they should be redone")
    for w in (a, b):
        if not (d / "human" / f"{w}-{split}.tsv").exists():
            sys.exit(f"human/{w}-{split}.tsv: no such file; {w} has not labelled the {split} split")
    la, lb = ({key(r): r for r in read_human(d / "human" / f"{w}-{split}.tsv")} for w in (a, b))
    missing = [r for r in rows if key(r) not in la or key(r) not in lb]
    if missing:
        sys.exit(f"{len(missing)} findings are not labelled by both yet, for example {key(missing[0])}")
    for w, labels in ((a, la), (b, lb)):
        bad = [k for k, r in labels.items() if r["label"] not in LABELS]
        if bad:
            sys.exit(f"{w}: label {labels[bad[0]]['label']!r} for {bad[0]}; labels are true or false")
    out, agree = [], 0
    for r in rows:
        x, y = la[key(r)], lb[key(r)]
        same = x["label"] == y["label"]
        agree += same
        out.append({"repo": r["repo"], "rule": r["rule"], "path": r["path"], "line": r["line"],
                    a: x["label"], f"{a}_reason": x["reason"], b: y["label"], f"{b}_reason": y["reason"],
                    "label": x["label"] if same else "", "resolution": "", "resolved_by": ""})
    write_tsv(out_path, ["repo", "rule", "path", "line", a, f"{a}_reason", b, f"{b}_reason", "label", "resolution", "resolved_by"], out)
    print(f"agreed on {agree} of {len(rows)}; a third person runs resolve for the other {len(rows) - agree}")


def resolve(name, split, who, clones):
    check_name(who)
    d, heads, _ = shown(name, split)
    path = d / f"labels-{split}.tsv"
    if not path.exists():
        sys.exit(f"{path.name}: run merge first")
    rows = read_merged(path)
    if not rows:
        sys.exit(f"{path.name} is empty; run merge first")
    fields = list(rows[0].keys())
    a, b = fields[4], fields[6]
    if who in (a, b):
        sys.exit(f"{who} labelled these findings; a third person settles the disagreements between {a} and {b}")
    for r in rows:
        if r["label"]:
            continue
        print(f"\n{'=' * 78}\n{r['rule']}  {r['repo']}  {safe(r['path'])}:{r['line']}\n{'-' * 78}")
        print(code(clones, r, heads[r["repo"]]))
        print(f"\n{a}: {r[a]}, {safe(r[a + '_reason'])}\n{b}: {r[b]}, {safe(r[b + '_reason'])}")
        while (x := input("label t / f, or q: ").strip().lower()) not in ("t", "f", "q"):
            pass
        if x == "q":
            break
        reason = ""
        while not reason:
            reason = one_line(input("why: "))
        r["label"], r["resolution"], r["resolved_by"] = ("true" if x == "t" else "false"), reason, who
        write_tsv(path, fields, rows)
    left = sum(not r["label"] for r in rows)
    print(f"{left} disagreements left" if left else "all resolved")


def wilson(p, n, z=Z90):
    if n == 0:
        return float("nan"), float("nan")
    centre, half = p + z * z / (2 * n), z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n))
    return (centre - half) / (1 + z * z / n), (centre + half) / (1 + z * z / n)


def checked_labels(d, split):
    """labels-SPLIT.tsv, refused unless it labels exactly the sampled findings, each by the process."""
    if not (d / "test.sha256").exists():
        sys.exit(f"{d.name}/test.sha256: no such file; the round is not sealed, run sample first")
    try:
        sealed = (d / "test.sha256").read_text() == seal(d)
    except FileNotFoundError as e:
        sys.exit(f"{e.filename}: no such file; the round's files do not match test.sha256")
    if not sealed:
        sys.exit("the round's files do not match test.sha256: the sealed sample was changed")
    if not (d / f"labels-{split}.tsv").exists():
        sys.exit(f"labels-{split}.tsv: run merge first")
    labels = read_merged(d / f"labels-{split}.tsv")
    if not labels:
        sys.exit(f"labels-{split}.tsv: run merge first")
    header = open(d / f"labels-{split}.tsv").readline().rstrip("\n").split("\t")
    a, b = header[4], header[6]
    if (a == b or not NAME.match(a) or not NAME.match(b) or {a, b} & RESERVED):
        sys.exit(f"labels-{split}.tsv: the header is not the one merge writes for two labellers")
    sampled = sorted(key(r) for r in read_tsv(d / "sample.tsv") if r["split"] == split)
    if sorted(key(r) for r in labels) != sampled:
        sys.exit(f"labels-{split}.tsv does not hold exactly the {split} split's findings")
    for r in labels:
        if r[a] not in LABELS or r[b] not in LABELS or r["label"] not in LABELS:
            sys.exit(f"{key(r)}: not labelled true or false by both and settled; run resolve")
        if r[a] == r[b] and (r["label"] != r[a] or r["resolution"]):
            sys.exit(f"{key(r)}: {a} and {b} agree, but the label or a resolution says otherwise")
        if r[a] != r[b] and (not r["resolution"] or r["resolved_by"] in (a, b) or not NAME.match(r["resolved_by"])):
            sys.exit(f"{key(r)}: a disagreement not settled by a third person named in resolved_by")
    return labels, a, b


def revised_keys(d, revised):
    """The sealed findings a re-scan in directory REVISED still reports; refused if it adds any."""
    r = Path(revised)
    sealed, sealed_unlisted = read_tsv(d / "findings.tsv"), read_tsv(d / "unlisted.tsv")
    new, new_unlisted = read_tsv(r / "findings.tsv"), read_tsv(r / "unlisted.tsv")
    of, known = stratum_of(sealed_unlisted), {key(f) for f in sealed}
    old_pop, new_pop = populations(sealed, sealed_unlisted), populations(new, new_unlisted, of)
    for f in new:
        # A finding new to the list is allowed only in a capped file, where it was counted before.
        if key(f) not in known and not of(f)[2]:
            sys.exit(f"{revised}: {key(f)} is not in the sealed population; a revision that adds findings needs a new round")
    for g, n in new_pop.items():
        if n > old_pop.get(g, 0):
            sys.exit(f"{revised}: {g} has {n} findings, the sealed round {old_pop.get(g, 0)}; a revision that adds findings needs a new round")
    return {key(f) for f in new}


def score(name, split, revised=None):
    d = round_dir(name)
    labels, a, b = checked_labels(d, split)
    if revised:
        # A removed finding leaves the sample; the others keep the weight they were drawn with, since
        # removing findings does not change how likely each was to be drawn.
        still = revised_keys(d, revised)
        labels = [r for r in labels if key(r) in still]
    of = stratum_of(read_tsv(d / "unlisted.tsv"))
    weight = {(s["repo"], s["rule"], s["file"]): int(s["findings"]) / int(s[split])
              for s in read_tsv(d / "strata.tsv") if int(s[split])}
    for rule in RULES:
        rows = [r for r in labels if r["rule"] == rule]
        if not rows:
            continue
        w = [weight[of(r)] for r in rows]
        p = sum(x for x, r in zip(w, rows) if r["label"] == "true") / sum(w)
        n_eff = sum(w) ** 2 / sum(x * x for x in w)
        lo, hi = wilson(p, n_eff)
        k = sum(r["label"] == "true" for r in rows)
        undecidable = sum(r["label"] == "false" and any(t.lower().startswith("undecidable:") for t in
                          ([r["resolution"]] if r["resolution"] else [r[a + "_reason"], r[b + "_reason"]])) for r in rows)
        agree = sum(r[a] == r[b] for r in rows) / len(rows)
        print(f"{rule} ({split}{', revised' if revised else ''}): {k} of {len(rows)} true from {len({r['repo'] for r in rows})} repositories; "
              f"weighted precision {p:.3f}, 90% {lo:.3f}-{hi:.3f} (Wilson on {n_eff:.1f} effective findings); "
              f"{undecidable} undecidable; {a} and {b} agreed on {agree:.0%}; {'meets' if lo >= BAR else 'below'} the {BAR} bar")


if __name__ == "__main__":
    args = sys.argv[1:]
    clones = Path(os.environ["PRECISION_CLONES"]) if os.environ.get("PRECISION_CLONES") else None
    cmds = {
        ("scan", 4): lambda: scan(args[1], Path(args[2]), args[3]),
        ("scan", 5): lambda: scan(args[1], Path(args[2]), args[3], args[4]),
        ("sample", 2): lambda: sample(args[1]),
        ("show", 3): lambda: show(args[1], args[2], clones),
        ("label", 4): lambda: label(args[1], args[2], args[3], clones),
        ("merge", 5): lambda: merge(args[1], args[2], args[3], args[4]),
        ("resolve", 4): lambda: resolve(args[1], args[2], args[3], clones),
        ("score", 3): lambda: score(args[1], args[2]),
        ("score", 4): lambda: score(args[1], args[2], args[3]),
    }
    run = cmds.get((args[0], len(args))) if args else None
    if run is None or (len(args) > 2 and args[0] in ("show", "label", "merge", "resolve", "score") and args[2] not in ("dev", "test")):
        sys.exit(__doc__)
    run()
