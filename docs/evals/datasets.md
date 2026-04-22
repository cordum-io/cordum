# Eval Datasets

Cordum's **eval dataset store** is the fixture layer of the phase-2
governance regression pipeline. Each dataset is a curated, versioned
collection of policy-test cases. The companion eval-runner task (sibling
in the same epic) replays these fixtures against current or candidate
policies to detect regressions: *a previously-denied action is now
allowed by a new policy*.

## Immutability contract

> **Datasets are immutable. Create a new version to change entries —
> updates are forbidden by design.**

The HTTP surface enforces immutability at three layers:

1. **Routing:** `PUT /api/v1/evals/datasets/{id}` does **not** mutate
   the referenced dataset in place. Instead it creates a **new version**
   with a fresh id, leaving the historical version untouched. Posting to
   the same `(name, version)` twice returns `409 Conflict`.
2. **Storage:** the Redis store uses `SETNX` on the `byname:<name>:<version>`
   key inside an atomic Lua script, so concurrent creates of the same
   version deterministically collide.
3. **Hashing:** every dataset carries a `content_hash` (SHA-256 over the
   canonical-JSON `entries` slice, with sorted keys) captured at create
   time. Operators auditing a long-lived dataset can re-compute the
   hash and compare it against the stored value to detect tampering.

To evolve a dataset, either call `POST /api/v1/evals/datasets` with the
same `name` and an incremented `version`, or call
`PUT /api/v1/evals/datasets/{id}` to create a successor version from an
existing dataset id. The historical versions stay queryable via
`/by-name/{name}` (newest-first) and `/by-name/{name}/versions/{version}`
for exact lookups.

## Retention semantics

Datasets have **no TTL** — they are durable by design. Destruction is a
two-key admin-only escape hatch:

- The caller must have the `evals.datasets.delete` permission (or the
  superseding `admin` role, which carries every permission by
  default).
- The request must carry `force=true` as a query parameter, spelled
  exactly. `force=1`, `force=TRUE`, `force=yes`, and no value are all
  rejected with `400 Bad Request`.

Delete wipes every associated key — the record hash, the
`byname:<name>:<version>` uniqueness sentinel, the name-scoped version
ZSET member, and the primary index entry — in a single atomic Lua
script. After deletion the `(name, version)` slot is free and can be
re-created.

## RBAC surface

| Permission | Endpoints |
|------------|-----------|
| `evals.datasets.read` | `GET /api/v1/evals/datasets`, `GET /api/v1/evals/datasets/{id}`, `GET /api/v1/evals/datasets/by-name/{name}`, `GET /api/v1/evals/datasets/by-name/{name}/versions/{version}` |
| `evals.datasets.write` | `POST /api/v1/evals/datasets`, `PUT /api/v1/evals/datasets/{id}` |
| `evals.datasets.delete` | `DELETE /api/v1/evals/datasets/{id}?force=true` |

When advanced RBAC is entitled, the fine-grained permissions gate every
route. When RBAC is disabled the handlers fall back to role checks: read
routes accept `admin | operator | viewer`, `POST` requires `admin`, and
`PUT`/`DELETE` require `admin`.

## Limits

| Limit | Value | Rationale |
|-------|-------|-----------|
| Max entries per dataset | 10,000 | Keeps the Redis hash payload bounded and replay time predictable. |
| Max canonical-JSON size | 16 MiB | Bounds a single Redis `HSET` payload. |
| Max request body | ~16.25 MiB | Model cap + a 256 KiB envelope margin so payloads exactly at the content cap still decode. |
| Max entry notes | 4 KiB | Stops a single annotator from blowing past the dataset cap alone. |
| Max entry metadata keys | 32 | Prevents unbounded metadata maps. |
| Max list `limit` | 200 | Default 50. |

Datasets that outgrow these caps should be **split along a meaningful
axis** (tenant, topic, risk tier) rather than bumped. The error returned
on a 413 nudges callers in that direction.

## Wire contract

### `EvalDataset`

```json
{
  "id": "f1d2a5e3-6c0b-4a3e-91d6-6a3f0c0e9a11",
  "name": "pii-leaks-q1",
  "version": 1,
  "tenant": "acme",
  "description": "Q1 regression cases for PII leak rule",
  "entries": [ /* see EvalEntry below */ ],
  "created_at": "2026-04-20T09:17:00.000Z",
  "updated_at": "2026-04-20T09:17:00.000Z",
  "created_by": "alice@acme.io",
  "entry_count": 1,
  "content_hash": "75a4d30dc748604979edbe388b42d3dddfad155c515e1552fa1bf435d937e4a8"
}
```

`updated_at` is always equal to `created_at` — the field exists only so
the wire envelope matches other Cordum resources. Any change to content
requires a new version.

### `EvalEntry`

```json
{
  "id": "entry-1",
  "input": {
    "tenant": "acme",
    "topic": "support",
    "agent_id": "agent-a",
    "capabilities": ["read"],
    "risk_tags": ["pii"],
    "metadata": {"origin": "ticket-42"}
  },
  "expected_decision": "DENY",
  "rule_id": "rule-pii-leak-01",
  "metadata": {"scenario": "denied-in-prod"},
  "source": "audit-import",
  "source_ref": "audit-xyz",
  "notes": "Reproduces the leak detected on 2026-03-12"
}
```

