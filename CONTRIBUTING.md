# Contributing to Cordum

Thanks for helping improve Cordum.

## Getting started

- Go toolchain: `go 1.26.3`
- Tests: `go test ./...`
- Coverage: `make coverage` (all) or `make coverage-core` (enforces 80% on `core/`)
- Format: `gofmt -w` on Go files

## Development workflow

1. Fork the repo and create a feature branch.
2. Make focused changes (small, reviewable commits).
3. Update docs in `docs/` when you add commands, binaries, or behavior changes.
   (The public docs site, <https://docs.cordum.io>, is built from `Cordum-site/docs-site` — not this repo.)
4. Run tests locally (`go test ./...`).
5. Open a PR with a clear description and test results.

## Merge queue

Required checks run on `pull_request` and `merge_group` events so `main` can
use a GitHub merge queue. The queue is not enabled until a maintainer adds
it to the Main ruleset. Once enabled with the squash merge method, add an
approved PR to the queue instead of updating and re-approving it as `main`
advances. The queue re-runs required checks on the PR combined with `main`
and the PRs ahead of it, then squash-merges it when they pass. Pushing to a
queued PR removes it from the queue.

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
- Use `slog.Info/Warn/Error` for structured logging (preferred for new code).
- Avoid panics in library code; return errors.
- Wrap external errors with context using `fmt.Errorf("context: %w", err)`.
- Keep functions small and focused.
- Follow existing naming conventions (`NewXxx`, `Engine`, `XxxStrategy`).
- For shared Redis records, every Get→mutate→Set sequence must use WATCH/CAS
  (or an equivalent atomic primitive) to avoid lost updates under concurrency.

## Reporting bugs

- Include steps to reproduce, expected vs actual behavior, and logs.
- For security issues, see `SECURITY.md`.
