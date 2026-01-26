# Repository Guidelines

## Project Structure & Module Organization
- `core/` contains the control-plane kernel (protocol, infra, gateway/scheduler/safety, agent runtime).
- `cmd/` holds thin binaries wiring config into core components (e.g., `cordumctl`).
- `packages/` contains plug-in packs (workers, workflows, providers).
- Protocols live in `core/protocol/proto/v1` with generated code in `core/protocol/pb/v1`.
- Docs are in `docs/`, examples in `examples/`, scripts in `tools/scripts/`.

## Build, Test, and Development Commands
- `go test ./...` – run all Go tests (baseline requirement).
- `go run ./cmd/cordumctl up` – start the local stack via the CLI.
- `docker compose build && docker compose up -d` – build and run the stack with Compose.
- `./tools/scripts/quickstart.sh` – one-command local stack + smoke test.
- `./tools/scripts/platform_smoke.sh` – create/run/approve/delete a workflow.

## Coding Style & Naming Conventions
- Language: Go 1.x. Use standard Go formatting (`gofmt`).
- Prefer small, focused functions and explicit error returns (no panics in library code).
- Use stdlib `log` only; no extra logging libraries.
- Follow naming patterns: `NewXxx`, `Engine`, `XXXStrategy`.

## Testing Guidelines
- Tests live alongside packages and use Go’s `testing` package.
- New packages under `core/` or `packages/` should include basic unit tests.
- If you add a new binary under `cmd/`, add a smoke test script in `tools/scripts/`.

## Commit & Pull Request Guidelines
- Prefer starting from a GitHub issue and reference the issue ID in commits/PRs.
- Keep commits focused (e.g., “Security: harden default auth”).
- PRs should include a clear summary and testing notes. Use PR tags when applicable.

## Security & Configuration Notes
- Do not change existing `.proto` field numbers; append new fields with new IDs.
- Scheduler must depend on interfaces in `core/controlplane/scheduler/types.go`, not concrete infra.
- Keep public wire contracts in `core/protocol/proto/v1` only.
- Security defaults: require API key + tenant header, and prefer fail-closed behavior.
- Production-first mindset: code and docs should assume production use (secure defaults, no silent bypasses).
- Keep docs and scripts aligned with configuration changes (README + `docs/` + `tools/scripts/`).
