# GHC-CPLX-008: function that only says it is not implemented

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

A function's whole body is `panic("...")`, or a `return` with `errors.New("...")` or
`fmt.Errorf("...")`, whose message says the function is not implemented: "not implemented", "not yet
implemented", "unimplemented", "implement me" or "TODO", in any case.

## Threshold and source

None: each function is a finding. A placeholder compiles and passes review at a glance, and fails only
when called. Code generators leave them when asked for more than they write (ADR 0001, D32).

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. A body with any
other statement is not a placeholder.

## Fix

Implement the function, or delete it and its callers until it is needed.
