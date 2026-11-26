# Retry-layer contract

Status: **source-reviewed; nothing measured**.
Reviewed source: cordum integration/next `660f9d05cd1dbee268adfb8f17fb7d4476cf902d`; CAP
`github.com/cordum-io/cap/v2` v2.17.0, the version go.mod pins.
Tags: contract, retries, PR2-10.
Rule: this document records each retry limit as the code sets it. It measures nothing, and no
number in it is a test result.

A handler run is one call of a worker's job handler. The job path starts at `POST /api/v1/jobs`.
The workflow-step path is one step of a run started with `POST /api/v1/workflows/{id}/runs`.
`TestRetryLayersContract` (tests/contract) checks this file against the code: every Code ref
resolves, every quoted Go default equals the code, and every cap equals the product of its factors.
A `cap:` ref is relative to the pinned CAP module root. A non-Go ref is a literal anchor in that file.

## Layers

| Layer | Path | Trigger | Re-runs handler | Limit | Setting | Code |
| --- | --- | --- | --- | --- | --- | --- |
| client | job, workflow step | A transport error, or status 408, 425, 429, 500, 502, 503 or 504, on GET, HEAD, PUT, DELETE or OPTIONS, or on a POST that carries an idempotency key. The Go SDK's `SubmitJob` does not retry. | No. The gateway reserves an idempotency key once per tenant, so a retried submit or run start never creates a second job or run. | Go: `defaultRequestMaxAttempts = 3` attempts. TypeScript `maxRetries: 3` and Python `max_retries: int = 3` retries, so 4 attempts; both are settable with no enforced maximum. One job or run per idempotency key. | TypeScript and Python `RetryPolicy`. The Go SDK has none. | sdk/client/request_detailed.go#defaultRequestMaxAttempts, sdk/client/request_detailed.go#isRetryableMethod, sdk/client/request_detailed.go#shouldRetryStatus, sdk/client/request_detailed.go#Client.RequestDetailed, sdk/typescript/src/retry.ts#maxRetries: 3, sdk/python/src/cordum_sdk/_retry.py#max_retries: int = 3, core/controlplane/gateway/helpers.go#idempotencyKeyFromRequest, core/controlplane/gateway/handlers_jobs.go#server.handleSubmitJobHTTP, core/infra/store/job_store.go#RedisJobStore.TrySetIdempotencyKeyScoped, core/controlplane/gateway/handlers_workflows.go#server.handleStartRun |
| bus | job, workflow step | JetStream redelivers a message on a durable subject when its consumer naks it (the handler returned a `RetryAfter` error) or does not ack it within the ack wait. A plain handler error is acked. | No. The CAP runtime takes worker subjects through a core NATS queue subscription, which has no acks, so JetStream redelivers nothing to a handler. A redelivered submit is counted under scheduler. | `maxJSRedeliveries = 100` deliveries per message, then the message is terminated. Ack wait `defaultAckWait = 10 * time.Minute`. | `NATS_USE_JETSTREAM` turns JetStream on (the compose file sets 1). `NATS_JS_ACK_WAIT` sets the ack wait. The delivery limit has no setting. | core/infra/bus/nats.go#maxJSRedeliveries, core/infra/bus/nats.go#defaultAckWait, core/infra/bus/nats.go#processBusMsgCtx, core/infra/bus/nats.go#NatsBus.terminateMaxDelivery, core/infra/bus/nats.go#isDurableSubject, docker-compose.yml#NATS_USE_JETSTREAM=1, cap:sdk/go/runtime/lifecycle.go#Agent.subscribeHandlers |
| scheduler | job, workflow step | The scheduler handles a job request again while the job is PENDING or SCHEDULED: a JetStream redelivery, a pending-job replay, or a dispatch whose publish failed and rolled the job back to SCHEDULED. DISPATCHED, RUNNING and terminal jobs are skipped. A worker's failed result is not dispatched again. | Only when a dispatch publish reports an error after the worker already got the message: the rolled-back job is dispatched again. | At most `maxSchedulingRetries = 50` dispatches per job. Each dispatch raises the job's attempts count by at least 1, and the job fails to the DLQ once attempts reach the limit. A policy `budgets.max_retries` of R lowers it to R + 1 attempts. open finding PR2-10-F1 | Policy `budgets.max_retries` (lowers only). `retry.max_retries` in config/system.yaml is read by no layer in this table. | core/controlplane/scheduler/engine.go#maxSchedulingRetries, core/controlplane/scheduler/engine.go#Engine.handleJobRequest, core/controlplane/scheduler/engine.go#Engine.processJob, core/controlplane/scheduler/engine.go#maxRetriesFromConstraints, core/controlplane/scheduler/engine.go#Engine.handleJobResult, core/infra/store/job_store.go#RedisJobStore.SetStateWithContext, core/infra/store/job_store.go#RedisJobStore.IncrAttempts |
| worker handler | job, workflow step | The job handler returns an error. | Yes, in the same process for the same message. | 1 + `retries` runs per message. `retries` defaults to 0, and a negative value counts as 0; no enforced maximum. | CAP `WithRetries(n)` per handler, else `Agent.Retries`. The demo workers set neither. | cap:sdk/go/runtime/runtime.go#Agent.handleMessage, cap:sdk/go/runtime/runtime.go#WithRetries, cap:sdk/go/runtime/runtime.go#Register |
| workflow step | workflow step | A step's job result is FAILED, TIMEOUT or FAILED_RETRYABLE and the step's `retry.max_retries` is above 0. A denied step is not retried. | Yes, as a new job `<run>:<step>@<n>`. A result for another attempt's job id, or one already applied, is ignored. | `max_retries` + 1 attempts per step. The default is 0 retries (no `retry` block); no enforced maximum. | Step `retry.max_retries` in the workflow definition. | core/workflow/engine_state.go#shouldRetry, core/workflow/engine_state.go#applyResult, core/workflow/models.go#RetryConfig, core/workflow/engine.go#Engine.handleJobResultLocked |
| manual DLQ retry | job, workflow step | An operator calls `POST /api/v1/dlq/{job_id}/retry`. | Yes, as a new job `<job_id>-retry-<8 hex>`. The DLQ entry is deleted. | One new job per call; no enforced maximum on calls. | None; each call is an operator request. | core/controlplane/gateway/handlers_dlq.go#server.handleRetryDLQ |

