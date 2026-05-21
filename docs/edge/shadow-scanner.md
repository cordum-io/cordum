# Cordum Edge — local shadow-agent scanner (`cordumctl shadow scan`)

EDGE-140's opt-in local scanner detects likely-unmanaged coding-agent
configurations and processes on a developer or CI host and emits
redacted observe-mode findings. It is a P3 (post-P0) capability,
deliberately scoped narrower than full Shadow IT discovery: scope is the
three first-party MCP clients (Claude Code, Codex, Cursor), a small set
of known agent process names, and a small set of known agent-credential
env-var names. See `docs/security/agentd-keychain.md` for the
defence-in-depth keychain story; see `docs/edge/managed-settings-deploy.md`
for the enterprise enforcement path.

## 1. Overview

`cordumctl shadow scan` walks three local detection sources and produces
a JSONL stream of findings:

1. Config-file presence under the user's home directory.
2. Process-name matches in the host process table (via
   `github.com/shirou/gopsutil/v3/process`).
3. Environment-variable name matches in the calling process's env.

The scanner is **observe-mode only**. It performs zero remediation
actions: no file modification, no process termination, no subprocess
spawning. Operators who want enforcement should route through
`cordumctl edge managed-settings` instead. The scanner is designed for
inventory awareness and CI advisory checks, not gating.

## 2. Trust and privacy boundary

| The scanner CAN | The scanner does NOT |
|-----------------|----------------------|
| Read JSON / TOML structural fields from known config paths. | Read command-line strings, env-var values, prompt text, or any non-schema field of a config. |
| Record server names, transport types, and endpoint hostnames. | Record full URLs, paths, tokens, or any value matching a credential-shape regex. |
| Record process executable names and PIDs. | Record process command-lines, environments, open files, network sockets, or working directories. |
| Record the **names** of known credential env vars. | Record the **values** of those env vars. |
| Emit findings to stdout JSONL or a mode-0600 output file. | Send findings off-host. (`EDGE-141` will add a server-side store; this task only writes locally.) |
| Refuse to follow symlinks (privacy + TOCTOU hardening). | Cross filesystem boundaries through arbitrary user-provided paths. |

Redaction is applied **at extraction time**, not as a post-process
filter. `RedactConfigSummary` only ever reads recognised structural
fields; even if a malicious config injected a value matching a secret
regex into one of those fields, the summary regex-strips the 8 supported
secret-shape patterns before emission and bounds the result to ≤2048
bytes.

## 3. Opt-in gate

The scanner refuses to enumerate anything unless the caller explicitly
opts in. The CLI honours two equivalent signals:

```sh
cordumctl shadow scan --enable-shadow-scan   # explicit flag
CORDUM_EDGE_SHADOW_SCAN_ENABLED=true cordumctl shadow scan   # env-var
```

Either signal is sufficient. Default invocation prints
`shadow scan disabled by default; use --enable-shadow-scan to opt in`
and exits 0 — the polite-no-op is a feature, not a failure, so CI
pipelines can include the command unconditionally.

`true`, `1`, and `yes` are all recognised values for the env-var. Any
other value (including `false`, `0`, empty string) is treated as
opt-out.

## 4. Detection sources

