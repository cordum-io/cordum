# Backend Feature Integration Matrix

This table tracks key backend features, their implementation status, and test coverage.

| Area | Implemented | Tests | Notes/Paths |
|------|-------------|-------|-------------|
| Scheduler dispatch (NATS direct + topic) | Yes | Unit + integration (`core/controlplane/scheduler/integration_test.go`) | Least-loaded picks direct subject via `bus.DirectSubject`; fallback to topic queue. |
| Pool routing + requires | Yes | Unit (`core/controlplane/scheduler/strategy_least_loaded_test.go`) | Pool capability filtering using `JobMetadata.requires`. |
| Job state store | Yes | Unit (`core/infra/store/job_store_test.go`) | Redis with WATCH; traces, deadlines, safety, idempotency. |
| Safety kernel decisions | Yes | Unit (`core/controlplane/safetykernel/kernel_test.go`) | Policy decisions + constraints + snapshots. |
| Workflow engine | Yes | Unit (`core/workflow/engine_test.go`) | Fan-out, retries/backoff, approvals, delay/notify/condition, rerun/dry-run. |
| Workflow run timeline | Yes | Unit (`core/workflow/store_redis_test.go`) | Append-only timeline stored per run. |
| Config service (hierarchical) | Yes | Unit (`core/configsvc/service_test.go`) | system -> org -> team -> workflow -> step merge. |
| DLQ store + retry | Yes | Unit (`core/infra/store/dlq_store_test.go`) | Gateway retry replays context with new job id. |
| Schema registry + validation | Yes | Unit (`core/infra/schema/registry_test.go`) | JSON schema validation for workflow inputs/outputs. |
| Locks service | Yes | Unit (`core/infra/locks/redis_store_test.go`) | Shared/exclusive locks with TTL. |
| Artifact store | Yes | None | Redis-backed store (`core/infra/artifacts`). |
| Gateway HTTP/WS endpoints | Yes | Unit (`core/controlplane/gateway/gateway_test.go`) | Jobs, workflows, approvals, policy (bundles/publish/rollback/audit), schemas, locks, artifacts, DLQ. |
| Auth (OSS) | Yes | Unit (gateway tests) | API key allowlist (`CORDUM_API_KEYS`/`CORDUM_API_KEY`) with single-tenant default; `X-Tenant-ID` required on HTTP. |
| Auth (enterprise) | Yes | Unit (enterprise repo) | Multi-tenant API keys and RBAC enforced by the enterprise auth provider. |
| Worker runtime SDK | Yes | None | `sdk/runtime` wraps CAP runtime (typed handlers + pointer hydration). |
| CLI (cordumctl) | Yes | None | `cmd/cordumctl` + smoke script; ships as `cordumctl`. |
| Topic Registry | Yes | Unit + integration | `GET/POST/DELETE /api/v1/topics`; submit-time topic validation at gateway and scheduler boundaries; pack manifest `inputSchema`/`outputSchema` registered at install. |
| Worker Credentials | Yes | Unit + integration | `GET/POST/DELETE /api/v1/workers/credentials`; argon2id-hashed tokens; `WORKER_ATTESTATION=enforce\|warn\|off`. |
| Worker readiness handshake | Yes | Unit (CAP v2.9.0 SDK + scheduler filter) | `Agent.Start()` auto-publishes `Handshake{ready_topics, auth_token}`; scheduler filters on `ready=true` when `WORKER_READINESS_REQUIRED=true`. |
| Output Policy (two-phase) | Yes | Unit + integration (`core/controlplane/safetykernel/scanners.go`, `core/protocol/proto/v1/output_policy.proto`) | Sync metadata fast-path + async content checks; decisions `ALLOW`/`QUARANTINE`/`REDACT`; `OUTPUT_QUARANTINED` job state. |
| Approvals + Delegations | Yes | Unit + integration (`handlers_approvals.go`, `handlers_delegation*.go`) | A2A delegation tokens, cascade revocation, scheduler dispatch-time re-verify, nonce/replay protection, `auth.PermDelegationImpersonate`. |
| Audit verification pipeline | Yes | Unit + integration (`core/audit/`, `core/audit/exporter*`) | Chain verification + consumer + legal-hold + multi-backend exporter (webhook HMAC-SHA256, syslog RFC 5424, Datadog v2, CloudWatch Logs SigV4). |
| Governance Timeline + analytics | Yes | Unit (`handlers_governance_*.go`, `useGovernanceHealth`, `useApprovalAnalytics`) | `/api/v1/governance/decisions`, `/api/v1/governance/health`, `/api/v1/governance/approvals/analytics`. |
| MCP Server | Yes | Unit + integration (`cmd/cordum-mcp`, `gateway_mcp.go`) | stdio + HTTP/SSE transport; tool registry; tool-approval flow; `cordum://` resource resolution; audit hook. |
| Evals | Yes | Unit + dashboard hooks (`useEvals`) | Evaluations CRUD + run; `EvalsPage` + `EvalDatasetDetailPage` + `EvalRunDetailPage` dashboard surfaces. |
| Enterprise (consolidated into core) | Yes | Unit (gateway + auth) | SSO/SAML/OIDC, SCIM provisioning, RBAC, SIEM export (4 backends), legal hold — all behind license entitlements; cordum-enterprise repo retired 2026-04-23. |
| Workflow new step types | Yes | Unit (`core/workflow/*_test.go`) | Switch, parallel, loop, transform, storage, sub-workflow. |

Keep this table updated when wiring new components.
