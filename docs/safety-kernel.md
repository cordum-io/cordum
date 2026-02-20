# Safety Kernel Reference

This document describes Safety Kernel behavior from code in:

- `core/controlplane/safetykernel/kernel.go`
- `core/controlplane/safetykernel/output_policy.go`
- `core/controlplane/safetykernel/scanners.go`
- `core/infra/config/safety_policy.go`
- `core/controlplane/scheduler/safety_client.go`
- `core/controlplane/gateway/gateway_jobs.go`

## 1. Overview

Safety Kernel is the policy decision point for Cordum:

- Input policy is evaluated before scheduler dispatch.
- Output policy is evaluated through `OutputPolicyService.CheckOutput`.
- Policy can be sourced from file or URL, merged with config-service fragments, and hot-reloaded.

The scheduler treats Safety Kernel as part of the hot path and uses short client timeouts (`2s`) plus a circuit breaker to protect throughput.

## 2. Input Policy Rules

Input rule model is defined in `core/infra/config/safety_policy.go` under `rules[]`.

Rule matching supports:

- `tenants`
- `topics` (glob match via `path.Match`, for example `job.*`)
- `capabilities`
- `risk_tags`
- `requires` (all required entries must be present)
- `pack_ids`
- `actor_ids`
- `actor_types`
- `labels`
- `secrets_present`
- `mcp` (server/tool/resource/action)

Decisions are normalized to:

- `allow`
- `deny`
- `require_approval`
- `throttle`
- `allow_with_constraints`

If constraints are present, response can be `ALLOW_WITH_CONSTRAINTS` even when decision is `allow`.

Approval binding behavior:

- `approval_required` is true for `require_approval`.
- `approval_ref` is set to the incoming `job_id`.

## 3. MCP Label Filtering

MCP request context is extracted from job labels:

- `mcp.server` | `mcp_server` | `mcpServer`
- `mcp.tool` | `mcp_tool` | `mcpTool`
- `mcp.resource` | `mcp_resource` | `mcpResource`
- `mcp.action` | `mcp_action` | `mcpAction` (normalized to lowercase)

MCP policy fields:

- `allow_servers`, `deny_servers`
- `allow_tools`, `deny_tools`
- `allow_resources`, `deny_resources`
- `allow_actions`, `deny_actions`

Evaluation order:

1. Rule-level `match.mcp` (if present)
2. Tenant-level `tenants.<tenant>.mcp`
3. Effective runtime safety overlay (`CORDUM_EFFECTIVE_CONFIG` -> `safety.mcp`)

Within each MCP field, deny takes precedence:

1. If value is in deny list -> deny
2. Else if allow list is non-empty and value is not in allow list -> deny
3. Else -> allow

MCP list matching is case-insensitive exact match. Topic matching supports glob patterns; MCP fields do not currently use glob pattern matching.

Example:

```yaml
tenants:
  default:
    mcp:
      allow_servers: ["github", "jira"]
      deny_servers: ["internal-admin"]
      allow_tools: ["search_issues", "get_issue"]
      deny_tools: ["delete_issue"]
      allow_resources: []
      deny_resources: ["repo://secret/*"]
      allow_actions: ["read", "list"]
      deny_actions: ["write", "delete"]
```

## 4. Policy Overlay System

Policy source selection:

- `SAFETY_POLICY_URL` (if set) overrides `SAFETY_POLICY_PATH`.
- If neither is set, loader can still use config-service fragments.

Config-service fragment loading:

- Controlled by:
  - `SAFETY_POLICY_CONFIG_SCOPE` (default `system`)
  - `SAFETY_POLICY_CONFIG_ID` (default `policy`)
  - `SAFETY_POLICY_CONFIG_KEY` (default `bundles`)
  - `SAFETY_POLICY_CONFIG_DISABLE` (disable fragment loading when set)
- Fragments are loaded from config service, sorted by key, and merged deterministically.
- Fragment entries may include `enabled: false` and are skipped when disabled.

Merge order:

```text
base policy (file/url)
  -> config-service fragments (enabled bundles)
    -> request effective config restrictions (topics + MCP) at evaluation time
```

Reload behavior:

