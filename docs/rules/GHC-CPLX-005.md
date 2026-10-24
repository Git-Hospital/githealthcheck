# GHC-CPLX-005: import of a package the module does not provide

**Family:** complexity. **Languages:** Go. **Needs:** the `go.mod` of the file's module.

A file imports a package that is not in the standard library, not a directory with Go files of a module
of the repository, and not under a module that the file's `go.mod` requires or replaces. With a
`go.work` anywhere in the repository, the requirements of every `go.mod` count for every module, as in
workspace mode (which modules the `go.work` uses is not read). The code cannot build as committed,
and the name may have been invented.

## Threshold and source

None: each import is a finding. Code-generating models invent package names: Spracklen et al., "We
Have a Package for You! A Comprehensive Analysis of Package Hallucinations by Code Generating LLMs",
USENIX Security 2025 (<https://arxiv.org/abs/2406.10279>), found "at least 5.2%" of the packages
named by commercial models and "21.7%" by open-source models to be hallucinated, over 576,000 code
samples. An invented name is a name anyone can register with malicious code.

The standard library is the list of packages in `GOROOT/src` of the Go release githealthcheck was built
with, for every platform and experiment, so `syscall/js` and `arena` count
(`internal/lang/golang/stdlib.go`). An invented standard package such as `encoding/yaml` is caught. A
module whose `go` directive names a newer Go than the list may import standard packages the list lacks,
so there an import whose first element has no dot is not judged.
The check is offline: a package missing from a module that is required is not caught, since that
needs the module's source.

## What is left out

Files outside any module (no `go.mod` above them), vendored and generated code, and what the go command
ignores. Test files are judged.

At most 50 findings of the rule are listed per file; one
more finding gives the count of the rest, and `smells_per_kloc` counts them all.

## Fix

Add the module with `go get`, which also checks that it exists, or replace the import with a package
that does.
