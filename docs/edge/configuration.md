# Edge configuration

This page covers the environment variables used by Cordum Edge P0: Gateway
credentials, Claude hook/agentd local transport, policy modes, approvals,
artifacts, and state security. Use it with the CLI guide in [cli.md](cli.md) and
the hook/agentd contracts in [cordum-hook.md](cordum-hook.md) and
[cordum-agentd.md](cordum-agentd.md).

## Security rules for configuration

- Treat `CORDUM_API_KEY`, `CORDUM_AGENTD_NONCE`, and
  `CORDUM_AGENTD_HOOK_NONCE` as secrets.
- Generated Claude settings and enterprise managed-settings JSON must never
  persist API keys, hook nonces, bearer tokens, raw prompts, raw tool payloads,
  transcripts, command output, or signed URLs.
- Keep `CORDUM_AGENTD_URL` and `CORDUM_AGENTD_SOCKET` loopback-only. Do not point
  `cordum-hook` at Gateway or any remote host.
- The developer wrapper is a convenience launcher. Enterprise enforcement still
  requires managed settings, endpoint controls, and service-bootstrap/keychain
  secret handling.

## Required Gateway identity

| Variable | Default | Required by | Security notes |
| --- | --- | --- | --- |
| `CORDUM_GATEWAY` | `cordumctl` defaults to `http://localhost:8081`; raw `cordum-agentd` requires a value. | `cordumctl edge claude`, `cordum-agentd` | Use HTTPS outside local dev. Do not include credentials in the URL. |
| `CORDUM_API_KEY` | none | `cordumctl edge claude`, `cordum-agentd` | Secret. Passed to agentd for Gateway calls; never written to generated Claude settings, logs, dashboard evidence, or export bundles. |
| `CORDUM_TENANT_ID` | `cordumctl` defaults to `default`; raw `cordum-agentd` requires a value. | Gateway auth, agentd, generated hook env | Must match the tenant header used by Edge APIs. |
| `CORDUM_PRINCIPAL_ID` | launcher-detected principal when available | agentd session metadata | Preferred explicit principal for audit evidence. |
| `CORDUM_EDGE_PRINCIPAL_ID` | falls back to `CORDUM_PRINCIPAL_ID` or launcher metadata | generated hook env | Hook-side principal correlation; use a stable non-secret user/service ID. |

`cordumctl edge claude` validates gateway, API key, tenant, and principal before
starting agentd. `cordum-agentd` also fails startup if Gateway credentials are
missing.

## Local hook transport and nonce

| Variable | Default | Used by | Notes |
| --- | --- | --- | --- |
| `CORDUM_AGENTD_SOCKET` | `http://127.0.0.1:8765/v1/edge/hooks/claude` for raw agentd; wrapper normally chooses a free loopback port. | `cordum-agentd` bind URL | P0 accepts HTTP loopback only. Non-HTTP socket paths fail fast. |
| `CORDUM_AGENTD_URL` | same hook path, bare URL | `cordum-hook` client env/generated settings | Must be loopback and must not contain `?nonce=`. Remote hosts are rejected. |
| `CORDUM_AGENTD_NONCE` | empty means agentd auto-generates; wrapper pre-seeds a random value. | trusted launcher -> `cordum-agentd` | Secret. If set, must be base64 that decodes to at least 32 bytes. Never log or persist. |
| `CORDUM_AGENTD_HOOK_NONCE` | none | trusted launcher -> `cordum-hook` process env | Secret. Hook sends it as `X-Cordum-Agentd-Nonce`; generated settings and managed settings must not include it. |

Nonce split: `CORDUM_AGENTD_NONCE` configures the daemon, and
`CORDUM_AGENTD_HOOK_NONCE` configures the hook process. They carry the same
value only in memory/process environments. Header-based nonce auth is the only
supported P0 delivery path; URL query-string nonces are refused.

## Hook and Gateway timeouts

| Variable | Default | Bounds | Notes |
| --- | --- | --- | --- |
| `CORDUM_AGENTD_HOOK_TIMEOUT` | generated Claude settings use `4.5s`; raw agentd internal default is `5s` | hook values must be `<5s`; agentd durations must be `>0` and `<=5m` | Controls the Claude hook wall-clock budget and agentd evaluator budget. Keep generated hook values below Claude Code's 5s command-hook deadline. |
| `CORDUM_AGENTD_GATEWAY_TIMEOUT` | `10s` | `>0` and `<=5m` | Per-call Gateway lifecycle/evaluate/approval/evidence timeout. |
| `CORDUM_EDGE_HEARTBEAT_TTL` | `30s` | `>0` and `<=5m` | Gateway heartbeat TTL for active Edge sessions. |
| `CORDUM_EDGE_HEARTBEAT_INTERVAL` | `TTL/2` | `>0`, `<=5m`, and `<= TTL/2` | Agentd heartbeat cadence. |
| `CORDUM_HOOK_MAX_INPUT_BYTES` | `1048576` | values above 8 MiB ignored by hook | Max stdin JSON payload for `cordum-hook`. Oversize payloads fail closed with redacted stderr. |