- Watch interval defaults to `30s`.
- Override with `SAFETY_POLICY_RELOAD_INTERVAL` (duration string, for example `10s`, `1m`).
- When snapshot changes, in-memory policy is replaced and recent snapshots are tracked.
- Snapshot history is stored in Redis list `cordum:safety:snapshots` (LPUSH + LTRIM to 10). All replicas share a single history, so the `ListSnapshots` gRPC call returns consistent results regardless of which replica handles it. If Redis is unavailable, the safety kernel falls back to a per-process in-memory list.

## 5. Decision Cache

Safety decision cache is controlled by:

- `SAFETY_DECISION_CACHE_TTL`
- `SAFETY_DECISION_CACHE_MAX_SIZE` (default `10000`)

Cache key:

- Deterministic protobuf marshal of `PolicyCheckRequest`
- `job_id` cleared before hashing (enables reuse across different jobs with same policy-relevant input)
- Snapshot-prefixed key: `<snapshot>:<sha256(request)>`

Cache semantics:

- Cached responses omit `approval_ref` at rest.
- On cache hit, `approval_ref` is re-bound to current `job_id` when approval is required.
- Eviction first removes expired entries, then evicts the entry closest to expiration when still over capacity.

Reload invalidation:

- Snapshot is part of the key, so policy reload naturally causes misses against old snapshot keys.
- Additionally, a `policyVersion` counter (atomic uint64) tags every cache entry with the version active when it was created.
- When `setPolicy()` is called, the version counter increments and the entire cache is cleared immediately.
- On cache lookup, if the entry's `policyVersion` does not match the current version, the entry is treated as a miss and deleted — this is a belt-and-suspenders guard in addition to the cache clear.
- In multi-replica deployments, each replica independently invalidates its cache when it receives the policy update (e.g., via NATS config notification or file watcher). No Redis is involved — cache management is purely local per-replica.

## 6. Policy Signature Verification (Ed25519)

Verification inputs:

- `SAFETY_POLICY_PUBLIC_KEY` (base64 or hex raw Ed25519 public key, 32 bytes)
- Signature from one of:
  - `SAFETY_POLICY_SIGNATURE` (base64 or hex)
  - `SAFETY_POLICY_SIGNATURE_PATH` (raw signature bytes)
  - `<policy-file>.sig` fallback for file-based policy source

When signature is required:

- Always in production (`CORDUM_ENV=production` or `CORDUM_PRODUCTION=true`)
- Or when `SAFETY_POLICY_SIGNATURE_REQUIRED=true`

Failure conditions include missing public key, invalid key/signature length, or verification failure.

Minimal signing flow (Go):

```go
// sign_policy.go
// go run sign_policy.go policy.yaml private.key > policy.sig.b64
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	policy, _ := os.ReadFile(os.Args[1])
	priv, _ := os.ReadFile(os.Args[2]) // raw 64-byte ed25519 private key
	sig := ed25519.Sign(ed25519.PrivateKey(priv), policy)
	fmt.Println(base64.StdEncoding.EncodeToString(sig))
}
```

Key rotation procedure:

1. Generate a new keypair and distribute the new public key.
2. Re-sign active policy bundles with the new private key.
3. Roll `SAFETY_POLICY_PUBLIC_KEY` and signature together.
4. Keep old key only for rollback window; remove after cutover validation.

## 7. Remediations

Policy rules can return remediations:

- `id`
- `title`
- `summary`
- `replacement_topic`
- `replacement_capability`
- `add_labels`
- `remove_labels`

Remediations are returned in `PolicyCheckResponse.remediations` and persisted with job safety records.

Apply remediation endpoint:

- `POST /api/v1/jobs/{id}/remediate`
- Requires `admin` role and tenant access
- Request body: `{"remediation_id":"<id>"}` (required when multiple remediations exist)

Replacement semantics in gateway:

- New job is cloned from original request.
- `replacement_topic` overrides `topic` if provided.
- `replacement_capability` overrides `meta.capability` if provided.
- Labels are rewritten:
  - add `remediation_of` and `remediation_id`
  - apply `add_labels`
  - remove `remove_labels`

## 8. gRPC Services and TLS