## Cap

The cap is the most handler runs one original request can cause under the shipped compose
defaults: `NATS_USE_JETSTREAM=1`, no policy `budgets.max_retries`, no CAP `retries` and no step
`retry` block. Each factor is the most handler runs its layer multiplies by. The cap is their
product.

| Path | Factors | Cap |
| --- | --- | --- |
| job | client 1 × bus 1 × scheduler 50 × worker handler 1 × manual DLQ retry 1 | 50 |
| workflow step | client 1 × workflow step 1 × bus 1 × scheduler 50 × worker handler 1 × manual DLQ retry 1 | 50 |

- client 1: `TrySetIdempotencyKeyScoped` reserves a key with a set-if-absent per tenant, and a
  retry with the same key gets the first job or run id back, or a 409 when that lookup fails, so no
  client retry creates a second job or run.
- workflow step 1: with no `retry` block `shouldRetry` returns false, so a step runs as one job;
  with `retry.max_retries` M the factor is M + 1, and M has no enforced maximum.
- bus 1: JetStream redelivers only to its own consumers, and the CAP runtime receives worker
  subjects through core NATS `QueueSubscribe` (`Agent.subscribeHandlers`), so no redelivery reaches
  a handler; a redelivered submit is bounded by the scheduler factor.
- scheduler 50: `SetStateWithContext` counts an attempt on entry to SCHEDULED and
  `handleJobRequest` counts one on a SCHEDULED replay, so every dispatch raises attempts by at least
  1, and `processJob` fails the job once attempts reach `maxSchedulingRetries`. 50 is the most the
  constant allows, not a count observed.
- worker handler 1: `handleMessage` runs the handler 1 + `retries` times per message and `retries`
  defaults to 0; with `retries` N the factor is N + 1, and N has no enforced maximum.
- manual DLQ retry 1: nothing calls the retry endpoint under the defaults. Each call is an operator
  request that starts a new job with its own cap, and calls have no enforced maximum.

## Questions settled from the code

**How many times can one job id reach a handler?** At most once per dispatch, times 1 + `retries`
in the CAP runtime: 50 under the defaults. Worker subjects (`job.*`, `worker.<id>.jobs`) are durable
on the control-plane side (`isDurableSubject`), but the CAP runtime subscribes to them with core
NATS `QueueSubscribe`. JetStream's ack wait (`defaultAckWait`) and delivery limit
(`maxJSRedeliveries`) therefore never apply to a handler delivery. The CAP runtime's
`ProductionAdmission` rejects a replayed message id only when a worker turns it on. It is off by
default, so the cap counts no dedupe.

**Can an idempotent client retry create a second job?** No. `handleSubmitJobHTTP` reserves the key
before it queues the job and does not release it when queueing fails, so a retry with the same key
gets the first job id, or a 409 when that lookup fails. `handleStartRun` releases a run key only
when it rejects the request before it creates the run. No SDK retries a POST that carries no
idempotency key.
