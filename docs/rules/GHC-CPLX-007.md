# GHC-CPLX-007: commented-out code

**Family:** complexity. **Languages:** Go, Python. **Needs:** nothing beyond the commit; for Python, python3 3.10 or later on `PATH`.

A comment holds code: lines that, without their `//`, parse as Go that does something.

## Threshold and source

None: each run of lines is a finding. It is SonarSource's S125, "Sections of code should not be
commented out": commented-out code "should be deleted and can be retrieved from source control
history". SonarSource has no Go version of the rule; this one follows the method of its Python and
JavaScript versions: group the comment lines, strip the comment marks, and run the parser.

A line counts when it parses on its own as a call, an assignment to something assignable, a
declaration, a `return`, a channel send or receive, or a control statement, or opens or closes a brace;
a run of such lines is one finding when it parses as a whole. Not counted:

- a comment after code on the same line, which is about that code;
- lines indented after the `//` with a tab or two spaces, which are examples in a doc comment;
- directives (`//go:`, `//line`, `//export`, `//nolint`, `// +build`);
- a single word, and prose such as `Wrap (2)` (gofmt writes no space before a call's parenthesis) or
  `Example: c := New(cfg)`, which parses as a labelled statement;
- the expected output of a testable example: the last comment of an `Example` function, when it starts
  with `Output:` or `Unordered output:` in any case, as go/doc reads it.

A comment line inside a run, such as a comment that was in the code, stays part of the run.

Checked by hand on seven Go repositories (cobra, logrus, fzf, yaml, MeshMedic, Helmbulance, boards):
39 findings, of which one is not commented-out code but a formula that explains the line below it.

## Python

The method SonarSource's python:S125 uses: runs of `#` comment lines, without the `#` and the space
after it, that parse with `ast` as statements of which one does something: a call written as code
(`f(x)`, not `f (x)`), an `await` or `yield`, an assignment to a name, attribute or item, a `return`,
`raise`, `del`, `import`, `assert`, `def`, `class`, or a control statement. A run that does not parse as a whole is judged line by line. Not
counted: a comment after code on the same line, directives (`# type:`, `# noqa`, `# pylint:`,
`# fmt:`, `# pragma`, `# isort:`, the coding line and others), a `#!` first line, and doctest lines
(`>>>`, `...`).

Checked by hand on samples from django: commented-out settings in `docs/conf.py`, code under a
`TODO`, and a few comments that are code in form but a note in intent, such as `# g = g1 | g2` above
`__or__`, or a grammar from an RFC.

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. `/* */` comments
are not read.

At most 1000 smells of each kind are kept per file and at most 50 findings of the rule are listed per
file; one more finding gives the count of the rest, and `smells_per_kloc` counts them all.

## Fix

Delete the code. To keep a reason, write the reason, not the code.
