# Dependency Updates

Dependabot checks GitHub Actions and Go module dependencies weekly.

## Reviewing updates

All Dependabot pull requests must go through the same review process as any other contribution. Review the proposed changes, check compatibility, and ensure that CI passes before merging.

For GitHub Actions updates, verify the proposed action versions and commit SHAs.

## Go version

Dependabot manages Go module dependencies, but the Go toolchain version declared by the `go` directive in `go.mod` must be updated manually.

Go version updates require an explicit decision and review rather than being treated as routine dependency updates.