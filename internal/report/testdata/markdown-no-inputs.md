## githealthcheck: **4.2/10**

repo at `0123456789ab`, 3 of 4 families measured, scored as go against 15 corpus repositories (100% of the code analysed).

| Family | Score | Metrics |
|---|---|---|
| tests | **4.2/10** | untested_package_share 50.0% (4.2) |
| history | **4.2/10** |  |
| complexity | null (not_calibrated) |  |
| architecture | null (not_implemented) |  |

### Findings (5)

- [GHC-TEST-001](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/GHC-TEST-001.md) pkg\/a: package declares 2 functions and has no \_test\.go file
- [GHC-HIST-001](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/GHC-HIST-001.md) one developer authors so much
- [GHC-HIST-002](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/GHC-HIST-002.md) dir with space\/a\#b\.go: its only author left
- [GHC-CPLX-001](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/GHC-CPLX-001.md) a\:b\/c\.go:12: F has cognitive complexity 20
- [GHC-CPLX-007](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/rules/GHC-CPLX-007.md) x\.go:3: line 3 is commented\-out code (suppressed: kept on purpose)
