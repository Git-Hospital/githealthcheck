# GHC-CPLX-003: control flow nested more than 4 deep

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

A function nests `if`, `for`, `switch` or `select` statements more than 4 deep.

## Threshold and source

Over 4, the default of SonarSource's go:S134, "Control flow statements should not be nested too
deeply" (sonar-go `TooDeeplyNestedStatementsCheck`). An `else if` continues its `if` and does not nest;
a statement inside an `else` block does. Statements inside a function literal count from the depth of
the literal.

## What is left out

As in [GHC-CPLX-001](GHC-CPLX-001.md#what-is-left-out).

## Fix

Return early, or move the inner loop into a function of its own.
