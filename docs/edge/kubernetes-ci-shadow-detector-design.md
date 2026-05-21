# EDGE-143 — Kubernetes and CI shadow-agent detection (design)

**Status:** Design only. Implementation tasks are NOT created by this document.
**Stage gate:** P3. Requires human review and signoff before any follow-up
implementation task is filed (see §16).
**Owner:** Cordum Edge platform.
**Companion docs:** [`shadow-scanner.md`](shadow-scanner.md) (local P3 scanner,
EDGE-140 shipped), EDGE-141 (server-side finding store, in flight),
EDGE-142 (remediation generator, in flight), [`managed-settings-deploy.md`](managed-settings-deploy.md),
[`cordum-agentd.md`](cordum-agentd.md), [`observability.md`](observability.md),
[`retention.md`](retention.md), PRD §6.4 / §10.6 / §13.7 / §18.

This document is the architecture & privacy design for **cluster-scope** and
**CI-scope** shadow-agent detection. It extends the local-host scanner story
(EDGE-140 plus EDGE-141 / EDGE-142) to two new environments where ungoverned
coding agents and direct provider traffic are commonly hidden today:

1. Kubernetes pods running unmanaged Claude Code / Codex / Cursor / MCP /
   provider-SDK workloads, or governed workloads that have drifted off the
   Cordum Edge attach path.
2. CI runners (GitHub Actions, GitLab CI, Jenkins, Buildkite, CircleCI) where
   an agent is invoked outside an Edge session and therefore produces no
   `EdgeSession → AgentExecution → AgentActionEvent` evidence chain.

This is **not** an enforcement design. Default rollout is observe-mode only;
warn-mode adds notifications and operator tasks; enforce-mode is gated behind
a future ADR (see §10).

---

## 1. Overview

```text
                  Cordum Edge known scope               +-- This design --+
                  (governed sessions)                   | new scope:      |
                                                        |   K8s + CI      |
  +-------------+        +----------------+             +-----------------+
  | cordum-hook |        | cordum-agentd  |     +------+   K8s inventory  |
  | Claude CLI  | -----> | local session  | --> |Gate- |   pod metadata   |
  +-------------+        +----------------+     |way   |   workload labels|
                                                |Edge  |                  |
  +-------------+        +----------------+     |APIs  |   CI provider    |
  | MCP client  | -----> |  MCP gateway   | --> |      |   OIDC + repo    |
  | (Codex/...) |        +----------------+     +------+   metadata       |
  +-------------+                                  |    |                  |
                                                   |    |   Egress / proxy |
  Local host scanner (EDGE-140)                    |    |   metadata (no   |
  cordumctl shadow scan ---------------------------+    |   payload capture)|
                                                        +------------------+

  ShadowAgentFinding store (EDGE-141, in flight)
        ^---- EDGE-143 contributes findings with source_type=kubernetes|ci|network
              (extends but does not fork the local source_type=local)
```

The design's job is to define **what signals** are collected, **how they map
to `ShadowAgentFinding` fields**, **what is never collected**, **how tenant
mapping works without payload inspection**, **what changes are needed to the
EDGE-141 store**, and **what rollout modes are safe**. The detector itself
is the next task family (out of scope for this document).

---

## 2. Goals and non-goals

### 2.1 Goals

- G1. Detect unmanaged Claude Code / Codex / Cursor / MCP / provider-SDK
  activity in Kubernetes clusters and CI pipelines using metadata signals
  only.
- G2. Map every finding to a tenant and (when available) a principal /
  workspace / repository / cluster / namespace so operators can act on it.
- G3. Reuse the EDGE-141 `ShadowAgentFinding` schema and store; extend it
  additively rather than fork it.
- G4. Reuse EDGE-142's remediation vocabulary (attach commands, managed
  settings, MCP gateway adoption, exception declarations) so cluster / CI
  findings produce the same operator action surfaces as local findings.
- G5. Default to observe mode; allow warn mode behind explicit configuration;
  defer enforce mode to a future ADR.
- G6. Define hard privacy boundaries that are **enforced before persistence**
  (extraction-time redaction, not post-process filtering), mirroring the
  EDGE-140 `RedactConfigSummary` model.
- G7. Tolerate ephemeral environments (CI fork PRs, dependabot/renovate runs,
  short-lived build pods) without producing false-positive storms.

### 2.2 Non-goals

The following are **explicitly excluded** from this design and from any
future implementation that cites it:

- NG1. **No payload capture.** The detector does not read prompt text,
  tool input bodies, tool response bodies, request/response bodies, command
  output, transcript contents, file contents, or environment-variable
  values.
