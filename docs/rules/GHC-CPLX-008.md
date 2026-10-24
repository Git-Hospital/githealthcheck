# GHC-CPLX-008: function that only says it is not implemented

**Family:** complexity. **Languages:** Go, Python. **Needs:** nothing beyond the commit; for Python, python3 3.10 or later on `PATH`.

A function's whole body is `panic("...")`, or a `return` with `errors.New("...")` or
`fmt.Errorf("...")`, whose message says the function is not implemented: it contains "not implemented", "not yet
implemented", "unimplemented" or "implement me", or it is "TODO" or starts with "TODO:", in any case. A
message about a to-do item, such as "todo not found", is not one.

## Threshold and source

None: each function is a finding. A placeholder compiles and passes review at a glance, and fails only
when called. Code generators leave them when asked for more than they write (ADR 0001, D32).

## Python

The whole body, after the docstring, is one of:

- `raise NotImplementedError` in a function that is not a method; in a method it is how a base class
  asks for an override, and is not judged;
- a `raise` whose message says the function is not implemented or is a TODO, as for Go;
- `pass` or `...` with a `# TODO` or `# FIXME` comment in the function.

A decorated function (an abstract method, an overload, a registered handler) is not judged.

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. A body with any
other statement is not a placeholder.

At most 1000 smells of each kind are kept per file and at most 50 findings of the rule are listed per
file; one more finding gives the count of the rest, and `smells_per_kloc` counts them all.

## Fix

Implement the function, or delete it and its callers until it is needed.