Do not copy Claude's command-hook `"timeout": 5` value into
`CORDUM_AGENTD_HOOK_TIMEOUT`; the environment value must leave response-write
reserve below Claude's 5s deadline.

## Policy and fail modes

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_EDGE_POLICY_MODE` | raw agentd `observe`; `cordumctl edge claude` defaults to `enforce` unless overridden by `--policy-mode`/env | Values: `observe`, `enforce`, `enterprise-strict`. Drives Gateway evaluate fail-mode behavior. |
| `CORDUM_EDGE_MODE` | generated settings mirror the selected policy mode | Hook-side mode for local/dev settings. Enterprise templates set `enterprise-strict`. |
| `CORDUM_AGENTD_FAIL_CLOSED` | `false` | When true, hook/agentd fail closed if local governance cannot start or respond safely. |
| `CORDUM_AGENTD_INLINE_APPROVAL_WAIT` | `false`; wrapper enables it for local/demo sessions | Local/demo-only inline wait for `REQUIRE_APPROVAL`; enterprise UX should not rely on interactive defer semantics. |
| `CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT` | `30s` | Strict wait budget. Rejection, timeout, pending, or wait errors deny and require retry. |

Policy-mode summary:

- `observe`: allow degraded actions and record evidence where possible.
- `enforce`: allow only known-safe actions during degraded misses; risky or
  unknown actions deny/fail closed.
- `enterprise-strict`: fail closed when Cordum governance is unavailable.
- Workflow actions tagged `requires-edge-governance` fail closed on Gateway miss
  regardless of session mode.

## Safe allow cache

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE` | `false` | Optional local in-memory cache for low-risk, cache-eligible Gateway `ALLOW` responses. |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL` | `5m` | Used only when cache is enabled. |
| `CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES` | `128` | Must be `>0` and `<=10000` when enabled. |

The cache never stores raw payloads, tokens, approval refs, reviewer-updated
input, degraded results, high-risk/unknown actions, or decisions from another
policy snapshot/mode.

## Local state and Windows ACLs

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_AGENTD_STATE_DIR` | `~/.cordum/edge/sessions` or temp fallback | Stores small non-secret session state. Do not use shared directories. |
| `CORDUM_AGENTD_STRICT_PERMS` | `false` | Windows-only hardening switch. When `1`/`true`, agentd fails closed if the state root ACL is broader than owner/SYSTEM-only before hardening. |

State files must not contain `CORDUM_API_KEY`, model-provider secrets, hook
nonces, raw hook payloads, raw transcripts, or authorization headers.

## Generated settings metadata

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_EDGE_SESSION_ID` | generated by agentd/wrapper | Correlates generated Claude settings with the Edge session. |
| `CORDUM_EDGE_EXECUTION_ID` | generated by agentd/wrapper | Correlates hook actions with the Edge execution. |
| `CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT` | wrapper approval wait timeout | Settings metadata for approval wait UX; agentd runtime uses `CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT`. |
| `CORDUM_EDGE_PLATFORM` | wrapper/runtime OS | Diagnostic marker only. |
| `CORDUM_HOOK_COMMAND` | `cordum-hook` | Hook command path used by generated settings. |

These metadata values are not secrets, but they still should be bounded and kept
out of metrics labels when high cardinality would create noise.

## Evidence export

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_EDGE_EXPORT_MAX_BYTES` | `10485760` (10 MiB) | Maximum serialized `POST /api/v1/edge/sessions/{session_id}/export` response. Values outside the allowed range fall back to the default. |

P0 evidence exports include session/execution/event data and artifact pointer
metadata. They do not inline raw artifact bodies.

## Retention and write-side caps

| Variable | Default | Notes |
| --- | --- | --- |
| `CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION` | `100` | Maximum number of `AgentExecution` rows that may be recorded under a single `EdgeSession`. The Gateway counts executions before each `POST /api/v1/edge/executions` and rejects the call with `429 max_executions_exceeded` once the cap is reached. Missing/invalid/non-positive values fall back to the default. |
| `CORDUM_EDGE_SESSION_RETENTION_TTL` | `720h` (30 days) | Sessions older than this are eligible for the Gateway retention sweeper. Explicit values must parse as positive Go durations; `0`, negative, or invalid values fail startup. |
| `CORDUM_EDGE_SESSION_SWEEP_INTERVAL` | `1h` | Sweep cadence after the initial Gateway boot sweep. Explicit values must parse as positive Go durations. |

The execution cap and the store-level 5000 events-per-execution cap protect
against pathological retry storms or runaway loops fanning a single session out
to unbounded Redis cleanup work. The cap is the maximum number of *stored*
executions for a session — to record more executions, end the current session
and start a new one.

The error envelope on rejection carries `details.limit` (the active cap) and
`details.current` (the count observed at rejection time) so clients can render
a helpful message and operators can correlate logs.

For the full retention policy, cleanup deadline, sweeper behavior, and metrics,
see [retention, caps, and cleanup](retention.md).

## Related docs

- [CLI guide](cli.md)
- [Manual demo](demo.md)
- [Edge API](api.md)
- [cordum-hook](cordum-hook.md)
- [cordum-agentd](cordum-agentd.md)
- [Managed settings template](managed-settings-template.md)
