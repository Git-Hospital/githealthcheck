# GHC-CPLX-010: function with the body of another

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

A function's body is that of another function in the repository, up to the names of its own
parameters, results and locals. The two do the same thing under two names.

## Threshold and source

A body of at least two statements, as SonarSource's S4144, "Functions should not have identical
implementations" (sonar-go `DuplicatedFunctionImplementationCheck.java`, `MINIMUM_STATEMENTS_COUNT =
2`). S4144 compares functions in the same file and needs every name to be the same; githealthcheck
compares across the repository and lets a copy rename its locals consistently, as
[GHC-CPLX-009](GHC-CPLX-009.md) does, since that is how a copied helper drifts into a second package.
Every other name, and every literal, must be the same: a function whose body differs only in a string
does something else.

Each function after the first of its kind, by path, is a finding that names the first. Methods count
too: two types with one method body are a sign the body belongs to a shared function.

## What is left out

As in [GHC-CPLX-009](GHC-CPLX-009.md#what-is-left-out). Function literals are part of the function
that holds them. Two functions of one name in one package are not compared, nor blocks inside them
(GHC-CPLX-009): Go allows that only in files under different build constraints, one per platform,
where repeating the body is the way to write it.

## Fix

Keep one function, and have the other places call it.
