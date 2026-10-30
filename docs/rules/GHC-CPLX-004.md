# GHC-CPLX-004: error checked and then dropped

**Family:** complexity. **Languages:** Go, Python. **Needs:** nothing beyond the commit; for Python, python3 3.10 or later on `PATH`.

The code checks an error and then acts as if there was none:

- `if err != nil {}`: the error is checked and nothing is done. An empty block with a comment in it is
  taken as meant, as SonarSource's S108 takes it.
- `if err != nil { return nil }` in a function whose last result is `error`: the error is replaced by
  success. Only a block that is that single return counts; one that logs first is not judged, and one
  with a comment in it, such as `return nil //nolint:nilerr // best effort`, is taken as meant.

## Threshold and source

None: each occurrence is a finding. It is CWE-390, "Detection of Error Condition Without Action"
(<https://cwe.mitre.org/data/definitions/390.html>): "The product detects a specific error, but takes
no actions to handle the error." staticcheck reports the first form as SA9003, "Empty body in an if or
else branch". Code generators produce it: a model asked for code that compiles writes the check and
leaves the handling out (ADR 0001, D32).

Without types, the error is recognised by its name, as in [GHC-CPLX-001](GHC-CPLX-001.md): an
identifier named `err`, or ending in `err` or `Err`, compared with `nil`. An error discarded with `_`
is not caught: telling an error result from any other needs types.

## Python

An `except` clause that catches every exception, bare, `except Exception` or `except BaseException`
(alone or in a tuple), and whose body is only `pass`, `...` or `continue`, with no comment in the
clause. It is CWE-390 in Python's form: a clause that broad also catches a bug, a `KeyboardInterrupt`
(when bare) or a `SystemExit`, and nothing is done with it. Narrow clauses, such as `except KeyError:
pass`, say which failure is expected and are not judged; a comment in the clause is taken as meant.

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. A function
literal, in a function or in a package-level variable, is judged for the first form only: a callback,
such as the function given to `filepath.WalkDir`, returns nil on an error to skip an entry by design.

At most 1000 smells of each kind are kept per file and at most 50 findings of the rule are listed per
file; one more finding gives the count of the rest, and `smells_per_kloc` counts them all. Below 1000
lines of analysed code `smells_per_kloc` is `null` (`insufficient_input`); the findings are still
reported.

## Fix

Return the error, wrapped with context (`fmt.Errorf("...: %w", err)`), or handle it. If dropping it is
right, say why in a comment inside the block.
