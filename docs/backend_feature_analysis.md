# Backend Feature Analysis (current state)

This document summarizes the production-bound backend surface, where it lives in code, and the status of each capability.

## Scheduler
- Dispatch & safety: NATS bus dispatch via least-loaded strategy with direct worker routing; safety decisions persisted.
  Code: `core/controlplane/scheduler/*`.
- Job metadata: Redis job store with atomic state transitions, tenant/team/actor metadata, deadlines, trace linkage.
  Code: `core/infra/memory/job_store.go`.
- Routing: topic -> pool config with pool capability checks (`requires`), label hints, overload detection.
  Code: `core/controlplane/scheduler/strategy_least_loaded.go`.
- Reconciliation: timeouts for dispatched/running jobs; deadline expirations.
  Code: `core/controlplane/scheduler/reconciler.go`.

## Workflow engine
- Store: Redis-backed workflows/runs and timeline.
  Code: `core/workflow/store_redis.go`.
- Execution: condition, delay, notify, for_each, retries/backoff, approvals, cancel, rerun/dry-run.
  Code: `core/workflow/engine.go`.
- Validation: workflow input and step input/output schema validation.
  Code: `core/infra/schema`, `core/workflow/engine.go`.

## Safety kernel
- Policy checks: allow/deny/require approval/constraints; snapshots and reload.
  Code: `core/controlplane/safetykernel/*`, `core/infra/config/safety_policy.go`.
- Explain/simulate APIs exposed in gateway.

## Config service
- Hierarchy: system -> org -> team -> workflow -> step merge; version/hash snapshot.
  Code: `core/configsvc`.
- Exposure: gateway endpoints for set/get/effective config.

## API gateway
- Jobs: submit/list/get/cancel with filters and cursor pagination.
- Workflows: CRUD, runs start/get/list, approvals, rerun, timeline.
- Policy: evaluate/simulate/explain + snapshots.
- Schemas: register/get/list/delete.
- Locks: acquire/release/renew/get.
- Artifacts: put/get with retention.
- DLQ: list/delete/retry.
  Code: `core/controlplane/gateway/`.

## Artifacts, schemas, locks
- Artifacts: Redis-backed store with retention classes.
  Code: `core/infra/artifacts`.
- Schemas: Redis-backed registry with JSON schema validation.
  Code: `core/infra/schema`.
- Locks: Redis-backed shared/exclusive locks.
  Code: `core/infra/locks`.

## SDK and CLI
- SDK client + CAP runtime for workers.
  Code: `sdk/client`, `sdk/runtime`.
- CLI: `cmd/coretexctl` (workflow/run/approval/dlq).

## Gaps / next
- External artifact backends (S3) and secrets management (Vault/KMS).
- Vector store bindings for embeddings.
- Stronger expression language for workflow conditions and dataflow.
- CAP signature verification and enforcement.
