# GitHealthCheck

A code health analyzer for Git repositories. It looks at tests and coverage, git history, complexity and duplication, and architecture and dependencies. Every number it prints is a measurement or `null` with a reason.

**Status: being rebuilt in Go.** Nothing is released from the new code yet. The plan and its reasons are in [docs/decisions/0001-go-rewrite.md](docs/decisions/0001-go-rewrite.md), and the design is in [docs/architecture.md](docs/architecture.md). Progress is tracked in [#1](https://github.com/Git-Hospital/githealthcheck/issues/1).

The earlier Python version is at tag [`v0.1.0`](https://github.com/Git-Hospital/githealthcheck/tree/v0.1.0). It is not maintained. On Go and Rust repositories it reads no files but still prints scores, so do not rely on its numbers.

## License

Apache-2.0. See [LICENSE](LICENSE).
