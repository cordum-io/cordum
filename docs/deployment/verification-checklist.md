# Deployment verification checklist

Use this checklist after a fresh self-hosted deployment or upgrade.

## Prerequisites

Set the standard operator environment:

```bash
export BASE=https://localhost:8081
export TENANT=default
export API_KEY=...
```

If TLS is enabled locally, append:

```bash
CACERT=(--cacert ./certs/ca/ca.crt)
```

## Post-deploy smoke checks

- [ ] `GET /api/v1/health` returns `200`
- [ ] `GET /api/v1/audit/verify?tenant=$TENANT` returns `200` and `status=ok`
- [ ] `GET /api/v1/governance/health?tenant=$TENANT` returns `200` and `grade` in `A`-`F`
- [ ] Workflow create → run → approve → success path still completes end-to-end

## Curl commands

### Audit verify

```bash
curl -sS "${CACERT[@]}" "$BASE/api/v1/audit/verify?tenant=$TENANT" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Tenant-ID: $TENANT" | jq '.status'
```

Expected:

```text
"ok"
```

Reference: [`docs/api/audit-verify.md`](../api/audit-verify.md)

### Governance health

```bash
curl -sS "${CACERT[@]}" "$BASE/api/v1/governance/health?tenant=$TENANT" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Tenant-ID: $TENANT" | jq '.grade'
```

Expected:

```text
"A"
```

Any `A`-`F` grade is valid; the smoke check is proving the endpoint is
live and returns the documented wire shape.

Reference: [`docs/api/governance-health.md`](../api/governance-health.md)

## CLI spot-check

```bash
cordumctl audit verify "$TENANT" --json | jq '.status'
```

Use the CLI check when validating operator tooling in addition to the raw
HTTP surface.
