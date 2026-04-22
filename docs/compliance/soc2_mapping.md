# SOC2 Control Mapping & Compliance Export

Cordum exports tamper-evident audit evidence with each event tagged
against the [SOC2 2017 Trust Services Criteria](https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022).
This document explains which controls Cordum maps today, how to
override the defaults for your own auditor, and how the compliance
export endpoint streams evidence.

> **Disclaimer.** The default mapping is a pragmatic starting point,
> not an audit opinion. Every SOC2 engagement is different and your
> CPA/auditor must validate the mapping against your System Description
> before relying on it in a formal report.

---

## 1. Scope

In scope (Cordum emits events that carry the corresponding controls):

| SOC2 TSC Criterion | Where Cordum contributes                                       |
|--------------------|----------------------------------------------------------------|
| CC6.1 — Logical and physical access controls | Per-tool approvals, system auth events |
| CC6.3 — Access revocation                       | MCP approval revocations                |
| CC7.2 — Monitoring of controls                  | Every safety decision, approval, shadow evaluation |
| CC7.3 — Detection of security incidents         | Safety violations, denied decisions, explicit tool denials |
| CC8.1 — Change management                        | Policy-bundle changes (PUT /policy/bundles, activate/rollback) |

Out of scope (Cordum does **not** emit evidence for these; the auditor
still needs an external source):

- CC1.x, CC2.x, CC3.x (governance structure, communication, risk
  assessment) — business processes outside the runtime layer.
- CC5.x (control activities) — Cordum is part of the control layer;
  evidence that controls *exist* comes from operator procedures.
- A1.x (availability) — Cordum's own uptime is observed via the
  `/health` endpoint and Prometheus metrics, not the audit chain.
- Privacy TSCs (P1–P8) — Cordum's audit chain records governance
  decisions, not subject data processing lifecycles.

Always pair the Cordum export with your identity provider's logs,
infrastructure audit trails, and HR onboarding/offboarding records for
a complete CC6.x story.

## 2. Default mapping

The authoritative source is `core/audit/soc2.go`; this table is a
human-readable mirror. If it drifts, the Go file wins.

| SIEMEvent.EventType | Controls | Overlay |
|---------------------|----------|---------|
| `safety.decision`    | `CC7.2` | `+CC7.3` when `Decision == "deny"` |
| `safety.approval`    | `CC6.1`, `CC7.2` | — |
| `safety.policy_change` | `CC8.1` | — |
| `safety.violation`   | `CC7.3` | — |
| `system.auth`        | `CC6.1` | — |
| `mcp.tool_approval`  | `CC6.1`, `CC7.2` | `+CC6.3` when `Extra[outcome] == "revoke"` |
| `mcp.tool_denied`    | `CC7.3` | — |
| `shadow_eval`        | `CC7.2` | — |

Every control ID in the default mapping has a human-readable description
in `DefaultSOC2Legend()` and is embedded in every export manifest.

## 3. Overriding the default

Set `CORDUM_SOC2_MAPPING_PATH` to a YAML file matching the
`SOC2Mapping` shape (`EventType: [list of control IDs]`).
The file is merged **over** the default, so a partial override only
changes the event types it explicitly lists — you cannot accidentally
delete a default mapping by omitting it.

Example `/etc/cordum/soc2_overrides.yaml`:

```yaml
# Extend the safety.decision mapping so our Cordum tenants also
# surface CC7.4 (restoration of data) — our auditor wants to see it.
safety.decision:
  - CC7.2
  - CC7.4

# Custom event type the tenant emits via the API.
custom.billing_change:
  - CC9.1
```

The gateway logs a single `INFO` line at boot stating `soc2 mapping
override loaded path=... merged_keys=...`. Malformed or missing files
fall back to the default with a `WARN` — the gateway never crashes on
a compliance misconfiguration.

## 4. `GET /api/v1/audit/export`

The export endpoint is admin-only, entitlement-gated (`siem_export` or
`audit_export`), and tenant-scoped from the caller's auth context.

Query parameters:

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `format`  | no       | `json`  | `json` (NDJSON) or `csv` (RFC 4180) |
| `from`    | **yes**  | —       | RFC 3339 lower bound, inclusive |
| `to`      | **yes**  | —       | RFC 3339 upper bound, inclusive |
| `excel`   | no       | `false` | Prepend a UTF-8 BOM to CSV for Excel |
| `limit`   | no       | tier-dependent | Cap events; Enterprise default 1,000,000, Team 10,000 |

Response headers:

- `Content-Type: application/x-ndjson` or `text/csv; charset=utf-8`
- `Content-Disposition: attachment; filename="cordum-audit-<tenant>-<YYYYMMDD>-<YYYYMMDD>.<ext>"`
- `X-Cordum-Export-Format: json|csv`
- `X-Cordum-Tenant: <tenant>`

