# GHC-TEST-001: package without test files

**Family:** tests. **Languages:** Go. **Needs:** nothing beyond the commit.

A Go package declares at least one function or method outside its tests, and the package directory
has no `_test.go` file.

## Threshold

None: the rule is a fact, not a cut-off. A package is counted once it declares a function with a body
in a non-test file. A package of only types, constants or a doc comment has nothing to run, and is not
counted. The metric `untested_package_share` is the share of counted packages that have no test file.

## What is left out

- Vendored code (`vendor/`, `third_party/`, `node_modules/`, `bower_components/`).
- Generated files (a `// Code generated ... DO NOT EDIT.` line before the package clause,
  <https://go.dev/s/generatedcode>).
- Anything the go command ignores: `testdata/`, and files or directories whose name starts with `_` or
  `.` (<https://pkg.go.dev/cmd/go#hdr-Package_lists_and_patterns>).

Build constraints are not evaluated: a file with `//go:build ignore` still counts.

Python is not judged: its tests usually live in a `tests` directory of their own, so a directory
without a test file says nothing. A repository with no Go has `untested_package_share` `null` with
the reason `unsupported_language`.

## Fix

Add a `_test.go` file that exercises the package. Suppressing a finding with a reason in
`.githealthcheck.yml` (ADR 0001, D21), for example for a `main` package that only wires other packages
together, is not implemented yet.
