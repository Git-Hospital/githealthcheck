# GHC-CPLX-003: control flow nested more than 4 deep

**Family:** complexity. **Languages:** Go, Python. **Needs:** nothing beyond the commit; for Python, python3 3.10 or later on `PATH`.

A function nests `if`, `for`, `switch` or `select` statements more than 4 deep.

## Threshold and source

Over 4, the default of SonarSource's go:S134, "Control flow statements should not be nested too
deeply" (sonar-go `TooDeeplyNestedStatementsCheck`). An `else if` continues its `if` and does not nest;
a statement inside an `else` block does. Statements inside a function literal count from the depth of
the literal; a literal in a statement's header (an `if` condition, a `for` clause, a `range`
expression, a `switch` tag) sits at the depth of that statement, as in cognitive complexity.

## Python

`if`, `for`, `while`, `try` and `with` nest, as in SonarSource's python:S134 (default 4): an `elif`,
`else`, `except` or `finally` continues its statement and does not nest. `match` does not count.

## What is left out

As in [GHC-CPLX-001](GHC-CPLX-001.md#what-is-left-out), with the same bound on findings per file.

## Fix

Return early, or move the inner loop into a function of its own.
