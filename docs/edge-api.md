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

## Evaluate action hash and approval CAS

`/api/v1/edge/evaluate` computes `action_hash` from the canonicalized action
tuple. `risk_tags` are sorted lexicographically before hashing, and label keys
are serialized in deterministic JSON key order. This keeps equivalent classifier
outputs stable across Go versions and platforms.

Approval consume-once CAS checks the stored `action_hash`, `policy_snapshot`,
and `input_hash`. The explicit `input_hash` equality check is defense-in-depth:
even if a future `action_hash` refactor changes which fields are folded into the
hash, an approval cannot be replayed against different input bytes.

Approval principal binding applies to `GET /api/v1/edge/approvals` list pages,
`GET /api/v1/edge/approvals/{ref}`, and
`POST /api/v1/edge/approvals/{ref}/wait`. For list, non-admin/non-operator
callers receive only approvals whose `principal_id` matches the authenticated
`auth.PrincipalID`; admin/operator callers can list all approvals in the tenant.
Status, tuple, cursor, and limit filters apply inside that visibility scope so
pagination remains stable per principal. For detail and wait, the caller must be
either the original requester (`auth.PrincipalID` matches the approval
`principal_id`) or an admin/operator role. Unauthorized detail/wait callers see
the same 404 envelope as cross-tenant or missing approvals, preventing
tenant-insider enumeration of approval timing and decisions.

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