Safety Kernel server implements:

- `SafetyKernelServer`
  - `Check()`
  - `Evaluate()`
  - `Explain()`
  - `Simulate()`
  - `ListSnapshots()`
- `OutputPolicyServiceServer`
  - `CheckOutput()`

`Check/Evaluate/Explain/Simulate` share the same evaluation path (`evaluate(...)` in `kernel.go`).

TLS for Safety Kernel server:

- `SAFETY_KERNEL_TLS_CERT`
- `SAFETY_KERNEL_TLS_KEY`
- Production requires server TLS cert/key.
- Minimum TLS version is controlled by `CORDUM_TLS_MIN_VERSION` (defaults to TLS 1.3 in production, TLS 1.2 otherwise).

TLS for clients (scheduler/gateway dialing Safety Kernel):

- `SAFETY_KERNEL_TLS_CA`
- `SAFETY_KERNEL_TLS_REQUIRED`
- `SAFETY_KERNEL_INSECURE` (for non-production/testing)

## 9. Distributed Circuit Breakers (Safety Client)

Both the input safety client (`SafetyClient`) and output safety client (`OutputSafetyClient`) use a Redis-backed distributed circuit breaker (`RedisCircuitBreaker` in `core/controlplane/scheduler/circuit_breaker.go`). When one scheduler replica detects safety kernel failures, all replicas see the open circuit immediately via shared Redis state.

### State Machine

```text
CLOSED --(3 failures)--> OPEN --(30s TTL expires)--> HALF_OPEN
HALF_OPEN --(2 successes)--> CLOSED
HALF_OPEN --(failure)------> OPEN
```

### Redis Keys

| Circuit | Key Pattern | Purpose |
|---------|-------------|---------|
| Input safety | `cordum:cb:safety:failures` | Shared failure counter for `SafetyClient.Check()` |
| Output safety | `cordum:cb:safety:output:failures` | Shared failure counter for `OutputSafetyClient.EvaluateOutput()` |

### How It Works

- **Failure recording**: Atomic Lua script (`INCR` + `EXPIRE`) increments the failure counter and sets a TTL equal to the open duration on first failure.
- **Open detection**: `GET` on the failures key — if count >= threshold, circuit is open.
- **Half-open transition**: When the TTL expires, the failures key is deleted by Redis. The next `IsOpen()` check returns false, allowing a probe request through.
- **Success recording**: `DEL` on the failures key resets the counter, closing the circuit.
- **Local fallback**: If Redis is unavailable, the circuit breaker falls back to a per-replica in-memory state machine with the same thresholds. This is fail-open — requests are allowed through to avoid blocking jobs when Redis is down.

### Constants

| Parameter | Input Safety | Output Safety |
|-----------|-------------|--------------|
| Request timeout | `2s` | `100ms` (meta), `30s` (content) |
| Open duration | `30s` | `30s` |
| Fail budget to open | `3` | `3` |
| Half-open max probes | `3` | `3` |
| Successes to close | `2` | `2` |

### Wiring

In `cmd/cordum-scheduler/main.go`:
- `SafetyClient` is created with a local-only breaker, then upgraded to Redis-backed via `safetyClient.WithRedis(sagaRedis)`.
- `OutputSafetyClient` uses its internal Redis connection (`resultClient`) for the distributed breaker automatically.

When the input circuit is open, the scheduler receives `SafetyUnavailable` decisions instead of blocking on RPC. The input fail mode (`POLICY_CHECK_FAIL_MODE`) then determines whether the job is requeued or allowed through.

## 10. Input Policy Fail Mode

When the safety kernel is unreachable during pre-dispatch policy checks, the scheduler's behavior is controlled by the `POLICY_CHECK_FAIL_MODE` setting:

| Mode | Behavior | Risk |
|------|----------|------|
| `closed` (default) | Job is requeued with exponential backoff until the safety kernel recovers | No unsafe jobs pass through; availability impact during outages |
| `open` | Job is allowed through with a warning log and metric increment | Jobs bypass safety checks; use only when availability is prioritized over safety |

