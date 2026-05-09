# Unified Policy Evaluator API

Backend 5 of the Policy Studio rewrite adds one evaluator entry-point for job
and Edge policy decisions while preserving the old split endpoints during the
migration window.

## Entry points

| Surface | Endpoint | Status | Auth |
| --- | --- | --- | --- |
| HTTP | `POST /api/v1/policy/evaluate` | canonical | API key or bearer token + `X-Tenant-ID`; requires `policy.write` or `admin` |
| gRPC | `PolicyEvaluator.EvaluateUnified` | canonical | gateway gRPC auth; requires `admin` or `operator` role |
| HTTP | `POST /api/v1/policy/simulate` | deprecated/live | same legacy policy auth; returns `Deprecation` + successor `Link` headers |
| HTTP | `POST /api/v1/policy/explain` | deprecated/live | same legacy policy auth; returns `Deprecation` + successor `Link` headers |
| HTTP | `POST /api/v1/edge/evaluate` | deprecated/live for new clients | same legacy Edge auth; returns `Deprecation` + successor `Link` headers |

`/api/v1/policy/evaluate` also accepts the old `PolicyCheckRequest` body while
clients migrate. Legacy-shape calls return the unchanged protobuf-JSON
`PolicyCheckResponse` plus the deprecation headers. Unified-shape calls return
`{ "decision": Decision }`.

## Unified HTTP request

A unified request must provide exactly one rule source and exactly one context:

- Rule source: either inline `rule`, or `bundle_id` + `scope`.
- Context: either `job_context`, or `edge_context`.

### Job inline rule example

```json
{
  "rule": {
    "id": "rule-input-allow",
    "name": "allow job input",
    "type": "input",
    "scope": { "kind": "tenant", "value": "tenant-acme" },
    "status": "published",
    "version": "v1",
    "match": { "topic": "job.deploy" },
    "decide": { "decision": "allow" }
  },
  "job_context": {
    "tenant_id": "tenant-acme",
    "job_id": "job-123",
    "topic": "job.deploy",
    "principal_id": "alice",
    "capability": "deploy",
    "risk_tags": ["release"],
    "input": {
      "content": "deploy service api",
      "content_type": "text/plain",
      "size_bytes": 18
    }
  }
}
```

Response:

```json
{
  "decision": {
    "type": "allow",
    "source": "job",
    "rule_id": "rule-input-allow",
    "timestamp": "2026-05-09T18:00:00Z",
    "trace": [
      {
        "rule_id": "rule-input-allow",
        "decision_type": "allow",
        "reason": "matched job-side safety policy"
      }
    ]
  }
}
```

### Edge inline rule example

```json
{
  "rule": {
    "id": "edge-shell-deny",
    "name": "deny shell writes",
    "type": "edge",
    "scope": { "kind": "tenant", "value": "tenant-acme" },
    "status": "published",
    "version": "v1",
    "match": { "tool_name": "Bash", "capability": "exec.shell" },
    "decide": { "decision": "deny", "reason": "shell writes require review" }
  },
  "edge_context": {
    "tenant_id": "tenant-acme",
    "principal_id": "alice",
    "session_id": "edge-session-1",
    "execution_id": "exec-1",
    "agent_product": "claude-code",
    "tool_name": "Bash",
    "tool_input_redacted": { "command": "rm -rf build" },
    "input_hash": "sha256:...",
    "risk_tags": ["shell"]
  }
}
```

## Dispatch matrix

| `Rule.type` | Required context | Dispatcher | Decision source |
| --- | --- | --- | --- |
| `input` | `job_context` | Safety Kernel input adapter/evaluator | `job` |
| `output` | `job_context` | Safety Kernel output adapter/evaluator | `job` |
| `velocity` | `job_context` | Safety Kernel velocity adapter/evaluator | `job` |
| `edge` | `edge_context` | Edge classifier adapter/evaluator | `edge` |

Type-confusion requests fail before dispatch. For example, `Rule.type=edge`
with `job_context` returns `400` over HTTP or `InvalidArgument` over gRPC; no
Safety Kernel or Edge classifier call is made.

## Bundle resolution

When a request uses `bundle_id` + `scope`, the evaluator:

1. Reads the active deployment for `scope` from `BundleStore`.
2. Requires the active deployment's `bundle_id` to equal the requested
   `bundle_id`.
3. Loads the active `BundleVersion` and scans its immutable `rule_snapshot`.
4. Selects the first `published` rule whose `Rule.type` is compatible with the
   supplied context.
5. Evaluates that rule and stamps the returned `Decision` with
   `bundle_id`/`bundle_version`.

No active deployment, a bundle mismatch, a missing version, or a snapshot with
no compatible published rule returns `404`/`NotFound`.

## Bundle lifecycle HTTP access

Backend 5 exposes real BundleStore-backed lifecycle routes; these are not
YAML-only OpenAPI placeholders.

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| `GET` | `/api/v1/policy/bundles/{id}/versions` | `policy.read` or `admin` | List immutable versions. |
| `POST` | `/api/v1/policy/bundles/{id}/versions` | `policy.write` or `admin` | Create a version with `version`, `rule_snapshot`, optional `deployed_at`, and `audit_hash`. |
| `GET` | `/api/v1/policy/bundles/{id}/versions/{version}` | `policy.read` or `admin` | Fetch one version. |
| `POST` | `/api/v1/policy/bundles/{id}/deploy` | `policy.write` or `admin` | Deploy version to a `RuleScope`; tenant scopes require matching tenant access. |
| `GET` | `/api/v1/policy/bundles/deployments` | `policy.read` or `admin` | Query history by `scope_kind`, optional `scope_value`, and `limit` (max 100). |
| `POST` | `/api/v1/policy/bundles/deployments/rollback` | `policy.write` or `admin` | Roll back the active deployment for a scope by one deploy step. |

## Error table

| Failure | HTTP | gRPC |
| --- | --- | --- |
| Invalid JSON, missing rule source/context, unsupported rule type, type confusion | `400` | `InvalidArgument` |
| Auth missing or tenant/RBAC denied | `401`/`403` | `Unauthenticated`/`PermissionDenied` |
| No active deployment, bundle/version missing, no compatible published rule | `404` | `NotFound` |
| Bundle store unavailable | `503` | `Unavailable` |
| Downstream evaluator failure | `502` or `503` | `Unavailable` |
| Unexpected server error | `500` | `Internal` |

## Audit-chain behavior

Unified evaluations emit the shared `policy.decision.v2` audit record. During
the migration window, job and Edge evaluators can run in dual-emission mode:
legacy audit records (`safety.decision` or legacy Edge decision events) are
written first, then the unified `policy.decision.v2` record. This preserves
existing consumers while the Decisions surface reads the unified stream.

## Migration guide

- New job and Edge clients should move to `POST /api/v1/policy/evaluate` (or
  `PolicyEvaluator.EvaluateUnified`) with `PolicyEvaluateRequest`.
- Existing `/api/v1/policy/simulate`, `/api/v1/policy/explain`, legacy-shape
  `/api/v1/policy/evaluate`, and `/api/v1/edge/evaluate` callers can continue
  during the Policy Studio transition window. They receive `Deprecation: true`
  and `Link: </api/v1/policy/evaluate>; rel="successor-version"`.
- Removal of deprecated evaluator surfaces is deferred until the later Policy
  Studio cut-over after backend and dashboard clients have migrated.
