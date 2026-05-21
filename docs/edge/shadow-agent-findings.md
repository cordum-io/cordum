# Shadow Agent Findings (EDGE-141)

The Edge Gateway persists redacted shadow-agent observations as
**ShadowAgentFinding** lifecycle records. Operators triage findings via
`/api/v1/edge/shadow-agents/*`; the scanner emits one record per shadow
observation, and the operator disposes of each via resolve or suppress.

For advisory remediation guidance (action kinds, commands, MDM-vs-dev
audience steering), see
[shadow-remediation.md](shadow-remediation.md) (EDGE-142).

**Observe / warn ONLY.** This subsystem does not enforce, does not
remediate, does not create Cordum Jobs, and does not call the Safety
Kernel. It is an evidence + operator-disposition surface. Enforcement
hooks are intentionally out of scope until a separate design lands.

## Lifecycle states

| Status        | Meaning                                                                          |
|---------------|----------------------------------------------------------------------------------|
| `detected`    | Fresh finding awaiting operator triage. No TTL.                                  |
| `resolved`    | Operator confirmed remediation (uninstalled, brought under management, false-positive). Terminal. |
| `suppressed`  | Operator deferred / accepted (approved exception, low-risk service account). Terminal. May carry `suppressed_until`. |

Transitions allowed: `detected → resolved`, `detected → suppressed`.
Re-issuing the same terminal transition is idempotent. Cross-terminal
transitions (resolved ↔ suppressed) return **409 Conflict**.

## Endpoints

All routes share the standard Edge auth + `X-Tenant-ID` header
requirements and return the `{code, message, request_id, details}`
error envelope.

| Method | Path                                                       | Operation                          |
|--------|------------------------------------------------------------|------------------------------------|
| POST   | `/api/v1/edge/shadow-agents`                               | Create one redacted finding.       |
| GET    | `/api/v1/edge/shadow-agents`                               | List with filters + cursor.        |
| GET    | `/api/v1/edge/shadow-agents/{finding_id}`                  | Get one (tenant-scoped).           |
| POST   | `/api/v1/edge/shadow-agents/{finding_id}/resolve`          | Transition to resolved.            |
| POST   | `/api/v1/edge/shadow-agents/{finding_id}/suppress`         | Transition to suppressed.          |
| POST   | `/api/v1/edge/shadow-agents/{finding_id}/ignore`           | Compatibility alias for /suppress. |

### List filters

`status` (`detected|resolved|suppressed`),
`risk` (`low|medium|high|critical`),
`agent` / `agent_product`,
`owner`,
`limit` (default 50, max 200),
`cursor` (opaque pagination token).

The store selects the narrowest filter as the primary index path.
Bad filter values return **400** with `code=invalid_request`.

## Evidence and redaction

Every finding **must** carry at least one of:

* `evidence_summary` — a bounded (≤2 KiB) string. Secret-shaped values
  (`sk-ant-…`, GitHub tokens, OpenSSH/PGP headers, Bearer tokens) are
  stripped to `<REDACTED>` at ingest by `shadow.RedactConfigSummary`.
* `evidence_artifact_ptr` — a `ShadowEvidencePointer` (URI + SHA256 +
  retention + redaction). The pointer's `tenant_id` MUST match the
  parent finding's tenant; cross-tenant pointers are rejected.

`redacted_path` is run through `shadow.RedactPath` so the home prefix
is stripped (`/Users/alice/.cursor/mcp.json` → `~/.cursor/mcp.json`).
No raw developer-machine path is ever persisted.

## Retention

* `detected` records have no TTL — they remain until triaged.
* `resolved`/`suppressed` records carry the configured terminal
  retention TTL (default **90 days**). After expiry, get/list returns
  404 / hides the record, and the secondary indexes are cleaned
  opportunistically by the next list call. Operators wanting longer
  retention can override via `shadow.WithTerminalRetention`.

## Audit events

| Event type                | When                                  | SOC2 Controls  |
|---------------------------|---------------------------------------|----------------|
| `shadow_agent.detected`   | After successful create.              | `CC7.2`         |
| `shadow_agent.resolved`   | After successful resolve transition.  | `CC7.2, CC8.1`  |
| `shadow_agent.suppressed` | After successful suppress transition. | `CC7.2, CC8.1`  |

Audit `Extra` carries the bounded fields:
`finding_id`, `agent_product`, `agent_id`, `hostname`, `risk`,
`status`, `evidence_type`, `redacted_path`, `owner_principal_id`,
`evidence_artifact_uri`/`evidence_artifact_sha256` when present, and
the (already-redacted, ≤256-byte) `reason` for lifecycle changes.

**`evidence_summary` itself is never included in the audit payload**
— even though it's already redacted, defense-in-depth keeps the
summary off SIEM rows.

## Limits

| Field                | Limit                                                |
|----------------------|------------------------------------------------------|
| `evidence_summary`   | ≤ 2 KiB (post-redaction)                             |
| `resolution_reason`  | ≤ 512 bytes                                          |
| `metadata`           | ≤ 16 entries, ≤ 64-byte keys, ≤ 256-byte values      |
| List `limit`         | default 50, max 200                                  |

## Deferred (out of scope for EDGE-141)

* Dashboard surfaces (Shadow Agents tab) — no P0 dashboard work; the
  observe-only API is the integration point for future UI.
* Runtime enforcement / remediation execution / Cordum Job creation.
* Per-detector identity attestation beyond the auth principal fallback.