**Risk implications of fail-open**: In `open` mode, jobs that would normally be denied or require approval are allowed through without evaluation. This should only be used in environments where safety violations are tolerable (e.g., staging) or where compensating controls exist downstream. Production deployments should use the default `closed` mode.

**Configuration**:
- Environment variable: `POLICY_CHECK_FAIL_MODE` (values: `closed`, `open`)
- Config file: `config/safety.yaml` under `input_policy.fail_mode`

**Prometheus metric**: `cordum_scheduler_input_fail_open_total` (counter, labels: `topic`) — incremented each time a job is allowed through under fail-open mode. Alert on this metric to detect safety kernel outages that are silently bypassing policy checks.

## 11. Environment Variables

| Variable | Component | Default | Purpose |
| --- | --- | --- | --- |
| `SAFETY_KERNEL_ADDR` | scheduler/gateway clients | `localhost:50051` | Safety Kernel gRPC address. |
| `SAFETY_POLICY_PATH` | safety kernel loader | `config/safety.yaml` | File policy source when URL is not set. |
| `SAFETY_POLICY_URL` | safety kernel loader | unset | URL policy source (overrides path). |
| `SAFETY_POLICY_RELOAD_INTERVAL` | safety kernel loader | `30s` | Policy reload interval. |
| `SAFETY_POLICY_MAX_BYTES` | safety kernel loader | `2097152` | Max policy size for file/URL load. |
| `SAFETY_POLICY_URL_ALLOWLIST` | safety kernel loader | unset | Comma-separated host allowlist for policy URL. |
| `SAFETY_POLICY_URL_ALLOW_PRIVATE` | safety kernel loader | `false` | Allow private/loopback URL hosts. |
| `SAFETY_POLICY_CONFIG_DISABLE` | safety kernel loader | unset | Disable config-service policy fragments. |
| `SAFETY_POLICY_CONFIG_SCOPE` | safety kernel loader | `system` | Config service scope for fragments. |
| `SAFETY_POLICY_CONFIG_ID` | safety kernel loader | `policy` | Config object ID for fragments. |
| `SAFETY_POLICY_CONFIG_KEY` | safety kernel loader | `bundles` | Config key containing policy bundle map. |
| `SAFETY_DECISION_CACHE_TTL` | safety kernel evaluator | `0` (disabled) | Cache TTL for policy decisions. |
| `SAFETY_DECISION_CACHE_MAX_SIZE` | safety kernel evaluator | `10000` | Max cache entries before eviction. |
| `SAFETY_POLICY_SIGNATURE_REQUIRED` | safety kernel loader | `true` in production | Enforce signature verification. |
| `SAFETY_POLICY_PUBLIC_KEY` | safety kernel loader | unset | Ed25519 public key (base64/hex). |
| `SAFETY_POLICY_SIGNATURE` | safety kernel loader | unset | Inline signature (base64/hex). |
| `SAFETY_POLICY_SIGNATURE_PATH` | safety kernel loader | unset | Detached signature file path. |
| `SAFETY_KERNEL_TLS_CERT` | safety kernel server | unset | TLS certificate path for server listener. |
| `SAFETY_KERNEL_TLS_KEY` | safety kernel server | unset | TLS private key path for server listener. |
| `SAFETY_KERNEL_TLS_CA` | scheduler/gateway clients | unset | CA bundle for mTLS/TLS verification. |
| `SAFETY_KERNEL_TLS_REQUIRED` | scheduler/gateway clients | `true` in production | Require TLS when dialing safety kernel. |
| `SAFETY_KERNEL_INSECURE` | scheduler/gateway clients | `false` | Allow insecure client transport outside production. |

Related (non-`SAFETY_*`) knobs:

- `OUTPUT_SCANNERS_PATH` for scanner config file (`config/output_scanners.yaml` by default)
- `CORDUM_ENV` / `CORDUM_PRODUCTION` for production-mode behavior
- `CORDUM_TLS_MIN_VERSION` for TLS minimum version
- `CORDUM_GRPC_REFLECTION` to enable gRPC reflection

## 12. Cross-References

- [Output Policy Guide](./output-policy.md)
- [API Reference](./api-reference.md)
- [Configuration](./configuration.md)
- [System Overview](./system_overview.md)

