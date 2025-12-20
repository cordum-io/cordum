# Backend Feature Integration Matrix

This page tracks backend features against their implementation and test coverage so we can ensure the system is fully integrated (Redis + NATS path).

| Area | Implemented | Tests | Notes/Paths |
|------|-------------|-------|-------------|
| Scheduler dispatch (NATS direct + topic) | Yes | Unit + integration (`core/controlplane/scheduler/integration_test.go`) | Least-loaded picks direct subject via `bus.DirectSubject`; falls back to topic queue. |
| Job state store | Yes | Unit (`core/infra/memory/job_store_test.go`) | Redis with WATCH; traces, deadlines, safety, tenant/team/principal metadata. |
| Safety circuit breaker | Yes | Unit (`core/controlplane/scheduler/safety_client_test.go`) | Half-open with throttle + close-after-success; sends `effective_config` to kernel. |
| Worker registry TTL | Yes | Unit (`core/controlplane/scheduler/registry_memory_test.go`) | Heartbeats expire; snapshot filters stale workers. |
| Reconciler timeouts | Yes | Unit (`core/controlplane/scheduler/reconciler_test.go`) | Bounded retries/backoff on SetState failures. |
| Job result mapping | Yes | Unit (`core/controlplane/scheduler/engine_test.go`) | `COMPLETED` alias maps to `SUCCEEDED`; DLQ only on non-success. |
| Job listing pagination | Yes | Unit (`core/infra/memory/job_store_test.go`), API handler exercised | Cursor-based via `ListRecentJobsByScore`; API returns `next_cursor`. |
| Workflow engine | Yes | Unit (`core/workflow/engine_test.go`) | Fan-out, retries/backoff, approvals, cancel guard, NATS dispatch. |
| Config service (hierarchical) | Yes | Unit (`core/configsvc/service_test.go`) | system→org→team→workflow→step merge; scheduler injects effective config. |
| DLQ store + retry | Yes | Unit (`core/infra/memory/dlq_store_test.go`) | Gateway retry replays original context with new job id. |
| Cancel propagation | Yes | Unit (`core/controlplane/scheduler/engine_test.go`) | Gateway + scheduler publish `sys.job.cancel`; workers built on `core/agent/runtime` cancel matching in-flight job IDs. |

Open items tracked in `docs/backend_tasks.md` (artifacts/secrets, richer trace search, per-step cancel hooks). Keep this table updated when wiring new components.
