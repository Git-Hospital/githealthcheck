# GHC-HIST-001: truck factor of one

**Family:** history. **Languages:** all recognised languages. **Needs:** the full git history, at
least 50 commits.

The truck factor is the smallest number of developers whose departure leaves more than half of the
files without an author. When it is one, a single developer authors so much of the code that without
them under half of it has anyone who knows it, or, after pruning (below), that the others whose
departure it would take each author under a tenth as many files as they do. The finding says which.

## Threshold and source

A truck factor of exactly one. The truck factor is that of Avelino, Passos, Hora and Valente, "A Novel
Approach for Estimating Truck Factors", ICPC 2016 (<https://arxiv.org/abs/1604.06766>), computed as
their tool computes it (<https://github.com/aserg-ufmg/Truck-Factor>, `config.properties`):

- **Degree of authorship** of developer d on file f: `DOA = 3.293 + 1.098*FA + 0.164*DL - 0.321*ln(1 + AC)`.
  FA is 1 when d added the file, DL counts d's modifications and renames of it, and AC counts everyone
  else's. A rename carries the history of the old path, and of the paths before it. Only exact renames
  are followed (see Input).
- **Author:** d's DOA divided by the file's highest DOA is above 0.75, and d's DOA is at least 3.293 or d
  added the file. The paper requires the DOA of 3.293 from every author; the tool also accepts whoever
  added the file, and so does githealthcheck. When the file was added more than once, by different
  developers, each of them counts as having added it.
- **Truck factor:** while at least half of the files have an author, remove the developer who authors
  the most files (ties: more adds, modifications and renames over all files; then the smaller
  developer key). The number removed is the truck factor, less those who author under 10% as many files
  as the top one.
- **Developers:** commits with the same email, after the `.mailmap`, are one developer, and so are
  commits with the same name; both compare ignoring case, and the groups chain. This is the tool's
  alias step. An author with neither name nor email is one developer. The paper also joins names within a Levenshtein distance of one; the tool does that only in
  a separate utility that writes alias lists, and githealthcheck joins equal names only.

githealthcheck gives the same truck factor, coverage and developer file counts as the tool on spf13/cobra,
sirupsen/logrus, junegunn/fzf, pallets/flask, go-yaml/yaml and aserg-ufmg/Truck-Factor, given the same
commits and files (`scripts/tf-oracle.sh`), with one fix to the tool (`scripts/tf-oracle.patch`): of two
files that share a rename chain, the tool lets only the first in path order see the older history.
Without the fix the tools differ on Flask by one file. The patch also makes the tool follow exact renames
only, as githealthcheck does; following similar renames too, the unpatched tool gets the same truck
factors on these six, with coverage up to two points apart and up to eight files more or fewer for a
TF developer (Flask: 27 against 19).
Three smaller differences remain: the tool strips accents from names and emails before it groups
developers, uppercases with Java's full case mapping (ß becomes SS, where Go keeps ß), and keeps the
commits of bots.

## Input

`git log --no-merges --find-renames=100% --name-status` from HEAD, run by githealthcheck with git's
mailmap support off. Only exact renames are detected: a file moved with its content changed in the same
commit is a delete and an add, and its new path starts a new history. Scoring a similar rename makes git
inflate both blobs in full, so a repository of a few hundred kilobytes could make it allocate gigabytes;
the `.mailmap` at the root of the work tree is read and applied by githealthcheck itself (a symlink or
FIFO there is skipped and listed). Commits by GitHub Apps (an author name or email user that ends in
`[bot]`) are left out; other bots can be given such a name in `.mailmap`. The files are the source files
of HEAD in a recognised language, not vendored or generated, that some commit added, modified or renamed
into; a file that only ever arrived as a copy has no history.

The family is `null` with the reason `history_too_short` when the clone is shallow or the history has
fewer than 50 commits by people; `too_large` when `git log` prints more than 512 MiB or more than 20
million changed paths, or when following rename chains would read more than 50 million history entries
(files that share an ancestor each read its history); and `tool_crashed` when `git log` fails, for
example on a missing object. The other families still report.

The finding names no developer: reports are shared, and the history already says who it is.

## Fix

Spread knowledge of the code: commits by other developers raise their degree of authorship (reviews
do not count, only commits). Suppression with a reason (D21) is not implemented yet.
