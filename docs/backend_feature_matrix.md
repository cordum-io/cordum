# Backend Feature Integration Matrix

This table tracks key backend features, their implementation status, and test coverage.

| Area | Implemented | Tests | Notes/Paths |
|------|-------------|-------|-------------|
| Scheduler dispatch (NATS direct + topic) | Yes | Unit + integration (`core/controlplane/scheduler/integration_test.go`) | Least-loaded picks direct subject via `bus.DirectSubject`; fallback to topic queue. |
| Pool routing + requires | Yes | Unit (`core/controlplane/scheduler/strategy_least_loaded_test.go`) | Pool capability filtering using `JobMetadata.requires`. |
| Job state store | Yes | Unit (`core/infra/memory/job_store_test.go`) | Redis with WATCH; traces, deadlines, safety, idempotency. |
| Safety kernel decisions | Yes | Unit (`core/controlplane/safetykernel/kernel_test.go`) | Policy decisions + constraints + snapshots. |
| Workflow engine | Yes | Unit (`core/workflow/engine_test.go`) | Fan-out, retries/backoff, approvals, delay/notify/condition, rerun/dry-run. |
| Workflow run timeline | Yes | Unit (`core/workflow/store_redis_test.go`) | Append-only timeline stored per run. |
| Config service (hierarchical) | Yes | Unit (`core/configsvc/service_test.go`) | system -> org -> team -> workflow -> step merge. |
| DLQ store + retry | Yes | Unit (`core/infra/memory/dlq_store_test.go`) | Gateway retry replays context with new job id. |
| Schema registry + validation | Yes | Unit (`core/infra/schema/registry_test.go`) | JSON schema validation for workflow inputs/outputs. |
| Locks service | Yes | Unit (`core/infra/locks/redis_store_test.go`) | Shared/exclusive locks with TTL. |
| Artifact store | Yes | None | Redis-backed store (`core/infra/artifacts`). |
| Gateway HTTP/WS endpoints | Yes | Unit (`core/controlplane/gateway/gateway_test.go`) | Jobs, workflows, approvals, policy, schemas, locks, artifacts, DLQ. |
| Worker runtime SDK | Yes | None | `sdk/runtime` CAP worker runtime. |
| CLI (coretexctl) | Yes | None | `cmd/coretexctl` + smoke script. |

Keep this table updated when wiring new components.
