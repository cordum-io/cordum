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
queued PR removes it from the queue. Adding a PR to the queue needs write
access; if you contribute from a fork, ask a maintainer to queue your
approved PR.

## CI tiers

CI has two tiers, so PR feedback stays fast.

- **Merge CI** runs on every PR, in the merge queue, and on pushes to `main`.
  The required checks are `Test (1.26.3)`, `Lint`, `Build`, `Security Scan`
  and `OpenAPI Validate`. CodeQL also runs on every PR. `Dashboard Tests` and
  Lighthouse run only when a change touches `dashboard/` or the OpenAPI spec
  in `docs/api/openapi/`. The SDK, binaries, docs-link and Grafana workflows
  run when their own paths change.
- **Nightly** runs on a schedule (02:00-03:40 UTC):
  - benchmarks, `Handshake Trust Interop` and `Quickstart Env Sharing` in
    `ci.yml`;
  - the E2E suites: `platform-smoke`, `demo-mock-bank-e2e`, `E2E TLS` and
    `edge-fake-hook-e2e`;
  - the integration-tagged tests and production gates in
    `integration-nightly.yml` and `nightly.yml`.

You can run any nightly workflow on your branch before you merge, for
example `gh workflow run demo-mock-bank-e2e --ref <branch>` or
`gh workflow run CI --ref <branch>`. Do this for changes to the stack's
startup, compose files, TLS or the approval flow. Nightly failures show in
the Actions tab; fix them in a follow-up PR.

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

## Maintainer smoke tests & local E2E harness

When verifying runtime changes locally before pushing, boot the local Compose
stack with `cordumctl up --dev`. It prints its next steps (`cordumctl status`,
`cordumctl edge claude --no-launch` and `./tools/scripts/demo_guardrails_run.sh`).
`cordumctl` commands default `--api-key` to the `CORDUM_API_KEY` environment
variable, otherwise to `CORDUM_API_KEY` and then `API_KEY` in `.env`.

### Minimal stack smoke test

`./tools/scripts/platform_smoke.sh` (or `make smoke`) needs `CORDUM_API_KEY`
and uses the `default` tenant unless `CORDUM_TENANT_ID` is set. Run it from the
repository root: it uses https with `./certs/ca/ca.crt` when that file exists.
It creates an approval workflow, starts a run, approves it, waits for the run
to succeed, verifies the audit chain and probes governance health, then deletes
the run and the workflow.

```bash
cordumctl up --dev
CORDUM_API_KEY="${CORDUM_API_KEY:-$(grep '^CORDUM_API_KEY=' .env | tail -n 1 | cut -d= -f2-)}" ./tools/scripts/platform_smoke.sh
```

The recipe keeps a `CORDUM_API_KEY` exported in your shell, and takes the last
definition from `.env` otherwise, as Docker Compose does.

### Local E2E suites

- `./tools/scripts/e2e_test.sh`: tests the full system through the dashboard
  nginx proxy on port 8082. It switches the hello worker's NATS and Redis URLs
  to TLS when the local CA exists.
- `./tools/scripts/edge_fake_hook_e2e.sh`: sends synthetic Claude hook payloads
  through `cordum-hook` and a locally started `cordum-agentd` to
  `/api/v1/edge/*`, then on to approvals, session events and evidence export.
  It needs no Claude Code binary and does not run `cordumctl edge claude`.
- `./tools/scripts/doctor_integration_test.sh`: tests `cordumctl doctor`
  against the Compose stack, stopping and restarting NATS. It runs only with
  `CORDUM_INTEGRATION=1`, and the Makefile has no target for it.

### Recording demo GIFs

From the repository root, `vhs tools/scripts/demo_guardrails.tape` records
`./tools/scripts/demo_guardrails.sh` into `docs/assets/guardrails-demo.gif`.
Install Charmbracelet `vhs` first (`brew install vhs` or a Go install).

### `CAP-PRODUCTION` profile notes

`CORDUM_CAP_PROFILE` defaults to `compat`; leave it there for local
development and quickstarts. Only the scheduler implements `production`: the
API gateway and the workflow engine refuse to start under it
(`docs/cap_production_profile.md`).
