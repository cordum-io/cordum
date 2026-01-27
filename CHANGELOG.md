# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [v0.2.0] - 2026-01-26
- Scheduler: add durable saga/compensation handling with reverse-stack rollback for fatal failures.
- Scheduler: add compensation idempotency keys and saga rollback metrics.
- Protocol: align job status handling with CAP v2.0.16 (FAILED_FATAL/FAILED_RETRYABLE).
- Workflow engine: treat FAILED_FATAL as terminal and FAILED_RETRYABLE as retryable.
- Security/docs: updated control-plane docs/wiki for saga semantics and CAP changes.
- Tests: added coverage for saga manager, safety kernel cache/URL validation, protobuf + grpc stubs, and Redis idempotency flows.

## [v0.1.4] - 2026-01-25
- Security: remove default API keys; deployments must supply `CORDUM_API_KEY`.
- Security: fail-closed API auth; enforce `X-Tenant-ID`; require policy signatures when enforcement is enabled.
- Dashboard: disable API key storage in localStorage (opt-in embed via `CORDUM_DASHBOARD_EMBED_API_KEY`).
- Breaking: clients must send `X-Tenant-ID` on all `/api/*` requests.
