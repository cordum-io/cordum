# cordum-agentd

`cordum-agentd` is the local Cordum Edge session daemon used by the Claude
command hook path. It owns the local EdgeSession/AgentExecution lifecycle,
heartbeat, local hook endpoint, and shutdown evidence. It does **not** create
Cordum Jobs for Claude/tool actions.

`cordum-hook` remains the Claude command hook process. See
[`docs/edge/cordum-hook.md`](./cordum-hook.md) for hook-side stdout/stderr,
fail-mode, and managed-settings behavior. Agentd is the local session/evidence
counterpart that the hook calls.

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
| `CORDUM_AGENTD_SOCKET` | User-local socket path or local loopback URL |
| `CORDUM_AGENTD_HOOK_TIMEOUT` | Local hook/evaluator timeout (positive, bounded) |
| `CORDUM_AGENTD_GATEWAY_TIMEOUT` | Per-call Gateway timeout |
| `CORDUM_EDGE_HEARTBEAT_TTL` | Gateway heartbeat TTL |
| `CORDUM_EDGE_HEARTBEAT_INTERVAL` | Heartbeat interval; must be <= TTL/2 |
| `CORDUM_AGENTD_FAIL_CLOSED` | Treat startup/Gateway failure as fail-closed |
| `CORDUM_AGENTD_STATE_DIR` | Override state root |

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
- local socket path / bind metadata
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

Loopback fallback requires a high-entropy per-session nonce. Agentd accepts the
nonce either in `X-Cordum-Agentd-Nonce` or as a `?nonce=` query parameter so the
existing `cordum-hook` HTTP client can use a configured local URL without a
shared-code change. Broad or remote binds such as `0.0.0.0` are rejected. Unix
socket directory preparation uses user-only permissions where supported.

Enterprise deployments should prefer a user-owned socket/named-pipe transport
when available. The local-dev loopback fallback is local-only and nonce guarded;
the nonce is process-local and must not be written into generated Claude
settings or persisted state.

## Heartbeat, degraded state, and shutdown

After session registration, agentd heartbeats the EdgeSession at an interval no
greater than half the configured TTL. Heartbeats do not overlap: if a previous
heartbeat is still in flight, the next tick is skipped rather than creating a
pile-up.

Consecutive Gateway failures mark local status degraded. In
`enterprise-strict`/fail-closed mode, repeated heartbeat or startup failures are
reported as fail-closed instead of silently allowing the session to proceed.

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

Until EDGE-018 wires the full evaluate/cache/approval path, the local hook
endpoint records bounded hook evidence and returns an explicit not-ready `deny`
decision. Raw hook payloads and raw tool inputs are not persisted; only
redacted summaries and hashes cross the local process boundary.
