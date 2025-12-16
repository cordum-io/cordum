# Backend Tasks Tracker (Redis-first)

This is a living checklist to converge backend features toward the plan.

## Recently Completed
- Redis-backed workflow store (definitions/runs) + workflow engine dispatch/advance.
- Gateway REST: workflow CRUD, runs start/get/list.
- Redis-backed config service with hierarchical merge + gateway endpoints.
- Redis-backed DLQ store with tests (add/list/delete).
- Removed unused configresolver; all tests passing.
- Scheduler now injects effective config into dispatched jobs and uses config service.
- Scheduler job status alignment with CAP (COMPLETED alias -> SUCCEEDED); handle direct worker routing and registry TTL; graceful Stop guard.
- Workflow engine: for_each fan-out, retries/backoff, timeout budget hinting, approval pause/resume, and new tests.
- Gateway: job list filters (state/topic/tenant/team/time/trace) with cursor pagination (`cursor`/`next_cursor`), DLQ retry (rehydrates context into new job id), workflow approval + cancel run endpoints.
- Scheduler hints/cancel: respects preferred worker/pool labels; publishes job cancel packets; trace lookups return full metadata (tenant/team/principal/etc.); safety client now sends effective_config to Safety Kernel (CAP proto updated).
- Safety: half-open circuit breaker (throttle + close-after-success) tested; job store pipelined listings with pagination tests.

## In Progress / Next
- Extend workflow engine: stronger routing hints to scheduler, explicit per-step cancellation hooks/acks, artifacts/secrets when steps need them.
- DLQ: add tracing/telemetry on retries and optional context/result replay confirmation.
- Ops: richer trace search and monitoring/alerts.

## Optional/Deferred
- Vector store bindings for embeddings/context steps.
- Artifacts (S3) and secrets (Vault) when steps require them.
- Worker manager abstractions (Docker/HTTP/Script) and GPU hints.

## Health
- Tests: `go test ./...` pass.
- State: Redis primary; Postgres not in use.
