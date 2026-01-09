# Backend Tasks Tracker (platform-only)

This is a living checklist to keep the platform core aligned with the plan.

## Recently completed
- CAP v2 integration with aliases in `core/protocol/pb/v1`.
- Workflow engine: condition/delay/notify steps, rerun-from-step, dry-run support.
- Workflow run timeline (append-only events).
- Schema registry + workflow input/step IO validation.
- Resource locks service (shared/exclusive) with gateway APIs.
- Artifact store (Redis) with retention classes and gateway APIs.
- Policy explain/simulate APIs and snapshot listing.
- Run idempotency keys on workflow run creation.
- cordumctl CLI + smoke script.

## In progress / next
- External artifact backends (S3) and secret management (Vault/KMS).
- Vector store bindings for embeddings and retrieval.
- Stronger expression language for workflow conditions and dataflow.
- CAP signature verification and enforcement on the bus.
- Richer DLQ pagination and telemetry.

## Optional / deferred
- Worker manager abstractions (Docker/HTTP/Script).
- GPU-aware scheduling and advanced placement constraints.

## Health
- Tests: `go test ./...` pass.
- State: Redis primary; Postgres not in use.