- NG2. **No secret collection.** The detector does not read or persist
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CURSOR_API_KEY`, bearer tokens,
  cookies, SSH keys, signed URLs, or any other credential material — only
  the **name** of a known-secret env var or the **presence** of a known
  credential file may be recorded.
- NG3. **No production cluster enforcement** in this task. No
  `ValidatingWebhookConfiguration`, no `MutatingWebhookConfiguration`,
  no Gatekeeper / OPA policy that blocks pods, no Kubernetes Job killer,
  no CI workflow blocker.
- NG4. **No dashboard work.** EDGE-143 ships zero React/Tailwind/dashboard
  code. The future P3 dashboard surface is a separate task family with its
  own ADR.
- NG5. **No Cordum Jobs per agent action.** Findings remain
  `ShadowAgentFinding` records (per PRD §18.5). Agent actions are not
  re-modeled as Cordum Jobs.
- NG6. **No mutation of customer clusters** while in observe or warn mode.
  Detection is read-only against the Kubernetes API and CI provider APIs.
  Enforce mode (gated, future) is the only mode that may produce side
  effects, and even then only via explicit operator-approved actions
  (managed-settings deployment, MCP gateway attach), never via auto-delete
  or auto-kill.
- NG7. **No TLS interception by default.** The detector never decrypts
  agent ↔ provider traffic. Network signals are restricted to lawful,
  pre-existing metadata (DNS query category, SNI/CONNECT hostname,
  egress-policy logs, proxy access logs the operator already collects),
  never to mid-stream packet inspection.
- NG8. **No customer-cluster code execution.** The detector does not push
  DaemonSets, sidecars, init containers, or eBPF probes to discover
  workloads. If a future task wants a cluster-resident collector, it
  files its own ADR.

### 2.3 Out-of-scope but related

These items overlap with EDGE-143 thematically but are owned elsewhere:

- Managed-settings rollout to corporate desktops — owned by EDGE-031
  ([`managed-settings-deploy.md`](managed-settings-deploy.md)).
- LLM Proxy traffic redirection and provider-token governance — owned by
  the LLM Proxy feature family (PRD §5.3 / §6.3).
- Runtime sidecar telemetry / eBPF — PRD §4.3 P3 item, separate design.
- Agentd / agent-action-event redaction — EDGE-004 family, already shipped.

---

## 3. Relationship to existing shadow-detection pieces

EDGE-143 is the third and broadest leg of the shadow-detection stool. It
inherits as much as possible from the first two legs so operators see one
finding model, not three.

| Capability | Owner task | Status (2026-05-17) | What EDGE-143 reuses |
| --- | --- | --- | --- |
| Local host scanner | EDGE-140 | Shipped — `cordumctl shadow scan`, observe-only, JSONL out. | `Finding` record shape (`Product`, `EvidenceType`, `RedactedPath`, `RedactedConfigSummary`, `Risk`, `Status`, `RemediationHint`, `ObservedAt`) and the at-extraction redaction discipline (`RedactConfigSummary` returns ≤2048 bytes, regex-strips 8 secret patterns). |
| Server-side finding store + ingest API | EDGE-141 | In flight — extends Gateway with `POST /api/v1/edge/shadow/findings` + tenant-isolated retrieval. | Will publish findings into this same store. EDGE-143 proposes the additive schema deltas in §11 below; **the deltas do not land until EDGE-141 ships and the deltas are approved.** |
| Remediation generator | EDGE-142 | In flight — generates attach commands, managed-settings snippets, policy templates, risk explanations from local findings. | Will extend the same generator with cluster-scope and CI-scope remediation templates (attach pod, attach repo, deploy managed settings via MDM, route provider traffic through LLM proxy, declare exception). Implementation lives in EDGE-142's package; EDGE-143 only specifies the template inputs in §13. |

**EDGE-143 does not duplicate the `core/edge/shadow` package.** The local
scanner stays single-host; the cluster / CI detector is a separate code
path that **emits the same `Finding` shape** into the EDGE-141 store via
the same ingest API.

---

## 4. Threat model

The detector exists to defend against:

| Adversary / scenario | Detected by | Mitigation we offer |
| --- | --- | --- |
| **Employee runs Claude Code in a personal sandbox namespace** to avoid governance. | K8s pod label/annotation absence + process indicator + (if collected) DNS to `api.anthropic.com`. | Operator-actioned attach via `cordumctl edge claude` install + managed settings via MDM (NG3: no auto-kill). |
| **Build job invokes a coding agent without going through Cordum** (e.g. `pip install anthropic && python agent.py` in a CI step). | CI workflow metadata + runner identity + (optionally) job timing and CI env-var **names**. | Operator-actioned CI workflow refactor or runner-image bake-in of `cordum-agentd` attach. |
| **MCP server deployed in-cluster without registration** with the Cordum MCP Gateway. | K8s Service with `port.name`/`port.number` matching common MCP ports + missing Cordum gateway adoption labels. | Operator-actioned MCP Gateway attach (EDGE-100 family). |
| **Direct provider SDK use** from a workload that should be routed through the LLM proxy. | Egress-policy log or proxy access log entries to `api.anthropic.com` / `api.openai.com` / `api.cohere.com` from a workload/identity not on the proxy allowlist. | Operator-actioned LLM Proxy adoption + provider-key revocation off-cordum. |
| **Managed-settings drift** on a previously governed host or pod (e.g. an admin edited the file). | EDGE-031 verifier + this detector's "previously managed, now drifted" signal (heartbeat present but managed-settings hash absent). | Operator-actioned MDM re-push (see [`managed-settings-deploy.md`](managed-settings-deploy.md) §8). |
| **Ephemeral fork-PR CI run** (legitimately unmanaged for security reasons). | Same CI signals as above. | Treated as a known-exception class (§5.4); finding marked `status=managed_skip` with `false_positive_reason=fork_pr_ephemeral`. |

The detector does **not** defend against:

- An attacker with cluster admin who edits Kubernetes API objects to
  forge Cordum labels — the design does not include cryptographic
  attestation of label authenticity. Out of scope; revisit at enforce
  mode.
- An attacker who runs an agent inside an existing governed pod (the
  governed pod's session is the evidence root; if the session is silent
  about an action, that's an action-log gap, not a shadow gap).
- An attacker who exfiltrates secrets via a non-AI channel — outside
  the shadow-AI threat surface.

---

## 5. Data minimization

EDGE-140 established the rule that redaction happens **at extraction
time**, not as a post-process filter. EDGE-143 inherits and extends that
rule.

### 5.1 What MAY be collected

| Class | Examples | Rationale |
| --- | --- | --- |
| Kubernetes object names | namespace name, pod name, deployment name, service-account name, image reference (`repo:tag@sha256:...`), owner references. | These appear in `kubectl get -o yaml` output for any caller with read-only RBAC; they are not secret. |
| Kubernetes labels and annotations | `cordum.io/edge-session-id`, `cordum.io/tenant-id`, `app.kubernetes.io/name`, `team`, `cost-center`. | Conventional metadata; already used by every observability stack. |
| Kubernetes timestamps | `creationTimestamp`, `lastTransitionTime` on conditions. | Standard scheduling metadata. |
| Pod process indicators | container image name, container `command` and `args` array entries that are **literal token matches** for known agent product names (`claude`, `codex`, `cursor`, `mcp-server`, `mcp-gateway`). | Already in PodSpec; no additional reads. |
| CI provider metadata | provider name, repository (`org/repo`), branch / ref, workflow name, job name, run id, runner identity / OIDC subject claim, runner labels, scheduled-vs-triggered indicator. | Public metadata in GitHub Actions / GitLab CI / Jenkins / Buildkite APIs. |
| CI **env-var names** | the strings `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CURSOR_API_KEY`, `MCP_*`, `CORDUM_*` — fact-of-definition only. | Same model as EDGE-140 §4. |
| Egress signals | destination hostname (SNI / CONNECT-host / DNS query), destination IP category (provider ASN), request count / first_seen / last_seen, source workload identity. | Already collected by every enterprise egress policy / proxy. |
| Hashes | SHA-256 of normalized config file content, SHA-256 of normalized endpoint URL (host + path, query stripped). | Hashes do not reverse to plaintext given salt + bounded input space; used only for dedupe. |

### 5.2 What MUST NOT be collected

The following are **forbidden** even if technically accessible:

| Class | Examples | Why forbidden |
| --- | --- | --- |
| Prompt text or transcript content. | LLM request body, assistant message body, tool input body, tool response body. | Customer data. NG1. |
| Secret values. | env-var VALUES, `Secret` resource data, signed URLs with query tokens, Authorization headers, API keys, cookies, mounted credential file contents. | NG2. |
| Full URL with query string. | `https://api.example.com/v1/x?token=...`. | Tokens travel in query strings. Always strip query. |
| Pod command-line arguments beyond literal product-name tokens. | `["claude", "--prompt", "..."]` — capture `"claude"`, never the `--prompt` value. | Args contain user input. |
| Container env-var values. | The literal value of `ANTHROPIC_API_KEY`. | NG2. |
| Mounted file contents. | The body of `/secrets/api-key`. | NG2. |
| Raw network payloads. | TCP body, TLS plaintext, JSON bodies from a proxy. | NG7. |
| Real customer source code. | `git diff` contents of a CI build. | Customer data. |

### 5.3 Defense-in-depth: extraction-time redaction

