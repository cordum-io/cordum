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

CompleteIdempotency failure mode: if the event persists but the idempotency key
fails to commit, the response uses `code="partial_idempotency_failure"` with a
5xx status. The pending key is released; clients SHOULD dedupe at the application
layer using `event_id` from the persisted log. Auto-seq clients MAY observe a
duplicate event on retry under this code; explicit-seq clients are protected by
the `seq=lastSeq+1` invariant.

This is a forward-only fix. Existing orphaned pending markers from before this
change are not backfilled; operators may manually delete those Redis
`edge:idempotency:*` keys if needed after confirming the persisted event log.
If the Gateway process crashes after append but before the cleanup attempt runs,
the same pending-marker recovery procedure applies.

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