| Source | What is recorded |
|--------|------------------|
| Claude Code | `~/.claude/settings.json` — JSON parse → `mcpServers` keys + per-entry `transport` + endpoint hostname. |
| Codex | `~/.codex/config.toml` — minimal regex extract of `[mcp_servers.<name>]` headers + `transport = "..."` + `endpoint = "..."` hostname. |
| Cursor | `~/.cursor/mcp.json` — JSON parse, same shape as Claude Code. |
| Process names | Case-insensitive substring match against `claude-code`, `claude`, `codex`, `cursor`, and the `mcp-*` prefix. PID is recorded; cmdline / environ are not. |
| Env vars | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CURSOR_API_KEY` — only the **fact-of-presence** is recorded; values are never read. |

Adding a new source means amending `clientSpecs`, `processMatches`, or
`knownEnvVars` in `core/edge/shadow/scanner.go`. Each addition should
keep the privacy boundary: structural fields only.

## 5. Finding schema

Every finding is one JSON object with the following fields:

```jsonc
{
  "tenant_id":   "string — tenant attribution from --tenant or env",
  "principal_id":"string — principal attribution from --principal or env",
  "hostname":    "string — os.Hostname()",
  "product":     "claude-code | codex | cursor | mcp-server",
  "evidence_type": "config_file | process_name | environment_var",
  "redacted_path": "~/.claude/settings.json | process:claude:1234 | env:ANTHROPIC_API_KEY",
  "redacted_config_summary": "N mcp servers configured (transports: ...; hosts: ...) | empty | ...",
  "risk":           "low | medium | high",
  "remediation_hint": "string — operator-facing one-liner; never imperative",
  "status":         "observed | unreadable | managed_skip | partial",
  "observed_at":    "RFC3339 timestamp"
}
```

`risk` never emits `critical` because enforcement is out of scope for
this task (task rail #2). The most severe shadow observation that can
ship from this package is `high`.

## 6. Managed-config skip

Configs that originated from a Cordum managed-settings deployment carry
the `CORDUM_EDGE_MANAGED_POLICY_MODE=enterprise-strict` invariant
(established by EDGE-150). The scanner recognises this signature and
emits `Finding{Status:"managed_skip"}` instead of flagging the config
as a shadow observation. This satisfies DoD #4 'managed config not
flagged' and prevents enterprise-managed fleets from being drowned in
false-positive shadow alerts.

A managed-skip finding is still emitted — it carries the redacted
summary so operators see the file was inspected and identified as
managed, rather than being silently filtered out.

## 7. Output

Default: stdout, one finding per line as JSON (JSONL). Pipe into `jq`
for ad-hoc filtering or into a SIEM ingester for collection.

`--output <path>` writes to a file opened with `O_WRONLY|O_CREATE|O_TRUNC`
at mode `0o600`. The file is truncated on each scan — these JSONL
outputs are transient artefacts, not long-lived audit logs. If you
need historical retention, pipe the stdout JSONL into an append-only
collector.

`--tenant <id>` and `--principal <id>` override the per-finding
attribution fields. Defaults are empty. Production deployments should
set these from the operator's tenant / principal binding rather than
auto-inferring from process state (cross-tenant safety: the scanner
runs as a local CLI and has no authoritative tenant context).

## 8. Operational guidance

| Use case | Invocation |
|----------|------------|
| Developer-local audit | `cordumctl shadow scan --enable-shadow-scan` |
| CI advisory check | `cordumctl shadow scan --enable-shadow-scan --output shadow-findings.jsonl` |
| Tenant-attributed enterprise audit | `cordumctl shadow scan --enable-shadow-scan --tenant <id> --principal <id>` |
| Pipeline-friendly default (no-op when disabled) | `cordumctl shadow scan` |

**Do not wire shadow scan to enforcement.** The CLI never returns
non-zero based on findings — exit 0 means "scan completed", regardless
of whether findings exist. Wiring shadow scan to a CI gate that fails
the build on any finding would conflict with task rail #2 (no
enforcement) and would create the same false-positive amplification
that the managed-config skip is designed to prevent.

## 9. Roadmap

| Sibling    | Status  | Adds |
|------------|---------|------|
| EDGE-141   | DONE    | Server-side finding store + `/api/v1/edge/shadow-agents` lifecycle APIs (`detected → resolved/suppressed`). |
| EDGE-142   | WORKING | Remediation-hint generator (still observe-mode). |
| EDGE-143   | DESIGN  | [K8s + CI shadow detector design doc](kubernetes-ci-shadow-detector-design.md) (design only, awaiting human signoff). |
| EDGE-143.5 | DONE    | Store extensions — §10.1 fields, §10.2 filters, §10.5 Redis indexes + per-finding retention. See §9.1 below. |
| EDGE-143.1 | DONE    | Kubernetes shadow detector library — 9 signal extractors per design doc §7.1, observe-only. See §9.2 below. |
| EDGE-143.2 | DONE    | GitHub Actions CI shadow detector library — 6 signal extractors per design doc §8.1, observe-only. See §9.3 below. |

### 9.1 EDGE-143.5 — store extensions (shipped)

Adds the additive store surface from the EDGE-143 design doc §10. All
changes are backward-compatible — EDGE-141 records without these
fields continue to round-trip and surface in lists.

**23 new fields on `ShadowAgentFinding`** (§10.1) — `source_type`
(enum `local|kubernetes|ci|network`, defaults to `local` on read),
`source_id`, `cluster_id`, `namespace`, `workload_kind`,
`workload_name`, `pod_uid`, `ci_provider` (enum
`github_actions|gitlab_ci|jenkins|buildkite|circleci|other`), `repo`
(composite-indexed with `ci_provider`), `ref`, `workflow_id`,
`job_id`, `run_id`, `runner_id`, `tenant_source`, `principal_source`,
`signal_set` (≤16 entries, `[a-z0-9_]{1,32}`), `confidence` ([0,1]),
`first_seen`, `last_seen`, `false_positive_reason`, `exception_id`,
`retention_class` (enum `shadow_short|shadow_default|shadow_long`).

**11 new query filters on `GET /api/v1/edge/shadow-agents`** (§10.2)
— `source_type`, `cluster_id`, `namespace`, `ci_provider`, `repo`
(requires `ci_provider`), `signal` (repeatable any-of, capped at 16),
`confidence_min`, `first_seen_after`, `last_seen_before`,
`exception_id`, `include_managed_skip` (default `false`; when `false`,
findings carrying `false_positive_reason` are excluded). Combined
filters apply AND semantics across dimensions; `signal` applies IN
semantics within the dimension.

**4 new Redis indexes** (§10.5) — `edge:shadow:index:source:<source>`,
`edge:shadow:index:cluster:<cluster_id>`,
`edge:shadow:index:repo:<provider>:<org/repo>`,
`edge:shadow:index:signal:<signal>`. These are **NOT tenant-scoped**
(per the Q7 binding governor ruling — store-level federation, not
detector-level): multiple tenants share the same ZSET on these keys,
and tenant isolation is enforced at read time. Cross-tenant index
entries are **skipped** during a tenant's query, never deleted —
deletion would be cross-tenant data loss.

**Per-finding retention** (§10.5) — `retention_class` drives terminal
TTL when the finding transitions to `resolved` or `suppressed`. Empty
class falls back to the store's `terminalRetention` (default 90d) so
EDGE-141 records keep their existing lifecycle.

| Retention class  | Default TTL | Env var override                       |
|------------------|-------------|----------------------------------------|
| `shadow_short`   | 7 days      | `CORDUM_EDGE_SHADOW_RETENTION_SHORT`   |
| `shadow_default` | 90 days     | `CORDUM_EDGE_SHADOW_RETENTION_DEFAULT` |
| `shadow_long`    | 365 days    | `CORDUM_EDGE_SHADOW_RETENTION_LONG`    |

Env vars use Go `time.ParseDuration` syntax (`7d` is **not** supported;
use `168h`). Zero or negative values cause the gateway to fail at
startup, matching the EDGE-141 "positive durations only" convention.

**Backward-compatibility (§10.4):** legacy EDGE-141 findings (no
`source_type` set) read back with `source_type="local"` defaulted
on read; the `GET ?source_type=local` query falls back to the broad
tenant index + post-filter so legacy rows still surface.

This task explicitly does **not** add a dashboard surface for shadow
findings (task rail #3 'Shadow Agents were cut from P0; do not add P0
nav/page here'). The future P3 dashboard surface is intentionally
deferred; see EDGE-143's design doc §17 for the proposed follow-up task
that designs it.

### 9.2 EDGE-143.1 — Kubernetes shadow detector library (shipped)

Library package at `core/edge/shadow/k8s/`. Vendors `k8s.io/client-go@v0.34.8`
as the **first Kubernetes dependency in cordum** (transitive
`k8s.io/api`, `k8s.io/apimachinery`). Observe-only by design and by
the type system: the detector holds a narrow internal `kubeReader`
interface that declares only `listPods` / `listNamespaces` /
`listServices` / `getServiceAccount` / `listNetworkPolicies`. No
mutating verb (Create/Update/Patch/Delete) is reachable from any code
path in this package, so any future regression that tries to mutate
cluster state is a Go compile error. Design doc reference: design doc
§7 + binding governor ruling
[comment-a17f4f1c on task-de50a293](kubernetes-ci-shadow-detector-design.md).

**9 signal extractors** (design doc §7.1):

| Signal                         | Risk   | Trigger |
|--------------------------------|--------|---------|
| `k8s_heartbeat_missing`        | medium | Pod whose image matches a known agent image but the Cordum heartbeat label is missing. §14 N-consecutive-poll gate (default 3) suppresses single-poll false positives. |
| `k8s_unmanaged_process`        | medium | Pod container `command`/`args` leading-token matches a known agent executable AND no heartbeat label. Only the leading token is captured — never subsequent args. |
| `k8s_unmanaged_mcp_service`    | medium | Service with `port.name ∈ {mcp, mcp-stdio, mcp-sse, mcp-http}` missing the Cordum gateway-adoption label. |
| `k8s_unmanaged_workload`       | medium | Pod owned by a Deployment/DaemonSet/StatefulSet/Job/CronJob/ReplicaSet not on the operator allowlist; emits at owner level so a 100-replica rogue deployment produces ONE finding. |
| `k8s_untrusted_agent_image`    | low    | Pod image registry-prefix not in `ImageRegistryAllowlist` and image name matches an agent token. |
| `k8s_namespace_untenanted`     | low    | Namespace missing tenant label AND containing ≥1 agent-image pod (§14 aggregation guard). |
| `k8s_admission_observed`       | medium | Reserved for operator-supplied admission-log tail; observe-only, never installs a webhook. |
| `k8s_egress_bypass`            | high   | NetworkPolicy with broad egress (`0.0.0.0/0`) not scoped to the operator's LLM proxy allowlist. |
| `k8s_ephemeral_indicator`      | low    | Pod present last scan, gone this scan — **never auto-promoted** to a finding without corroboration per §14. |

**Tenant mapping precedence** (design doc §6.1, recorded in
`tenant_source` field):

1. `pod_label` — pod `cordum.io/tenant-id`.
2. `namespace_label` — namespace `cordum.io/tenant-id`.
3. `cluster_config` — operator-maintained `Config.ClusterTenantMap[ClusterID]`.
4. `sa_config` — pod's service-account annotation `cordum.io/tenant-id`.
5. `quarantine` — terminal default (`cordum.shadow.quarantine`); finding remains actionable because workload identity is still captured.

**Principal mapping precedence** (§6.2, recorded in `principal_source`):

1. `pod_label` → 2. `pod_annotation` → 3. `sa_name` (`sa:<ns>:<name>`) → 4. `unknown`.

**Data minimization** (§5 — enforced at extraction time, not as a
post-filter):

| MAY collect | MUST NOT collect |
|-------------|------------------|
| Pod/Service/Namespace `.name`, `.labels`, `.annotations` (tenancy-purposeful). | Container `Env[].Value` (only `Env[].Name`). |
| Container image registry+name; tag scrubbed via `imageTagSafe` if secret-shape. | Mounted Secret/ConfigMap data bodies. |
| Container `Command[0]` / `Args[0]` leading token via `leadingToken`. | Command-arg values beyond the leading token. |
| OwnerReferences kind+name+UID. | Full URLs with query string. |
| | Raw network payloads. |

Defense-in-depth: every string entering `CreateFindingRequest` runs
through `redactField` (8-pattern regex strip mirroring
`core/edge/shadow/redaction.go:23-36`, plus a 2048-byte cap). The
shadow store applies the same patterns again at write time via
`stripSecretMarkers` on `EvidenceSummary` — two independent strip
passes so a regression on one side does not silently leak.

**Configuration** (`k8s.Config`):

| Field                       | Default | Purpose |
|-----------------------------|---------|---------|
| `ClusterID`                 | (required for prod) | Used as `cluster_id` on every emitted finding + tier-3 tenant lookup key. |
| `ScanInterval`              | 60s     | Cadence for `Run()`-driven polling. |
| `TenantLabelKey`            | `cordum.io/tenant-id` | Label/annotation key consulted by the tenant resolver. |
| `PrincipalLabelKey`         | `cordum.io/principal-id` | Same for principal. |
| `HeartbeatLabelKey`         | `cordum.io/edge-session-id` | Label whose presence marks a pod as Cordum-governed. |
| `GatewayAdoptionLabel`      | `cordum.io/mcp-gateway` | Label whose presence marks a Service as MCP-gateway-attached. |
| `HeartbeatMissedThreshold`  | 3       | §14 N-consecutive-poll gate before `heartbeat_missing` promotes. |
| `KnownAgentImages`          | —       | Allowlisted agent image prefixes (e.g. `anthropic/claude-code`). Set explicitly per operator deployment. |
| `KnownAgentExecutables`     | `claude, codex, cursor, mcp-server, mcp-gateway` | Leading-token allowlist for `unmanaged_process`. |
| `ImageRegistryAllowlist`    | —       | Registry prefixes trusted not to be shadow agents. Set explicitly. |
| `MCPPortNames`              | `mcp, mcp-stdio, mcp-sse, mcp-http` | Service port names that flag an MCP service. |
| `WorkloadAllowlist`         | —       | Workload names exempt from `unmanaged_workload`. |
| `NamespaceAllowlist`        | —       | Namespaces exempt from `namespace_untenanted`. |
| `ClusterTenantMap`          | —       | Tier-3 `cluster_id → tenant_id` map for the default resolver. |
| `LLMProxyEndpoints`         | —       | Endpoints considered safe for egress; broader egress triggers `egress_bypass`. |
| `QuarantineTenantID`        | `cordum.shadow.quarantine` | Terminal tenant when all other tiers fail. |

**Observability** (Observer interface; design doc §13):

```go
type Observer interface {
    RecordFindingEmit(signal, risk string)
    EmitAudit(event audit.SIEMEvent)
}
```

Production wiring backs `Observer` with a Prometheus `CounterVec`
(`cordum_edge_shadow_finding_emit{source_type, signal, risk}`) and the
shared `audit.AuditSender`. Bounded labels only — tenant_id, cluster_id,
namespace, workload_name, pod_uid, repo, run_id are NEVER passed as
metric labels (high cardinality); they live on the persisted finding
and on `audit.SIEMEvent.Extra`. The audit event uses
`Action="shadow_agent.observed"`, `Decision="observed"`,
`EventType="edge.shadow_finding_created"` with severity derived from
risk per §13.2.

**Observe-mode enforcement contract** (design doc §11, ADR-gated
future):

- This task ships ZERO enforce-mode code paths. No
  `ValidatingAdmissionWebhook`, no `kubectl patch`, no `kubectl
  delete`, no auto-label-mutation.
- `CORDUM_EDGE_SHADOW_K8S_ENFORCE` env var name is **reserved** for the
  future enforce-mode hook but is not consulted anywhere today.
- The narrow `kubeReader` interface (`detector.go:79`) is the primary
  enforcement mechanism — adding a mutating verb to the library would
  require widening this interface, which would be reviewable.

**EDGE-141 reuse**: findings are persisted via the existing
`shadow.Store.CreateFinding` API. No parallel finding store, no
parallel ingest pipeline. The Source ID emitted is
`"k8s-detector-" + ClusterID` so SIEM aggregators can correlate.

### 9.3 EDGE-143.2 — GitHub Actions CI shadow detector library (shipped)

Library package at `core/edge/shadow/github/`. It uses the canonical
`github.com/google/go-github/v74/github` read-only client and the
existing `shadow.Store.CreateFinding` path from EDGE-141; there is no
parallel GitHub client, no parallel finding store, and no mutating CI
API path. The package is observe-mode only: no repository mutation, no
PR comment, no workflow dispatch, no check-run write.

Design references: [kubernetes-ci-shadow-detector-design.md §8.1](kubernetes-ci-shadow-detector-design.md)
for GitHub Actions signals, §6.3/§6.4 for tenant/principal mapping,
§14 for false-positive controls, and binding governor ruling
`comment-a17f4f1c` on task-de50a293 for Q6 OIDC trust roots.

**6 signal extractors** (design doc §8.1):

| Signal | Trigger |
| --- | --- |
| `agent_action_used` | Workflow YAML references a known agent action (`uses:`) or a known `run:` leading token (`claude`, `codex`, `cursor`). |
| `missing_cordum_attach` | Agent use is present and both `cordum/cordum-edge-attach` and a Cordum EdgeSession heartbeat are absent. |
| `self_hosted_runner_unlabeled` | Job runner labels include `self-hosted` without a Cordum managed-runner label. |
| `env_var_name_indicator` | Workflow/job/step env-var **names** are present; values are never read. |
| `agent_config_present` | Known agent/MCP config-file path exists; only redacted path + structural summary are recorded. |
| `direct_provider_endpoint` | Operator-supplied CI-log metadata reports a known provider hostname; query strings and tokens are stripped. |

**Configuration**:

| Field / env var | Default | Purpose |
| --- | --- | --- |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_github` | `https://token.actions.githubusercontent.com` | Q6 Cordum-default GitHub Actions OIDC issuer. Set to a custom issuer URL to override, or `disabled` to refuse OIDC and fall back to org/repo mapping. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_github` | `cordum-edge` | Expected OIDC audience for verified claims. |
| `Config.OrgRepoMap` | operator-supplied | Tier-2 `org/repo → tenant_id` map. |
| `Config.QuarantineTenantID` | required by constructor | Terminal fallback tenant for unmapped/untrusted runs. |
| `Config.KnownAgentActionRefs` / `KnownAgentRunTokens` / `AgentConfigPaths` / `ProviderEndpointHosts` | operator-supplied with safe defaults for run tokens | Closed sets for action refs, run-command leading tokens, config paths, and provider hostnames. |
| `Config.EdgeSessionHeartbeat` | optional | Read-only seam for cross-referencing workflow runs against Cordum EdgeSession heartbeats. |

**Tenant + principal mapping**:

1. Verified OIDC claims first (`iss` + `aud` match the configured trust
   root via discovery/JWKS signature + expiry verification;
   `sub=repo:<org>/<repo>:ref:<ref>` resolves through `OrgRepoMap`
   and is captured verbatim as `principal_id`).
2. Configured `org/repo` map from workflow/repository metadata.
3. Quarantine tenant + `principal_id="unknown"`.

When OIDC is disabled (`CORDUM_EDGE_SHADOW_OIDC_TRUST_github=disabled`
or `Config.OIDCDisabled=true`), tier 1 is skipped and the detector does
not call the OIDC claims provider. Fork PRs are forced to quarantine
even if the base repo maps to a tenant.

**False-positive controls** (§14):

| Control | Behavior |
| --- | --- |
| Ephemeral runners | `ephemeral` label hard-suppresses the self-hosted-runner signal. |
| Fork PRs | Tagged `false_positive_reason=fork_pr_ephemeral` and routed to quarantine. |
| Scheduled jobs | Tagged `false_positive_reason=scheduled`. |
| Dependabot / Renovate | Actor allowlist tags `false_positive_reason=automation_bot`. |
| Test fixtures | Operator-curated repo set tags `false_positive_reason=test_fixture`. |
| Dev sandboxes | Operator-curated repo set tags `false_positive_reason=dev_sandbox`. |

**Data minimization** (§5):

- Env-var **names** only; values and `${{ secrets.* }}` expansions are
  never persisted.
- Config-file contents are not stored. If content is decoded, it is
  summarized through `shadow.RedactConfigSummary`; persisted evidence
  records only `github://org/repo/path` and a bounded structural summary.
