# Edge Evidence Export

Audit-ready bundles for governed agent sessions. The export endpoint
packages a single `EdgeSession`'s redacted evidence — the session record,
its executions, the ordered action events, the approvals raised on those
events, and a metadata-only manifest of every artifact pointer attached to
those events.

> **EdgeSession is not a Cordum Job.** EdgeSession / AgentExecution /
> AgentActionEvent are session log + audit evidence. They reference Job
> and WorkflowRun IDs (`job_id`, `workflow_run_id`) only as cross-links
> when a real production workflow is involved; the export bundles those
> as `job_links` so auditors can pivot, but the bundle itself is not a
> job artifact.

## Endpoint

```
POST /api/v1/edge/sessions/{session_id}/export
```

| Aspect | Value |
| --- | --- |
| Auth | `auth.PermJobsRead` plus role `admin` / `user` / `viewer` |
| Tenant resolution | `X-Tenant-ID` header (required); cross-tenant access returns 404 |
| Request body | Optional JSON `{ "max_events"?: int }` |
| Response | `application/json` body of `SessionExportBundle` |

### Request body

```json
{
  "max_events": 5000
}
```

* `max_events` — clamps the number of `AgentActionEvent` entries the
  bundle carries. Default and ceiling: `5000`. When the session has more
  events the bundle reports `truncation.events_truncated = true` and
  `truncation.event_count` records the actual session-wide event total.
* `include_artifact_bodies` is **not** accepted in P0. Bundles always
  carry artifact metadata only; raw bodies stay in the artifact store.

### Response status codes

| Code | Cause |
| --- | --- |
| 200 | Bundle assembled successfully |
| 400 | Missing `session_id` path parameter or malformed body |
| 401 / 403 | Auth gate (missing/invalid auth, missing/wrong tenant) |
| 404 | Session not found (also returned for cross-tenant probes — existence does not leak) |
| 413 | Bundle exceeds `CORDUM_EDGE_EXPORT_MAX_BYTES`; lower `max_events` and retry |
| 500 | Unexpected store error |
| 503 | Edge store unavailable |

## SessionExportBundle wire shape

```jsonc
{
  "manifest_version": "edge.export.v1",
  "generated_at": "2026-05-02T18:30:00Z",
  "tenant_id": "acme-corp",
  "redaction_level": "standard",
  "session": { /* EdgeSession */ },
  "executions": [ /* []AgentExecution */ ],
  "events": [ /* []AgentActionEvent — sorted by (seq, timestamp, event_id) */ ],
  "approvals": [ /* []EdgeApproval */ ],
  "artifacts": [
    {
      "session_id": "edge_sess_01",
      "execution_id": "exec_01",
      "event_id": "evt_01",
      "artifact_type": "edge.tool_input",
      "retention_class": "audit",
      "redaction_level": "standard",
      "sha256": "sha256:…",
      "uri": "art://…",
      "size_bytes": 1234,
      "content_type": "application/json",
      "created_at": "2026-04-30T10:00:03Z"
    }
  ],
  "missing_artifacts": [
    {
      "uri": "art://…",
      "sha256": "sha256:…",
      "artifact_type": "edge.transcript",
      "session_id": "edge_sess_01",
      "execution_id": "exec_01",
      "event_id": "evt_42",
      "reason": "not_found"
    }
  ],
  "job_links": [
    { "execution_id": "exec_01", "job_id": "job-1", "workflow_run_id": "run-1", "step_id": "step-1" }
  ],
  "truncation": {
    "events_truncated": false,
    "event_count": 4,
    "event_scan_limit_hit": false,
    "executions_truncated": false
  }
}
```

`manifest_version` (`edge.export.v1`) is bumped only on
backwards-incompatible wire changes; new optional fields and new
`missing_artifacts.reason` values stay on the same version. Re-import
tooling pins on this string.

`redaction_level` on the bundle envelope is the maximum strictness across
the artifact entries (`strict` wins over `standard`). Per-pointer
redaction levels are still available on each entry.

## Artifact types

The `artifact_type` enum covers the P0 evidence catalog:

| Type | Carries |
| --- | --- |
| `edge.transcript` | Redacted Claude session transcript |
| `edge.diff` | Redacted file diffs (Edit/Write/MultiEdit) |
| `edge.tool_input` | Redacted Bash/Read/Edit input parameters |
| `edge.tool_result` | Redacted tool output |
| `edge.test_output` | Redacted `npm test`/`go test`/CI output |
| `edge.mcp_request` | Redacted MCP tool-call request body |
| `edge.mcp_response` | Redacted MCP tool-call response body |
| `edge.llm_prompt_redacted` | Redacted LLM prompt (provider-side) |
| `edge.llm_response_redacted` | Redacted LLM response (provider-side) |
| `edge.evidence_bundle` | This bundle, stored as an artifact (self-reference) |

`retention_class` is `short` / `standard` / `audit`; `redaction_level` is
`standard` / `strict`. Both are pinned by the producing pack; the export
bundle preserves them verbatim.

## Missing artifact reasons

The `missing_artifacts` section records every artifact pointer the bundler
saw on a session event but could not include in `artifacts`. Reasons:

| Reason | Cause |
| --- | --- |
| `not_found` | Artifact body has been removed by TTL expiry, or was never stored. Operationally normal; auditors may need to retrieve a fresh copy from the source system. |
| `tenant_mismatch` | The pointer's `tenant_id` did not match the export tenant. Should never happen for current data (the `AttachArtifactPointer` helper rejects cross-tenant pointers at write time) but the bundler defends historical data anyway. |
| `store_error` | Transient artifact-store error during this export. Caller may retry. |

A pointer with `reason = tenant_mismatch` is recorded **without** calling
`Stat` on the artifact store — defense in depth against historical
cross-tenant evidence.

## Size limits

The Gateway clamps the response size at `CORDUM_EDGE_EXPORT_MAX_BYTES`
(default 10 MiB; ceiling 64 MiB). Oversize bundles return HTTP 413 with a
hint at lowering `max_events`. Auditors must NOT silently receive a
truncated bundle — the only way to get a partial bundle is to set
`max_events` explicitly, in which case `truncation.events_truncated` is
the authoritative signal.

## Future scope

* SIEM / compliance export with provider-specific schemas remains
  enterprise scope. The OSS export is the JSON manifest above.
* `include_artifact_bodies` opt-in (already-redacted, small bodies only)
  is reserved for a follow-up task with stricter authentication.
* Streaming export (NDJSON over HTTP) for sessions exceeding the size cap
  is not in P0; the size cap is the boundary today.
