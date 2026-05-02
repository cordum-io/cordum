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

## Evaluate, cache, approvals, and fail modes

For each local Claude hook request, agentd forwards the already-redacted and
hashed action summary to Gateway `POST /api/v1/edge/evaluate`. It sends only
bounded metadata such as tenant/principal/session/execution IDs, hook layer and
kind, tool name, action/input hashes, classifier labels/risk tags, and
`input_redacted`. It does **not** send raw `tool_input`, raw prompts, raw
transcripts, authorization headers, local transcript paths, or model-provider
secrets.

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
bounded in memory by TTL and max entries, keyed by tenant, policy mode,
`policy_snapshot`, action kind/capability/risk, action hash, and input hash. It
stores only minimal sanitized allow metadata. It never stores raw payloads,
tokens, approval references, reviewer-updated inputs, degraded results, high-risk
actions, unknown actions, or decisions from a different policy snapshot/mode.

Gateway outage behavior follows the PRD modes:

- `observe`: allow degraded and write evidence.
- `enforce`: allow only locally known-safe actions during a degraded miss; risky
  or unknown actions deny/fail closed.
- `enterprise-strict`: deny/fail closed when Cordum governance is unavailable.
- Workflow actions tagged `requires-edge-governance` fail closed on a Gateway
  miss even if the session policy mode is observe.

Agentd records hook/evaluate/decision/degraded evidence using Edge session/action
events and the shared observability recorder when supplied. Evidence writes and
metrics/audit emission are best-effort: failure to upload evidence is recorded as
degraded but does not change a fresh Gateway decision.

## State persistence

By default, agentd stores session state under:

```text
~/.cordum/edge/sessions/<session_id>/state.json
```

`CORDUM_AGENTD_STATE_DIR` can override the root directory. The state file is
written atomically with a temp file + rename. On Unix-like platforms, agentd
creates the session directory with `0700` and the state file with `0600`. On
Windows, agentd uses the best permissions exposed by the Go runtime and does
not claim Unix mode semantics.

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
