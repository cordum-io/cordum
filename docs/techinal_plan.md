# Cordum Technical Plan (current + roadmap)

This document summarizes what is implemented today and what remains planned. For
full current behavior, see `docs/system_overview.md`.

## Current implementation (code-accurate)

Architecture:
- NATS bus for job submission, dispatch, results, and heartbeats.
- Redis for job state, ctx/res payload pointers, workflows/runs, config, and DLQ.
- Control plane services: scheduler, safety kernel, workflow engine, API gateway.
- Context engine for context window assembly and generic memory.
- External workers connect via job topics (no built-in workers in this repo).
- Public SDK module under `sdk/` (generated protos + minimal gateway client).
- CAP protocol integration for bus and safety contracts.
- Redis-backed artifact store, schema registry, and lock service.
- cordumctl CLI for platform operations.

Workflow engine:
- Redis-backed definitions and runs.
- Condition, delay, notify, for_each fan-out, retries/backoff, approvals, cancel.
- Rerun-from-step and dry-run support.
- Run timeline and schema validation for inputs/outputs.
- Dispatches steps as jobs on the bus.

Safety:
- Tenant/topic allow/deny via `config/safety.yaml`.
- MCP allow/deny support when `JobRequest.labels` include MCP fields.
- Scheduler enforces safety before dispatch and applies policy constraints.

Operations:
- JetStream optional durability (`NATS_USE_JETSTREAM=1`).
- Prometheus metrics on scheduler and gateway.
- API gateway exposes job/workflow/config/policy/schema/lock/artifact/DLQ endpoints and WS event stream.

## Roadmap (not implemented in code)

These are planned or speculative and should be treated as future work:
- Worker manager abstractions (Docker/HTTP/Script/Lambda).
- Vector store integrations for embeddings.
- Artifact storage (S3 or compatible) and secret management (Vault/KMS).
- Stronger expression language for workflow conditions and dataflow.
- Bus message signatures and verification.

## Guiding principle

Keep Redis + NATS as the primary system and evolve incrementally. Avoid parallel
systems until a concrete feature requires it.
