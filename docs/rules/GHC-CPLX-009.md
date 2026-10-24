# GHC-CPLX-009: block of 100 tokens or more repeated

**Family:** complexity. **Languages:** Go. **Needs:** nothing beyond the commit.

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
- a parameter, result, receiver or local of the function may be renamed, as long as each name of one
  copy stands for one name of the other throughout;
- any other name (a function, method, field, type, package or constant) must be the same. Where a copy
  changes one, or renames a local inconsistently, the block ends there, and each part is judged on its
  own size.

Generated code repeats itself this way: GitClear's 2025 study of 211 million changed lines found the
share of copy-pasted lines rising from 8.3% to 12.3% (ADR 0001, D32).

A block starts at the first token of a line. Each copy is reported once, against the first place in the
repository, by path, where its first 100 tokens occur; three copies give two findings. A copy does not
overlap the block it repeats. A block with fewer than 10 identifiers is a table of literals, data
rather than code, and is not reported.

The metric `duplicated_line_share` is the share of lines, in all the files duplication is looked in, that
are in a reported block, either copy, each line counted once. It is SonarSource's
`duplicated_lines_density`, whose denominator is also all lines (sonarqube `DuplicationMeasures.java`).

## What is left out

Test files, as SonarSource leaves them out of duplication; vendored and generated code; what the go
command ignores; the package clause and imports. A block inside two functions that
[GHC-CPLX-010](GHC-CPLX-010.md) reports as the same is not reported again. Past 20 million tokens,
about two and a half million lines, duplication is not looked for and the metric is `null` with the
reason `too_large`. At most 50 findings are listed per file.

## Fix

Move the block into a function both places call, with what differs as parameters.
