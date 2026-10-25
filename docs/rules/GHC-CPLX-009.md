# GHC-CPLX-009: block of 100 tokens or more repeated

**Family:** complexity. **Languages:** Go, Python. **Needs:** nothing beyond the commit; for Python, python3 3.10 or later on `PATH`.

A block of at least 100 tokens, over at least 10 lines, repeats a block earlier in the repository. The
copy may rename the function's own parameters and locals, consistently, and change literals. A change
to one copy is easy to forget in the other.

## Threshold and source

At least 100 tokens over at least 10 lines that hold tokens: SonarSource's thresholds for duplicated
blocks, "at least 100 successive and duplicated tokens" spanning "10 lines of code"
(`sonar.cpd.<language>.minimumTokens` and `minimumLines`,
<https://docs.sonarsource.com/sonarqube-server/user-guide/code-metrics/metrics-definition>). D34
replaces the 50 tokens of D17, which had no source.

What counts as the same goes further than SonarSource. For Go, SonarSource's copy detection compares
tokens as written, with only `"..."` strings masked (sonar-go `CpdVisitor.java`): a copy that renames a
variable is not found. githealthcheck finds Type-2 clones, "syntactically identical fragments except
for variations in identifiers, literals" (Roy, Cordy and Koschke, "Comparison and evaluation of code
clone detection techniques and tools: A qualitative approach", Science of Computer Programming 74(7),
2009), with the consistency of Baker's parameterised matching (B. S. Baker, "On Finding Duplication
and Near-Duplication in Large Software Systems", WCRE 1995):

- every literal may differ;
- a parameter, result, receiver, type parameter or local of the function may be renamed, as long as
  each name of one copy stands for one name of the other throughout. Without types, a name is local
  from where the function declares it to the end of the block that holds the declaration; elsewhere
  it names whatever it names outside;
- any other name (a function, method, field, key of a struct literal, type, package or
  constant) must be the same. Where a copy changes one, or renames a local inconsistently, the block
  ends there, and each part is judged on its own size.

Semicolons are not compared, so `a; b` on one line repeats `a` and `b` on two.

Generated code repeats itself this way: GitClear's 2025 study of 211 million changed lines found the
share of copy-pasted lines rising from 8.3% to 12.3% (ADR 0001, D32).

A block starts at the first token of a line and ends with the last token of a line, in both copies.
Each copy is reported once, against the first earlier place in the repository, by path, that it
repeats. Earlier places are found by a hash of the block's first 100 tokens and of the pattern of its
locals, so only places that are the same up to a consistent renaming are compared; up to eight are
kept for each, the hash is chosen anew on every run, and a comparison that fails hides nothing after
it. Three copies give two
findings. A copy does not overlap the block it repeats. A block with fewer than 10 identifiers is a table of literals, data
rather than code, and is not reported.

The metric `duplicated_line_share` is the share of lines, in all the files duplication is looked in, that
are in a repeated block, either copy, each line counted once: the blocks reported, and those inside two
functions [GHC-CPLX-010](GHC-CPLX-010.md) reports, but not those between files written once per
platform. It is SonarSource's
`duplicated_lines_density`, whose denominator is also all lines (sonarqube `DuplicationMeasures.java`).

## Python

The same engine over the tokens of Python's `tokenize`, each language on its own. Imports, comments,
newlines and `;` are not compared; `INDENT` and `DEDENT` are, so a block keeps its shape. An f-string
or t-string is one literal. A local is a parameter of the function (or of a function around it) or a
name it assigns, unless declared `global` or `nonlocal`, as Python's own scoping has it; a keyword
argument's name and an attribute after `.` are never locals. The platform exception below is Go's
alone.

## What is left out

Test files, as SonarSource leaves them out of duplication; vendored and generated code; what the go
command ignores; the package clause and imports. A block inside two functions that
[GHC-CPLX-010](GHC-CPLX-010.md) reports as the same is not reported again, nor a block between two
files of one package that declare a function of the same name: Go allows that only in files under
different build constraints, one per platform (see GHC-CPLX-010). Past 20
million tokens in the repository, about two and a half million lines, no token stream is kept,
duplication is not looked for and the metric is `null` with the reason `too_large`. At most 50 findings are listed per file.

## Fix

Move the block into a function both places call, with what differs as parameters.
