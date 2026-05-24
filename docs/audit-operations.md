# Audit Chain Operations Guide

> Practical runbook for deploying, monitoring, and troubleshooting the
> tamper-evident audit chain.

## Quick Start

### 1. Verify the Chain is Active

The audit chain starts automatically with the gateway. Check the boot log:

```text
INFO audit NATS consumer started subject=sys.audit.export queue=audit-exporters chain_enabled=true chain_fail_mode=strict
```

### 2. Enable HMAC (Recommended for Production)

```bash
# Generate a 256-bit key (64 hex chars = 32 bytes)
HMAC_KEY=$(openssl rand -hex 32)

# Add to your gateway environment
export CORDUM_AUDIT_HMAC_KEY="$HMAC_KEY"
```

> **Important**: Store this key securely (e.g., Vault, AWS Secrets Manager,
> K8s Secret). If the key is lost, new events cannot be HMAC-verified, though
> the SHA-256 chain remains intact.

### 3. Run a Verification Check

```bash
curl -H "Authorization: Bearer $API_KEY" \
  "https://your-cordum/api/v1/audit/verify?tenant=default"
```

Expected response for a healthy chain:

```json
{
  "status": "ok",
  "total_events": 42,
  "verified_events": 42,
  "gaps": [],
  "hmac_verified": 42,
  "hmac_skipped": 0
}
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `CORDUM_AUDIT_HMAC_KEY` | _(empty)_ | Hex-encoded HMAC-SHA256 signing key (min 32 bytes decoded) |
| `CORDUM_AUDIT_CHAIN_FAIL` | `strict` | `strict`: drop unchained events; `permissive`: export anyway |
| `CORDUM_AUDIT_EXPORT_TYPE` | `none` | SIEM backend: `webhook`, `syslog`, `datadog`, `cloudwatch`, `chain-only` |
| `CORDUM_AUDIT_RETENTION_HOURS` | `168` (7 days) | Audit retention window in hours |
| `CORDUM_AUDIT_READ_SAMPLE_RATE` | `0.0` | Fraction (`0.0`–`1.0`) of `/api/v1/audit/events` reads that emit an `audit.read.events` meta-event. **Recommended prod default `0.0`** — raising it floods the chain with routine `system.auth` / `audit.read.events` rows that bury governance signal. If your goal is a governance-focused audit artifact, filter with `?category=governance` (see below) rather than tuning this sample rate. |

## HMAC Key Management

### Generating Keys

```bash
# Generate using OpenSSL (recommended — hex-encoded, consistent with event_hash)
openssl rand -hex 32

# Generate using Python (alternative)
python3 -c "import secrets; print(secrets.token_hex(32))"
```

### Key Rotation Procedure

1. **Generate new key**:
   ```bash
   NEW_KEY=$(openssl rand -hex 32)
   ```

2. **Deploy to all replicas**:
   Update `CORDUM_AUDIT_HMAC_KEY` in your deployment config and restart
   gateway pods. All replicas must use the same key.

3. **Understand verification behavior during rotation**:
   - Events with **no HMAC tag** (pre-HMAC era): `hmac_skipped` (not failure)
   - Events signed with **old key**: `hmac_mismatch` under new key (expected)
   - Events signed with **new key**: `hmac_verified`

   > **Note**: The verify endpoint uses the HMAC key from the gateway's
   > environment (`CORDUM_AUDIT_HMAC_KEY`), not from query parameters.
   > Operators should note the `first_seq`/`last_seq` boundary where the
   > key changed.

4. **Archive old key**: Store the old key securely for forensic verification
   of historical events if needed.

### Key Storage Best Practices

| Environment | Recommendation |
|---|---|
| Kubernetes | `Secret` mounted as env var, rotated via `ExternalSecret` |
| AWS | SSM Parameter Store (SecureString) or Secrets Manager |
| Docker Compose | `.env` file with restricted permissions (`chmod 600`) |
| Development | Direct env var (acceptable for local dev only) |

## Monitoring

### Health Indicators

| Metric | Healthy | Alert Threshold |
|---|---|---|
| `verify.status` | `ok` | Any `compromised` → PagerDuty |
| `hmac_verified` | = `total_events` | < 90% → Warning |
| `hmac_skipped` | 0 (post-rollout) | > 0 after full rollout → Investigate |
| CAS retry rate | < 5% | > 20% → Redis contention |

### Prometheus Metrics

The audit pipeline exposes counters via the `/metrics` endpoint:

- `cordum_audit_chain_append_total` — total events chained
- `cordum_audit_chain_append_errors_total` — chain failures
- `cordum_audit_export_total` — events exported to SIEM
- `cordum_audit_export_errors_total` — export failures

### Automated Verification

Run periodic verification in CI or a cron job:

```bash
#!/bin/bash
RESULT=$(curl -s -H "Authorization: Bearer $API_KEY" \
  "https://your-cordum/api/v1/audit/verify?tenant=default")

STATUS=$(echo "$RESULT" | jq -r '.status')
if [ "$STATUS" != "ok" ]; then
  echo "ALERT: Audit chain integrity check failed: $STATUS"
  echo "$RESULT" | jq '.gaps'
  exit 1