- Direct-provider observations persist hostnames only; full URLs,
  query strings, Authorization headers, and raw log bodies are not
  collected by the detector.
- Metrics use bounded labels only:
  `cordum_edge_shadow_finding_emit_total{source_type="github_actions",signal,risk}`,
  `cordum_edge_shadow_oidc_verify_total{provider,result}`, and
  `cordum_edge_shadow_gh_rate_limit_remaining{provider}`. Repo, run,
  workflow, job, and tenant identifiers remain on findings/audit events,
  never metric labels.

### 9.3.1 EDGE-143.3 — GitLab CI / Jenkins / Buildkite / CircleCI shadow detectors (shipped)

Library package at `core/edge/shadow/ci/` provides observe-only shadow
detectors for the four additional CI providers covered by design doc
§8.2–§8.5. The package reuses EDGE-143.2's emit pattern (one
`shadow.Store.CreateFinding` per run, no parallel store), OIDC verifier
interface (generic over coreos/go-oidc), §6.3/§6.4 resolver vocabulary,
and §14 false-positive controls — there is no parallel CI subsystem.
This shipped-library status is separate from the `cordumctl edge doctor
--shadow-ci` CLI integration: as of this build, `cordumctl` still reports the
flag as unsupported/pending instead of invoking these detectors. See
[`docs/cordumctl/edge-doctor.md`](../cordumctl/edge-doctor.md).

