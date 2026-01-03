# CAP v2 Integration Status (coretexOS)

coretexOS uses CAP v2 as the canonical wire contract. The platform does not duplicate CAP protos for bus/safety types.

## Current alignment

- CAP module: `github.com/coretexos/cap/v2` (pinned in `go.mod`)
- CAP types used directly via aliases in `core/protocol/pb/v1`
- Bus subjects and protocol version constants in `core/protocol/capsdk`
- Safety Kernel gRPC surface uses CAP `PolicyCheck*` messages

## What remains local

- Gateway gRPC APIs (`CoretexApi`, `ContextEngine`) live under `core/protocol/proto/v1`.
- Generated Go types live in `core/protocol/pb/v1` and `sdk/gen/go/coretex/v1`.

## Update workflow (when CAP changes)

1) Update CAP repo and tag a new version.
2) Bump `github.com/coretexos/cap/v2` in `go.mod`.
3) Run tests:

```bash
go test ./...
```

## Hard rules

- Do not change existing field numbers in CAP protos.
- New fields must be appended with new IDs.
- Coretex should not reintroduce duplicate CAP definitions.