fi
echo "Audit chain verified: $(echo "$RESULT" | jq '.verified_events') events OK"
```

## Troubleshooting

### `status: compromised` with `hash_mismatch` Gaps

**Cause**: An event's payload was modified in Redis after being chained.

**Investigation**:
1. Note the `at_seq` from the gaps array
2. Check Redis directly: `XRANGE audit:chain:<tenant> - +`
3. Compare the stored event JSON against the recomputed hash
4. Check Redis access logs for unauthorized writes

**Remediation**: This is a security incident. Follow your incident response
playbook. The chain integrity violation is permanent — the affected events
are forensic evidence.

### `status: compromised` with `hmac_mismatch` Gaps

**Cause**: Events were signed with a different HMAC key than the one used
for verification, OR events were forged by a process without the key.

**Investigation**:
1. Confirm the verify endpoint is using the correct key
2. If you recently rotated keys, verify old events with the old key
3. If the key is correct, treat as a security incident

### `status: compromised` with `missing` Gaps

**Cause**: Sequential events were deleted from the Redis Stream.

**Investigation**:
1. Check for `XDEL` commands in Redis slow log
2. Check for Redis memory pressure causing eviction (should not happen with
   streams, but verify `maxmemory-policy`)
3. Distinguish from retention trimming by checking `retention_boundary_seq`

### CAS Retry Budget Exhausted

**Cause**: Extreme write contention on a single tenant's chain.

**Investigation**:
1. Check for runaway audit producers (e.g., looping agent)
2. Check Redis latency (`LATENCY LATEST`)
3. Consider sharding high-volume tenants

### Events Missing HMAC After Key Deployment

**Cause**: Not all gateway replicas received the new key.

**Fix**: Verify all pods/replicas have `CORDUM_AUDIT_HMAC_KEY` set and
restart any that don't.

### Edge approval retry fails with `audit_evidence_missing`

**Cause**: A destructive Edge action had a backend approval, but the audit-chain
provenance check could not find a matching resolved approval event. The accepted
event is `EventEdgeApprovalResolved` / `edge.approval_resolved` with decision
`approved` or `approve`, matching tenant, matching `extra.approval_ref`, and
matching `extra.action_hash`. `EventEdgeApprovalRequested` /
`edge.approval_requested` only proves the approval was requested; requested-only
evidence is not sufficient.

**Investigation**:
1. Confirm the approval was actually approved, not still pending, rejected,
   expired, invalidated, or already consumed against a different action.
2. Verify the retry used the same tenant and action. Compare the dashboard or
   API approval detail `approval_ref`/`action_hash` with the bounded audit
   event `extra` fields; do not copy raw prompts or tool payloads into the
   incident notes.
3. Run `GET /api/v1/audit/verify?tenant=<tenant>&since=<approval_created_ms>&until=<now_ms>`
   (respecting the default 10,000 / max 100,000 scan cap) and check for
   `compromised`, `missing`, or HMAC gaps.
4. If verification reports `partial`, confirm retention trimming did not remove
   the approval window. Partial history is acceptable only when the in-window
   resolved approval evidence is still present.

**Remediation**: Treat missing/malformed/mismatched provenance as fail-closed.
Fix the audit pipeline or rerun the approval flow after the chain is healthy;
do not bypass by relying on the approval-requested event or by pasting raw
payloads into audit records.

## Legal Hold Integration

When a legal hold is active (`POST /api/v1/audit/legal-holds`), audit events
for the affected tenant are **never** retention-trimmed. The hold prevents
deletion until explicitly released.

```bash
# Create a legal hold
curl -X POST -H "Authorization: Bearer $API_KEY" \
  -d '{"tenant_id":"acme-corp","reason":"SEC investigation ref-2024-001"}' \
  "https://your-cordum/api/v1/audit/legal-holds"

# Release when investigation concludes
curl -X DELETE -H "Authorization: Bearer $API_KEY" \
  "https://your-cordum/api/v1/audit/legal-holds/<hold_id>"
```

## Compliance

The audit chain maps to SOC2 2017 Trust Services Criteria:

| Control | Mapping |
|---|---|
| CC7.2 — Monitoring of controls | `safety.decision`, `shadow_eval`, `mcp.tool_approval` |
| CC7.3 — Detection of incidents | `safety.violation`, `mcp.tool_denied`, denied decisions |
| CC6.1 — Access controls | `system.auth`, `safety.approval` |
| CC8.1 — Change management | `safety.policy_change` |

See `core/audit/soc2.go` for the authoritative mapping and override
instructions.

### Governance-focused exports

Routine telemetry (`system.auth`, `audit.read.events`, edge/MCP lifecycle) can
dominate an unfiltered export — observed ~90% noise when the read sample rate
was raised — burying the governance events an auditor needs. Rather than tuning
`CORDUM_AUDIT_READ_SAMPLE_RATE` (keep it at the recommended `0.0`), scope the
artifact at query time:

```bash
# Governance-only compliance export (CSV), chain integrity still attested over
# the full range — the manifest's row_filter records the scope.
curl -H "Authorization: Bearer $API_KEY" \
  "https://your-cordum/api/v1/audit/export?format=csv&category=governance&from=<RFC3339>&to=<RFC3339>"
```

The export's `event_count` is then the post-filter row count while
`chain_verification` still covers the full range, so a filtered export is never
mistaken for a tamper gap (see `docs/audit.md` §7). The same `?category=` filter
works on `GET /api/v1/audit/events` for the dashboard read surface.
