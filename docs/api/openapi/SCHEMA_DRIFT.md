# OpenAPI Schema Drift Audit

Captured: 2026-04-18 (refresh after QA reopen)
Task: task-f2143638 (OpenAPI spec audit and completion)
Scope: every path documented in `docs/api/openapi/cordum-api.yaml`
cross-referenced against its Go handler and handler-test fixtures.

This document records drift findings only. Where drift was found that could
be closed additively, the fix was applied in the same task (see the
CHANGELOG). Fields present in the spec but absent from the response are
flagged with `deprecated: true` per
`feedback_triple_check_deletions.md` — never silently removed.

## Method

For each tag group in the spec, I opened:
- `core/controlplane/gateway/handlers_*.go` for the handler signature and
  the Go response struct,
- the matching `_test.go` file for the authoritative JSON fixture when
  the struct had non-obvious marshalling (embedded fields, custom
  MarshalJSON, interface-typed values),
- `core/infra/store/*.go` / `core/audit/*.go` / other domain packages
  for the source-of-truth struct when the handler just copied one out.

## Findings by tag

### Agents (new tag — see step 5)
Handler struct `agentResponse` at `handlers_agents.go:41` maps cleanly
through `agentResponseFromIdentity` from `store.AgentIdentity`. No drift;
spec schema `AgentIdentity` was written from the struct directly.

### MCP
- `handlers_mcp_tools.go` — the `tools` field carries `[]mcp.Tool`
  wrapped in a map alongside `agent_id`, `filtered`, and an optional
  `note`. `AgentToolVisibility` schema and `MCPTool` (with
  `additionalProperties: true` since the registry shape evolves)
  capture both the filtered and unfiltered-admin responses.
- `mcp_approval_handlers.go` — the list response uses `next_cursor:
  "0"` as an end-of-scan sentinel. Documented in the
  `MCPApprovalList.next_cursor` description so SDK callers don't treat
  `"0"` as a live cursor.

### Audit
- `handlers_audit_verify.go` — `VerifyResult` from `core/audit`
  includes `retention_window_hours` (added in a prior task) and
  `first_seq` / `last_seq`. All three were missing from the spec
  (there was no `/api/v1/audit/verify` entry at all) and have been
  added in step 7's new path stanza plus the new `AuditVerifyResult`
  schema.

### Policy (shadow activation)
- The handler registers
  `POST|GET|DELETE /api/v1/policy/shadows/{id}`, but the spec only
  listed `POST|GET|DELETE /api/v1/policy/bundles/{id}/shadow` — the
  pre-Go-1.22-mux-collision path. This is structural drift: callers
  that followed the spec would get 404 at the gateway. Remediation:
  duplicate the path entries at `/api/v1/policy/shadows/{id}` with
  the live operationIds, and leave the old entries in place flagged
  `deprecated: true` with a pointer to the replacement. Audit tool
  was updated to skip deprecated ops when computing coverage so CI
  doesn't report a false gap on the re-homed paths.

### Stream (/api/v1/stream)
- Spec previously declared `get:` only; the gateway registers the
  path without a method prefix so every verb forwards to the
  WebSocket upgrader. Fix: added `x-any-method: true` (consumed by
  the new audit tool), `x-websocket: true`, and
  `x-websocket-message-schema` pointing to the new
  `StreamEvent` schema, which enumerates the 7 discriminators from
  `busPacketType()` in `handlers_stream.go:613`.

### Governance / Audit Export / Policy Bundles / Shadow Results
Existing schemas (added in the last several tasks — `GovernanceHealth`,
`ShadowPolicy`, `ShadowResultsSummary`, et al.) were cross-referenced
against their handler responses and found to match. No drift to
record.

### Workers (`handlers_workers.go`)
Handler returns one of three shapes via `writeJSON`:
- `workerSummaryToResponse(ws, capturedAt)` — projects `core/infra/store`
  snapshot fields into the spec's `WorkerSnapshot` schema. Field-for-field
  match with `WorkerSnapshot` in `components.schemas`.
- `{"items": [...]}` — list shapes for `/workers`, `/workers/{id}/jobs`,
  `/workers/{id}/credentials`. The spec models these as paginated
  collections; cursor handling is delegated to the underlying store and
  the handlers do not add cursor metadata for these endpoints.
- The credential-issue path returns a token via the
  `IssueWorkerCredentialResponse` schema (see Worker Credentials below).

No drift found at the handler-response level. Field-level drift between
`store.WorkerSnapshot` and the schema would only matter on a fresh field
addition at the store layer; the existing `WorkerSnapshot` schema fields
all map to live struct fields.

### WorkerCredentials (`handlers_worker_credentials.go`)
Two response structs auditable directly in the handler file:

- `workerCredentialResponse` (line 22): `worker_id`, `allowed_pools`,
  `allowed_topics`, `pack_id`, `agent_id`, `created_by`, `created_at`,
  `revoked_at`. Spec's `WorkerCredential` schema lists the same fields
  with matching `omitempty` semantics. No drift.
- `issueWorkerCredentialResponse` (line 33): `worker_id` + `token`.
  Matches `IssueWorkerCredentialResponse` in spec.

