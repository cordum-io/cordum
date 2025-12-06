# AGENTS.md – CortexOS AI Control Plane

## Mission

You are helping build **CortexOS**, an AI Control Plane.
Core ideas:
- NATS-based bus
- Go control-plane (scheduler + safety)
- Protobuf contracts in `api/proto/v1` + `pkg/pb/v1`
- Workers as separate binaries under `cmd/`

## Architecture rules

- Do NOT change field numbers in existing .proto files.
- New proto fields must be appended with new IDs.
- Scheduler must depend on interfaces in `internal/scheduler/types.go`,
  not on concrete infra (NATS, config, etc.).
- Bus = NATS implementation in `internal/infrastructure/bus`. Treat it as
  an abstraction behind the `Bus` interface.
- Keep all public wire contracts in `api/proto/v1` and generated code in `pkg/pb/v1`.

## Code style

- Language: Go 1.x
- Use stdlib `log` for now, no extra logging library.
- No panics in library code. Return errors.
- Prefer small, focused functions.
- Follow existing naming: `NewXxx`, `Engine`, `XXXStrategy`, etc.

## Testing and sanity checks

- For now: at minimum `go test ./...` must pass.
- When adding new packages under `internal/`, add basic unit tests where meaningful.
- If you create a new binary under `cmd/`, add a tiny smoke test script under `tools/scripts`.

## What you MUST do on every change

- Preserve existing behavior unless the task explicitly asks to change it.
- Update docs in `docs/` if you add new commands, new binaries, or adjust the architecture.
- If you touch .proto files, re-run `make proto` and ensure build passes.

## Things you should avoid

- Don’t introduce new dependencies unless necessary.
- Don’t collapse multiple responsibilities into a single package.
- Don’t hardcode NATS subjects everywhere – prefer central constants later.
