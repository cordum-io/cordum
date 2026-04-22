# demo-mock-bank

A governed financial-transfer demo. Three amounts, three policy verdicts,
one workflow — the flagship proof that Cordum can route, gate, and audit
a mixed-risk workload end-to-end.

| Amount  | Risk tag   | Rule                     | Terminal state                       | Wall-clock budget |
|---------|------------|--------------------------|--------------------------------------|-------------------|
| `$40`   | `low`      | `bank-transfer-allow`    | `succeeded`                          | ≤ 10 s            |
| `$200`  | `review`   | `bank-transfer-review`   | `require_approval` → `succeeded`[^1] | ≤ 15 s post-approve |
| `$5000` | `blocked`  | `bank-transfer-blocked`  | `denied`                             | ≤ 10 s            |

[^1]: A second shell approves via `cordumctl approval job <id> --approve`.

Four pools (`demo-mock-bank`, `bank-validators`, `bank-executors`) and
seven agents (transfer / compliance / audit / validator / executor) back
the workflow; the safety kernel decides each step from the risk tag
attached at workflow-authoring time.

## Quickstart

```bash
# From the repo root, with the core stack already up (make dev-up).
cordumctl pack install ./demo/mock-bank/pack --upgrade
docker compose --profile demo up -d

# Wait for the transfer agents to heartbeat.
curl -sk "https://127.0.0.1:8081/api/v1/workers" -H "X-API-Key: $CORDUM_API_KEY" \
  | jq '.items[] | select(.worker_id | startswith("megacorp-transfer-agent-")) | .worker_id + " online=" + (.online|tostring)'

# Allow path.
RUN_ID=$(cordumctl run start demo-mock-bank.transfer \
  --input '{"amount":40,"currency":"USD","customer":"Alice","reason":"demo","requested_by":"qa"}')
cordumctl run get "$RUN_ID" | jq '.status, .steps.execute_low.status'

# Require-approval path.
RUN_ID=$(cordumctl run start demo-mock-bank.transfer \
  --input '{"amount":200,"currency":"USD","customer":"Alice","reason":"demo","requested_by":"qa"}')
JOB_ID=$(cordumctl run get "$RUN_ID" | jq -r '.steps.execute_review.job_id')
cordumctl approval job "$JOB_ID" --approve
cordumctl run get "$RUN_ID" | jq '.status, .steps.execute_review.status'

# Deny path.
RUN_ID=$(cordumctl run start demo-mock-bank.transfer \
  --input '{"amount":5000,"currency":"USD","customer":"Alice","reason":"demo","requested_by":"qa"}')
cordumctl run get "$RUN_ID" | jq '.status, .steps.execute_high.status'
```

`CORDUM_API_KEY` is in `.env` after `make dev-up`.

## The three verdict paths

The `demo-mock-bank.transfer` workflow has four steps: `validate` (a
transform) and three mutually-exclusive worker steps gated on amount.

| Step            | Condition                    | Risk tag   | Rule                     | Behaviour                                              |
|-----------------|------------------------------|------------|--------------------------|--------------------------------------------------------|
| `execute_low`   | `amount < 100`               | `low`      | `bank-transfer-allow`    | Allowed immediately, worker processes.                 |
| `execute_review`| `100 <= amount < 300`        | `review`   | `bank-transfer-review`   | Safety kernel escalates — awaits `cordumctl approval`. |
| `execute_high`  | `amount >= 300`              | `blocked`  | `bank-transfer-blocked`  | Safety kernel denies. Worker never runs.               |

## Troubleshooting a stalled run

A run can appear stuck for three structurally different reasons. Each
has a one-line diagnostic:

```bash
# 1. Scheduler-side vs worker-side vs workflow-side split.
docker compose logs mock-bank-worker | grep 'mock-bank job_'
```

- **No `job_received` line for your `job_id`** → *scheduler-side*: the
  job was never delivered. The scheduler did not dispatch (no live
  worker? pool drained? heartbeat TTL expired?), or the safety kernel
  denied before dispatch.
- **`job_received` but no `job_completed`** → *worker-side*: the worker
  crashed mid-job, the NATS subject changed, or a panic bypassed the
  counter wrapper. Check the immediately following log lines for a
  stack trace.
- **`job_completed` but workflow still `running`** → *workflow-engine
  side*: the result published but the engine has not transitioned. Look
  at `cordum-workflow-engine` logs for the run id.

```bash
# 2. Sanity cross-checks (use the run id from cordumctl run start).
cordumctl run get "$RUN_ID"                                         | jq '.status, .steps'
docker compose logs cordum-scheduler        | grep "$RUN_ID"
docker compose logs cordum-workflow-engine  | grep "$RUN_ID"
```

```bash
# 3. First-run dispatch delay.
#    If you install the pack BEFORE mock-bank-worker is up, the first
#    job waits for heartbeats + scheduler pickup — up to several minutes.
#    Wait for at least one transfer agent to report online before submitting,
#    or restart the scheduler to flush the dispatch queue.
curl -sk "https://127.0.0.1:8081/api/v1/workers" -H "X-API-Key: $CORDUM_API_KEY" \
  | jq '.items[] | select(.worker_id=="megacorp-transfer-agent-01") | .online'
```

```bash
# 4. Verify active_jobs is real, not simulated.
#    Submit a job, check mid-flight, check after.
curl -sk "https://127.0.0.1:8081/api/v1/workers" -H "X-API-Key: $CORDUM_API_KEY" \
  | jq '.items[] | select(.worker_id=="megacorp-transfer-agent-01") | .active_jobs'
```

The `active_jobs` value is derived from a real in-flight counter in the
worker process (per `workerDef`, atomic, decremented on return / error /
panic). A non-zero reading during a run followed by zero afterward is
the positive signal; a value that doesn't move when jobs are arriving
means heartbeats are not flowing — fall back to checking
`cordum-scheduler` logs for the worker id.

## Implementation notes

- **Workflow**: [`pack/workflows/transfer.yaml`](pack/workflows/transfer.yaml)
  — one `validate` transform, three mutually-exclusive `worker` steps
  on `job.demo-mock-bank.transfer`.
- **Policy fragment**: [`pack/overlays/policy.fragment.yaml`](pack/overlays/policy.fragment.yaml)
  — three rules keyed off the step risk tags (`low` / `review` /
  `blocked`).
- **Pools**: [`pack/overlays/pools.patch.yaml`](pack/overlays/pools.patch.yaml)
  patches `demo-mock-bank`, `bank-validators`, `bank-executors` into
  the system pool config.
- **Worker fleet**: [`worker/main.go`](worker/main.go) — seven agents
  across three pools. Per-agent `atomic.Int32` in-flight counter feeds
  `heartbeat.active_jobs`. Structured slog JSON to stderr: every job
  emits `job_received` → `decision_made` → `job_completed` with the
  keys `job_id`, `worker_id`, `pool`, `topic`, `amount`, `verdict`,
  `rule`, `duration_ms`. Raw payload fields (`customer`, `note`,
  `prompt`) are never logged.

## Uninstalling

```bash
cordumctl pack uninstall demo-mock-bank --purge
docker compose --profile demo down
```