Request struct `createWorkerCredentialRequest` matches
`CreateWorkerCredentialRequest` schema (`worker_id`, `allowed_pools`,
`allowed_topics`, `agent_id`).

No drift found.

### Pools (`handlers_pools.go`)
- `createPoolRequest` (line 129) and `updatePoolRequest` (line 204)
  map cleanly to `CreatePoolRequest` / `UpdatePoolRequest` schemas.
- `drainPoolRequest` (line 346) — single field `timeout_seconds`,
  matches `DrainPoolRequest` schema.
- Pool list / detail responses use the `Pool` schema sourced from the
  pool store struct; spec fields and store fields align on the union of
  declared json tags.
- Three error wrappers (`poolNotFoundError`, `topicNotFoundError`,
  `topicPoolMappingNotFoundError`) marshal to the standard Error
  envelope via `writeErrorJSON`; they don't escape into the spec as
  separate schemas.

No drift found.

### Topics (`handlers_topics.go`)
- `createTopicRequest` (line 16) and `topicResponse` (line 27) — every
  field has a matching property in the spec's `Topic` /
  `CreateTopicRequest` schemas including `active_worker_count` (added
  by the dynamic-registry task).
- `omitempty` semantics align: `input_schema_id`, `output_schema_id`,
  `pack_id`, `requires`, `risk_tags` are non-required in the spec.

No drift found.

### Velocity (`handlers_velocity.go`)
- `velocityRuleMatch` (line 34): `topics`, `tenants`, `risk_tags` all
  `omitempty` — matches the spec's `VelocityRuleMatch` schema's
  optional fields.
- `velocityRuleUpsertRequest` (line 40) and `velocityRuleResponse`
  (line 54) include `id`, `name`, `match`, `window`, `key`, `threshold`,
  `decision`, `reason`, `enabled`, `author`, `message`. Spec's
  `VelocityRule` / `VelocityRuleUpsert` schemas list every one of
  these fields. `enabled` is `*bool` in Go (tri-state: omitted means
  "use existing") — spec models it as `boolean` with no required flag,
  which preserves the omitted-on-input semantics.

No drift found.

### LegalHold (`handlers_legal_hold.go`)
- POST body uses an inline anonymous struct `{tenant_id, reason}`
  matching the spec's `CreateLegalHoldRequest` schema (line 61 of the
  handler).
- GET responses wrap the store's `LegalHold` value in either
  `{"hold": <hold>}` (single) or `{"holds": [...]}` (list). The spec
  models these via the `LegalHoldResponse` / `LegalHoldList` envelope
  schemas; both match the wrapper key names exactly.
- DELETE returns `{"released": true, "id": <id>}` per the
  `LegalHoldReleaseResponse` schema.

No drift found.

### License (`handlers_license.go`)
Handler dispatches to three writers (`writeJSON(w, resp)` at lines
26, 50, 138) where `resp` is the licensing-package `LicenseInfo` /
`LicenseStatus` / `EntitlementsResponse` struct. Each struct has
`json:` tagged fields that map to the spec's `LicenseInfo`,
`LicenseStatus`, and `Entitlements` schemas. The store package owns
those structs so the source of truth lives outside the handler — the
spec's schema definitions were written from the licensing package's
struct declarations.

No drift found at the handler boundary. A struct-level diff against
`core/licensing/*.go` was outside this audit's scope but the spec
matches the wire shape verified by `handlers_license_test.go`'s JSON
fixtures.

### Chat (`handlers_chat.go`)
- `chatEvent` (line 25) — every field has a matching
  `ChatEvent` schema property; `omitempty` semantics align.
- `chatMessage` (line 37) — required `id`, `run_id`, `role`,
  `content`, `created_at` map to required fields in `ChatMessage`;
  `step_id`, `job_id`, `agent_id`, `agent_name`, `metadata` are all
  `omitempty` and optional in spec.
- `chatResponse` (line 50) — `items` + nullable `next_cursor` (typed
  as `*int64` in Go, `integer | null` in spec).

No drift found.

## Summary

- 0 existing ops needed their spec schema narrowed or changed in a
  breaking way.
- 15 ops were either entirely missing from the spec or pointed at a
  dead path; step 5 / step 6 / step 7 added them in an additive-only
  manner.
- 3 deprecated path entries were retained (policy/bundles/{id}/shadow)
  so SDK-generated clients continue to compile.
- All 8 tags previously deferred (Workers, WorkerCredentials, Pools,
  Topics, Velocity, LegalHold, License, Chat) have been re-audited;
  zero field-level drift found at the handler-marshalling boundary.

## Follow-ups

1. **Mechanized parity** — write a CI job that decodes a recorded
   handler-test JSON fixture against the spec schema using
   `kin-openapi`. Right now the audit confirms the schemas were
   written from the structs at the time of writing but does not
   prevent silent future drift when a new field is added to a
   request/response struct. The human follow-up captured here is the
   correct, scope-bounded next step rather than expanding this task
   into a generator-driven spec.
2. The `MCPApprovalRecord.decision` field is typed as `model.ApprovalDecision`
   in Go; the spec documents it as a plain `string`. The string form is
   accurate for the wire (the Go type marshals to its string name) but
   a follow-up could harden this to an explicit enum once the canonical
   set is audited from `model.ApprovalDecision*` constants.
