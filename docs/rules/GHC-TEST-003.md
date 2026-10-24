# GHC-TEST-003: file with no statement covered

**Family:** tests. **Languages:** Go. **Needs:** a fresh coverage report.

A file has at least one statement in the coverage report, and none of them ran in the tests.

## Threshold

0%: every statement of the file is uncovered. A file can be a finding while its package is above 60%
(GHC-TEST-002), and a package at 0% gives both findings.

## Input

The same report, with the same freshness checks and exclusions, as
[GHC-TEST-002](GHC-TEST-002.md#input).

## Fix

Exercise the file from a test, or delete it if it is dead code. Suppression with a reason (D21) is not
implemented yet.
