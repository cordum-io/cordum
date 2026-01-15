# Contributing to Cordum

Thanks for helping improve Cordum.

## Getting started

- Go toolchain: `go 1.24`
- Tests: `go test ./...`
- Coverage: `make coverage` (all) or `make coverage-core` (enforces 80% on `core/`)
- Format: `gofmt -w` on Go files

## Development workflow

1. Fork the repo and create a feature branch.
2. Make focused changes (small, reviewable commits).
3. Update docs in `docs/` when you add commands, binaries, or behavior changes.
4. Run tests locally (`go test ./...`).
5. Open a PR with a clear description and test results.

## Collaborator requests

If you'd like to collaborate regularly, open a GitHub Issue titled
`Collaborator request: <your name>` and include:

- Areas you want to help with.
- Links to relevant PRs or technical work.
- Your expected availability.

Maintainers will reply with next steps. All changes still go through PR review.

## License for contributions

By contributing, you agree that your contributions will be licensed under the
BUSL-1.1 license for this repository and may be relicensed under the Change
License on the Change Date (see `LICENSE`).

## Code guidelines

- Use standard library `log` for logging.
- Use `logging.Info/Warn/Error` for consistent levels where applicable.
- Avoid panics in library code; return errors.
- Wrap external errors with context using `fmt.Errorf("context: %w", err)`.
- Keep functions small and focused.
- Follow existing naming conventions (`NewXxx`, `Engine`, `XxxStrategy`).

## Reporting bugs

- Include steps to reproduce, expected vs actual behavior, and logs.
- For security issues, see `SECURITY.md`.