### JSON example

```bash
curl -H "Authorization: Bearer $CORDUM_API_KEY" \
     -H "X-Tenant-ID: default" \
     "https://gateway.cordum.internal/api/v1/audit/export?format=json&from=2026-04-01T00:00:00Z&to=2026-04-17T23:59:59Z" \
     | head -3
```

First line is the manifest, subsequent lines are events, last line is
the footer:

```json
{"type":"manifest","generated_at":"2026-04-18T09:00:00Z","tenant_id":"default","from":"2026-04-01T00:00:00Z","to":"2026-04-17T23:59:59Z","format":"json","chain_verification":{"status":"ok","total_events":42,"verified_events":42,"gaps":[]},"policy_snapshots":[{"bundle_id":"secops/baseline","version":"v3","ed25519_signature":"...","public_key_id":"primary-2026"}],"soc2_legend":{"CC7.2":"Monitoring of controls"},"event_count":0,"truncated_at_max":false}
{"type":"event","timestamp":"2026-04-01T00:01:00Z","event_type":"safety.decision","severity":"INFO","tenant_id":"default","action":"db.read","decision":"allow","seq":1,"event_hash":"a1b2...","prev_hash":"","soc2_controls":["CC7.2"]}
{"type":"event","timestamp":"2026-04-01T00:02:00Z","event_type":"safety.decision","severity":"INFO","tenant_id":"default","action":"db.write","decision":"deny","seq":2,"event_hash":"...","prev_hash":"a1b2...","soc2_controls":["CC7.2","CC7.3"]}
```

### CSV example

```bash
curl -H "Authorization: Bearer $CORDUM_API_KEY" \
     "https://gateway.cordum.internal/api/v1/audit/export?format=csv&from=2026-04-01T00:00:00Z&to=2026-04-17T23:59:59Z" \
     > q1.csv
```

The first line is a `#`-prefixed JSON manifest (ignored by Excel, kept
by Python's `csv.reader` when `skipinitialspace=False` — Python users
should strip lines starting with `#` before parsing):

```csv
# cordum-manifest: {"type":"manifest","tenant_id":"default","format":"csv","chain_verification":{"status":"ok"}}
timestamp,event_type,severity,tenant_id,agent_id,agent_name,agent_risk_tier,job_id,action,decision,matched_rule,reason,risk_tags,capabilities,policy_version,identity,seq,event_hash,prev_hash,soc2_controls,extra_json
2026-04-01T00:01:00Z,safety.decision,INFO,default,,,,,"db.read",allow,,,,,,,1,a1b2...,,CC7.2,
```

## 5. Tamper detection

Every export runs `audit.VerifyChain` against the same time range
before streaming events. The manifest's `chain_verification.status`
field distinguishes three outcomes:

| Status         | Meaning                                                           | Auditor action |
|----------------|-------------------------------------------------------------------|----------------|
| `ok`           | Every event in range hashes correctly + links to its predecessor. | Accept evidence. |
| `partial`      | Some events were retention-trimmed before the range; chain intact above the boundary. | Evidence is valid for `retention_boundary_seq` onward. |
| `compromised`  | At least one event failed hash recomputation, was missing, or landed out of order. | **Do not rely on any event from or after the first gap.** See incident runbook. |

A compromised manifest carries a `gaps` array describing each failure:

```json
{
  "status": "compromised",
  "gaps": [
    {"at_seq": 142, "type": "hash_mismatch"},
    {"at_seq": 155, "type": "missing"}
  ],
  "retention_boundary_seq": 12,
  "total_events": 168,
  "verified_events": 165
}
```

## 6. Incident response

A `compromised` export is evidence of tampering or corruption. The
runbook is in `docs/deployment/audit-chain.md`; the top-level actions
are:

1. **Do not delete or re-run the export.** The current output IS the
   incident artefact.
2. Snapshot Redis immediately (`BGSAVE` + offsite copy).
3. Freeze policy changes on the affected tenant until the chain is
   re-verified.
4. Cross-reference `gap.at_seq` with the surrounding events in the
   exported range — the first gap is the tampering point.
5. File the export, the Redis snapshot, and the gap list with your
   security operations team before regenerating.

## 7. Reference

- `core/audit/soc2.go` — authoritative mapping + legend + override loader.
- `core/audit/export_compliance.go` — streaming writer (`WriteComplianceExport`).
- `core/audit/chain_verify.go` — chain verifier used for tamper attestation.
- `core/controlplane/gateway/handlers_audit_compliance.go` — HTTP handler.
- `docs/deployment/audit-chain.md` — audit chain architecture + incident runbook.
- `docs/api/openapi/cordum-api.yaml` — OpenAPI spec for the endpoint.
