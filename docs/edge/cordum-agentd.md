# cordum-agentd

`cordum-agentd` is the local Cordum Edge session daemon used by the Claude
command hook path. It owns the local EdgeSession/AgentExecution lifecycle,
heartbeat, local hook endpoint, and shutdown evidence. It does **not** create
Cordum Jobs for Claude/tool actions.

`cordum-hook` remains the Claude command hook process. See
[`docs/edge/cordum-hook.md`](./cordum-hook.md) for hook-side stdout/stderr,
fail-mode, and managed-settings behavior. Agentd is the local session/evidence
counterpart that the hook calls.

For the developer wrapper that starts agentd, generates temporary settings,
and launches Claude Code, see [`cordumctl edge claude`](./cordumctl-edge-claude.md).

## Build and run

From the repository root:

```bash
make build SERVICE=cordum-agentd
go run ./cmd/cordum-agentd --gateway http://127.0.0.1:8081 --tenant <tenant-id>
```

Required Gateway credentials:

- `CORDUM_GATEWAY` or `--gateway`
- `CORDUM_API_KEY`
- `CORDUM_TENANT_ID` or `--tenant`

Common options:

| Setting | Purpose |
| --- | --- |
| `CORDUM_EDGE_POLICY_MODE` | `observe`, `enforce`, or `enterprise-strict` |
| `CORDUM_AGENTD_SOCKET` | Local `http://127.0.0.1`/`localhost` hook URL; non-HTTP socket paths are rejected in P0 |
| `CORDUM_AGENTD_HOOK_TIMEOUT` | Local hook/evaluator timeout (positive, bounded) |
| `CORDUM_AGENTD_GATEWAY_TIMEOUT` | Per-call Gateway timeout |
| `CORDUM_EDGE_HEARTBEAT_TTL` | Gateway heartbeat TTL |
| `CORDUM_EDGE_HEARTBEAT_INTERVAL` | Heartbeat interval; must be <= TTL/2 |
| `CORDUM_AGENTD_FAIL_CLOSED` | Treat startup/Gateway failure as fail-closed |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE` | Optional in-memory cache for low-risk Gateway `ALLOW` responses; default off |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL` | Safe-allow cache TTL when enabled |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES` | Safe-allow cache entry cap when enabled |
| `CORDUM_AGENTD_INLINE_APPROVAL_WAIT` | Local/demo-only inline approval wait; default off |
| `CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT` | Strict inline wait timeout; timeout/rejection denies and asks the user to retry |
| `CORDUM_AGENTD_STATE_DIR` | Override state root |
| `CORDUM_AGENTD_NONCE` | Trusted-launcher-only loopback nonce override; see [Trusted launcher override](#trusted-launcher-override-cordum_agentd_nonce) |

## Evaluate, cache, approvals, and fail modes

For each local Claude hook request, agentd forwards the already-redacted and
hashed action summary to Gateway `POST /api/v1/edge/evaluate`. It sends only
bounded metadata such as tenant/principal/session/execution IDs, hook layer and
kind, tool name, action/input hashes, classifier labels/risk tags, and
`input_redacted`. It does **not** send raw `tool_input`, raw prompts, raw
transcripts, authorization headers, local transcript paths, or model-provider
secrets.

`input_redacted` field-name convention (EDGE-041): every Claude `tool_input`
field name is renamed with a `_redacted` suffix on the wire — `command` →
`command_redacted`, `file_path` → `file_path_redacted`, `old_string` →
`old_string_redacted`, etc. PostToolUse adds `tool_response_redacted` and
`error_redacted`; UserPromptSubmit emits `prompt_redacted`. Unknown / version-
drifted Claude tool fields are bucketed under `tool_input_redacted` so
evidence never silently drops content. The suffix is the wire signal that
`edge.RedactValue` (EDGE-004) has already scrubbed each value; the dashboard
sanitizer (`dashboard/src/api/transform.ts isUnsafeEdgeKey`) trusts only
suffixed keys and strips bare ones as defense-in-depth.

Gateway decisions map to the hook result as follows:

- `ALLOW` returns a quiet allow so safe actions are not noisy.
- `DENY`, `THROTTLE`, malformed responses, and fail-closed degraded paths return
  concise deny copy. The action is not run.
- `CONSTRAIN` returns allow with `updated_input` from Gateway.
- `REQUIRE_APPROVAL` defaults to an immediate retry flow: agentd returns the
  `approval_ref`, approval URL/context when available, and guidance to approve
  then retry the same tool call. P0 does not rely on Claude interactive defer
  semantics.

Inline approval wait is intentionally opt-in and local/demo-oriented. It is
enabled only when `CORDUM_AGENTD_INLINE_APPROVAL_WAIT=true`; agentd then calls
`POST /api/v1/edge/approvals/{approval_ref}/wait` with
`CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT`. Approval allows the action,
optional reviewer-updated input is forwarded, and rejection/timeout/Gateway wait
errors return `DENY` with retry guidance. Approval-derived allows are never
stored in the safe allow cache.

The safe allow cache is disabled by default. When explicitly enabled, it is
bounded in memory by TTL and max entries, keyed by tenant, policy mode, the
global `policy_snapshot`, workflow/job override snapshots, action
kind/capability/risk, action hash, and input hash. It
stores only minimal sanitized allow metadata. It never stores raw payloads,
tokens, approval references, reviewer-updated inputs, degraded results, high-risk
actions, unknown actions, or decisions from a different policy snapshot, scoped
override snapshot, or mode.

Concurrent identical hook requests are coalesced with singleflight using the
same deterministic decision identity: tenant, policy mode, `policy_snapshot`,
workflow/job override snapshots, action hash, input hash, action
kind/capability, normalized command labels, and sorted risk tags. For a fully
identified logical action, only one Gateway
evaluate call and one decision-evidence event row are produced even when many
callers arrive at the same time; followers receive the leader's hook decision.
Gateway error/degraded paths do not poison the singleflight slot: once the leader
finishes, the next caller with the same key issues a fresh Gateway evaluate call.
Requests missing stable tenant/policy/hash identity skip coalescing rather than
sharing a decision too broadly.

Gateway outage behavior follows the PRD modes:

- `observe`: allow degraded and write evidence.
- `enforce`: allow only locally known-safe actions during a degraded miss; risky
  or unknown actions deny/fail closed.
- `enterprise-strict`: deny/fail closed when Cordum governance is unavailable.
- Workflow actions tagged `requires-edge-governance` fail closed on a Gateway
  miss even if the session policy mode is observe.

Agentd records hook/evaluate/decision/degraded evidence using Edge session/action
events and the shared observability recorder when supplied. Hook events are
atomic: the receipt event and decision-evidence event are committed via a single
Gateway `/api/v1/edge/events/batch` call after the evaluator returns. Either
both events appear in the session log, or neither does; there is no half-written
audit record across shutdown or transient errors. Receipt timestamps reflect
actual hook receipt time, not batch commit time. Non-hook evidence writes and
metrics/audit emission remain best-effort: upload failures are recorded as
degraded where possible and do not rewrite a fresh Gateway decision.

Agentd's evidence-event id is in a DISTINCT namespace from the gateway's
authoritative `event_id` — agentd records carry an `agentd-`-prefixed id
and link to the gateway record via `parent_event_id`. The dual-witness
audit invariant is: exactly one gateway record + AT MOST one agentd
evidence record per decision. See
[Edge identity contract](identity-contract.md) for the full ID-namespace
+ approval-lifecycle reference.

## State persistence

By default, agentd stores session state under:

```text
~/.cordum/edge/sessions/<session_id>/state.json
```

`CORDUM_AGENTD_STATE_DIR` can override the root directory. The state file is
written atomically with a temp file + rename. On Unix-like platforms, agentd
creates the session directory with `0700` and the state file with `0600`; this
is the correct Unix permission model.

On Windows, agentd applies an explicit DACL granting Full Control to the file
owner and `SYSTEM` only on the state root, per-session state directories, and
`state.json` files. Startup verifies the state root DACL before hardening it:
by default, a broader inherited ACL produces a single `slog` warning and agentd
continues after applying the owner-only DACL. Set
`CORDUM_AGENTD_STRICT_PERMS=1` to fail closed instead when the configured state
directory's ACL is broader than owner-only. Recommended custom state-dir paths
stay inside the user's profile, for example `%LOCALAPPDATA%\cordum\edge`
(normally owner-only by default). Avoid shared roots such as
`C:\ProgramData\...`, whose defaults often grant `Authenticated Users:Read`,
unless strict-permissions startup checks pass.

Persisted state is intentionally small:

- `session_id`, `execution_id`, `trace_id`
- `tenant_id`, `principal_id`
- `policy_snapshot`, `policy_mode`, `dashboard_url`
- local hook bind metadata
- start/end timestamps and degraded/pending-shutdown flags
- non-secret metadata such as cwd/repo/git identifiers

Agentd must never persist `CORDUM_API_KEY`, model-provider secrets, hook nonces,
raw Claude hook payloads, raw transcripts, or authorization headers. Secret-like
metadata keys are dropped before state is written.

## Local transport note

The P0 implementation defaults to a local-only hook endpoint:

```text
http://127.0.0.1:8765/v1/edge/hooks/claude
```

Loopback transport requires a high-entropy per-session nonce. Hook-to-agentd
authentication uses `CORDUM_AGENTD_HOOK_NONCE` in the `cordum-hook` process
environment and sends it as the `X-Cordum-Agentd-Nonce` request header. The
nonce is **never** embedded in `CORDUM_AGENTD_URL`, generated Claude settings,
managed-settings JSON, or persisted agentd state. Header-only authentication is
the only supported loopback nonce delivery path; the legacy `?nonce=`
query-parameter path was removed in `EDGE-017.4.1`. Broad or remote binds such
as `0.0.0.0` are rejected.

P0 does **not** start a Unix socket or Windows named-pipe listener. If
`CORDUM_AGENTD_SOCKET` is set to a non-HTTP path such as
`/tmp/cordum-agentd.sock`, startup fails instead of silently running without a
hook listener. Enterprise deployments should prefer a user-owned
socket/named-pipe transport once that listener is implemented; until then the
local-dev loopback endpoint is local-only and nonce guarded. The nonce is
process-local and must not be written into generated Claude settings or
persisted state.

### Trusted launcher override (`CORDUM_AGENTD_NONCE`)

A trusted launcher may pre-seed the loopback nonce by generating at least 32
bytes of entropy and exporting it as base64 in `CORDUM_AGENTD_NONCE` for the
`cordum-agentd` subprocess:

```bash
export CORDUM_AGENTD_NONCE="$(openssl rand -base64 32)"
```

Equivalent `crypto/rand` generation is acceptable. Agentd validates that the
value base64-decodes to at least 32 raw bytes and refuses to start on malformed
or too-short values; it does not silently fall back to auto-generation. The
trusted launcher then gives `cordum-hook` the matching value only through the
runtime process environment as `CORDUM_AGENTD_HOOK_NONCE`; `CORDUM_AGENTD_URL`
must remain the bare loopback endpoint without `?nonce=`.

Security constraints:

- agentd never logs the nonce value and never includes it in HTTP responses;
- agentd never writes the nonce to `state.json`, the state directory, audit
  events, metrics labels, or evidence exports;
- the value MUST NOT appear in generated Claude settings files or any other
  persistent user-editable location;
- process-listing exposure (`ps`, `/proc/<pid>/environ`, platform process
  inspectors) is a known local-development tradeoff and is covered by
  [ADR-010's token-storage decision](../adr/010-edge-p0-architecture-decisions.md#security-token-storage-and-product-scope).

This override is used by the EDGE-027 fake-hook E2E and the
[`cordumctl edge claude`](./cordumctl-edge-claude.md) wrapper. It is not
production enterprise enforcement without managed settings plus
sealed-process/keychain/service-bootstrap protections.

See [`LOCAL_E2E.md` § Edge fake-hook E2E](../LOCAL_E2E.md#edge-fake-hook-e2e)
for a CI-safe end-to-end exerciser of the Gateway side of this contract.

## Heartbeat, degraded state, and shutdown

After session registration, agentd heartbeats the EdgeSession at an interval no
greater than half the configured TTL. Heartbeats do not overlap: if a previous
heartbeat is still in flight, the next tick is skipped rather than creating a
pile-up.

Consecutive Gateway failures mark persisted local status degraded and, when the
Gateway is reachable for evidence writes, emit a session-degraded event. In
`enterprise-strict`/fail-closed mode, repeated heartbeat or startup failures are
reported as fail-closed instead of silently allowing the session to proceed.
State persistence failures are returned as runtime errors instead of being
reported as success with stale or missing local evidence.

On SIGINT/SIGTERM or context cancellation, agentd:

1. stops accepting hook requests,
2. stops heartbeat,
3. sends execution end,
4. sends session end,
5. writes final local state.

Shutdown drainage order: heartbeat goroutines are drained with a bounded
`service.Wait()` before `SessionManager.Shutdown` writes the terminal state.
`RecordHeartbeatStatus` checks the manager shutdown flag and returns early once
shutdown begins, so heartbeat callbacks cannot overwrite the terminal session
state. Final on-disk state is always the terminal status (`ended`/`failed`).

If the Gateway is unreachable during shutdown, agentd records failed/degraded
local state with `pending_gateway_end=true` so a future doctor/retry flow can
reconcile evidence. It does not delete local evidence or mark a false success.

## Current P0 boundary

Agentd is the local Edge session/action/evidence path for Claude Code. Claude
tool actions are represented as `EdgeSession -> AgentExecution ->
AgentActionEvent` evidence plus audit/artifact pointers. They are **not** Cordum
Jobs unless a real production workflow/job already exists and the Edge execution
links to that job/workflow. Raw hook payloads and raw tool inputs are not
persisted; only redacted summaries, hashes, and artifact pointers cross the
local process boundary.
