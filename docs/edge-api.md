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
