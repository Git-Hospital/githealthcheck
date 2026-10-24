# GHC-CPLX-004: error checked and then dropped

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

The code checks an error and then acts as if there was none:

- `if err != nil {}`: the error is checked and nothing is done. An empty block with a comment in it is
  taken as meant, as SonarSource's S108 takes it.
- `if err != nil { return nil }` in a function whose last result is `error`: the error is replaced by
  success. Only a block that is that single return counts; one that logs first is not judged.

## Threshold and source

None: each occurrence is a finding. It is CWE-390, "Detection of Error Condition Without Action"
(<https://cwe.mitre.org/data/definitions/390.html>): "The product detects a specific error, but takes
no actions to handle the error." staticcheck reports the first form as SA9003, "Empty body in an if or
else branch". Code generators produce it: a model asked for code that compiles writes the check and
leaves the handling out (ADR 0001, D32).

Without types, the error is recognised by its name, as in [GHC-CPLX-001](GHC-CPLX-001.md): an
identifier named `err`, or ending in `err` or `Err`, compared with `nil`. An error discarded with `_`
is not caught: telling an error result from any other needs types.

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. A function literal
is not judged for the second form, since its results are its own.

## Fix

Return the error, wrapped with context (`fmt.Errorf("...: %w", err)`), or handle it. If dropping it is
right, say why in a comment inside the block.
