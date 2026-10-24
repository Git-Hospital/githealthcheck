# GHC-CPLX-001: function with cognitive complexity over 15

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

A function's cognitive complexity is over 15: following its control flow takes holding too many
branches, loops and conditions in mind at once.

## Threshold and source

Over 15, the default of SonarSource's rule S3776 for Go, Python and TypeScript (`DEFAULT_THRESHOLD = 15`
in sonar-go's `FunctionCognitiveComplexityCheck.java`). SonarSource explains the value as McCabe's 10,
raised after use: <https://community.sonarsource.com/t/s3776-reason-for-the-current-default-value-of-15/127103>.

Cognitive complexity is that of G. Ann Campbell, "Cognitive Complexity: A new way of measuring
understandability", SonarSource, version 1.7, 2023
(<https://www.sonarsource.com/docs/CognitiveComplexity.pdf>):

- +1 for each `if`, `else if`, `else`, `switch`, `select`, `for`, `goto`, `break` or `continue` to a
  label, and each sequence of like `&&` or `||` operators (`a && b || c` is two);
- `if`, `switch`, `select` and `for` add their nesting level as well; `else` and `else if` do not;
- a function literal raises the nesting level of what it contains;
- a function that calls itself directly adds 1.

One change, as in SonarSource's Go analyzer: an error check without an `else`, such as
`if err != nil { return err }`, adds nothing, since it is how Go handles an error. Without types the
check is recognised by its shape: an identifier named `err`, or ending in `err` or `Err`, compared with
`nil`. Without that change the measure is the paper's, and it equals gocognit's
(<https://github.com/uudashr/gocognit>) on every function of eight repositories, 2,563 in all
(`scripts/cplx-oracle.sh`).

The metric `complex_function_share` is the share of functions over the threshold, given from 20
functions on.

## What is left out

Test files, vendored and generated code, and what the go command ignores (as in
[GHC-TEST-001](GHC-TEST-001.md#what-is-left-out)). Function literals are measured as part of the
function that holds them.

## Fix

Split the function: move a nested loop or a branch into a function of its own, return early instead
of nesting, and name complex conditions.
