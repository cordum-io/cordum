# Audit verify API

`GET /api/v1/audit/verify` is the operator-facing read path for Cordum's
tamper-evident audit chain. It walks the tenant's hash chain and reports
whether the sequence is intact, retention-trimmed, or compromised.

For the deeper chain design, storage model, and incident runbook, see
[`docs/deployment/audit-chain.md`](../deployment/audit-chain.md).

## Endpoint

```text
GET /api/v1/audit/verify
```

## Auth

- **Admin required**
- Tenant scope is resolved from auth context plus `X-Tenant-ID`
- Optional `tenant` query parameter must match the caller's tenant scope

Examples below use the bootstrap API-key flow because that is the common
self-hosted operator path.

## Request

### Query parameters

| Parameter | Type | Default | Notes |
|-----------|------|---------|-------|
| `tenant` | string | caller tenant / `default` in local dev | Must match caller scope. |
| `since` | unix milliseconds | unset | Inclusive lower bound on audit event time. |
| `until` | unix milliseconds | unset | Inclusive upper bound on audit event time. |
| `limit` | integer | `10000` | Maximum `100000`. |

### Curl

```bash
export BASE=https://localhost:8081
export TENANT=default
export API_KEY=...

curl -sS "$BASE/api/v1/audit/verify?tenant=$TENANT" \
  --cacert ./certs/ca/ca.crt \
  -H "X-API-Key: $API_KEY" \
  -H "X-Tenant-ID: $TENANT"
```

### CLI

```bash
cordumctl audit verify default --json
```

## Response shape

Representative response:

```json
{
  "status": "ok",
  "total_events": 1284,
  "verified_events": 1284,
  "first_seq": 1,
  "last_seq": 1284,
  "retention_boundary_seq": 1,
  "retention_window_hours": 168,
  "gaps": []
}
```

Important fields:

| Field | Meaning |
|-------|---------|
| `status` | `ok`, `partial`, or `compromised` |
| `total_events` | Events scanned in the requested window |
| `verified_events` | Events that verified cleanly |
| `first_seq` / `last_seq` | Sequence range actually observed |
| `retention_boundary_seq` | Lowest sequence still present in the stream |
| `retention_window_hours` | Effective retention policy window |
| `gaps` | Missing / hash-mismatch / out-of-order findings |

The endpoint never returns raw event bodies. It is an integrity-report
surface, not an audit-event export surface.

## Performance characteristics

`GET /api/v1/audit/verify` deliberately re-walks the requested audit
chain window on every call. That full re-hash is the tamper-evidence
guarantee: a caller gets a fresh verification of the bytes currently in
Redis instead of trusting a cached attestation that could be stale or
mutated between checks.

Latency therefore scales roughly linearly with events scanned. The
llm-chat governance probe 4 measured a 10,000-event chain at roughly
199-241 ms for serial full-window calls (about 25 μs/event on that dev
stack) and 2.3s p99 when 20 identical callers each forced their own walk.
Current builds coalesce concurrent identical requests with `singleflight`,
so the 20 callers share one leader walk when their tenant, `since`,
`until`, effective `limit`, retention boundary, and effective HMAC
verification configuration match exactly.

Operators should watch:

- `cordum_audit_verify_duration_seconds{status=...}` — leader-walk
  latency histogram.
- `cordum_audit_verify_events_total{status=...}` — events scanned by
  leader walks.
- `cordum_audit_verify_inflight` — active chain walks.
- `cordum_audit_verify_coalesced_total` — callers served by another
  in-flight identical verify.

## Recommended hot-path pattern (since cursor)

Use a narrow `since` cursor for dashboards, chat governance probes, and
other recurring health checks. A five-minute cursor keeps the tamper check
fresh without re-hashing the full default 10,000-event window every time:

```bash
SINCE_MS=$((($(date +%s) - 300) * 1000))

curl -sS "$BASE/api/v1/audit/verify?tenant=$TENANT&since=$SINCE_MS" \
  --cacert ./certs/ca/ca.crt \
  -H "X-API-Key: $API_KEY" \
  -H "X-Tenant-ID: $TENANT"
```

Mid-chain slices still verify linkage. `VerifyChain` (see
`core/audit/chain_verify.go` near the predecessor lookup for `SinceMs`)
reads one predecessor before the requested `since` boundary, so a window
that starts in the middle of a tenant chain still checks the first
in-window event against the previous hash.

Reserve full-window verify for operator attestations, incident response,
and low-frequency compliance checks. As a rule of thumb, run at most one
full-window verify per tenant per minute; the dashboard's five-minute
refresh interval is the canonical recurring check cadence.

## Error codes

| Status | When it happens |
|--------|-----------------|
| `400` | Invalid `since`, `until`, or `limit` query parameters |
| `401` | Missing or invalid authentication |
| `403` | Caller is not an admin or crossed tenant scope |
| `500` | Redis / chain walk / internal server failure |
| `503` | Audit chainer not installed, so integrity cannot be attested |

## Operator notes

- A tenant with no recent events can still return `status=ok` if the audit
  chainer is installed.
- `status=partial` means the chain is explainable only by a
  retention-trimmed prefix; investigate, but it is not automatically a
  compromise.
- Any `hash_mismatch`, `missing`, or `out_of_order` gap is a compliance
  stop-condition.

## See also

- [`docs/deployment/audit-chain.md`](../deployment/audit-chain.md)
- [`docs/api/openapi/cordum-api.yaml`](openapi/cordum-api.yaml)
