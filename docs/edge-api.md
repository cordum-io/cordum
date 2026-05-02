# Edge API observability notes

This page supplements the generated OpenAPI spec for `/api/v1/edge/*` with the
observability contract used by the P0 Edge APIs.

## Error envelope

All Edge API errors use the standard JSON envelope:

```json
{
  "code": "idempotency_conflict",
  "message": "idempotency key already used with a different request",
  "request_id": "req-...",
  "details": { "safe_code": "idempotency_conflict" }
}
```

`details` is optional and is redacted centrally before serialization. Do not add
raw hook payloads, idempotency keys, Authorization headers, signed URLs, prompts,
commands, or tool output to error details. If a handler needs client-actionable
context, use stable codes and bounded enum-like fields.

## Event idempotency replay contract

`POST /api/v1/edge/events` and `/api/v1/edge/events/batch` accept an optional
`Idempotency-Key` scoped by tenant and endpoint. A retry with the same normalized
request replays the first `201` response; the same key with a different normalized
request returns `409` with `code="idempotency_conflict"`.

For idempotent event writes, event append and replay-record completion commit in
the same Redis transaction. A client observes either a committed event with a
replayable `201` response, or no committed event for that failed attempt. If the
replay record expires before a retry and the same logical `event_id` is already
present in the execution log, the API returns `409` with
`code="idempotency_window_expired"` and does not append a duplicate event.
Explicit-seq clients remain protected by the `seq=lastSeq+1` invariant.

This is a forward-only fix. Existing orphaned pending markers from before this
change are not backfilled; operators may manually delete those Redis
`edge:idempotency:*` keys if needed after confirming the persisted event log.

## Audit and metrics

Gateway Edge handlers reuse `core/edge.Recorder` and the existing audit exporter:

- session/execution lifecycle routes emit `edge.session_*` and
  `edge.execution_*` audit events;
- `/api/v1/edge/evaluate` emits policy decision / denial / approval-requested
  audit events and action/evaluate metrics;
- approval routes emit approval resolved/rejected/expired metrics and audit;
- `/api/v1/edge/sessions/{id}/export` emits artifact export metrics/audit;
- the Edge stream bridge emits bounded stream drop reasons.

See [Edge observability](edge-observability.md) for metric names, labels, audit
fields, and redaction rules.

## Not Cordum Jobs

Edge sessions/actions are compliance evidence for local agent activity. They are
not Cordum Jobs and do not create job lifecycle audit entries unless explicitly
linked to an existing production `job_id` or workflow run.
