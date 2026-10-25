# Calibration

A family score says where a repository stands among well-known projects in its language (ADR 0001,
D18 to D20): 7/10 is about the 70th percentile of the corpus. The corpus, the commit of each
repository and every value measured on it are locked in [`calibration/corpus.lock`](../calibration/corpus.lock),
which `githealthcheck calibrate` writes from [`calibration/corpus.txt`](../calibration/corpus.txt).

## The corpus

Fifteen repositories per analysed language, picked by hand against these criteria:

- actively maintained: a commit in the last six months when the list was made;
- at least 1,000 stars, and not a fork;
- not mostly generated or vendored code;
- a mix of sizes (small, medium, large) and kinds (library, command-line tool, service).

| | Go | Python |
|---|---|---|
| small | go-chi/chi, urfave/cli, stretchr/testify, charmbracelet/bubbletea, spf13/cobra | python-attrs/attrs, pallets/click, fastapi/typer, encode/httpx, psf/requests |
| medium | gin-gonic/gin, junegunn/fzf, caddyserver/caddy, golangci/golangci-lint, cli/cli | pallets/flask, psf/black, Textualize/rich, fastapi/fastapi, scrapy/scrapy, pytest-dev/pytest |
| large | prometheus/prometheus, etcd-io/etcd, gohugoio/hugo, grpc/grpc-go, traefik/traefik | django/django, sqlalchemy/sqlalchemy, pydantic/pydantic, celery/celery |

JS/TS joins with its own fifteen when it is analysed. Changing the list is a reviewed PR that shows the
distributions before and after.

## From metrics to scores

- **A repository's language** is its analysed language with the most code lines. Its metrics are
  compared with that language's corpus only. A repository whose analysed lines are under half of its
  code lines has no family scores (`low_analysed_share`): its score would describe a minority of it.
  In a repository with both Go and Python, the metrics cover both and are compared with the corpus of
  the larger one; scoring each language on its own is left for later.
- **A metric's score** is its percentile among the corpus values of the language, from 0 to 10. With
  the corpus values sorted from worst to best, the percentile is the position of the last value the
  repository is at least as good as, over n−1, linear between two corpus values. A tie counts in the
  repository's favour: a repository with no untested package scores 10 even when half the corpus has
  none either. A value past the best corpus value scores 10, one worse than every corpus value 0. A
  metric is scored only when at least 10 corpus repositories of the language measured it.
- **Which way is better.** Less is better for the shares (complex, long, single-author, abandoned
  files, untested packages, duplicated lines), the mean cyclomatic complexity and the smells per
  1000 lines; more is better for the truck factor, an absolute count (one person leaving is the same
  risk in a small project as in a large one), and the test-to-code line ratio.
- **Minimum input.** A metric below its minimum input (20 functions, 1000 code lines, 5 Go packages
  or 50 commits, by metric: architecture §5) is `null` and not scored, so a small repository does not
  score 10 on a density computed from a few lines.
- **Statement coverage** is not calibrated, since the corpus is scanned without running its tests:
  X% coverage scores X/10.
- **A family's score** is the mean of its metrics' scores, over the metrics that have a score. The
  report lists each one under `metricScores`. A measured family with no scored metric is null with
  the reason `not_calibrated`.
- **The overall score** is the mean of the family scores, with equal weights, over the families
  that have a score; it is null (`insufficient_families`) with fewer than two.

The weights and the method are fixed for a schema version, and `.githealthcheck.yml` cannot change
them (D21). The report's `overall.calibration` names the language a repository was scored as, the
share of its code that was analysed, the number of corpus repositories in the language and the
githealthcheck version that measured them.

## Reproducing the lock

`githealthcheck calibrate` clones each repository of `corpus.txt` without blobs (`--filter=blob:none`:
the full history, and the file contents of the one commit checked out), checks out its commit, scans
it, deletes the clone and writes `corpus.lock`: the githealthcheck version, the python3 version, each
repository's commit and metrics, and the sorted values per language and metric. A repository that is
not scanned as its listed language, with at least half its code analysed, stops the run. The clones
use HTTPS only, without the user's or the system's git config, so no credential helper, filter (such
as Git LFS) or hooks path applies; the code is parsed, never run.

`githealthcheck calibrate --check` measures again and compares with the lock, metric by metric. The
[Calibration](../.github/workflows/calibration.yml) workflow runs it on ubuntu-24.04, whose python3
3.12.3 is the version the lock is measured with; it fails when anything differs. The lock is
measured locally with the same python3 version, so that the check can match it.
