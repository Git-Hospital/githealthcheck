# GHC-HIST-002: file whose authors have all stopped committing

**Family:** history. **Languages:** all recognised languages. **Needs:** the full git history, at
least 50 commits.

Every author of the file made their last commit at least a year before the repository's latest commit.
Nobody who knows the file is still working on the project.

## Threshold and source

365 days. A developer has abandoned a project when "their last commit occurred at least one year before
the most recent repository commit" (Avelino, Constantinou, Valente and Serebrenik, "On the abandonment
and survival of open source projects: An empirical investigation", ESEM 2019,
<https://arxiv.org/abs/1906.08058>). They tested five thresholds and chose one year as the least
sensitive to error.

The authors of a file are its DOA authors, as in [GHC-HIST-001](GHC-HIST-001.md#threshold-and-source). A
developer's last commit is their latest non-merge commit on any file, by author date; the latest
commit is the latest author date in the history, not today, so a report on an old commit says the same
thing whenever it is made. A file without an author is not a finding. A commit whose author time git
cannot print (a corrupt or crafted object) still counts for authorship but not for dates, and a file
with an author whose every commit is like that is not judged.

## Input

The same history, files and exclusions as [GHC-HIST-001](GHC-HIST-001.md#input).

`abandoned_file_share` is the share of files with at least one author that are findings.

## Fix

Have a current developer take the file over: their commits to it make them an author (reviews do not
count, only commits). Delete the file if it is dead code. Suppression with a reason (D21) is not
implemented yet.