The same `RedactConfigSummary` discipline applies. Any extracted string
field passes through a regex strip of the 8 EDGE-140 secret-shape patterns
(`sk-...`, `ghp_...`, `xoxb-...`, `AKIA...`, `Bearer ...`, `eyJ...`, generic
hex-256 patterns, generic base64-32+ patterns) **before** persistence,
even though we believe the source field cannot contain such material.
Persisted strings are bounded to ≤2048 bytes; longer strings are recorded
as their SHA-256 prefix only and the finding is marked `status=partial`.

### 5.4 False positives ARE first-class

A finding with `status=managed_skip` and a populated `false_positive_reason`
is **not** a quality bug. The detector deliberately tolerates noise in
order to remain observe-mode safe. See §9 for the full controls.

---

## 6. Tenant and principal mapping

Tenant mapping is the hardest privacy question in this design because
neither a pod nor a CI run carries a Cordum tenant ID natively. The
design uses a **precedence chain**: the first source that resolves wins,
and the source itself is recorded in the finding.

### 6.1 Kubernetes tenant mapping precedence

1. **Pod label** `cordum.io/tenant-id` — explicit operator declaration.
2. **Namespace label** `cordum.io/tenant-id` — namespace-level default.
3. **Cluster ↔ tenant config** maintained by the operator in the
   detector's own config (`shadow_detector.cluster_tenants` map keyed
   by cluster name).
4. **Service-account ↔ tenant config** — operator-maintained for shared
   clusters where namespace labels are not viable.
5. **Quarantine tenant** (`cordum.shadow.quarantine`) — terminal default.

### 6.2 Kubernetes principal mapping precedence

1. **Pod label** `cordum.io/principal-id`.
2. **Pod annotation** `cordum.io/principal-id` (annotations allowed
   longer values).
3. **Service-account name** — recorded as `principal_id=sa:<namespace>:<name>`.
4. **`unknown`** — terminal default; finding remains actionable because
   `evidence_type` still names the workload.

### 6.3 CI tenant mapping precedence

1. **Signed OIDC claim from CI** — preferred. GitHub Actions OIDC subject
   (`repo:<org>/<repo>:ref:<ref>`) is matched against an operator-maintained
   `repo_tenants` map. The match is record-level: tenant is set only if
   the OIDC claim itself verifies (signature + iss + aud).
2. **`org/repo` ↔ tenant map** without OIDC — fallback for non-OIDC
   providers (Jenkins, older GitLab). Operator-maintained, audit-logged.
3. **CI workspace ↔ tenant map** — for self-hosted runners where the repo
   identifier is ambiguous.
4. **Quarantine tenant** — terminal default.

### 6.4 CI principal mapping precedence

1. **OIDC subject** `repo:<org>/<repo>:ref:<branch>` — captured verbatim
   as `principal_id`.
2. **Workflow actor** (GitHub: `github.actor`; GitLab:
   `CI_PIPELINE_USER`) — only the public username, never an email or token.
3. **`runner:<runner-id>`** — for self-hosted runners with no actor.
4. **`unknown`**.

### 6.5 Why precedence over auto-inference

Auto-inference (e.g. parsing pod image registry → tenant) is **explicitly
rejected**. Every tenant mapping must be a configured operator decision
or a cryptographically-verifiable claim, never a heuristic. The
quarantine tenant exists so that a finding without a verified tenant
still lands somewhere actionable instead of being silently dropped or
mis-attributed.

### 6.6 The tenant-mapping source field

Every finding records `tenant_source ∈ {pod_label, namespace_label,
cluster_config, sa_config, oidc_claim, repo_map, workspace_map,
quarantine}` so operators can audit attribution drift and tighten the
mapping over time.

---

## 7. Kubernetes detection signals

The detector polls the Kubernetes API server with **read-only** RBAC.
Required RBAC verbs: `list` and `watch` on `pods`, `services`,
`namespaces`, `serviceaccounts`, `deployments`, `daemonsets`,
`statefulsets`, `cronjobs`, `jobs`, and `replicasets`. No writes. No
`exec` on pods. No `proxy` subresource access.

### 7.1 Signal catalog

| Signal | Source | Maps to | Risk default |
| --- | --- | --- | --- |
| Missing Cordum heartbeat label on a pod whose container image matches a known agent image. | `pod.metadata.labels[cordum.io/edge-session-id]` absent or stale. | `evidence_type=k8s_heartbeat_missing`, `product` from image match. | medium |
| Pod with container `command` or `args` literal token in `{claude, codex, cursor, mcp-server, mcp-gateway}` and no Cordum-managed namespace label. | PodSpec scan. | `evidence_type=k8s_unmanaged_process`, `product` from token. | medium |
| Service with `port.name` ∈ `{mcp, mcp-stdio, mcp-sse, mcp-http}` and missing Cordum gateway adoption label. | `service.metadata.labels`. | `evidence_type=k8s_unmanaged_mcp_service`. | medium |
| DaemonSet / Deployment owning a pod that matches an agent image but is not on the operator's managed-workload allowlist. | OwnerReference traversal + allowlist check. | `evidence_type=k8s_unmanaged_workload`. | medium |
| Image pulled from a non-trusted registry whose name suggests an agent. | `image` string parsing (registry prefix). | `evidence_type=k8s_untrusted_agent_image`. | low |
| Namespace missing `cordum.io/tenant-id` label AND containing one or more shadow indicators. | Namespace scan + indicator aggregation. | `evidence_type=k8s_namespace_untenanted`. | low |
| Admission-controller observation log entry showing a previously-blocked pod creation attempt for an agent image (future enforce-mode signal — recorded as observation only in observe / warn). | Admission log tail (if the operator already collects). | `evidence_type=k8s_admission_observed`. | medium |
| Egress NetworkPolicy explicitly allowing direct provider traffic from a workload that is not on the LLM Proxy allowlist. | NetworkPolicy `egress.to` clauses + DNS resolution to provider categories. | `evidence_type=k8s_egress_bypass`. | high |
| Cluster inventory diff: pod present last poll, gone now, no completion event. | Inventory cache. | `evidence_type=k8s_ephemeral_indicator` — recorded but **never** auto-promoted to a finding without corroboration. | low |

### 7.2 Field mapping to `ShadowAgentFinding`

Each Kubernetes signal produces one finding with these fields populated
beyond the EDGE-140 baseline:

```jsonc
{
  // Inherited from EDGE-140:
  "tenant_id":   "...",          // §6.1
  "principal_id":"...",          // §6.2
  "hostname":    "<cluster-id>", // not the host that ran the detector
  "product":     "claude-code | codex | cursor | mcp-server | mcp-gateway",
  "evidence_type": "k8s_*",      // §7.1
  "redacted_path": "k8s://<cluster>/<namespace>/<workload-kind>/<workload-name>[/<pod-name>]",
  "redacted_config_summary": "image=<image-without-tag-or-digest>; ports=N; labels=N; ann=N",
  "risk":           "low | medium | high",
  "remediation_hint": "attach pod via cordumctl edge claude --pod ... | adopt MCP gateway | declare exception",
  "status":         "observed | managed_skip | partial",
  "observed_at":    "RFC3339",

  // EDGE-143 additions (proposal — see §11):
  "source_type":    "kubernetes",
  "source_id":      "<detector-instance-id>",
  "cluster_id":     "<operator-configured cluster name>",
  "namespace":      "<namespace>",
  "workload_kind":  "Deployment | StatefulSet | DaemonSet | Job | CronJob | Pod",
  "workload_name":  "<name>",
  "pod_uid":        "<uid or empty for workload-level findings>",
  "tenant_source":  "pod_label | namespace_label | cluster_config | sa_config | quarantine",
  "principal_source":"pod_label | pod_annotation | sa_name | unknown",
  "signal_set":     ["heartbeat_missing", "process_indicator", "untrusted_image"],
  "confidence":     0.7,
  "first_seen":     "RFC3339",
  "last_seen":      "RFC3339",
  "false_positive_reason": "",
  "exception_id":   "",
  "retention_class":"shadow_default"
}
```

