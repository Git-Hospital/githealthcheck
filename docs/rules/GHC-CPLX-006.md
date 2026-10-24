# GHC-CPLX-006: function nothing references

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

An unexported function is never referenced by any file of its package. No other package can reach it,
so it is dead code.

## Threshold and source

None: each function is a finding. It is the dead code that staticcheck reports as U1000, "Unused code"
(<https://github.com/dominikh/go-tools/tree/master/unused>). Generated code tends to bring helpers that nothing ends
up calling (ADR 0001, D32).

A reference is any use of the name in a file of the same directory, tests and generated files
included, an `//export` or `//go:linkname` directive naming it, or a `//go:wasmexport` directive on it. A function calling itself does not
keep itself alive. Files are read whatever their build constraints, so a function used only under
another platform counts as used.

## What is left out

Methods, which an interface can call without naming them; `init`, `main` and `_`; exported functions;
and every function of a package with a Go file that does not parse in full or was not read (too large,
a symlink, unreadable), whose references are incomplete. At most 50 findings are listed per file. A function called only from assembly is reported, and suppressing that finding with a reason
(D21) is not implemented yet.

## Fix

Delete the function. Git keeps it.
