#!/usr/bin/env python3
"""Draw the labelled set for the fix rule (ADR 0002, E3) from bare clones.

Usage: python3 -I scripts/fixrule-sample.py CLONES_DIR testdata/fixrule

CLONES_DIR holds one bare clone per repository listed in testdata/fixrule/repos.txt,
named owner_name.git, cloned with --filter=blob:none --shallow-since=<window start>.
Each line of repos.txt is "owner/name <HEAD sha> <language>".

Per repository it draws 15 commits the v0 rule matches and 15 it does not, from the
non-merge commits by people in the window that touch a source file, with a fixed seed.
Of each 15, 5 go to the dev split and 10 to the test split. It writes sample.tsv,
strata.tsv (population sizes, for the weights) and test.sha256 (the test split's
commitment, made before any label exists).
"""

import hashlib
import random
import re
import subprocess
import sys
from pathlib import Path

SEED = 20261028
WINDOW_START = "2023-11-28"  # a month after the shallow boundary, so no boundary commit is drawn
PER_STRATUM, DEV_PER_STRATUM = 15, 5

# The experiment's rule, frozen as v0 (ADR 0002, "The experiment").
FIX = re.compile(r"\b(fix(e[sd])?|bug|bugfix|hotfix|regression|crash(es)?|broken|panic|revert)\b", re.I)
NOT_FIX = re.compile(r"\b(typo|docs?|readme|changelog|lint|format(ting)?|ci|spelling|comment|tests?)\b", re.I)

VENDORED = re.compile(r"(^|/)(vendor|third_party|node_modules|_vendor)/")


def v0(subject):
    return bool(FIX.search(subject)) and not NOT_FIX.search(subject)


def is_source(path, lang):
    if VENDORED.search(path):
        return False
    name = path.rsplit("/", 1)[-1]
    if lang == "go":
        return name.endswith(".go") and not name.endswith("_test.go")
    if lang == "python":
        if not name.endswith(".py") or name.startswith("test_") or name.endswith("_test.py") or name == "conftest.py":
            return False
        return not re.search(r"(^|/)tests?/", path)
    raise ValueError(lang)


def git(repo, *args):
    env = {"GIT_NO_LAZY_FETCH": "1", "PATH": "/usr/bin:/bin:/opt/homebrew/bin:/usr/local/bin"}
    return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, env=env).stdout


def population(repo, head, lang):
    """Commits in the window, by people, that touch a source file: [(sha, subject)]."""
    out = git(repo, "log", "--no-merges", "--no-renames", f"--since={WINDOW_START}",
              "--format=%x00%H%x1f%an%x1f%s", "--name-only", head).decode("utf-8", "replace")
    shallow = set((Path(repo) / "shallow").read_text().split()) if (Path(repo) / "shallow").exists() else set()
    commits = []
    for block in out.split("\x00")[1:]:
        header, _, files = block.partition("\n")
        sha, author, subject = header.split("\x1f", 2)
        if sha in shallow or author.endswith("[bot]"):
            continue
        if any(is_source(f, lang) for f in files.split("\n") if f):
            commits.append((sha, subject))
    return commits


def main(clones, out):
    repos = [line.split() for line in (out / "repos.txt").read_text().splitlines() if line and not line.startswith("#")]
    rng = random.Random(SEED)
    sample, strata = [], []
    for name, head, lang in repos:
        commits = population(clones / (name.replace("/", "_") + ".git"), head, lang)
        for stratum, members in (("matched", [c for c in commits if v0(c[1])]), ("unmatched", [c for c in commits if not v0(c[1])])):
            if len(members) < PER_STRATUM:
                sys.exit(f"{name}: only {len(members)} {stratum} commits")
            strata.append((name, stratum, len(members)))
            drawn = rng.sample(sorted(members), PER_STRATUM)
            for i, (sha, _) in enumerate(drawn):
                sample.append((name, sha, stratum, "dev" if i < DEV_PER_STRATUM else "test"))
    (out / "sample.tsv").write_text("repo\tcommit\tstratum\tsplit\n" + "".join("\t".join(r) + "\n" for r in sample))
    (out / "strata.tsv").write_text("repo\tstratum\tcommits\n" + "".join(f"{r}\t{s}\t{n}\n" for r, s, n in strata))
    test = "".join(f"{r}\t{c}\n" for r, c, _, split in sample if split == "test")
    (out / "test.sha256").write_text(hashlib.sha256(test.encode()).hexdigest() + "  test commits (repo<TAB>commit, one per line, in sample.tsv order)\n")


if __name__ == "__main__":
    main(Path(sys.argv[1]), Path(sys.argv[2]))