Design references: [kubernetes-ci-shadow-detector-design.md §8.2–§8.5](kubernetes-ci-shadow-detector-design.md)
for per-provider signals, §6.3/§6.4 for tenant/principal mapping, §14
for false-positive controls, and binding governor ruling
`comment-a17f4f1c` on task-de50a293 for Q6 OIDC trust roots.

The four provider scanners share the same five signal extractors as
§9.3 above (`agent_action_used`, `missing_cordum_attach`,
`self_hosted_runner_unlabeled`, `env_var_name_indicator`,
`direct_provider_endpoint`). Provider workflow file paths:
`.gitlab-ci.yml`, `Jenkinsfile`, `.buildkite/pipeline.yml`,
`.circleci/config.yml`.

**Read-only API surface** (minimal HTTP clients; no provider SDK
dependencies — `xanzy/go-gitlab` is archived March 2025 and a
uniform httptest-injectable client gives one test contract across all
four providers):

| Provider | Endpoints consumed (GET only) | Auth |
| --- | --- | --- |
| GitLab CI | `/api/v4/projects/<id>`, `/projects/<id>/pipelines`, `/pipelines/<id>/jobs`, `/projects/<id>/variables` (names only), `/repository/files/.gitlab-ci.yml/raw` | `PRIVATE-TOKEN` header (read_api scope) |
| Jenkins | `/job/<name>/api/json`, `/job/<name>/<build>/api/json`, `/job/<name>/config.xml` | HTTP Basic (no crumb fetch, no mutating endpoints) |
| Buildkite | `/v2/organizations/<org>/pipelines/<slug>`, `/v2/organizations/<org>/pipelines/<slug>/builds` (jobs + agent + env names) | Bearer (read_pipelines, read_builds, read_pipeline_files) |
| CircleCI | `/api/v2/project/<vcs>/<org>/<repo>`, `/api/v2/project/.../pipeline`, `/api/v2/pipeline/<id>/workflow`, `/api/v2/workflow/<id>/job`, `/api/v2/project/.../envvar` (names only), `/api/v1.1/project/.../configuration` | `Circle-Token` header |

