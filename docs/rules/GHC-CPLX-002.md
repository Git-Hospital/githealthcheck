# GHC-CPLX-002: function with more than 90 lines of code

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

A function's body has more than 90 lines that hold code.

## Threshold and source

Over 90 lines of code: halfway between the defaults of golangci-lint's funlen (60 lines,
<https://github.com/ultraware/funlen>) and SonarSource's go:S138 (120 lines of code, `DEFAULT_MAX = 120`
in sonar-go's `TooLongFunctionCheck.java`). The midpoint is a choice of this project (ADR 0001, D34),
not a published value.

A line counts when it holds a token other than a comment, between the line of the function's opening
brace and the line of its closing one: blank lines and comment lines do not count, and a raw string
counts every line it spans. go:S138 also counts lines of code, without comments and blank lines.

The metric `long_function_share` is the share of functions over the threshold, given from 20
functions on.

## What is left out

As in [GHC-CPLX-001](GHC-CPLX-001.md#what-is-left-out).

## Fix

Split the function into steps with names of their own.
