# Policy Decision Log

Cordum's **Policy Decision Log** is the governance-native read path for
policy outcomes. It records matched rule, verdict, reason, constraints,
and approval lifecycle metadata for every safety decision so operators can
ask *why did policy allow, deny, constrain, throttle, or require
approval for this job?*

> **Policy Decision Log and governance analytics — NOT a trace viewer.**

This surface is intentionally governance-specific. It is not a span
waterfall and it is not framed as infrastructure tracing.

## What ships in this task

- `GET /api/v1/governance/decisions` for tenant-scoped reads with cursor
  pagination
- Redis-backed decision-log indexes keyed by time, rule, agent, topic,
  and verdict
- Synchronous write-through from the authoritative
  `SetSafetyDecision` scheduler path
- `cordumctl governance backfill-decisions` to index historical safety
  decisions
- `cordumctl governance tail` to self-heal gaps from `sys.audit.export`
  `safety.decision` events

## Wire contract

### Endpoint

```text
GET /api/v1/governance/decisions
```

### Query parameters

| Parameter | Type | Default | Notes |
|-----------|------|---------|-------|
| `since` | RFC3339 timestamp or unix-milliseconds | last 24h | Inclusive lower bound. |
| `until` | RFC3339 timestamp or unix-milliseconds | now | Inclusive upper bound. |
| `topic` | string | unset | Exact topic match. |
| `rule_id` | string | unset | Exact matched-rule ID match. |
| `verdict` | enum | unset | `allow`, `deny`, `constrain`, `require_approval`, `throttle`. |
| `agent_id` | string | unset | Exact agent identity match. |
| `cursor` | opaque string | unset | Pass the previous page's `next_cursor`. |
| `limit` | integer | `50` | Maximum `500`. |

### Response shape

```json
{
  "items": [
    {
      "job_id": "job-42",
      "topic": "payments.transfer",
      "matched_rule": "payments-high-value-approval",
      "verdict": "require_approval",
      "reason": "manual review required",
      "constraints": {
        "maxRatePerMinute": 5
      },
      "approval_status": "pending",
      "approval_decision": "",
      "agent_id": "agent-finance-01",
      "policy_version": "snap-2026-04-20",
      "timestamp": "2026-04-20T09:15:00Z"
    }
  ],
  "next_cursor": "MTcxMzYwNDUwMDAwMDoyM2E0..."
}
```

`next_cursor` is omitted when the current page is the last page.

## Curl recipes

Set these once:

```bash
export BASE=http://localhost:8081
export TENANT=acme
export AUTH="X-API-Key: $CORDUM_API_KEY"
```

### List the last 24 hours of policy decisions

```bash
curl -sS "$BASE/api/v1/governance/decisions" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH"
```

### Filter by verdict + topic + time window

```bash
curl -sS "$BASE/api/v1/governance/decisions?verdict=require_approval&topic=payments.transfer&since=2026-04-13T00:00:00Z&until=2026-04-20T00:00:00Z&limit=100" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH"
```

### Continue pagination

```bash
curl -sS "$BASE/api/v1/governance/decisions?cursor=<next_cursor>&limit=100" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH"
```

## Retention semantics

Decision-log records inherit the governance log retention policy from the
Redis-backed index store:

- Default TTL: **30 days**
- Override: `CORDUM_DECISION_LOG_TTL_SECONDS`
- Expiry is enforced on the per-record hash and by trimming time/rule/
  agent/topic/verdict indexes when new writes land

The log is an operator-facing governance index, not the canonical audit
archive. Long-lived compliance retention should still rely on the audit
export + legal-hold surfaces.

## RBAC notes

Reads require the `governance.read` permission. When advanced RBAC is
entitled, that permission gates the endpoint directly. When RBAC is not
available, the gateway falls back to the built-in viewer/operator/admin
read posture already used by the governance surfaces.

Tenant scope is always resolved from authenticated middleware context +
`X-Tenant-ID`; the handler does **not** accept tenant overrides in query
parameters.

## Operator runbook

### 1. Backfill historical decisions

Historical jobs only become visible after they are projected into the
Policy Decision Log. Run the helper from the repo or ship the helper
binary alongside `cordumctl`.

```bash
# Dry-run first
cordumctl governance backfill-decisions --since 2026-04-01 --until 2026-04-20 --dry-run

# Then write
cordumctl governance backfill-decisions --since 2026-04-01 --until 2026-04-20
```

Behavior:

- Scans persisted `job:req:*` records in Redis
- Loads safety decisions from `job:meta:*`
- Projects each decision into a deterministic decision-log record ID:
  `sha256(tenant|job_id|checked_at_ms)`
- Writes only missing entries; repeat runs are idempotent
- Streams progress to stderr and emits a final JSON summary to stdout

### 2. Tail audit export for drift repair

The primary path is synchronous scheduler write-through. The tail command
is a self-healing backstop for replica drift:

```bash
cordumctl governance tail
```

Behavior:

- Subscribes to `sys.audit.export`
- Extracts only `safety.decision` alerts emitted by `audit-export`
- Appends the corresponding decision-log record when it does not already
  exist
- Ignores non-governance audit traffic

In production, prefer shipping the `cordumctl-governance-helper` binary
next to `cordumctl`. When running from the repo, `cordumctl governance …`
falls back to `go run ./cmd/cordumctl-governance-helper ...`.

## Performance expectations

The store is designed to keep a 7-day query under **500 ms** on the task
benchmark gate. The benchmark seeds 50k records and exercises the query
path with time-index scans plus secondary indexes.

Operationally:

- Default page size is `50`
- Hard maximum page size is `500`
- Multi-filter intersections use precomputed Redis sorted-set indexes
- Candidate scanning is bounded to avoid pathological full-window reads

If operators need longer windows, prefer paging by time or rule/topic
rather than forcing a single unbounded request.