`input` is a free-form JSON snapshot of the originating job-request
shape. The SDK treats it as opaque; downstream consumers (the eval
runner) know how to interpret it. `expected_decision` reuses the
existing `SafetyDecision` enum (`ALLOW`, `DENY`, `REQUIRE_APPROVAL`,
`THROTTLE`, `ALLOW_WITH_CONSTRAINTS`). `source` captures entry origin
(`manual`, `audit-import`, `replay-import`) so the runner can attribute
regressions by origin.

## Curl recipes

Set these once:

```bash
export TENANT=acme
export AUTH="X-API-Key: $CORDUM_API_KEY"
export BASE=http://localhost:8081
```

### Create a dataset

```bash
curl -sS -X POST "$BASE/api/v1/evals/datasets" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "pii-leaks-q1",
        "version": 1,
        "description": "Q1 regression cases for PII leak rule",
        "entries": [
          {
            "id": "entry-1",
            "input": {"tenant": "acme", "topic": "support", "agent_id": "agent-a"},
            "expected_decision": "DENY",
            "rule_id": "rule-pii-leak-01",
            "source": "manual"
          }
        ]
      }'
```

### List datasets (paginated)

```bash
curl -sS "$BASE/api/v1/evals/datasets?limit=50&name_prefix=pii-" \
  -H "X-Tenant-ID: $TENANT" -H "$AUTH"
```

Pass the `next_cursor` from the response on the next call as
`&cursor=...` to continue paging.

### Create a successor version

```bash
curl -sS -X PUT "$BASE/api/v1/evals/datasets/<id>" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{
        "version": 2,
        "description": "Q1 regression cases for PII leak rule (expanded)",
        "entries": [
          {
            "id": "entry-1",
            "input": {"tenant": "acme", "topic": "support", "agent_id": "agent-a"},
            "expected_decision": "DENY",
            "rule_id": "rule-pii-leak-01",
            "source": "manual"
          },
          {
            "id": "entry-2",
            "input": {"tenant": "acme", "topic": "support", "agent_id": "agent-b"},
            "expected_decision": "REQUIRE_APPROVAL",
            "rule_id": "rule-pii-leak-02",
            "source": "audit-import"
          }
        ]
      }'
```

This returns `201 Created` with a **new dataset id**. The original
dataset remains queryable and unchanged.

### Get by id / by name

```bash
# By id
curl -sS "$BASE/api/v1/evals/datasets/<id>" \
  -H "X-Tenant-ID: $TENANT" -H "$AUTH"

# All versions of a named dataset, newest-first
curl -sS "$BASE/api/v1/evals/datasets/by-name/pii-leaks-q1" \
  -H "X-Tenant-ID: $TENANT" -H "$AUTH"

# Exact (name, version)
curl -sS "$BASE/api/v1/evals/datasets/by-name/pii-leaks-q1/versions/2" \
  -H "X-Tenant-ID: $TENANT" -H "$AUTH"
```

### Force-delete (admin only)

```bash
curl -sS -X DELETE "$BASE/api/v1/evals/datasets/<id>?force=true" \
  -H "X-Tenant-ID: $TENANT" -H "$AUTH"
```

Omitting or misspelling `force=true` returns `400`.

## How the sibling replay task will consume this

The phase-2 architecture plan lists four stages: **import → generate →
store → replay**. This document covers *only the store*. The follow-up
tasks in the same epic will:

1. **Import:** extract `(input, expected_decision)` pairs from
   `core/audit/` SIEM events where `decision ∈ {DENY, REQUIRE_APPROVAL}`
   and persist them as entries with `source="audit-import"`.
2. **Generate:** expose curated or LLM-assisted manual construction from
   the dashboard (UI is a sibling task).
3. **Replay runner:** extend `handlers_policy_replay.go` so it can
   replay an entire dataset against a candidate policy and report
   per-entry pass/fail plus aggregate regression counts. A regression is
   *a previously-denied action now allowed by the candidate*.

The store surface exposed here is deliberately minimal — no replay
coupling leaks into this task. The `evalDatasetStore` field on the
gateway `server` struct is a single shared handle so the runner task can
wire itself in with zero additional plumbing.

## Storage layout (operator reference)

Keys live under `eval:dataset:<tenant>:`:

| Key | Type | Purpose |
|-----|------|---------|
| `rec:<id>` | HASH | Serialized `EvalDataset` JSON (`json` field). |
| `byname:<name>:<version>` | STRING | `<id>`. Uniqueness sentinel written via atomic `SETNX`. |
| `name:<name>` | ZSET | Score=version, member=id. Drives `/by-name/{name}` history. |
| `index` | ZSET | Score=created_at_ms, member=id. Drives paginated list. |

There is no cross-tenant index. A `ReconcileIndexes(ctx, tenant)` helper
prunes dangling index members whose record hash is missing; it is
intended for one-shot invocation after crash recovery and is safe to
call at any time.
