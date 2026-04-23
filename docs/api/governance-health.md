# Governance health API

`GET /api/v1/governance/health` returns the Command Center's composite
governance score for a tenant. The score rolls four equally-weighted
factors into a 0-100 total and maps that total to an `A`-`F` grade.

## Endpoint

```text
GET /api/v1/governance/health
```

## Auth

- **Admin required**
- Tenant scope is resolved from auth context plus `X-Tenant-ID`
- Optional `tenant` query parameter must match the caller's tenant scope

## Request

### Query parameters

| Parameter | Type | Default | Notes |
|-----------|------|---------|-------|
| `tenant` | string | caller tenant / `default` in local dev | Must match caller scope. |

### Curl

```bash
export BASE=https://localhost:8081
export TENANT=default
export API_KEY=...

curl -sS "$BASE/api/v1/governance/health?tenant=$TENANT" \
  --cacert ./certs/ca/ca.crt \
  -H "X-API-Key: $API_KEY" \
  -H "X-Tenant-ID: $TENANT"
```

## Response shape

Representative response:

```json
{
  "score": 88,
  "grade": "B",
  "generated_at": "2026-04-23T09:00:00Z",
  "factors": {
    "denial_rate": { "score": 92, "weight": 25, "raw": { "allow": 42, "deny": 3, "require_approval": 5, "other": 0, "scanned_events": 50 } },
    "approval_latency_p95": { "score": 81, "weight": 25, "raw": { "p95_ms": 41000, "samples": 12 } },
    "policy_coverage": { "score": 80, "weight": 25, "raw": { "topics": 10, "covered": 8, "ratio": 0.8 } },
    "chain_integrity": { "score": 100, "weight": 25, "raw": "ok" }
  }
}
```

Wire fields:

| Field | Meaning |
|-------|---------|
| `score` | Weighted aggregate score from `0` to `100` |
| `grade` | `A`, `B`, `C`, `D`, or `F` |
| `generated_at` | UTC timestamp when the aggregate was computed |
| `factors` | Per-factor score, weight, raw measurement, and notes |
| `truncated_at_max` | Present when an underlying audit scan hit its cap |

## Factor model

Each factor is weighted **25%**:

| Factor | Meaning | Scoring |
|--------|---------|---------|
| `denial_rate` | Ratio of denies over the last 24h | `0%` deny => `100`, `50%+` deny => `0`, linear in between |
| `approval_latency_p95` | 95th percentile approval latency over the last 24h | `<=30s` => `100`, `>=5m` => `0`, linear in between |
| `policy_coverage` | Ratio of registered topics with at least one active rule | `100%` => `100`, `0%` => `0`, linear |
| `chain_integrity` | Current audit-chain integrity state | `ok` => `100`, `partial` => `85`, `compromised` => `0`, `unavailable` => neutral |

Special rules:

- **Compromised chain floor:** if `chain_integrity=compromised`, the overall
  score is capped at `55` (`F`) even if the other factors are healthy.
- **Neutral fallback:** when a factor has no data or an upstream dependency is
  unavailable, that factor returns `NeutralFactorScore = 70` and populates
  `notes`.
- **Cache TTL:** results are cached per tenant for **60 seconds** so dashboards
  can poll without forcing a full recompute on every refresh.

Grade mapping:

| Score | Grade |
|-------|-------|
| `>= 90` | `A` |
| `>= 80` | `B` |
| `>= 70` | `C` |
| `>= 60` | `D` |
| `< 60` | `F` |

## Error codes

| Status | When it happens |
|--------|-----------------|
| `401` | Missing or invalid authentication |
| `403` | Caller is not an admin or crossed tenant scope |
| `500` | Health computation failed before a response could be produced |

## Operator notes

- No recent decisions, no recent approvals, or no registered topics do **not**
  force a failing score; those cases fall back to neutral with explanatory
  notes.
- The `raw` field is intentionally heterogeneous (`object` for rates/counts,
  string for chain status) so the dashboard can render detailed factor
  breakdowns without another API call.

## See also

- [`docs/api/openapi/cordum-api.yaml`](openapi/cordum-api.yaml)
- [`dashboard/src/hooks/useGovernanceHealth.ts`](../../dashboard/src/hooks/useGovernanceHealth.ts)
