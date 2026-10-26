# Cordum MCP Server

Cordum exposes MCP using JSON-RPC 2.0 so Claude clients can discover tools and
resources through a standard protocol.

Current implementation status:

- Transport and protocol handlers are implemented.
- Tool/resource registries are implemented and config-toggle aware.
- Gateway MCP auth + tenant checks are implemented.
- Built-in tool/resource handlers are registered by default in both stdio and
  gateway HTTP modes (`core/mcp/tools/register.go`,
  `core/mcp/resources/register.go`).

## Overview

Cordum MCP supports these JSON-RPC methods:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`
- `resources/list`
- `resources/templates/list`
- `resources/read`

Primary code paths:

- `cmd/cordum-mcp/main.go`
- `core/mcp/server.go`
- `core/mcp/transport_stdio.go`
- `core/mcp/transport_http.go`
- `core/controlplane/gateway/handlers_mcp.go`

## Transport Modes

### 1) `stdio` (local client integration)

Run:

```bash
go run ./cmd/cordum-mcp --addr http://localhost:8081 --api-key "$CORDUM_API_KEY" --allow-private-gateway
```

Flags:

- `--addr` (default `http://localhost:8081`)
- `--api-key` (or `CORDUM_API_KEY`)
- `--allow-private-gateway` (or `CORDUM_MCP_ALLOW_PRIVATE_GATEWAY=true`;
  default `false`): allow a gateway on `localhost` or a private or loopback
  address. Without it, every gateway call to such an `--addr`, the default
  included, fails with `target host resolves to private address`.