### 7.3 What is NOT collected from Kubernetes

- Pod environment variable values (only Secret-reference **names** if the
  pod mounts secrets; the values are never read).
- Container command-line argument values beyond the leading token match.
- Mounted Secret bodies.
- Pod logs.
- ConfigMap bodies that contain non-schema data.
- Service endpoint URLs with query strings.
- `kubectl exec` output (the detector never execs).
- Network packet payloads.

---

## 8. CI detection signals

The detector queries each supported CI provider's read-only API. RBAC /
scopes required per provider are documented inline; nothing requires
write or admin scope.

### 8.1 GitHub Actions

- **API:** `GET /repos/{org}/{repo}/actions/workflows` and
  `/actions/runs/{run_id}/jobs`. Optional: `GET /repos/{org}/{repo}/actions/oidc/customization`.
- **Scope:** `actions:read`, `metadata:read`. No write scopes.
- **Signal catalog:**

| Signal | Detection rule |
| --- | --- |
| Workflow file references known agent tool (`anthropic-ai/claude-code-action`, `cursor-sh/...`, `openai/codex`) and the workflow run did not produce an `EdgeSession` heartbeat. | Workflow YAML parse (metadata only — `uses:` references and `run:` shell command leading-token match) cross-referenced against EDGE-141 store. |
| Workflow run uses a self-hosted runner without the Cordum runner-image label. | Runner labels in run metadata. |
| Workflow logs (if collected by the operator's existing CI logs pipeline; we never fetch them ourselves) show direct provider API calls (`api.anthropic.com`, `api.openai.com`). | Hand-off: the detector reads from the operator's pre-existing log index, never raw log content. Match on hostname pattern only. |
| Repo allowlist includes the agent action but the action invocation does not pass `CORDUM_*` env. | Workflow YAML parse + env-name set comparison. |
| Fork PR or `pull_request_target` event without OIDC claim. | Run event metadata. Recorded with `false_positive_reason=fork_pr_ephemeral`. |
| Scheduled bot run (dependabot, renovate). | Actor identity. Recorded with `false_positive_reason=automation_bot`. |

### 8.2 GitLab CI

- **API:** `GET /projects/{id}/pipelines` and `/jobs`. Token requires
  `read_api` scope.
- **Signals:** project / pipeline / job metadata, runner identity, repo
  branch / tag. Variable **names** only; variable values are masked by
  GitLab itself in API responses, but we additionally never request the
  variables endpoint with `unmask=true`.

### 8.3 Jenkins

- **API:** `GET /job/<name>/api/json?depth=1`. Token requires read
  permission on the job.
- **Signals:** pipeline definition (Jenkinsfile leading-token scan),
  build-cause user, executor identity, last-build status. Console
  output is **not** fetched.

### 8.4 Buildkite / CircleCI

- Similar shape: read-only API, pipeline metadata only, no log bodies.
- Each provider's detector module declares the exact endpoints and
  scopes it uses in the implementation task.

### 8.5 What is NOT collected from CI

- Job log bodies (we use the operator's existing log index if any, via
  hostname/category pattern queries only; we never persist log lines).
- Secret variable values (CI providers mask them on the wire; we also
  do not request unmasked endpoints).
- Source code (`git` archive endpoints).
- Customer build artifacts.
- Workflow run inputs that are user-supplied free text.

### 8.6 Tenant attribution under fork / ephemeral conditions

Fork PRs, scheduled bot runs, and `pull_request_target` events on
unknown forks are tagged with `false_positive_reason ∈ {fork_pr_ephemeral,
automation_bot, fork_unknown_actor}` and routed to the quarantine tenant
unless an explicit operator allowlist promotes them. This satisfies G7
without dropping the signal entirely.

---

## 9. Direct provider traffic signals and privacy boundary

Direct provider traffic is the strongest signal that an unmanaged agent
is running, but it is the most privacy-sensitive. Defaults are deliberately
narrow.

### 9.1 What MAY be observed

| Signal | Source | Privacy posture |
| --- | --- | --- |
| Egress destination **hostname** (SNI / CONNECT host / DNS query name). | Operator's existing egress proxy / NetworkPolicy logs / DNS query log. | Already collected by every enterprise; we only add **categorization**, not capture. |
| Destination **category** (`provider:anthropic`, `provider:openai`, `provider:cohere`, `gateway:cordum_llm_proxy`, `gateway:cordum_mcp`). | Hostname → category map maintained by the detector. | Bounded label set; no high-cardinality data. |
| Request **count**, **first_seen**, **last_seen**, **bytes_sent_bucket** (`<1KB`, `<10KB`, `<100KB`, `>=100KB`). | Aggregated from the same log source. | Counts only; no payload, no per-request timestamp series beyond bucket boundaries. |
| Source workload identity. | Kubernetes pod identity, CI runner identity, OIDC subject. | Same precedence chain as §6. |
| **Normalized endpoint hash** (SHA-256 of `<scheme>://<host><path>` with query stripped). | Aggregated. | Dedupe key; no plaintext URL persisted. |

### 9.2 What MUST NOT be observed (default)

| Forbidden | Reason |
| --- | --- |
| Raw HTTP request bodies. | NG1 / NG7. |
| Raw HTTP response bodies. | NG1 / NG7. |
| Authorization headers, cookies, API keys, bearer tokens. | NG2. |
| Full URLs with query string. | Query strings carry tokens. |
| Packet payloads. | NG7. |
| TLS-decrypted traffic. | TLS interception is **not enabled by default** and requires a separate ADR. |

### 9.3 Artifact pointer for externally-supplied summaries

If an operator wants to attach a richer summary (e.g. a pre-existing
SIEM-generated weekly report on direct provider traffic), the detector
accepts an **artifact pointer** (`sha256`, `uri`, `content_type`,
`retention_class`, `redaction_level`) following the Edge artifact-pointer
contract ([`README.md`](README.md) §"Data hierarchy"). The summary body
is **not** persisted in Redis; only the pointer.

Before persistence, the detector verifies that the redaction_level is
`level3_pseudonymized` or stricter, and rejects any pointer with
`level0_raw` or `level1_lightly_redacted`.

### 9.4 Verifying redaction before persistence

For every string field captured from §7 / §8 / §9.1, the detector runs
the EDGE-140 secret-shape regex strip and length cap **immediately
after extraction**, never lazily. The Redis writer asserts the same
properties (length cap, regex match) as a final guard so a future
refactor cannot silently relax the contract.

---

## 10. API and store deltas beyond local `ShadowAgentFinding`

EDGE-141 ships the local-scope finding store. EDGE-143 proposes the
following **additive** changes. These are proposals only; landing them
requires (a) EDGE-141 to be in DONE state, and (b) explicit governor /
human signoff per §16.

### 10.1 New fields on `ShadowAgentFinding`

| Field | Type | Cardinality | Indexed | Notes |
| --- | --- | --- | --- | --- |
| `source_type` | enum (`local`, `kubernetes`, `ci`, `network`) | very low | yes | Joins this finding to the detector family that produced it. Backward-compatible default for existing rows: `local`. |
| `source_id` | string ≤128 | medium | yes | Detector instance identifier; useful for ops dashboards filtering by which collector emitted what. |
| `cluster_id` | string ≤64 | medium | yes | Operator-configured K8s cluster name. Empty for non-K8s sources. |
| `namespace` | string ≤63 (K8s rules) | medium | yes | K8s namespace. Empty for non-K8s sources. |
| `workload_kind` | enum (K8s kinds + `none`) | low | yes | Empty for non-K8s sources. |
| `workload_name` | string ≤253 | high | no | Joined with `cluster_id`+`namespace` for K8s detail queries. |
| `pod_uid` | UUID ≤36 | high | no | Optional; populated when the finding pins a specific pod. |
| `ci_provider` | enum (`github_actions`, `gitlab_ci`, `jenkins`, `buildkite`, `circleci`, `other`) | very low | yes | Empty for non-CI sources. |
| `repo` | string ≤256 (`org/repo`) | high | yes (composite with `ci_provider`) | CI source. |
| `ref` | string ≤256 | high | no | branch/tag/ref. |
| `workflow_id` | string ≤128 | high | no | CI workflow identifier. |
| `job_id` | string ≤128 | high | no | CI job identifier. |
| `run_id` | string ≤128 | high | no | CI run identifier. |
| `runner_id` | string ≤128 | medium | no | Self-hosted vs hosted distinguisher. |
| `tenant_source` | enum (§6.1 / §6.3) | low | yes | Audit trail for attribution decisions. |
| `principal_source` | enum (§6.2 / §6.4) | low | yes | Same. |
| `signal_set` | string-set ≤16 entries | medium | yes (any-of filter) | Bounded enum from §7.1 / §8.x; supports "find all heartbeat-missing findings". |
| `confidence` | float [0,1] | low | yes (range filter) | Detector's self-rated confidence; observe mode prefers high recall over high precision. |
| `first_seen` | RFC3339 | high | yes (range) | Distinct from `observed_at`. |
| `last_seen` | RFC3339 | high | yes (range) | Updated on every re-observation. |
| `false_positive_reason` | enum (§5.4) | low | yes | Empty unless `status=managed_skip`. |
| `exception_id` | string ≤64 | medium | yes | Joins to operator-defined exception declarations (see §10.3). |
| `retention_class` | enum (`shadow_default`, `shadow_short`, `shadow_long`) | very low | no | Mirrors artifact-pointer retention semantics; default 90 days for K8s/CI findings, 30 days for ephemeral signals. |

### 10.2 New filters on `GET /api/v1/edge/shadow/findings`

Beyond EDGE-141's `tenant`, `status`, `risk`, the new query parameters:

```
source_type=kubernetes|ci|network|local
cluster_id=<id>
namespace=<ns>
ci_provider=github_actions|...
repo=<org/repo>
signal=<signal_name>            (any-of; repeatable)
confidence_min=<float>          (default 0)
first_seen_after=<RFC3339>
last_seen_before=<RFC3339>
exception_id=<id>
include_managed_skip=true|false (default false)
```

All filters apply at the Redis-index layer using sorted-set indexes keyed
by the indexed fields in §10.1; no in-memory full-scan.

### 10.3 New API: exception declarations

```
POST /api/v1/edge/shadow/exceptions
GET  /api/v1/edge/shadow/exceptions
DELETE /api/v1/edge/shadow/exceptions/{exception_id}
```

An exception is an operator-signed declaration that a matching finding
(by tenant + source filters) is intentional. The store joins exceptions
to findings via `exception_id` and elevates matching findings to
`status=managed_skip` with `false_positive_reason=operator_exception`.
Exceptions carry `expires_at` (max 90 days; longer requires re-affirmation)
and `created_by` (auditable principal). The detector never auto-creates
exceptions.

### 10.4 Backward-compatibility for existing local findings

Existing `ShadowAgentFinding` rows written by EDGE-140 / EDGE-141 do not
have the new fields. Migration:

- `source_type` defaults to `"local"` on read.
- All other new fields default to empty string / null on read.
- No Redis migration job is needed; the store reads back legacy rows
  unchanged, and rewrites them with defaults only when the status
  transitions.

### 10.5 Indexes and retention sweeper updates

The Redis layout extends (proposed; final naming subject to EDGE-141
review):

```text
edge:shadow:finding:<finding_id>             finding JSON (existing)
edge:shadow:index:tenant:<tenant_id>         sorted set (existing)
edge:shadow:index:source:<source_type>       sorted set
edge:shadow:index:cluster:<cluster_id>       sorted set
edge:shadow:index:repo:<provider>:<org/repo> sorted set
edge:shadow:index:signal:<signal_name>       sorted set
edge:shadow:index:exception:<exception_id>   sorted set
edge:shadow:exception:<exception_id>         exception JSON
```

Retention classes (§10.1) drive a sweeper analogous to EDGE-141's:

| retention_class | TTL default | Use |
| --- | --- | --- |
| `shadow_short` | 7 days | Ephemeral CI signals, fork PR signals, pod-lifecycle indicators. |
| `shadow_default` | 90 days | Standard K8s / CI / network findings. |
| `shadow_long` | 365 days | High-risk findings or operator-pinned. |

Configurable via `CORDUM_EDGE_SHADOW_RETENTION_*` env vars (positive
durations; 0 / negative fail at startup, per the EDGE-141 convention).

---

## 11. Rollout modes

Three modes, in order of permissiveness, with explicit per-mode behavior.

### 11.1 Observe mode (default)

- Detector runs on its configured cadence.
- Findings written to the store.
- Audit events emitted (`shadow_agent.detected`,
  `shadow_agent.resolved`, `shadow_agent.exception_applied`).
- **No notifications.** **No operator tasks.** **No cluster mutation.**
- This is the only mode that ships in the first EDGE-143 implementation
  wave.

### 11.2 Warn mode

- Everything observe mode does, plus:
- For each `risk=high` finding without a matching exception, the
  detector emits a Slack / email / PagerDuty webhook **to the
  operator's existing notification pipeline** — we do not build a new
  notification surface. Endpoint URLs are operator-configured.
- For findings flagged `requires_action=true` (a new optional field
  set by the remediation generator when an attach path is automatable),
  the detector files an operator-facing task in the operator's existing
  ticket system via webhook.
- **Still no cluster mutation.** Warn mode is informational, not
  enforcing.

### 11.3 Enforce mode (future, ADR-gated)

- **NOT implemented by EDGE-143 or any of its immediate follow-up
  tasks.** Documented here only to scope it out.
- Hypothetical capabilities: deny new pods matching a shadow signature
  via a `ValidatingAdmissionWebhook`; revoke a CI runner registration;
  rotate a provider API key via the operator's secret manager.
- Each of those needs:
  1. A dedicated ADR.
  2. A signed operator allowlist (which finding signatures may trigger
     which actions).
  3. An always-on operator preview surface so no enforce action runs
     without explicit human confirmation per fleet.
  4. A rollback plan tied to managed-settings rollback ([§8 of
     `managed-settings-deploy.md`](managed-settings-deploy.md)).

### 11.4 Mode transitions

- Default is observe; transitions are operator-driven only.
- Changing modes is recorded as an audit event with the actor and the
  reason.
- Warn → enforce is **gated by the ADR above**, regardless of
  configuration. The detector refuses to start in enforce mode unless
  the ADR-acceptance config flag is present **and** the operator
  allowlist is non-empty.

---

## 12. Remediation model and runbooks

EDGE-142 owns the remediation-template engine. EDGE-143's contribution
is template **inputs** — i.e. for each new K8s / CI / network signal,
what remediation classes are appropriate.

### 12.1 Remediation classes

| Class | When | Action shape |
| --- | --- | --- |
| `attach_mcp_gateway` | K8s service or pod with unmanaged MCP. | Operator-facing one-liner: `cordumctl mcp gateway attach --kind <kind> --namespace <ns> --name <name>`. The CLI does not execute; it generates a manifest patch the operator applies. |
| `attach_edge_session` | K8s pod with unmanaged Claude/Codex/Cursor invocation. | Operator-facing: bake `cordum-agentd` into the workload image + redeploy with managed settings via MDM. Pre-existing playbook: [`managed-settings-deploy.md`](managed-settings-deploy.md). |
| `deploy_managed_settings` | Heartbeat present but managed-settings hash absent. | Operator-facing MDM rollout step (Jamf / Intune / Ansible) per [`managed-settings-deploy.md`](managed-settings-deploy.md). |
| `route_via_llm_proxy` | Direct provider traffic observed from an unproxied workload. | Operator-facing: configure egress NetworkPolicy + provider-token rotation + workload env update to point at LLM proxy. Multi-step; the remediation generator outputs a checklist, not a script. |
| `register_ci_workflow` | CI workflow uses an agent action without Cordum env. | Operator-facing: PR template against the workflow file adding `CORDUM_*` env + `cordum-agentd` adapter. The detector NEVER opens the PR itself. |
| `declare_exception` | Operator decides the finding is intentional. | API call to `POST /api/v1/edge/shadow/exceptions` (§10.3). |
| `resolve_manually` | Operator has remediated out-of-band. | `POST /api/v1/edge/shadow/findings/{id}/resolve` from EDGE-141. |

### 12.2 Destructive action gates

Every remediation that **mutates** customer state requires:

- A preview surface — the detector outputs the proposed change as a
  diff before any apply step.
- A backup or change-ticket reference — the operator records a backup
  / change ticket id in the remediation request; the detector echoes it
  into the audit event.
- An idempotent apply — re-running the apply step is a no-op when the
  desired state is already reached.

The detector **MUST NOT** auto-delete config files, auto-kill processes,
auto-revoke tokens, or auto-modify cluster state. Every destructive
action passes through the operator.

---

## 13. Observability and audit

### 13.1 Metrics (proposed names — final naming with EDGE-141 / observability owner)

| Metric | Type | Labels | Source |
| --- | --- | --- | --- |
| `cordum_edge_shadow_findings_total` | counter | `source_type`, `risk`, `status` | One increment per finding write (mirrors PRD §17.1). |
| `cordum_edge_shadow_findings_active` | gauge | `source_type`, `tenant_present` | Periodic re-derivation from the store. |
| `cordum_edge_shadow_detector_poll_duration_seconds` | histogram | `source_type` | Per detector run. |
| `cordum_edge_shadow_detector_failures_total` | counter | `source_type`, `reason_code` | Detector run failed; bounded reason codes (`k8s_api_unavailable`, `ci_api_rate_limited`, `auth_failed`, `redacted`). |
| `cordum_edge_shadow_exceptions_active` | gauge | none | Operator-declared exceptions in effect. |
| `cordum_edge_shadow_remediations_emitted_total` | counter | `class` | One increment per remediation-template emit. |

All labels stay within the bounded-label discipline of
[`observability.md`](observability.md) §"Bounded label sets". `tenant_id`,
`pod_uid`, `repo`, and `run_id` are **never** label values; they live in
finding records and audit `extra` fields.

### 13.2 Audit events

Reuse existing `audit.SIEMEvent` pipeline:

| Event type | When | Severity |
| --- | --- | --- |
| `edge.shadow_finding_created` | New finding lands in the store. | `INFO` for low, `MEDIUM` for medium, `HIGH` for high. |
| `edge.shadow_finding_status_changed` | Operator resolves / ignores / restores. | `INFO`. |
| `edge.shadow_exception_created` / `_deleted` | Operator manages exceptions. | `MEDIUM`. |
| `edge.shadow_detector_mode_changed` | observe ↔ warn (enforce gated separately). | `HIGH`. |
| `edge.shadow_detector_degraded` | Detector cannot poll source (API down). | `MEDIUM`. |

Safe `extra` fields per builder (mirrors [`observability.md`](observability.md)):

| Builder | Allowed `extra` keys |
| --- | --- |
| `shadowFindingExtra` | `finding_id`, `source_type`, `source_id`, `cluster_id`, `namespace`, `workload_kind`, `ci_provider`, `repo`, `signal_set`, `confidence`, `tenant_source`, `principal_source`. |
| `shadowExceptionExtra` | `exception_id`, `created_by`, `expires_at`. |
| `shadowDetectorExtra` | `source_type`, `reason_code`, `degraded_at`. |

Forbidden in `extra` (same as [`observability.md`](observability.md)):
`raw_prompt`, `raw_tool_input`, `secret_token`, env-var values, full URLs
with queries, pod env, container args beyond product-name tokens,
authorization headers.

### 13.3 Structured logs

`shadow_detector` slog component, attributes use the
`core/edge/observability.go` helpers (`EventLogAttrs`-style) with new
`ShadowFindingLogAttrs` / `ShadowExceptionLogAttrs` builders. Bounded
fields only.

---

## 14. False-positive controls

This table is the operator's reference for tuning the detector.

| Likely false positive | Control |
| --- | --- |
| Approved exception (e.g. a security research pod that legitimately runs Claude). | `POST /api/v1/edge/shadow/exceptions` with finding selector; status flips to `managed_skip`. |
| Allowlisted namespace (entire namespace exempt by operator policy). | Namespace-level config `shadow_detector.namespace_allowlist: ["security-research"]`. |
| Repo test fixtures that mock an agent. | `false_positive_reason=test_fixture` derived from operator-supplied repo path glob. |
| Ephemeral fork PR. | Auto-detected; routed to quarantine tenant with `false_positive_reason=fork_pr_ephemeral`. |
| Managed but late heartbeat (network blip). | Detector waits N consecutive polls (default 3) before promoting to `heartbeat_missing`. |
| Local dev on a corporate-network bastion. | Optional bastion-IP allowlist on the network signal source. |
| Telemetry gaps (egress proxy logs missing for a window). | Detector records `cordum_edge_shadow_detector_failures_total{reason_code=telemetry_gap}` and does NOT emit new findings during the gap (avoids false-negative spikes). |
| Direct provider traffic from a non-agent workload (e.g. a translation microservice). | Operator-maintained `workload_allowlist` keyed by service-account or workload selector. |
| Renovate / Dependabot / scheduled bots. | Actor-based exclusion list; default includes `dependabot[bot]`, `renovate[bot]`, `cordum-bot`. |
| Same finding rediscovered every poll. | Dedupe by `(source_type, source_id, cluster_id, namespace, workload_name, signal_set hash)`; existing rows update `last_seen` only. |

---

## 15. Security and privacy review checklist

This checklist must be ticked off in the implementation task PR for any
follow-up to EDGE-143.

- [ ] Tenant mapping precedence chain matches §6; quarantine tenant is
      terminal default.
- [ ] No payload capture (NG1) — code grep confirms zero `request_body`,
      `response_body`, `prompt`, `tool_input`, `transcript` extractions.
- [ ] No secret value capture (NG2) — code grep confirms env-var values,
      Secret data, Authorization headers, full URLs with query strings
      are never read.
- [ ] No customer cluster mutation (NG3 / NG6) — code grep confirms no
      `kubectl apply`, `kubectl delete`, `kubectl patch`, `kubectl exec`,
      no `MutatingWebhookConfiguration` creation, no Job killer.
- [ ] All string fields persisted to Redis ≤2048 bytes; longer strings
      degrade to SHA-256 prefix + `status=partial`.
- [ ] All string fields pass the EDGE-140 secret-shape regex strip
      **before** persistence; the store layer asserts the same on write.
- [ ] Audit events emitted for every finding lifecycle change.
- [ ] Metric label cardinality bounded; no tenant / pod / repo / run as
      label values.
- [ ] Exception API requires authenticated principal; default expires_at
      ≤90 days; renewals are auditable.
- [ ] Regional / sovereignty: detector respects the tenant's regional
      bucket; cross-region writes refused.
- [ ] Access control: `read:shadow_findings`, `write:shadow_exceptions`,
      `manage:shadow_detector_mode` are distinct permissions (RBAC
      lives in `core/controlplane/gateway/auth/rbac.go`); enforce mode
      requires `manage:shadow_detector_enforce` which is unassigned by
      default.
- [ ] Rate limits: ingest API mirrors EDGE-141's rate-limit envelope.
      Detector polls back off on `429`.
- [ ] Retention sweeper deletes findings past `retention_class` TTL and
      records `cordum_edge_shadow_findings_swept_total`.
- [ ] All detector RBAC scopes documented (§7, §8) and provisioned via
      least-privilege ServiceAccount / token.
- [ ] PR template includes a checklist line: "no new metric label
      carries tenant / pod / repo / run".

---

## 16. Open questions requiring human signoff

> **Resolution callout (binding ADR-style ruling).** Q1–Q8 below were
> resolved by `governor-8964b81b` via `comment-a17f4f1c` on
> `task-de50a293` (2026-05-17, per Yaron directive "investigated deeply
> and decided"). The original open-questions text is preserved below
> for historical context; the inline resolutions are the current
> binding decisions for `§17` follow-up task scoping. Architects may
> override any single resolution via a formal counter-comment.
>
> - **Q1 (K8s tenant-mapping source of truth):** **RATIFY** the `§6.1`
>   5-tier label-first precedence (pod label → namespace label →
>   cluster config → SA config → quarantine-terminal).
> - **Q2 (lawful network metadata for the default configuration):**
>   **APPROVE** the `§9.1` catalog as the global default, and add a
>   future `CORDUM_EDGE_SHADOW_PII_MODE=pseudonymize|hash|drop` flag
>   (default `pseudonymize`) so `principal_id` from `github.actor` is
>   GDPR/UK-DPA-safe; `managed-settings-deploy.md` adds a new GDPR /
>   UK-DPA processing-record template.
> - **Q3 (default retention class):** **RATIFY** `shadow_default=90d`
>   (matches Cordum's existing 90-day audit default), with
>   `shadow_short=7d` for ephemeral CI and `shadow_long=365d` for
>   high-risk findings.
> - **Q4 (warn / enforce-mode ship gates):** **STAGED.** Observe ships
>   in `EDGE-143` wave 1 (already designed); warn ships in wave 2 only
>   after three preconditions (≥1 customer on observe ≥30d with
>   reviewable findings, signed `acknowledge_warn_mode` opt-in, and
>   `§14` false-positive controls tuned to FP rate <5% on
>   `risk=high`); enforce remains parked indefinitely under the
>   per-action ADR discipline in `§11.3`.
> - **Q5 (enforce-mode ADR scope):** **RATIFY** `§11.3` as-is. The
>   4-item gate (ADR + allowlist + preview + rollback) is correct
>   discipline for irreversible cluster mutation; the hypothetical
>   admission-webhook / runner-deregister / token-rotation capabilities
>   stay scope-out, and no `EDGE-143` follow-up ships any enforce
>   action.
> - **Q6 (OIDC trust roots for CI tenant mapping):** **Cordum ships
>   defaults** for GitHub Actions (`https://token.actions.githubusercontent.com`)
>   and GitLab.com SaaS (`https://gitlab.com`); self-hosted GitLab,
>   Jenkins, Buildkite, and CircleCI are operator-supplied via
>   `CORDUM_EDGE_SHADOW_OIDC_TRUST_<provider>` and
>   `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<provider>` env-vars (set to
>   `disabled` to refuse OIDC for that provider and fall back to the
>   `§6.3` tier-2 path).
> - **Q7 (cross-cluster federation):** **STORE-LEVEL** federation, not
>   detector-level. One Cordum tenant = one logical finding collection;
>   per-cluster detectors emit to the same tenant-scoped Redis index,
>   and the `§10.5` `edge:shadow:index:cluster:<cluster_id>` secondary
>   index handles cross-cluster slicing. No new federation protocol or
>   detector-to-detector auth — Redis is the single source of truth.
> - **Q8 (exception-API step-up auth):** **RATIFY** step-up
>   (re-MFA / signed admin token) for `risk=high` exception creation;
>   regular auth for `risk=medium|low`. Audit event
>   `shadow_agent.exception_applied` records both actor and step-up
>   factor used.

The original open questions are preserved below for historical
context. These questions blocked follow-up implementation tasks before
the `comment-a17f4f1c` ruling above; consult the resolution callout
for the binding answers.

1. **Source of truth for tenant mapping in K8s** — operator-maintained
   config map vs. cluster-resident CR vs. label-only. The design favors
   label-first with config-map fallback; needs confirmation per
   customer profile.
2. **Which network metadata is lawful and acceptable for the default
   configuration**, given regional privacy regimes (EU / Germany / UK /
   APAC). The design assumes operators have pre-existing legal cover
   for egress logging; we never introduce new collection.
3. **Default retention class** for K8s / CI findings — proposal: 90
   days; needs confirmation from compliance.
4. **Whether and when warn mode may ship** — proposal: warn mode lands
   in the second EDGE-143 follow-up wave, gated behind operator
   acknowledgment. Needs product / security signoff.
5. **Enforce-mode ADR scope** — confirm what enforce actions are even
   on the table for future debate (admission webhook? runner deregister?
   token rotation?). The design says **all** require their own ADR and
   none are planned by this task.
6. **OIDC trust roots** for CI tenant mapping — operator supplies
   trusted issuers? Cordum ships defaults for GitHub / GitLab? Affects
   the `tenant_source=oidc_claim` precedence credibility.
7. **Cross-cluster federation** — for customers with N clusters and 1
   Cordum tenant, do we federate ingest at the detector or at the
   store? Affects §10.5 indexes and §11 mode-flip granularity.
8. **Exception-API auth** — should exception creation require step-up
   auth (re-MFA)? Default proposal: yes for `risk=high` exception
   creation, regular auth otherwise.

---

## 17. Proposed follow-up tasks

These are **proposals** only. No tasks are created by this design doc;
that happens after human signoff per §16.

| Proposed task | Scope | Depends on |
| --- | --- | --- |
| EDGE-143.1 | Implement the Kubernetes detector module (observe mode only): K8s client, signal extractors per §7, finding emit through EDGE-141 ingest API, tenant mapping per §6.1 / §6.2, false-positive controls per §14. | EDGE-141 DONE; ADR confirming §16 Q1, Q3, Q5. |
| EDGE-143.2 | Implement the GitHub Actions CI detector module (observe mode only): API client, signal extractors per §8.1, tenant mapping per §6.3 / §6.4. | EDGE-141 DONE; EDGE-143.1 finding-emit pattern reused. |
| EDGE-143.3 | Implement the GitLab CI / Jenkins / Buildkite / CircleCI CI detector modules. | EDGE-143.2 contract. |
| EDGE-143.4 | Implement the network-signal aggregator that reads operator-supplied egress logs / proxy access logs. | EDGE-141 DONE; ADR confirming §16 Q2. |
| EDGE-143.5 | Extend the EDGE-141 store with the §10.1 fields, §10.2 filters, §10.5 indexes. | EDGE-141 DONE; ADR confirming §10 deltas. |
| EDGE-143.6 | Implement the §10.3 exception API + audit events from §13.2. | EDGE-143.5. |
| EDGE-143.7 | Extend the EDGE-142 remediation generator with cluster-scope / CI-scope templates from §12.1. | EDGE-142 DONE. |
| EDGE-143.8 | Add `--shadow-cluster` / `--shadow-ci` flags to `cordumctl edge doctor` for operator preview (read-only, no persistence). | EDGE-143.1 + EDGE-143.2. |
| EDGE-143.9 | Implement warn mode (§11.2) including the operator-pipeline webhook out. | EDGE-143.5 + product signoff. |
| EDGE-143.10 | Dashboard surface for the unified `ShadowAgentFinding` view across `local | kubernetes | ci | network`. | Separate dashboard ADR; this design intentionally ships **no** dashboard work. |

---

## 18. Adversarial self-review notes

Recorded during the design phase so future readers can see what was
considered and discarded.

- **"Why not deploy a DaemonSet?"** — Considered and rejected for the
  default detector. A cluster-resident collector reads pod metadata
  with no more privilege than the K8s API server already grants
  read-only clients, but it introduces a customer-cluster code
  surface that the customer has to trust and maintain. The default
  detector runs outside the customer cluster and uses read-only API
  access; if a customer asks for a DaemonSet variant later, that is
  its own ADR.
- **"Why not parse pod logs?"** — Logs contain prompt and tool output
  bodies (NG1). Even with regex stripping, the false-negative risk on
  unknown formats is too high for default behavior. If an operator's
  existing log index produces a pre-redacted summary, the artifact
  pointer in §9.3 is the right entry point.
- **"Why are env-var **names** acceptable but **values** not?"** —
  Names are bounded enums maintained by the operator's tooling and
  contain no customer data. Values are arbitrary strings provided by
  the customer's secret manager; reading them at all creates
  exfiltration risk and trust-store complexity.
- **"Why a quarantine tenant rather than dropping unmappable
  findings?"** — Dropping silently hides shadow activity; that
  defeats the detector. Mis-attributing actively misleads operators
  and creates cross-tenant blast radius. The quarantine tenant is a
  single tenant that the operator inspects on a fixed cadence to
  tighten the mapping config; it cannot leak data into the wrong
  tenant.
- **"What about workload identity attestation (SPIFFE / SPIRE)?"** —
  Out of scope for observe mode. If an operator already runs SPIRE,
  the SPIFFE ID is a strong tenant-mapping signal and could be added
  to the §6.1 precedence chain in a future revision.
- **"Could enforce mode use Gatekeeper?"** — The future ADR may take
  that route; this design notes only that **any** enforce path needs
  the full §11.3 gate (ADR, allowlist, preview, rollback).

---

## 19. Glossary

| Term | Meaning |
| --- | --- |
| Shadow agent | An AI coding agent or MCP server running outside Cordum governance. |
| Cluster scope | Kubernetes pods, services, and workloads. |
| CI scope | GitHub Actions, GitLab CI, Jenkins, Buildkite, CircleCI jobs. |
| Network scope | Direct provider traffic visible via egress proxy / NetworkPolicy / DNS / SNI. |
| Quarantine tenant | Terminal default tenant for findings whose tenant cannot be verified. |
| Managed-skip | A finding the detector recognises as intentionally outside scope (managed-by-Cordum signature, operator exception, ephemeral fork PR, etc.) and does not surface as an alert. |
| Exception | Operator-signed declaration that a finding is intentional; expires; auditable. |
| Source type | `local | kubernetes | ci | network` — which detector family produced the finding. |
| Signal | Bounded enum naming the detection rule that fired (`heartbeat_missing`, `process_indicator`, `egress_bypass`, ...). |

---

## 20. References

- PRD §6.4 Shadow Agent Detection, §10.6 Shadow-agent APIs, §13.7
  Shadow Agents page, §18 Shadow Agent Detection requirements.
- [`shadow-scanner.md`](shadow-scanner.md) — EDGE-140 local scanner.
- [`managed-settings-deploy.md`](managed-settings-deploy.md) — EDGE-031 fleet rollout playbook.
- [`cordum-agentd.md`](cordum-agentd.md) — local session lifecycle.
- [`observability.md`](observability.md) — metric / log / audit conventions.
- [`retention.md`](retention.md) — Edge Redis retention model.
- [`README.md`](README.md) — Edge product overview and OSS/enterprise boundary.
- ADR-010 — Edge P0 architecture decisions (Shadow Agents P0 descope).
