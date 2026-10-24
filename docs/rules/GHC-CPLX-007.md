# GHC-CPLX-007: commented-out code

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

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
- a single word, a label, and prose such as `Wrap (2)` (gofmt writes no space before a call's
  parenthesis).

A comment line inside a run, such as a comment that was in the code, stays part of the run.

Checked by hand on seven Go repositories (cobra, logrus, fzf, yaml, MeshMedic, Helmbulance, boards):
39 findings, of which one is not commented-out code but a formula that explains the line below it.

## What is left out

Vendored and generated code, and what the go command ignores. Test files are judged. `/* */` comments
are not read.

## Fix

Delete the code. To keep a reason, write the reason, not the code.