- `--gateway-allowlist` (or `CORDUM_MCP_GATEWAY_ALLOWLIST`): comma-separated
  hosts or domains that the `--addr` host must match. A domain also matches
  its subdomains. It applies even with `--allow-private-gateway`. See
  [Endpoint validation](#endpoint-validation).
- `--request-timeout` (default `30s`)

### 2) Gateway HTTP + SSE (remote clients)

Enable MCP in config:

- `mcp.enabled=true`
- `mcp.transport=http`
- `mcp.port=<optional metadata>`

Routes:

- `GET /mcp/sse`
- `POST /mcp/message`
- `GET /mcp/status`

`/mcp/sse` returns `X-MCP-Session-ID`. Clients can send `X-MCP-Session-ID` on
`/mcp/message` to correlate session responses.
Each session is bound to the authenticated caller that opened it (tenant and
principal, plus the API key or login session used), so a `/mcp/message` naming
an unknown session or another caller's session gets HTTP `404` with JSON-RPC
error `-32001` (`session not found`); reconnect to `/mcp/sse` for a new id.

### Policy Gate + Approval Hold (EDGE-102 / EDGE-103)

`tools/call` requests can run through the production action-gate pipeline
before reaching the underlying tool handler. The gate emits structured
`mcp.tool.pre` / `mcp.tool.post` / `mcp.tool.failed` events to the Edge
event stream so audit consumers and the dashboard's governance timeline
see every MCP call alongside hook + LLM events. Oversized redacted args
land in artifact storage with content-addressed SHA-256 pointers.

#### Feature flag

The wiring is gated on `mcp.policy_gate_enabled` (default **false** so
legacy deploys keep the direct-dispatch path until operators explicitly
opt in). Set it via the config service:

```yaml
mcp:
  enabled: true
  transport: http
  policy_gate_enabled: true   # EDGE-102/103 wiring
```

#### Boot log line

When the gate is wired the gateway emits one greppable boot log:

```
INFO mcp.policy_gate wired server_name=cordum.builtin
     policy_gate_active=true approval_hold_active=true
     emitter=edge.RedisStore artifact_store=artifacts.RedisStore
```

When the flag is off:

```
INFO mcp.policy_gate skipped reason="policy_gate_enabled config flag is false"
```

Operators grep these lines after a rolling restart to confirm the gate
came up. The `partial-wiring guard` in `core/mcp/server.go` resets the
gate to off if required deps (action-gate pipeline, event emitter,
policy snapshot) are missing — the boot log then reports
`policy_gate_active=false` so misconfigured deploys are visible.

#### Constraint decisions

The production action-gate pipeline is a blockers/approval path: gates
allow, deny, throttle, or mint/consume approval holds, but they do not
emit enforceable generic runtime constraints. Do not rely on an
action-gate `_constraints` map for single-use approval or other runtime
limits; those semantics are enforced by the approval store consume path.

`ALLOW_WITH_CONSTRAINTS` remains a compatibility/future-typed dispatcher
shape for policy-bundle/SafetyKernel constraints and fake-dispatcher
tests outside the production action-gate pipeline. When such a
non-actiongate dispatcher returns `ALLOW_WITH_CONSTRAINTS` with a
structured constraint payload, the payload propagates to both pre and
post events:

- `event.Decision = constrain` (NOT `allow`)
- `event.Constraints = {...}` (same shape as
  `agentd EvaluateResponse.Constraints` — single canonical wire shape
  across hook + MCP surfaces)
- `event.ErrorMessage` empty (AWC is an allow, just bounded)

If upstream fails after that compatibility/future-typed verdict, the resulting
`mcp.tool.failed` event still records `Decision=constrain` +
the constraint map so the audit trail preserves the dispatcher intent.

The operator-facing log line carries `constraint_count=<N>` so AWC
volume from those non-actiongate paths is greppable, but never the constraint values
themselves (CLAUDE.md security rail — constraint values may carry
sensitive policy detail; the full map lives on the audit-bound event
plus the artifact pointer for forensics).

#### Approval-hold consume path

When `mcp.policy_gate_enabled=true`, `tools/call` arguments may carry
an `_approval_ref` field referencing a previously-minted Edge
approval. The server-reserved field is stripped before the upstream
handler sees the args, and the approval is consumed atomically through
`edge.RedisStore.ClaimApproval` with the bundle-updated-at timestamp
as the consume-time `PolicySnapshot`. Lifecycle conflicts (rejected /
expired / consumed / args_mismatch / policy_mismatch / self_approval /
cross_tenant / not_found) surface as JSON-RPC `-32096` with
`error.data.kind` carrying the typed conflict family.

#### Retry dedupe

`InvokeToolWithPolicy` collapses idempotent retries of the same MCP
tool call into a single pre/post event pair via an in-process
singleflight. The dedupe key is the hex SHA-256 over the canonical
`(tenant, server, tool, action_hash, session, execution, principal)`
tuple, joined with the 0x1F unit-separator byte so pipe-bearing
tenant or tool identifiers cannot smuggle a delimiter and collide
with neighboring fields. `action_hash` is `ActionTupleHash(tenant,
server, tool, normalized_target_path)` — backslash and forward-slash
path spellings produce the same hash.

The key is derived BEFORE the policy pipeline runs, so the first
caller wins the `sync.Map.LoadOrStore` slot, evaluates the gate,
forwards to upstream, and emits the pre + post events; concurrent
callers with identical semantic inputs block on the winner's done
channel and return the cached result without re-running the pipeline
or emitting duplicate events. The `EventID` stays random (one fresh
ID per call for tracing); two retries with different `EventID`s but
identical semantic inputs still dedupe.

Successful outcomes stay cached in the singleflight map until the
process restarts; error outcomes delete the entry so the next retry
fires a fresh upstream attempt (transient transport failures do not
become sticky).

**Cross-process scope**: today's gateway is single-instance per
deployment unit, so the in-process `sync.Map` is sufficient. A
multi-instance HA gateway behind a load balancer would need a Redis
SETNX-backed `DedupeStore` for cross-process collapse; this is
tracked as a follow-up Moe task (PR #276 Sub-B deferred) — when
adopted, the in-process backend stays the default and the Redis
backend is gated by `CORDUM_MCP_DEDUPE_BACKEND=redis`.


## Upstream Server Registry (EDGE-101)

Cordum keeps approved upstream MCP server definitions in a tenant-scoped
registry instead of letting enterprise deployments attach arbitrary unmanaged
servers. The registry is the contract consumed by the EDGE-104 attach/preview
commands and the future EDGE-105 dashboard surface; the dashboard work is
intentionally deferred and no `cordum/dashboard/` files are touched here.

### Data model

Each registry entry stores:

- `name` - stable upstream identifier (`A-Z`, `a-z`, `0-9`, `.`, `-`).
- `transport` - one of `http`, `sse`, or `stdio`.
- `endpoint` - URL for the `http` and `sse` transports. It must be `https://`,
  and internal or cloud-metadata hosts are rejected in every policy mode (see
  [Endpoint validation](#endpoint-validation)).
- `command` - shell-free argv vector for `stdio`; shell metacharacters such as
  `;`, `&&`, pipes, redirects, backticks, and `$(` are rejected.
- `tenant_id` - tenant scope; `*` is reserved for system-wide entries and
  requires cross-tenant authority at the API layer.
- `auth_secret_ref` - optional secret reference. Raw secrets are rejected;
  values must be `secret://...` references when present.
- `labels` - operator metadata (`key=value`) for ownership and routing.
- `risk` - `low`, `medium`, `high`, or `critical`.
- `enabled` - disabled records remain visible to admins by default; add
  `?enabled=true` to list only active records.

List/Get responses never resolve credentials and never return raw secret
material. They return the `secret://` reference only, so secret resolution stays
inside the runtime/keychain boundary.

### HTTP API

All routes are under `/api/v1/edge/mcp/upstreams`, use the existing gateway
auth middleware, require `X-Tenant-ID`, run through the bounded-body cap, and
emit only redacted error details.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/edge/mcp/upstreams` | List tenant + system entries. Optional `?enabled=true|false`. |
| `GET` | `/api/v1/edge/mcp/upstreams/list` | Compatibility alias for list. |
| `POST` | `/api/v1/edge/mcp/upstreams` | Create an entry, or validate-only with `?validate_only=true`. |
| `GET` | `/api/v1/edge/mcp/upstreams/{name}` | Read one entry by name. |
| `PUT` | `/api/v1/edge/mcp/upstreams/{name}` | Update an entry. The previous payload is backed up first. |
| `POST` | `/api/v1/edge/mcp/upstreams/{name}/disable` | Disable an entry without deleting it. |
| `POST` | `/api/v1/edge/mcp/upstreams/{name}/enable` | Re-enable a disabled entry. |

Create, update and validate-only requests reject an `auth_secret_ref` that is
not a `secret://` reference, an unsafe endpoint (in every mode), a transport
other than `http`, `sse` or `stdio`, empty or shell-metacharacter `stdio` argv
elements, a name or a tenant ID (other than `*`) outside `[A-Za-z0-9.-]`
(first character alphanumeric), an unknown `risk`, and, in enterprise-strict
mode, a name that is not allowlisted.

### Endpoint validation

`core/edge/mcp_upstream_validation.go` checks `http` and `sse` endpoints in
every policy mode:

- The URL needs a scheme and a host, and the scheme must be `https`. Plain
  `http` is rejected unless trusted tenant config sets
  `safety.mcp.allow_plain_http: true`. The opt-in covers `?validate_only=true`,
  create and update. The Redis-backed registry validates again on create and
  update, and the gateway passes it the same opt-in.
- Cloud-metadata hostnames are rejected before any DNS lookup:
  `metadata.google.internal`, `metadata.azure.com` and
  `instance-data.ec2.internal` (case-insensitive, trailing dot ignored).
- `localhost` is rejected, and so is any IP literal or resolved address in an
  internal range: loopback (`127.0.0.0/8`, `::1`), "this network"
  (`0.0.0.0/8`) and IPv6 unspecified (`::`), RFC 1918 (`10.0.0.0/8`,
  `172.16.0.0/12`, `192.168.0.0/16`), carrier-grade NAT (`100.64.0.0/10`),
  link-local (`169.254.0.0/16`, which includes the cloud metadata IP
  `169.254.169.254`, and `fe80::/10`), IPv6 unique-local (`fc00::/7`) and
  multicast. These are the same IPv4 and IPv6 ranges the gateway guard below
  rejects. IPv4-mapped IPv6 addresses are checked as IPv4. One internal
  address among a host's DNS answers is enough to reject it. There is no
  opt-in for private upstreams. Tailscale gives tailnet devices addresses in
  `100.64.0.0/10`, so an upstream hosted on a tailnet needs a public
  endpoint.
- A failed DNS lookup rejects the endpoint only in enterprise-strict mode.

The addresses a host resolves to at registration are stored as
`resolved_ips`. Right before Cordum dials an upstream,
`RevalidateMCPUpstreamAtUse` resolves the host again and refuses the call if
the host is a cloud-metadata name, the lookup fails, any address is internal,
or the set of addresses differs from `resolved_ips` (DNS-rebinding
protection). An entry stored without `resolved_ips` is therefore never dialed.
`stdio` entries skip the use-time check.

The `cordum-mcp` server's own calls to the gateway (`--addr`) go through a
separate guard, `core/mcp/url_guard.go`. The scheme must be `http` or `https`,
and the host must match `--gateway-allowlist` (`CORDUM_MCP_GATEWAY_ALLOWLIST`)
when one is set. Unless `--allow-private-gateway`
(`CORDUM_MCP_ALLOW_PRIVATE_GATEWAY=true`) is set, the host must not be
`localhost` or `metadata.google.internal`, must resolve, and must not resolve
to `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`,
`169.254.0.0/16`, `172.16.0.0/12`, `192.168.0.0/16`, `::1`, `fe80::/10`,
`fc00::/7` or a multicast address. The connection is then pinned to the
checked addresses. A local gateway such as the default
`http://localhost:8081` therefore needs `--allow-private-gateway`.

### `cordumctl mcp upstream`

Operators can manage entries through the existing MCP CLI tree:

```bash
cordumctl mcp upstream add --name tenant-tools --transport http --endpoint https://mcp.example.com/tools --auth-secret-ref secret://vault/mcp/tenant-tools --risk medium --label team=platform --validate-only

cordumctl mcp upstream add --name tenant-tools --transport http --endpoint https://mcp.example.com/tools --auth-secret-ref secret://vault/mcp/tenant-tools

cordumctl mcp upstream validate --name tenant-tools --transport http --endpoint https://mcp.example.com/tools --auth-secret-ref secret://vault/mcp/tenant-tools
cordumctl mcp upstream list --json
cordumctl mcp upstream get tenant-tools
cordumctl mcp upstream disable tenant-tools
cordumctl mcp upstream enable tenant-tools
```

`--validate-only` calls the API with `?validate_only=true` and does not write a
registry record. `--auth-secret-ref` must be a `secret://` reference; do not pass
provider API keys or bearer tokens on the command line.

### Enterprise-strict allowlist

The policy mode comes only from trusted tenant config: `safety.mcp.policy_mode`
(`enterprise-strict`), which defaults to enterprise-strict when
`safety.mcp.allowed_upstreams` is set. Requests that pass `policy_mode`,
`allowed_upstream`, `allowed_upstreams` or `allow_plain_http` as query
parameters are rejected with 400. In enterprise-strict mode, registry writes
are allowed only when the upstream `name` appears in
`safety.mcp.allowed_upstreams`. This prevents unmanaged upstreams from being
introduced by local attach flows.

### Backup-on-update

`PUT` stores the previous record under a Redis backup key before overwriting the
active registry entry. Backup keys include a nanosecond timestamp plus collision
avoidance so rapid consecutive updates keep distinct prior payloads. Backups
expire after 30 days and are intended for operator rollback/forensics, not for
secret recovery (only `secret://` references are stored).

### Structured logs

Handlers emit one structured log per registry outcome:

```json
{"event":"mcp-upstream-<op>","tenant_id":"tenant-a","name":"tenant-tools","decision":"allow|deny","reason":"..."}
```

The log fields intentionally omit secret values and do not include the
`auth_secret_ref` field, even as a reference. Store/API responses may include the
`secret://` reference; logs stay descriptor-only.

## Attach Commands (EDGE-104)

`cordumctl mcp preview|attach|rollback` adds Cordum's MCP Gateway to a
local AI-client config (Claude Code, OpenAI Codex CLI, Cursor) so the
client routes `tools/call` through the gateway's policy gate and
upstream registry instead of reaching the original tool directly.

Attach is the **convenience/adoption path**. For enterprise enforcement
use the managed-settings flow (`cordumctl edge managed-settings export`,
EDGE-150) — managed settings deploy at the fleet level and cannot be
overridden by the user.

### Per-client default config paths

| Client       | Path                                | Format |
|--------------|-------------------------------------|--------|
| `claude_code`| `~/.claude.json` (top-level `mcpServers`) | JSON   |
| `codex`      | `~/.codex/config.toml` (`[mcp_servers.<id>]` sections) | TOML   |
| `cursor`     | `~/.cursor/mcp.json` (top-level `mcpServers`) | JSON   |

Override the default with `--config-path` (used by CI and tests; in
production prefer the documented user-scope path so other tooling
finds the same entry).

### Subcommand surface

```bash
# Read-only diff — never writes; exits 2 on parse failure.
cordumctl mcp preview --client claude_code

# Apply the merge. Without --apply, falls through to a preview so
# operators see exactly what would change.
cordumctl mcp attach --client claude_code --apply

# Restore the most-recent .bak.<unix_ms> snapshot via atomic rename.
cordumctl mcp rollback --client claude_code
```

Flags common to `preview` and `attach`:

- `--gateway-name` (default `cordum-gateway`) — entry name in the
  client config.
- `--gateway-transport` (default `http`) — `http` | `sse` | `stdio`.
- `--gateway-endpoint` (default
  `https://localhost:8081/api/v1/mcp/gateway/upstream`) — canonical
  MCP Gateway upstream URL for HTTP/SSE; ignored for stdio.
- `--gateway-secret-ref` — optional `secret://` reference written into
  the entry's env block as `CORDUM_AUTH_SECRET_REF` (see EDGE-101 for
  the upstream registry's secret-ref contract).

### Backup-on-modify

Every `attach` against an existing file writes a byte-identical
`<path>.bak.<unix_ms>` snapshot **before** atomic-renaming the merged
payload into place. The unix-ms suffix gives a deterministic newest-
first sort and lets rapid consecutive applies coexist. Backups stay
adjacent to the target file so they survive operator `rm -rf ~/.cache`
without affecting attach state; cleanup is manual today (sibling task
tracks an auto-rotate `cordumctl mcp cleanup-backups`).

### Rollback semantics

`rollback` selects the most-recent `<path>.bak.<unix_ms>` via Glob +
lexicographic sort (equal-width unix_ms is sort-safe) and `os.Rename`s
it back over the target. Cross-filesystem rename triggers a copy-and-
remove fallback. Exits `2` with a `"no backup"` signal if no snapshot
is found — operators cannot silently overwrite their current config
with a no-op restore.

### Secret redaction

Preview output runs through `redactSecrets` (mcp_attach_common.go)
which masks OpenAI-style `sk-*`, GitHub `ghp_*`/`gho_*`, and
`Bearer <token>` patterns with `<REDACTED>` before any stdout write.
This is a hard contract per the task DoD: a pre-existing target config
that contains a raw token never echoes the token into chat / paste-bin
/ CI log. Apply output does not include redacted content (only the
target path + backup path).

### Schema-version tracking

Each adapter source bakes in the URL + fetch-date of the client doc it
was validated against:

| Client       | Doc URL                                                     | Validated |
|--------------|-------------------------------------------------------------|-----------|
| `claude_code`| https://code.claude.com/docs/en/mcp                         | 2026-05-16 |
| `codex`      | https://developers.openai.com/codex/config-reference        | 2026-05-16 |
| `cursor`     | https://cursor.com/docs/context/mcp                         | 2026-05-16 |

Audit scripts can call `AttachSchemaProvenance(client)` (Go API) to
read these so a periodic review surfaces stale schemas. The runtime
does NOT fetch live docs on every invocation; operators who suspect
drift run `cordumctl mcp upstream validate` against the EDGE-101
registry for an authoritative gateway-side check.

### Cross-link to EDGE-101 upstream registry

Attach writes the per-client server entry; **what** it writes is
derived from the canonical `cordum-gateway` `UpstreamServer` record
that EDGE-101 owns (see `core/edge/mcp_upstream_registry.go`
`UpstreamServer{Name, Transport, Endpoint, Command, ...}`). The same
fields drive both surfaces — no parallel source of truth.

## Available Tools

Current runtime behavior:

- `tools/list` returns the built-in registered tool set (subject to config
  toggles).
- Tool IDs implemented in `core/mcp/tools.go`:
  - `cordum_submit_job`
  - `cordum_cancel_job`
  - `cordum_trigger_workflow`
  - `cordum_approve_job`
  - `cordum_reject_job`
  - `cordum_query_policy`

### `cordum_submit_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "topic": { "type": "string" },
    "prompt": { "type": "string" },
    "priority": { "type": "string", "enum": ["low", "normal", "high", "critical"] },
    "capability": { "type": "string" },
    "risk_tags": { "type": "array", "items": { "type": "string" } },
    "labels": { "type": "object", "additionalProperties": { "type": "string" } },
    "memory_id": { "type": "string" },
    "pack_id": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "tools/call",
  "params": {
    "name": "cordum_submit_job",
    "arguments": {
      "topic": "job.default",
      "prompt": "Summarize release notes",
      "priority": "normal",
      "risk_tags": ["external_api"]
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {
    "content": [{ "type": "text", "text": "job submitted" }],
    "structuredContent": {
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
      "trace_id": "6e0f1c62-12fd-45c8-95fb-f4cbaf955312",
      "status": "pending"
    }
  }
}
```

### `cordum_trigger_workflow`

JSON schema:

```json
{
  "type": "object",
  "required": ["workflow_id"],
  "properties": {
    "workflow_id": { "type": "string" },
    "input": { "type": "object", "additionalProperties": true },
    "dry_run": { "type": "boolean" },
    "idempotency_key": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "method": "tools/call",
  "params": {
    "name": "cordum_trigger_workflow",
    "arguments": {
      "workflow_id": "wf-demo",
      "input": { "message": "run guardrails check" },
      "dry_run": false
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "result": {
    "content": [{ "type": "text", "text": "workflow triggered" }],
    "structuredContent": {
      "run_id": "7e96f7dd-d6e8-4f1f-b7f5-84ad1bbd7aa3",
      "workflow_id": "wf-demo",
      "status": "pending"
    }
  }
}
```

### `cordum_approve_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id"],
  "properties": {
    "job_id": { "type": "string" },
    "note": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "method": "tools/call",
  "params": {
    "name": "cordum_approve_job",
    "arguments": {
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1",
      "note": "Reviewed and accepted."
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "result": {
    "content": [{ "type": "text", "text": "job approved" }],
    "structuredContent": {
      "approved": true,
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1"
    }
  }
}
```

### `cordum_reject_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id", "reason"],
  "properties": {
    "job_id": { "type": "string" },
    "reason": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "method": "tools/call",
  "params": {
    "name": "cordum_reject_job",
    "arguments": {
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1",
      "reason": "Policy exception not approved."
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "result": {
    "content": [{ "type": "text", "text": "job rejected" }],
    "structuredContent": {
      "rejected": true,
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1"
    }
  }
}
```

### `cordum_query_policy`

JSON schema:

```json
{
  "type": "object",
  "required": ["topic"],
  "properties": {
    "topic": { "type": "string" },
    "priority": { "type": "string", "enum": ["low", "normal", "high", "critical"] },
    "capability": { "type": "string" },
    "risk_tags": { "type": "array", "items": { "type": "string" } },
    "labels": { "type": "object", "additionalProperties": { "type": "string" } }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 15,
  "method": "tools/call",
  "params": {
    "name": "cordum_query_policy",
    "arguments": {
      "topic": "job.bank-executors.process",
      "priority": "normal",
      "capability": "shell.exec",
      "risk_tags": ["dangerous"]
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 15,
  "result": {
    "content": [{ "type": "text", "text": "policy simulated" }],
    "structuredContent": {
      "decision": "require_approval",
      "reason": "Financial execution actions require explicit human authorization.",
      "rule_id": "policy-finance-approval",
      "constraints": {},
      "remediations": []
    }
  }
}
```

### `cordum_cancel_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id"],
  "properties": {
    "job_id": { "type": "string" },
    "reason": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 16,
  "method": "tools/call",
  "params": {
    "name": "cordum_cancel_job",
    "arguments": {
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
      "reason": "Operator cancelled from MCP client"
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 16,
  "result": {
    "content": [{ "type": "text", "text": "job cancelled" }],
    "structuredContent": {
      "cancelled": true,
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1"
    }
  }
}
```

## Available Resources

Current runtime behavior:

- `resources/list` and `resources/templates/list` return built-in registered
  resources (subject to config toggles).

Registered resource catalog (`core/mcp/resources.go`):

- `cordum://jobs/{id}`
- `cordum://jobs?status={status}&limit={n}`
- `cordum://workflows/{id}/runs`
- `cordum://workflows/{id}/runs/{runId}`
- `cordum://audit?limit={n}`
- `cordum://health`
- `cordum://policies`

### `cordum://jobs/{id}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 21,
  "method": "resources/read",
  "params": {
    "uri": "cordum://jobs/2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 21,
  "result": {
    "contents": [
      {
        "uri": "cordum://jobs/2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
        "mimeType": "application/json",
        "text": "{\"id\":\"2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1\",\"state\":\"SUCCEEDED\",\"topic\":\"job.default\",\"result_ptr\":\"redis://res:2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1\"}"
      }
    ]
  }
}
```

### `cordum://jobs?status={status}&limit={n}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 22,
  "method": "resources/read",
  "params": {
    "uri": "cordum://jobs?status=running&limit=25"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 22,
  "result": {
    "contents": [
      {
        "uri": "cordum://jobs?status=running&limit=25",
        "mimeType": "application/json",
        "text": "{\"items\":[{\"id\":\"job-1\",\"state\":\"RUNNING\"}],\"next_cursor\":1739433600000000}"
      }
    ]
  }
}
```

### `cordum://workflows/{id}/runs`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 23,
  "method": "resources/read",
  "params": {
    "uri": "cordum://workflows/wf-demo/runs"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 23,
  "result": {
    "contents": [
      {
        "uri": "cordum://workflows/wf-demo/runs",
        "mimeType": "application/json",
        "text": "{\"workflow_id\":\"wf-demo\",\"items\":[{\"id\":\"run-1\",\"status\":\"running\",\"started_at\":\"2026-02-13T15:00:00Z\"}]}"
      }
    ]
  }
}
```

### `cordum://workflows/{id}/runs/{runId}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 24,
  "method": "resources/read",
  "params": {
    "uri": "cordum://workflows/wf-demo/runs/run-1"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 24,
  "result": {
    "contents": [
      {
        "uri": "cordum://workflows/wf-demo/runs/run-1",
        "mimeType": "application/json",
        "text": "{\"id\":\"run-1\",\"workflow_id\":\"wf-demo\",\"status\":\"running\",\"steps\":[{\"id\":\"step-1\",\"status\":\"succeeded\"}]}"
      }
    ]
  }
}
```

### `cordum://audit?limit={n}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 25,
  "method": "resources/read",
  "params": {
    "uri": "cordum://audit?limit=50"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 25,
  "result": {
    "contents": [
      {
        "uri": "cordum://audit?limit=50",
        "mimeType": "application/json",
        "text": "{\"items\":[{\"timestamp\":\"2026-02-13T15:01:00Z\",\"actor\":\"admin\",\"action\":\"approve\",\"target\":\"job-1\",\"decision\":\"allow\"}]}"
      }
    ]
  }
}
```

### `cordum://health`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 26,
  "method": "resources/read",
  "params": {
    "uri": "cordum://health"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 26,
  "result": {
    "contents": [
      {
        "uri": "cordum://health",
        "mimeType": "application/json",
        "text": "{\"worker_count\":8,\"connected_pools\":3,\"redis_status\":\"ok\",\"nats_status\":\"ok\",\"uptime\":7200}"
      }
    ]
  }
}
```

### `cordum://policies`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 27,
  "method": "resources/read",
  "params": {
    "uri": "cordum://policies"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 27,
  "result": {
    "contents": [
      {
        "uri": "cordum://policies",
        "mimeType": "application/json",
        "text": "{\"active_bundles\":[{\"id\":\"baseline\",\"enabled\":true,\"rule_count\":8}],\"current_snapshot_id\":\"v2026-02-13\",\"safety_stance\":\"balanced\"}"
      }
    ]
  }
}
```

## Authentication and Tenant Isolation

### `stdio` mode

- Provide gateway credentials via `--api-key` or `CORDUM_API_KEY`.
- The binary forwards calls through gateway-backed clients.

### HTTP/SSE mode

- Gateway MCP auth wrapper applies `AuthenticateHTTP`.
- Use:
  - `X-API-Key: <key>`
  - `X-Tenant-ID: <tenant>`
- Cross-tenant requests are denied unless key context explicitly allows
  cross-tenant access.

Quick status check:

```bash
curl -sS http://localhost:8081/mcp/status \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default"
```

## Integration Examples

### Claude Desktop (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "cordum": {
      "command": "cordum-mcp",
      "args": ["--addr", "http://localhost:8081", "--allow-private-gateway"],
      "env": {
        "CORDUM_API_KEY": "replace-with-api-key"
      }
    }
  }
}
```

### Claude Code CLI

```bash
claude mcp add cordum -- cordum-mcp --addr http://localhost:8081 --allow-private-gateway
```

### HTTP/SSE test call

```bash
curl -sS -X POST http://localhost:8081/mcp/message \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -d '{"jsonrpc":"2.0","id":1,"method":"ping"}'
```

### TypeScript custom client (HTTP mode)

```ts
const res = await fetch("http://localhost:8081/mcp/message", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-API-Key": process.env.CORDUM_API_KEY ?? "",
    "X-Tenant-ID": "default"
  },
  body: JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "initialize",
    params: { protocolVersion: "2024-11-05" }
  })
});
const payload = await res.json();
console.log(payload);
```

## Dashboard Configuration

**Settings > MCP Server** (`/settings/mcp`) shows the MCP server's state:

- **Status**: running or not, connected clients, uptime, and the configured
  transport, port and whether an API key is required. A warning card appears
  while MCP is disabled.
- **Tools and Resources**: the registered MCP tools (e.g. `cordum_submit_job`)
  and resources (e.g. `cordum://jobs/{id}`), each marked enabled or disabled.
  The lists are read-only; change them with the config API below.
- **Enable runtime / Disable runtime** sets `mcp.enabled`.
- **Copy URL** copies `<mcp.transport>://<dashboard host>:<mcp.port>`, and the
  page shows port 3001 when `mcp.port` is unset. `mcp.port` is metadata only:
  the gateway serves `/mcp/sse` and `/mcp/message` on its own port
  (`http://localhost:8081` in the default stack).

The page has no client config snippets. Create an API key under
**Settings > API Keys** (`/settings/keys`), then configure the client as in
[Integration Examples](#integration-examples), which point `cordum-mcp` at the
gateway with `--addr http://localhost:8081 --allow-private-gateway`.

### API-based Configuration

You can also configure MCP through the config API:

- `GET /api/v1/config` — read current MCP configuration
- `POST /api/v1/config` — set `mcp.enabled`, `mcp.transport`, `mcp.port`, and per-item toggles (`mcp.tools.<name>.enabled`, `mcp.resources.<name>.enabled`)
- `GET /mcp/status` — runtime status (running, connected clients, uptime)

See [Dashboard Guide](dashboard-guide.md#how-to-configure-mcp-server) for the full walkthrough.

## Troubleshooting

- `401 unauthorized` on `/mcp/*`: missing/invalid API key.
- `403 tenant access denied`: tenant header mismatch with auth context.
- `404` on `/mcp/*`: MCP routes disabled (`mcp.enabled=false`) or non-HTTP
  transport.
- `tools/list` or `resources/list` empty: entries are disabled by config
  (`mcp.tools.<tool_id>.enabled=false`,
  `mcp.resources.<resource_name>.enabled=false`).

## MCP Gateway (multi-upstream mode) — EDGE-100 skeleton

EDGE-100 introduces a per-tenant MCP Gateway skeleton at
`/api/v1/mcp/gateway/*`. The gateway sits between MCP clients and upstream
MCP servers while reusing the existing Edge primitives (EdgeSession,
AgentExecution, AgentActionEvent) — no parallel store, no parallel event
bus. This P1 ships disabled-by-default; EDGE-101 will populate the
upstream registry consumed when enabled.

The canonical client attach / upstream-forwarding endpoint is
`POST /api/v1/mcp/gateway/upstream` (for local defaults:
`https://localhost:8081/api/v1/mcp/gateway/upstream`). The shorter
`/api/v1/mcp/gateway` base path is not an attach endpoint or alias.

### Routes

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/api/v1/mcp/gateway/health` | Always 200; body `{status, gateway_enabled, component}`. Never touches the store — safe operator probe even when disabled. |
| `GET` | `/api/v1/mcp/gateway/config` | Returns redacted per-tenant config `{gateway_enabled, upstream_count, upstream_forwarding}`. Never echoes upstream credentials or tokens. |
| `POST` | `/api/v1/mcp/gateway/upstream/*` | 503 always in P1: `gateway_disabled` on every tenant, with zero Edge records, because the per-tenant flag is not read yet (see below). The `no_upstream_configured` branch (flag on but registry empty, with `session_id` + `execution_id` correlation IDs and a persisted `mcp.server.failed` event) is unreachable until EDGE-101. |
| `POST` | `/api/v1/mcp/gateway/clients/connect` | Creates EdgeSession + AgentExecution attributed to the **resolved** tenant + principal — NEVER body claims. Emits `mcp.server.connected` only after the event is durably appended. Storage failures before or during evidence creation return 500 and are logged structurally. |

### Per-tenant enable flag

`MCPPolicy.GatewayEnabled` (`tenants.<tenant>.mcp.gateway_enabled`, defined
in `core/infra/config/mcp.go`) is meant to control the upstream-forwarding
family per tenant. It is reserved and not enforced yet: the gateway
hard-codes the lookup to `false` (TODO EDGE-101 in
`core/controlplane/gateway/gateway.go`), so the upstream route family returns
503 on every tenant whatever the policy says. The same holds for
`tenants.<tenant>.mcp.upstream_servers` and `allowed_upstreams`: the policy
schema accepts them, nothing reads them, and the Safety Kernel logs a warning
when a loaded policy sets any of them. Upstreams are registered through
`/api/v1/edge/mcp/upstreams` and governed by the config-service
`safety.mcp.*` settings described above. Health and config routes remain
reachable regardless so operators can probe a disabled deployment.

### Tenant/principal attribution contract

The gateway resolves tenant + principal via the API gateway's existing
`s.resolveTenant` + `s.requireTenantAccess` + `auth.FromRequest` plumbing.
**Body-claimed tenants are ignored.** The test
`TestMCPGatewayTenantAttribution` posts `{"claimed_tenant":"tenant-spoofed"}`
with `X-Tenant-ID: tenant-a` and asserts the resulting session has
`TenantID = "tenant-a"`. This locks task rail #3 (`All MCP sessions must
be tenant/principal attributed`) at the contract layer.

### Gateway event contract

| Kind | When | Required fields |
| --- | --- | --- |
| `mcp.server.connected` | Successful client connect (session + execution created) | `tenant_id`, `session_id`, `execution_id`, `principal_id` |
| `mcp.server.failed` | Gateway failure after tenant/principal resolution and after a valid EdgeSession + AgentExecution evidence root exists. EDGE-100's concrete P1 case is `GatewayEnabled=true` with no upstream configured; future EDGE-101 upstream handshake failures use the same root. | `tenant_id`, `session_id`, `execution_id`, `principal_id` |

Store/bootstrap failures before session/execution creation cannot be
represented in the Edge event stream because `edge.RedisStore.AppendEvent`
requires an existing AgentExecution. Those failures are logged structurally
and returned as 500 responses; they are not recorded as orphan events.
Failed events deliberately **do not** carry the underlying error string in
their event body, preventing transport-error leakage into the
audit-evidence stream. Operators correlate by timestamp, tenant,
`session_id`, and `execution_id`.

### Migration path

1. Bring up the gateway disabled (default; EDGE-100 ships this).
2. After EDGE-101 lands, set `MCPPolicy.GatewayEnabled = true` per tenant
   and register upstream MCP servers via the upstream registry.
3. EDGE-104 wires real client attach over the upstream registry.
4. EDGE-105 surfaces gateway sessions + events on the Cordum dashboard.

### Construction failure → 503 stub

If the API gateway boots without an `edgeStore` (e.g. unit tests, dev
mode missing Redis), `mcpGatewayHandlers` substitutes a stub gateway
whose four handlers each return 503 `gateway_unavailable`. Routes still
register so the table is consistent across environments; the
misconfiguration surfaces as a logged warning + per-request 503
instead of a missing-route 404.

## Dashboard MCP Lane

The Edge Session detail page (`/edge/sessions/:sessionId`) renders a
dedicated **MCP lane** below the primary hook timeline. The lane
surfaces every MCP-layer event recorded for the session so operators
can audit upstream tool invocations without sifting through the full
event stream.

### What it shows

| Event kind | Lane icon | Category |
| --- | --- | --- |
| `mcp.server.connected` | Plug (cyan) | Servers |
| `mcp.server.failed` | Unplug (red) | Failures |
| `mcp.tool.pre` | Send (cyan) | Tools |
| `mcp.tool.post` | CornerDownLeft (emerald) | Tools |
| `mcp.tool.failed` (or any `mcp.tool.*` with `status=failed`) | AlertOctagon (red) | Failures |
| `approval.requested` (when `layer=mcp`) | Shield (amber) | Approvals |
| `approval.granted` (when `layer=mcp`) | ShieldCheck (emerald) | Approvals |
| `approval.rejected` (when `layer=mcp`) | ShieldOff (red) | Approvals |

Other layers and kinds remain on the existing P0 hook timeline above
the MCP lane; the MCP lane never re-renders or replaces P0 lanes.

### Filter chips

Four chip toggles — **Servers · Tools · Approvals · Failures** — gate
which rows the lane shows. All four chips are active by default. The
chip state persists in the URL via `?mcp_lane=`:

- `?mcp_lane=tools` — show only Tools-category rows.
- `?mcp_lane=tools,approvals` — show Tools and Approvals.
- Default (every chip active) omits the query param.

Invalid tokens (`?mcp_lane=bogus`) are silently dropped; a parse that
yields zero valid chips falls back to the default all-active state so
a typo never traps users in the empty filter state.

### Inspector row-expand

Clicking a row expands an inline inspector body with six fields:

1. **Upstream server** — `event.labels.mcp_server` (with
   `agentProduct` fallback when the label is absent on legacy events).
2. **Tool** — `event.toolName`.
3. **Decision** — coloured `StatusBadge` (ALLOW / DENY /
   REQUIRE_APPROVAL / RECORDED).
4. **Approval** — when `event.approvalRef` is set, a router `Link`
   to `/approvals/<approvalRef>` opens the approval-detail page;
   "—" otherwise.
5. **Args (redacted)** — JSON-stringified
   `event.inputRedacted` after the client-side defense-in-depth
   sanitizer pass.
6. **Result (redacted)** — `event.labels.result_redacted` after the
   sanitizer pass.

When an event carries an artifact pointer, an artifact chip with the
sha256-short and an external link to the artifact-store URI appears
below the six fields. The chip opens in a new tab with
`rel="noopener noreferrer"`.

### Redaction contract

The dashboard trusts server-side redaction: any `<field>_redacted`
key (suffix contract) is rendered verbatim. As a defense-in-depth
layer, the dashboard also runs `sanitizeMCPField` /
`sanitizeMCPPayload` (from `dashboard/src/lib/redaction.ts`) over
every payload before display. If a bare sensitive field name
(`prompt`, `tool_input`, `tool_output`, `result`, `args`, etc.)
slips past the server, the sanitizer replaces its value with
`"[redacted by client sanitizer]"`. The contract memo lives at
`project_edge_redaction_contract`; EDGE-041 tracks the matching
server-side rename.

### Empty state

When a session emits no MCP events, the lane still mounts and
shows a discoverable empty card: muted `Plug` icon plus the
text "No MCP activity recorded for this session". This keeps the
surface visible so operators don't conclude the lane is broken when
a particular session simply did not call any MCP tools.

### Accessibility

- The lane passes the dashboard's strict axe-core gate
  (`runAxe: true` — WCAG 2 A/AA, every impact, all rules enabled
  except the jsdom-incompatible `color-contrast`).
- Reduced motion is honored automatically via the global
  `<MotionConfig reducedMotion="user">` wrapper in `App.tsx`; the
  lane's `motion.li` entries respect `prefers-reduced-motion: reduce`
  without per-component code.
- Filter chips are wrapped in a `role="group"` with an
  `aria-label="MCP lane filters"`; every chip exposes
  `aria-pressed` for the toggle state.

## Cross References

- [API Reference](./api-reference.md)
- [Configuration](./configuration.md)
- [System Overview](./system_overview.md)
