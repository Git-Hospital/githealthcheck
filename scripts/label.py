#!/usr/bin/env python3
"""Label a sealed split by hand, blind, and merge two people's labels.

Usage:
  python3 -I scripts/label.py fixrule test label NAME     label the test commits as NAME
  python3 -I scripts/label.py fixrule test merge A B      write labels-test-human.tsv from A's and B's labels
  python3 -I scripts/label.py fixrule test resolve NAME   settle the disagreements in labels-test-human.tsv as NAME

`label` shows one commit at a time: its repository, hash, subject, message and diff (vendored and
generated files left out, cut at 400 lines), as GUIDELINE.md says. It never reads the models' labels,
the rule's verdicts or the other person's file. The order is shuffled per labeller. Each answer is
written at once to testdata/fixrule/human/NAME.tsv, so a session can stop and resume with the commits
still unlabelled.

`resolve` is for a third person: it refuses the two whose labels it settles, and writes the
resolver's name beside each label it settles.

Diffs come from the GitHub API through `gh`, which must be logged in. The commit text is written by
the repository's authors: control and format characters (escape sequences, bidi overrides, zero-width
characters) are shown as escapes, so it cannot drive the terminal or reorder what is on screen.
"""

import csv
import os
import random
import re
import subprocess
import sys
import unicodedata
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DIR = ROOT / "testdata" / "fixrule"
HUMAN = DIR / "human"
MAX_DIFF = 400
NAME = re.compile(r"^[a-z][a-z0-9-]{0,31}$")
RESERVED = {"repo", "commit", "stratum", "label", "resolution", "split", "reason"}
NOT_READ = re.compile(r"(^|/)(vendor|third_party|node_modules)/|\.pb\.go$|_generated\.|\.generated\.|"
                      r"(^|/)(go\.sum|package-lock\.json|yarn\.lock|poetry\.lock|uv\.lock)$")
FIELDS = ["repo", "commit", "label", "reason"]


def safe(s):
    # Cc are the C0 and C1 controls, ESC among them; Cf are the format characters: bidi embeddings,
    # overrides and isolates, zero-width characters and the BOM. Tabs stay.
    return "".join(c if c == "\t" or unicodedata.category(c) not in ("Cc", "Cf") else f"\\u{ord(c):04x}" for c in s)


def check_name(name):
    if not NAME.match(name) or name in RESERVED:
        sys.exit(f"{name!r}: a name is lowercase letters, digits and -, for example kadirhan, and not one of {sorted(RESERVED)}")


def split_rows(split):
    return [r for r in csv.DictReader(open(DIR / "sample.tsv"), delimiter="\t") if r["split"] == split]


def gh(*args):
    try:
        return subprocess.run(["gh", "api", *args], check=True, capture_output=True).stdout.decode("utf-8", "replace")
    except FileNotFoundError:
        sys.exit("the diffs come through the GitHub CLI: install gh (https://cli.github.com) and run gh auth login")


def show(repo, commit):
    import json
    meta = json.loads(gh(f"repos/{repo}/commits/{commit}"))
    message = meta["commit"]["message"].strip()
    raw = gh("-H", "Accept: application/vnd.github.diff", f"repos/{repo}/commits/{commit}")
    kept, skipped, keep = [], [], True
    for line in raw.splitlines():
        if line.startswith("diff --git "):
            path = line.split(" b/", 1)[-1]
            keep = not NOT_READ.search(path)
            if not keep:
                skipped.append(path)
        if keep:
            kept.append(line)
    out = [f"\n{'=' * 78}", f"{repo}  {commit[:12]}", ""] + [safe(l) for l in message.splitlines()] + ["", "-" * 78]
    out += [safe(l) for l in kept[:MAX_DIFF]]
    if len(kept) > MAX_DIFF:
        out.append(f"... diff cut at {MAX_DIFF} of {len(kept)} lines")
    if skipped:
        out.append(f"... left out as vendored or generated: {', '.join(safe(p) for p in skipped[:10])}")
    print("\n".join(out))


def read_labels(path):
    if not path.exists():
        return {}
    return {(r["repo"], r["commit"]): r for r in csv.DictReader(open(path), delimiter="\t", quoting=csv.QUOTE_NONE)}


def one_line(s):
    return " ".join(s.replace("\t", " ").split())


def write_atomic(path, text):
    tmp = path.with_suffix(".tmp")
    tmp.write_text(text)
    os.replace(tmp, path)