**OIDC trust roots** (Q6 binding governor ruling — operators MUST
configure per-provider env vars; defaults differ by provider):

| Provider | Env var | Default | Behavior |
| --- | --- | --- | --- |
| GitLab.com SaaS | `CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab` | `https://gitlab.com` | Cordum-default when `GitLabBaseURL` host is `gitlab.com`. |
| GitLab self-hosted | `CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab` | (none) | Non-`gitlab.com` `GitLabBaseURL` requires operator override; absent override sets `Disabled=true` and falls back to tier-2 §6.3 map. |
| Jenkins | `CORDUM_EDGE_SHADOW_OIDC_TRUST_jenkins` | (none) | Operator-only per Q6; absent override falls back to tier-2 §6.3 map. |
| Buildkite | `CORDUM_EDGE_SHADOW_OIDC_TRUST_buildkite` | (none) | Operator-only per Q6; absent override falls back to tier-2 §6.3 map. |
| CircleCI | `CORDUM_EDGE_SHADOW_OIDC_TRUST_circleci` | (none) | Operator-only per Q6; absent override falls back to tier-2 §6.3 map. |

Each provider also accepts `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<provider>`
(default `cordum-edge`). The literal value `disabled` sets
`OIDCConfig.Disabled=true` for any provider, forcing tier-2 fallback.

