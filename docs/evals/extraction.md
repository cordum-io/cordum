# Incident-to-dataset extraction

Cordum can turn real production incidents from the **Policy Decision Log**
into immutable eval datasets with one API call:

- scan decision-log incidents for a tenant and time window
- project each incident to a **minimal** job-input snapshot
- deduplicate near-identical cases
- optionally preview with `dry_run=true`
- persist a new dataset version only when the preview looks right

The HTTP endpoint is `POST /api/v1/evals/datasets/from-incidents`.

## Pipeline overview

The extraction pipeline intentionally stays narrow:

1. **Scan** the tenant-scoped Policy Decision Log for the requested time
   window and filters (`topic`, `rule_id`, `verdicts`, `agent_id`).
2. **Load** the originating job request from the job store for each matched
   decision.
3. **Project** the job request to a compact input snapshot containing only:
   - tenant
   - topic
   - capabilities
   - risk tags
   - metadata
   - agent id
   - a stable `input_hash`
4. **Deduplicate** incidents by the tuple:
   `(topic, rule, verdict, input shape)`.
5. **Preview or persist** the resulting eval entries as a new immutable
   dataset version.

The extractor defaults to the **last 24 hours** and to verdicts
`deny` + `require_approval` when the caller omits filters.

## Curl recipes

Set these once:

```bash
export BASE=http://localhost:8081
export TENANT=acme
export AUTH="X-API-Key: $CORDUM_API_KEY"
```

### Preview the last 7 days of denies across all topics

```bash
curl -sS -X POST "$BASE/api/v1/evals/datasets/from-incidents?dry_run=true" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "incident-denies-last-7d",
        "description": "Preview of deny incidents from the last week",
        "since": "2026-04-13T00:00:00Z",
        "until": "2026-04-20T00:00:00Z",
        "verdicts": ["deny"]
      }'
```

Because `topic` is omitted, this scans **all topics** for the tenant.

### Persist the last 7 days of deny + require-approval incidents

```bash
curl -sS -X POST "$BASE/api/v1/evals/datasets/from-incidents" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "incident-regressions-apr20",
        "description": "Seed dataset from production incidents",
        "since": "2026-04-13T00:00:00Z",
        "until": "2026-04-20T00:00:00Z",
        "verdicts": ["deny", "require_approval"],
        "max_entries": 1000
      }'
```

### Filter by topic pattern or rule id

```bash
curl -sS -X POST "$BASE/api/v1/evals/datasets/from-incidents?dry_run=true" \
  -H "X-Tenant-ID: $TENANT" \
  -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "payments-denies-preview",
        "topic": "payments-*",
        "rule_id": "rule-pii-leak-01",
        "verdicts": ["deny"]
      }'
```

`topic` supports:

- exact match: `"support"`
- glob: `"payments-*"`
- regex: `"re:^payments\\.(charge|refund)$"`

## Dedupe semantics

The extractor collapses incidents that are effectively the same replay case.
The dedupe key is derived from:

- topic
- rule id
- verdict
- input shape hash

“**Different input**” means the projected job snapshot changed in a way that
changes the stable `input_hash`. Examples:

- different capability set
- different risk tags
- different metadata after sensitive/raw payload fields are dropped
- different agent id

If two incidents share the same dedupe tuple, the extractor keeps the
**oldest** matching case and increments `deduped_count`.

## Retention and privacy

This endpoint reads from two internal sources:

- the **Policy Decision Log**
- the **job store**

To reduce privacy risk, the stored eval entry does **not** copy raw job
payloads into the dataset. Instead, the snapshot keeps a minimal subset of
fields plus an `input_hash`. Raw payload fragments and `_content.*` style
fields are dropped before persistence.

The created dataset itself is still immutable and versioned like any other
eval dataset. If you need to refresh the dataset with newer incidents, create
another version instead of editing an existing one.

## Operational notes

- `dry_run=true` returns counts and warnings without writing a dataset.
- The endpoint is rate-limited per tenant to prevent accidental repeated
  large scans.
- If the request times out, the API returns `504` with any partial counts and
  warnings gathered before the deadline.
- If no incidents match the filters, the API returns `404`.