def label(split, name):
    check_name(name)
    HUMAN.mkdir(exist_ok=True)
    path = HUMAN / f"{name}.tsv"
    done = read_labels(path)
    todo = [r for r in split_rows(split) if (r["repo"], r["commit"]) not in done]
    random.Random(f"{split}/{name}").shuffle(todo)
    if not path.exists():
        path.write_text("\t".join(FIELDS) + "\n")
    total = len(done) + len(todo)
    print(f"{len(done)} of {total} labelled. Read GUIDELINE.md first. Answers: f = fix, n = not_fix, "
          "u = unsure (then f or n), s = skip for now, q = quit.")
    for i, r in enumerate(todo, len(done) + 1):
        try:
            show(r["repo"], r["commit"])
        except subprocess.CalledProcessError as e:
            print(f"\ncould not fetch {r['repo']} {r['commit'][:12]} through gh: {safe(e.stderr.decode('utf-8', 'replace').strip())}")
            if input("s = skip it for now, q = quit: ").strip().lower() == "q":
                return
            continue
        while True:
            a = input(f"\n[{i}/{total}] f / n / u / s / q: ").strip().lower()
            if a in ("f", "n", "s", "q"):
                break
            if a == "u":
                b = input("more likely f or n: ").strip().lower()
                if b in ("f", "n"):
                    a = "u" + b
                    break
        if a == "q":
            return
        if a == "s":
            continue
        reason = ""
        while not reason:
            reason = one_line(input("one sentence: what was wrong, or why it is not a fix: "))
        if a.startswith("u"):
            reason = "unsure: " + reason
        with open(path, "a") as f:
            f.write(f"{r['repo']}\t{r['commit']}\t{'fix' if a.endswith('f') else 'not_fix'}\t{reason}\n")
    left = len(split_rows(split)) - len(read_labels(path))
    print(f"{path.relative_to(ROOT)}: {left} commits left" if left else f"all labelled: {path.relative_to(ROOT)}")


def merge(split, a, b):
    check_name(a)
    check_name(b)
    if a == b:
        sys.exit("merge needs two different people")
    out_path = DIR / f"labels-{split}-human.tsv"
    if out_path.exists() and any(r["resolution"] for r in read_labels(out_path).values()):
        sys.exit(f"{out_path.name} already holds resolutions; move it away first if they should be redone")
    la, lb = read_labels(HUMAN / f"{a}.tsv"), read_labels(HUMAN / f"{b}.tsv")
    rows = split_rows(split)
    missing = [r["commit"][:12] for r in rows if (r["repo"], r["commit"]) not in la or (r["repo"], r["commit"]) not in lb]
    if missing:
        sys.exit(f"{len(missing)} commits are not labelled by both yet, for example {missing[0]}")
    out = [f"repo\tcommit\tstratum\t{a}\t{a}_reason\t{b}\t{b}_reason\tlabel\tresolution\tresolved_by\n"]
    agree = 0
    for r in rows:
        x, y = la[(r["repo"], r["commit"])], lb[(r["repo"], r["commit"])]
        same = x["label"] == y["label"]
        agree += same
        out.append(f"{r['repo']}\t{r['commit']}\t{r['stratum']}\t{x['label']}\t{x['reason']}\t{y['label']}\t{y['reason']}\t"
                   f"{x['label'] if same else ''}\t\t\n")
    write_atomic(out_path, "".join(out))
    print(f"agreed on {agree} of {len(rows)}; a third person runs resolve for the other {len(rows) - agree}")


def resolve(split, name):
    check_name(name)
    path = DIR / f"labels-{split}-human.tsv"
    rows = list(csv.DictReader(open(path), delimiter="\t", quoting=csv.QUOTE_NONE))
    if not rows:
        sys.exit(f"{path.name} is empty; run merge first")
    fields = list(rows[0].keys())
    a, b = fields[3], fields[5]
    if name in (a, b):
        sys.exit(f"{name} labelled these commits; a third person settles the disagreements between {a} and {b}")
    if "resolved_by" not in fields:
        fields.append("resolved_by")
        for r in rows:
            r["resolved_by"] = ""
    for r in rows:
        if r["label"]:
            continue
        try:
            show(r["repo"], r["commit"])
        except subprocess.CalledProcessError:
            print(f"\n{r['repo']} {r['commit'][:12]}: the diff could not be fetched; judge from the reasons")
        print(f"\n{a}: {r[a]}, {safe(r[a + '_reason'])}\n{b}: {r[b]}, {safe(r[b + '_reason'])}")
        while (x := input("label f / n, or q: ").strip().lower()) not in ("f", "n", "q"):
            pass
        if x == "q":
            break
        reason = ""
        while not reason:
            reason = one_line(input("why: "))
        r["label"], r["resolution"], r["resolved_by"] = ("fix" if x == "f" else "not_fix"), reason, name
        write_atomic(path, "\t".join(fields) + "\n" + "".join("\t".join(row[k] for k in fields) + "\n" for row in rows))
    left = sum(not r["label"] for r in rows)
    print(f"{left} disagreements left" if left else "all resolved")


if __name__ == "__main__":
    args = sys.argv[1:]
    if len(args) >= 3 and args[0] == "fixrule" and args[1] in ("dev", "test"):
        if args[2] == "label" and len(args) == 4:
            label(args[1], args[3])
        elif args[2] == "merge" and len(args) == 5:
            merge(args[1], args[3], args[4])
        elif args[2] == "resolve" and len(args) == 4:
            resolve(args[1], args[3])
        else:
            sys.exit(__doc__)
    else:
        sys.exit(__doc__)
