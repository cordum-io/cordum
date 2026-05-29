# Audit chain list API — `GET /api/v1/audit/events`

The audit chain list endpoint exposes the per-tenant SIEM-feed (the chained
audit stream populated from NATS `sys.audit.export`) for the dashboard Audit
Log page and any downstream operator tool that needs to read recent audit
events without running the full `/audit/verify` integrity check.

## Source

- Handler: `core/controlplane/gateway/handlers_audit_events.go::handleListAuditEvents`
- Route registration: `core/controlplane/gateway/gateway.go` § 2.7.2
- Underlying stream: `audit.Chainer.StreamKey(tenant)` — Redis Stream with
  HMAC-chained entries appended by `audit.Chainer.Append`.
- OpenAPI: `docs/api/openapi/cordum-api.yaml` (operationId `getAuditEvents`,
  tag `AuditExport`, schema `AuditEvent` / `AuditEventsEnvelope`).

## Contract

`GET /api/v1/audit/events`

Query parameters (all optional):

| Param        | Type   | Notes |
|--------------|--------|-------|
| `tenant`     | string | Override tenant. Subject to `requireTenantAccess` — non-admin callers may only pass their own tenant. |
| `limit`      | int    | Page size. Default `100`, hard cap `MaxAuditEventsLimit = 200`. Values above the cap are silently clamped. |
| `cursor`     | string | Opaque Redis Stream ID for pagination. Pass the previous page's `next_cursor`. Empty cursor returns the most recent page. |
| `event_type` | string | Exact match on `event_type` (case-insensitive). |
| `severity`   | string | Exact match on `severity` (case-insensitive). |
| `from`       | RFC3339| Inclusive lower bound on `timestamp`. |
| `to`         | RFC3339| Inclusive upper bound on `timestamp`. Returns `400` if `to < from`. |
| `search`     | string | Lowercase substring match across `action`, `event_type`, `agent_id`, `job_id`, `identity`, `reason`. |

Response (`200 OK`):

```json
{
  "items": [ /* AuditEvent[] */ ],
  "next_cursor": "1700000000000-0",
  "returned": 47
}
```

`next_cursor` is empty when the stream has been fully drained for the
current filter set. Clients should stop paginating when `next_cursor === ""`.

## Permission and tenancy

- Caller must hold `auth.PermAuditRead = "audit.read"`. The legacy `admin`
  role is also accepted via the standard `requireStoreAndPermissionOrRole`
  permission wall.
- Tenant is resolved via `resolveTenant(r, ?tenant)`:
  - Default: caller's bound tenant from the auth context.
  - Admin callers with `AllowCrossTenant=true` may override via `?tenant=`.
- `requireTenantAccess` rejects mismatched overrides with `403`.

## 503 — chainer not installed

If the gateway is started without an HMAC-enabled audit chainer
(`CORDUM_AUDIT_HMAC_KEY` unset or chainer construction failed), the endpoint
returns `503` with body code `audit_chainer_not_installed`. The dashboard
surface translates this into a human-readable banner ("Audit chain not
installed — contact your operator").

> In production (`CORDUM_ENV=production`), an unset `CORDUM_AUDIT_HMAC_KEY`
> is no longer a 503-at-request-time fallback — the gateway refuses to boot
> at all unless the operator sets `CORDUM_AUDIT_HMAC_OPTIONAL=true` as an
> explicit override. This 503 path now only fires in dev mode or when the
> override is active.

## Cursor stability under concurrent appends

The endpoint reads via `client.XRevRangeN` in batches of `limit *
auditEventsFetchMultiplier` (`= 4`). A concurrent append to the chained
stream:

- Appears in subsequent pages only — never in the current page mid-read.
  Redis Stream IDs are monotonic per stream, so the cursor (last seen ID
  minus one) excludes any append that lands between page boundaries.
- Cannot cause a duplicate within a single page. Each Redis Stream entry has
  a unique ID; the cursor's exclusive upper bound is decremented via
  `xRevBefore` before the next batch.

The cursor is opaque — it is the raw Redis Stream ID and contains no tenant
prefix, no secret material, and no caller identity. Operators may share a
cursor across sessions safely; the cursor encodes "where you are in the
read", not "who you are".

## Redaction (defense-in-depth)

The handler runs `redactExtraSecrets` over every `Extra` map before writing
the JSON response. The redactor drops keys matching
`(?i)secret|token|password|api[_-]?key|private[_-]?key` outright — the value
is not masked (`***`) because mask sentinels still carry a length signal
and would otherwise leak via JSON length.

This is defense-in-depth on top of the emit-side scrubbing performed by
`core/mcp/argument_redactor.go` and equivalent layer redactors. Operators
should still treat the emit-side rules as the primary contract; the read-side
redactor is a fail-safe for misconfigured emitter rules.

## Meta-audit (audit of audit)

Every successful `/audit/events` response emits an additional SIEM event
`audit.read.events` via `audit.Chainer.Append` to the same tenant's chained
stream. The meta-event records `caller_tenant`, `target_tenant`, `returned`,
`limit`, and the active filters — but never any redacted value. Failure to
append the meta-event is logged via `slog.Warn` and does not fail the
read (`200` is still returned).

This closes the audit-of-audit loop so any inspection of the chained stream
is itself observable in the chained stream.

## Filter semantics

Server-side filters are applied during the `XRevRangeN` walk. Client-side
filters (`event_type` multi-value, additional substring searches) MAY be
applied by the dashboard after the page returns — the server treats the
query string as the canonical set.

## Distinction from sibling endpoints

| Endpoint              | Purpose                                                              |
|-----------------------|----------------------------------------------------------------------|
| `/api/v1/audit/events`| **Read**: list chained audit events with cursor pagination.          |
| `/api/v1/audit/verify`| Verify: re-compute the per-tenant HMAC chain and report gaps.        |
| `/api/v1/audit/export`| Export: forward chained events to an external SIEM backend.          |
| `/api/v1/policy/audit`| Read the policy-bundle audit subset (deployments, rule edits, etc.). |

`/audit/events` is the SIEM read path consumed by the dashboard Audit Log
page; `/policy/audit` remains in use for the policy-bundle drilldown,
correlation, and export paths.