**Tenant + principal mapping** (§6.3 / §6.4):

1. Verified OIDC claim (`iss` + `aud` match, signature + expiry
   verified). Subject parsing handles GitLab
   `project_path:<group>/<project>:ref:<ref>` and Buildkite
   `organization:<slug>:pipeline:<slug>:...` shapes; principal_id
   captured verbatim from `sub`.
2. Run's `Repo` field (`<owner>/<repo>`) → `OrgRepoMap` lookup.
3. Quarantine tenant + `principal_id="unknown"`.

Fork PRs and scheduled CI runs surface `false_positive_reason` per §14
but never bypass quarantine. Each scanner sets `IsForkPR` / `IsScheduled`
from native provider metadata (GitLab `source=external_pull_request_event|schedule`,
Buildkite `source=schedule`, CircleCI `trigger.type=schedule|scheduled_pipeline`).

**Data minimization**:

- Env-var **names** only — GitLab variables endpoint values are
  dropped; Jenkins build `environment` map keys captured (values
  dropped); Buildkite job `env` map keys captured (values dropped);
  CircleCI `/envvar` endpoint dropped (names only).
- URL query strings stripped from all redacted paths via `RedactCIPath`
  before persistence; bearer tokens never appear in error logs
  (`redactURLForError` drops scheme://host + query before wrapping).
- `shadow.StripSecretMarkers` runs as defense-in-depth on every
  emitted evidence summary; the shadow store re-runs the same strip
  on write.
- `shadow.RedactPath` normalizes paths cross-platform; per-provider
  scheme prefixes (`gitlab://`, `jenkins://`, `buildkite://`,
  `circleci://`) plus repo identifier lets SIEM consumers pivot per
  provider without parsing.
- Metrics use bounded labels only:
  `cordum_edge_shadow_finding_emit_total{source_type=gitlab_ci|jenkins|buildkite|circleci,signal,risk}`
  and `cordum_edge_shadow_oidc_verify_total{provider,result}`. No
  repo / pipeline / build / runner identifiers appear as label
  values per design §13.1.

**Observe-only guarantee**: enforced by type system (no scanner
publishes a write/mutation method) plus a black-box test
(`TestAllProviders_NoMutationCalls`) that asserts every HTTP method
issued against all four mock servers is GET or HEAD.

### 9.4 EDGE-143.4 — Network-signal aggregator (shipped)

Library package at `core/edge/shadow/network/`. Reads
operator-supplied egress / proxy logs and emits ShadowAgentFinding
records for traffic to direct LLM-provider endpoints (Anthropic /
OpenAI / Google API hosts) from sources lacking a Cordum Edge attach.
Design doc reference: [kubernetes-ci-shadow-detector-design.md §9](kubernetes-ci-shadow-detector-design.md)
+ binding governor ruling on Q2 PII handling
([comment-a17f4f1c on task-de50a293](kubernetes-ci-shadow-detector-design.md)).

**NG7 contract — observe-only, no Cordum-side capture.** Cordum does
**not** intercept network traffic. The detector reads only log records
the operator has already produced. No raw sockets, no TLS
termination, no pcap / eBPF, no admission-side packet inspection.
`TestNetworkDetector_Ingest_NoCaptureNG7` grep-asserts the absence of
`pcap.*`, `gopacket`, `afpacket`, `net.ListenPacket`,
`syscall.SOCK_RAW`, `crypto/tls.Server` / `NewListener`, and
`x/sys/unix.SOCK_RAW` across the package source.

**3 log ingest sources** (design doc §9.2):

| Ingestor                  | Source                          | API |
|---------------------------|---------------------------------|-----|
| `network.NewFileIngestor` | Operator log file               | `bufio.Scanner` line-by-line; EOF terminates cleanly; ctx-cancel respected; 256 KiB line cap. |
| `network.NewSyslogIngestor` | UDP syslog endpoint           | `net.ListenUDP` server bind (READ-only — never `Send`); 200 ms ctx-poll deadline; RFC 5424 messages parsed via shared parser. |
| `network.NewStdinIngestor` | `io.Reader` (default `os.Stdin`) | `bufio.Scanner`; closes cleanly on EOF; 256 KiB line cap. |

**§9.1 lawful metadata catalog** — the closed set of fields the
detector persists. `enforceCatalog` strips every field outside the
catalog at the ProcessRecord boundary so a careless ingestor cannot
re-leak forbidden values.

| MAY persist     | MUST NOT persist |
|-----------------|------------------|
| `hostname` (e.g. `api.anthropic.com`) | Full URLs with query strings |
| `category` (`anthropic_api` \| `openai_api` \| `google_api`) | IP addresses |
| `count_bucket` (1 / 10 / 100 / 1000 / 10000 / 100000+) | Bearer tokens / API keys |
| `workload identity` (post-PII per below) | Request / response bodies |
| `endpoint_hash` (opaque, operator-supplied) | User-Agent strings |

The store also runs `stripSecretMarkers` again on `EvidenceSummary`
at write time — two independent strip passes so a regression on one
side does not silently leak.

**Q2 PII_MODE env flag** (binding governor ruling, GDPR Art. 4(1)
pseudonym handling). `principal_id` derived from `github.actor` or
equivalent CI usernames IS personal data; the env flag controls how
the raw value is transformed before persistence.

| `CORDUM_EDGE_SHADOW_PII_MODE` | Effect |
|-------------------------------|--------|
| `pseudonymize` *(default)*    | `principal_id = <first-3-chars-of-raw> + <first-8-hex-chars-of-SHA256(raw)>` — stable correlation handle that does not reveal the full username. |
| `hash`                        | `principal_id = <first-16-hex-chars-of-SHA256(raw)>` — no prefix; weakest correlation strength still preserved. |
| `drop`                        | `principal_id = "dropped"` sentinel — no identity propagation whatsoever (strictest mode). |

Invalid env values fail-fast at `network.NewDetector` startup. An
empty raw input always collapses to the `"dropped"` sentinel
regardless of mode (no useful pseudonymization of ``).

**§9.3 risk classification** (recorded in finding `risk` field):

| Trigger                                                       | Risk     |
|---------------------------------------------------------------|----------|
| Tenant resolves to quarantine (no workload-identity / OIDC mapping) | high |
| Workload absent from `KnownAttachWorkloadIDs` (no Cordum Edge attach record) | medium |
| Workload last-attach older than `HeartbeatStaleThreshold` (default 5 min) | high |
| Workload present and fresh (still observably direct-to-provider)            | low |

**Tenant + principal mapping precedence** (§6.1 / §6.2, recorded in
`tenant_source` + `principal_source`):

1. `workload_identity` — `Config.WorkloadTenantMap[record.WorkloadID]` hit.
2. `oidc` — `Config.OIDCTenantMap[record.OIDCSub]` hit when no workload identity.
3. `quarantine` — terminal default (`cordum.shadow.quarantine`).

**Observability** (Observer interface; design doc §13):

```go
type Observer interface {
    RecordFindingEmit(signal, risk, ingestSource string)
    RecordPIIModeActive(mode string)
    RecordLogRecord(ingestSource, result string)
    EmitAudit(event audit.SIEMEvent)
}
```

Bounded labels only — `signal` is always `direct_provider_traffic`,
`risk` is the enum (`low|medium|high|critical`), `ingestSource` is
`file|syslog|stdin`, `result` is `emitted|skipped_unknown_host|
skipped_no_hostname|store_error|process_error`. `pii_mode_active` is
emitted as a gauge label once at `NewDetector` so dashboards see the
configured mode even before the first finding emits. Hostname /
category / tenant / workload are NEVER metric labels (high
cardinality); they live on the persisted finding and on
`audit.SIEMEvent.Extra`. Audit events use
`Action="shadow_agent.observed"` / `Decision="observed"` /
`EventType="edge.shadow_finding_created"` with
`Extra={finding_id, source_type=network, signal, hostname, category,
count_bucket, tenant_id, tenant_src, principal_src, risk}`.

**EDGE-141 reuse**: findings persist via `shadow.Store.CreateFinding`
with `SourceType=network` + `SourceID="network:<Config.SourceID>"`.
No parallel finding store, no parallel emit pipeline. The §10.1
typed fields (`hostname`, `evidence_type=network_direct_provider_traffic`,
`signal_set=[direct_provider_traffic]`, `retention_class=shadow_default`,
`first_seen` / `last_seen`) ride the same path as the K8s detector
in §9.2.

**GDPR / UK-DPA processing record**: see
[managed-settings-deploy.md §11](managed-settings-deploy.md) for the
operator-facing template that documents data categories, lawful
basis, retention, controller / processor split, and DSAR contact.

### 9.5 EDGE-143.6 — Operator-defined exception API (shipped)

Adds the operator-signed exception API from
[kubernetes-ci-shadow-detector-design.md §10.3](kubernetes-ci-shadow-detector-design.md).
Gated by the Q8 binding governor ruling on task-de50a293
(comment-a17f4f1c): risk=high exception creation **and revocation**
require step-up auth.

**Endpoints** (mounted under `/api/v1/edge/shadow/`):

| Method + path | Operation | Auth |
| --- | --- | --- |
| POST `/api/v1/edge/shadow/exception` | Create an exception | baseline + step-up if `scope_risk_level=high` |
| GET `/api/v1/edge/shadow/exception/{exception_id}` | Read one | baseline |
| DELETE `/api/v1/edge/shadow/exception/{exception_id}` | Revoke | baseline + step-up if original's `scope_risk_level=high` |
| GET `/api/v1/edge/shadow/exceptions` | List (paginated) | baseline |

Baseline is `requireEdgePermissionOrRole(auth.PermAuditExport, "admin", "user")`
— the same envelope EDGE-141 uses for the finding lifecycle. The
step-up gate is `requirePermissionOrRole(auth.PermShadowExceptionHighRisk, "admin")`
analogous to `auth.PermDelegationImpersonate` (no parallel auth
subsystem). Step-up failure returns 403 with the typed code
`step_up_required` and `details.required = "mfa_recent|signed_admin_token"`.

**Scope predicate**: an Exception matches a Finding when all of the
following hold: tenants agree, `scope_source_type` matches
`finding.source_type`, `scope_source_id` is empty OR equals
`finding.source_id`, `scope_risk_level` equals `finding.risk`, and
`scope_signal_set` is empty OR overlaps `finding.signal_set` (any-of).
Expired or revoked exceptions never match.

**Suppression at emit time**: `RedisStore.CreateFinding` calls
`MatchActiveExceptions` after validation, before persistence. A scope
match stamps `exception_id`, sets `false_positive_reason="operator_exception"`,
and flips `status=managed_skip` on the finding before the index
pipeline runs. The existing `IncludeManagedSkip=false` default on
`ListFindings` then hides the suppressed record from operator queries;
`?include_managed_skip=true` returns them. The detector itself never
auto-creates exceptions.

**Expiry**: `expires_at` is required, must be in the future, and may
not exceed 90 days from creation (§10.3 "longer requires re-affirmation").
Expired exceptions stop matching at the next `MatchActiveExceptions`
scan; `GetException` lazily promotes the status from `active` to
`expired` on read.

**Audit events** (severity HIGH when scope is high/critical, MEDIUM
for medium, INFO for low):

- `shadow_agent.exception_created` — emitted by the POST handler.
  Extra: `exception_id, scope_source_type, scope_risk_level,
  step_up_factor, expires_at, scope_source_id?, reason?`.
- `shadow_agent.exception_revoked` — emitted by the DELETE handler.
  Same Extra plus the revocation `reason?`.
- `shadow_agent.exception_applied` — emitted by the POST
  `/api/v1/edge/shadow-agents` handler when `CreateFinding` returns a
  finding stamped with `ExceptionID`. Extra:
  `exception_id, finding_id, scope_source_type, scope_risk_level,
  step_up_factor`. The `step_up_factor` is the value persisted on the
  Exception at create time, so SIEM rules can pivot on
  authority-at-time-of-action without joining against a live RBAC
  snapshot.

**Step-up factor mapping** (`StepUpFactor` enum):

- `signed_admin_token` — caller satisfied the gate via the legacy
  `admin` role.
- `mfa_recent` — caller satisfied via the explicit
  `shadow.exception.high_risk` RBAC permission grant (presumes
  recent-MFA workflow completed before the grant).
- `none` — gate not required (scope risk is `medium` or `low`).
  Audit events still record `"none"` so consumers can pivot on
  field-presence without nil checks.

**Per-tenant cap**: at most 1000 active exceptions per tenant; the
1001st create returns HTTP 429 with the existing `conflict` envelope
code. The cap bounds the `MatchActiveExceptions` scan that runs inline
on every finding ingest.
