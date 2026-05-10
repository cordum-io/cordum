# Policy Rules Write API

The Policy Studio write API (Backend 5c) lets dashboard authors and
tooling create, update, and bind unified `Rule` envelopes. It is the
authoring half of the unified policy authority shared with the four
runtime evaluators (`job`, `edge`, `mcp_tool`, `output`); the read /
list halves (Backend 1, 5a, 5b) are documented in
`docs/api-reference.md` § Policy.

This page covers three endpoints:

- `POST /api/v1/policy/rules` — create a Rule.
- `PUT  /api/v1/policy/rules/{id}` — update a Rule with optimistic
  concurrency.
- `POST /api/v1/policy/bundles/{id}/rules` — bind an existing rule into
  a bundle's `rule_ids` set.

OpenAPI source: `docs/api/openapi/cordum-api.yaml` v`2026-05-10.2`.

## Authorization

All three endpoints require:

- `X-Tenant-ID` header (RBAC tenant binding).
- An identity with `policy.write` permission OR the `admin` role.
- Standard auth (API key / SAML session / mTLS — see
  `docs/api-reference.md` § Authentication).

## Server-managed fields

The server is the sole writer of the following fields on every Rule
write. Client-supplied values are rejected with `400` on create and
silently overwritten on update — clients cannot fake history:

| Field | When set | Notes |
|-------|----------|-------|
| `version` | Create=`v1`, Update=`vN` -> `v(N+1)` | Used as the `If-Match` token. |
| `audit.created_at` | Create | RFC3339 UTC. |
| `audit.created_by` | Create | Resolved from auth context (principal id). |
| `audit.updated_at` | Update | RFC3339 UTC. |
| `audit.updated_by` | Update | Resolved from auth context. |
| `status` | Create defaults to `draft` if empty | Updates preserve prior `status` when body omits it. |

The four authoring fields callers control are: `id`, `name`, `type`,
`scope`, `match`, `decide`, `description`.

## POST /api/v1/policy/rules

Create a Rule.

### Request

```http
POST /api/v1/policy/rules HTTP/1.1
Content-Type: application/json
X-Tenant-ID: tenant-acme
X-API-Key: <key>

{
  "id": "rule.input.secret-scan",
  "name": "Block secrets in input",
  "type": "input",
  "scope": {"kind": "tenant", "value": "tenant-acme"},
  "match": {"topics": ["job.acme.evaluate"], "keywords": ["aws-access-key"]},
  "decide": {"decision": "deny", "reason": "secret pattern matched"},
  "description": "Tenant-scoped input rule."
}
```

### Responses

- `201 Created` + `Location: /api/v1/policy/rules/{id}` header + the
  persisted Rule envelope (with server-set `version`, `audit`, `status`).
- `400 Bad Request` — validation failure or client tried to set
  `version` / `audit.*`.
- `401 Unauthorized` — auth missing or invalid.
- `403 Forbidden` — caller lacks `policy.write`.
- `409 Conflict` — duplicate `id` (the rule store is `SETNX`).
- `500 Internal Server Error` — store failure.

## PUT /api/v1/policy/rules/{id}

Update a Rule with optimistic concurrency.

### Request

```http
PUT /api/v1/policy/rules/rule.input.secret-scan HTTP/1.1
Content-Type: application/json
X-Tenant-ID: tenant-acme
X-API-Key: <key>
If-Match: v1

{
  "id": "rule.input.secret-scan",
  "name": "Block secrets in input (v2)",
  "type": "input",
  "scope": {"kind": "tenant", "value": "tenant-acme"},
  "match": {"topics": ["job.acme.evaluate"], "keywords": ["aws-access-key", "secret"]},
  "decide": {"decision": "deny", "reason": "secret pattern matched"}
}
```

The `If-Match` header value is the current `Rule.Version`. The path id
wins over the body id on conflict — clients cannot rename a Rule via
PUT.

### Responses

- `200 OK` + the persisted Rule envelope. `version` is bumped
  (`vN` -> `v(N+1)`); `audit.updated_at` / `audit.updated_by` refreshed.
- `400 Bad Request` — validation failure.
- `401 Unauthorized` / `403 Forbidden`.
- `404 Not Found` — no such Rule.
- `409 Conflict` — `If-Match` does not equal the current version.
  Body:

  ```json
  {
    "error": "stale_version",
    "current_version": "v3",
    "current_audit_hash": "sha256:..."
  }
  ```

  The dashboard renders a reload banner from this body so the user can
  resolve the conflict without a follow-up GET. `current_audit_hash` is
  the SHA-256 hex of the server-side current Rule JSON envelope; clients
  can use it to confirm a follow-up fetch returned the same state.
- `412 Precondition Failed` (status `428` Precondition Required emitted
  by the handler — RFC 6585) — `If-Match` header missing.
- `500 Internal Server Error`.

## POST /api/v1/policy/bundles/{id}/rules

Bind an existing rule into a bundle. Idempotent on repeat. Concurrent
binds with distinct rule IDs converge under Lua CAS — no lost writes.

### Request

```http
POST /api/v1/policy/bundles/bundle-acme/rules HTTP/1.1
Content-Type: application/json
X-Tenant-ID: tenant-acme
X-API-Key: <key>

{
  "rule_id": "rule.input.secret-scan"
}
```

### Responses

- `200 OK` + the updated `Bundle` envelope (with the new entry in
  `rule_ids`).
- `400 Bad Request` — empty / missing `rule_id`.
- `401 Unauthorized` / `403 Forbidden`.
- `404 Not Found` with disambiguated body so the dashboard can show
  the right copy without guessing:

  ```json
  {"error": "bundle_not_found"}
  ```

  or

  ```json
  {"error": "rule_not_found"}
  ```

- `500 Internal Server Error`.

## Concurrency guarantees

- `POST /policy/rules` is atomic via Redis `SETNX` on the envelope
  STRING + `SET` on the version sidecar STRING. The version is written
  twice (envelope JSON + sidecar) but read only from the sidecar; the
  Lua script ensures both writes succeed or neither does.
- `PUT /policy/rules/{id}` compares the `If-Match` token against the
  sidecar STRING under a Lua `GET / compare / SET` script. Concurrent
  updates with the same `If-Match` race; one wins; the other receives
  `409 stale_version`.
- `POST /policy/bundles/{id}/rules` reads the bundle envelope, mutates
  the `rule_ids` set in process, and writes back via a Lua compare-set
  on the JSON envelope. Conflicts retry until convergence; the
  architect's race test (10 goroutines / distinct ruleIDs / single
  bundle / `-count=3`) is in `core/policy/bundle_store_addrule_test.go`.

## Versioning

`info.version` in `cordum-api.yaml` was bumped from `2026-05-10.1` to
`2026-05-10.2` to reflect the additive surface. No existing endpoints
changed; dashboard codegen drift is additions-only.

## Related work

- Backend 5a / 5b — read endpoints (`GET /policy/rules` list, decisions
  REST + WS stream).
- Backend 5d (planned) — dashboard hook wrapper that passes `If-Match`
  through to `apiClient` directly. orval does not auto-emit header
  params, so until 5d ships dashboard authors must call `apiClient`
  with `headers: { "If-Match": ... }` rather than the generated
  `updatePolicyRule(id, rule)` signature.
