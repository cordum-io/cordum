# Shadow Agent Remediation (EDGE-142)

Cordum Edge's shadow-agent surface produces redacted findings via the
EDGE-140 scanner and the EDGE-141 finding store. EDGE-142 adds a
deterministic **remediation generator** that maps a finding to
advisory guidance: classification, recommended action, audience-aware
steps, and machine-readable command/API request placeholders.

**Advisory only.** The generator does not execute. It does not enqueue
Cordum Jobs. It does not mutate finding state. It does not call the
Safety Kernel. Operators copy + parameterise the suggested commands
and run them under their own MDM or local-admin authority.

See also: [shadow-agent-findings.md](shadow-agent-findings.md) for the
EDGE-141 finding lifecycle, [shadow-scanner.md](shadow-scanner.md) for
the local opt-in scanner, and
[managed-settings-deploy.md](managed-settings-deploy.md) for the MDM
deployment workflow that the enterprise audience steps reference.

## Classification

The classifier folds two finding shapes onto a single feature
projection — both EDGE-141 lifecycle records
(`/api/v1/edge/shadow-agents/{finding_id}`) and EDGE-140 scanner
observations (`cordumctl shadow scan` JSONL output) are accepted.

Priority order (most-specific first; K8s + CI scope rows are checked
before generic signals so a Kubernetes or CI finding never decays to
the local-wrapper path):

| Signal / hint                                                         | Action kind                       |
|-----------------------------------------------------------------------|-----------------------------------|
| `signal_set` contains `namespace_untenanted`                          | `apply_tenant_label`              |
| `signal_set` contains `unmanaged_workload`                            | `adopt_unmanaged_workload`        |
| `signal_set` contains `untrusted_agent_image`                         | `rebase_agent_image`              |
| `signal_set` contains `egress_bypass`                                 | `extend_egress_policy`            |
| `signal_set` contains `missing_cordum_attach`                         | `add_cordum_edge_attach`          |
| `signal_set` contains `unmanaged_oidc`                                | `configure_oidc_trust`            |
| `signal_set` contains `direct_provider_endpoint` AND `source_type=ci` | `route_ci_sdk_through_proxy`      |
| `signal_set` contains `direct_provider_url`                           | `route_through_llm_proxy`         |
| `signal_set` contains `k8s_heartbeat_missing` or evidence=`heartbeat` | `run_edge_doctor`                 |
| `signal_set` contains `unmanaged_mcp_server`                          | `attach_mcp_gateway`              |
| `signal_set` contains `unmanaged_claude_settings`                     | `use_cordumctl_edge_claude`       |
| `signal_set` contains `unmanaged_process` or evidence=`process_name`  | `investigate_process`             |
| evidence=`environment_var`                                            | `route_through_llm_proxy`         |
| evidence=`config_file` + path matches `mcp.json` / `/mcp/`            | `attach_mcp_gateway`              |
| evidence=`config_file` + path matches `~/.claude/`, `~/.cursor/`, `~/.codex/` | `use_cordumctl_edge_claude` |
| no match                                                              | `manual_review`                   |

When `audience=enterprise` is requested AND the natural choice would
have been `use_cordumctl_edge_claude` (a dev-wrapper recommendation),
the resolver swaps in `deploy_managed_settings` so the plan surfaces
the MDM path rather than the per-developer wrapper.

## Audiences

| Audience      | Wording                                                                          |
|---------------|----------------------------------------------------------------------------------|
| `dev`         | Local-wrapper steps (`cordumctl edge claude`, `cordumctl edge doctor`).          |
| `enterprise`  | MDM-driven steps (`cordumctl edge managed-settings export/verify`).              |
| `both`        | Layered: dev steps first, enterprise steps second (default).                     |

Severity mirrors the source finding's risk: `low` → `low`, `medium` →
`medium`, `high|critical` → `high`. Severity is the plan-level scale;
consumers needing `critical` should surface it from the source finding
directly.

## Placeholders

Every command in every step uses literal placeholders. The generator
never substitutes live values — operators must replace placeholders
locally:

| Placeholder                              | Source                                            |
|------------------------------------------|---------------------------------------------------|
| `<gateway-url>`                          | Cordum Gateway base URL for the tenant.           |
| `<tenant-id>`                            | Cordum tenant id (matches `X-Tenant-ID`).         |
| `<principal-id>`                         | Operator principal initiating remediation.        |
| `<output-dir>`                           | Path for `managed-settings.json` + `managed-mcp.json`. |
| `<llm-proxy-url>`                        | Cordum LLM proxy base URL.                        |
| `<api-key-helper-command>`               | Local credential-helper script path.              |
| `<unmanaged-config-path>`                | Absolute path to the unmanaged config file.       |
| `<path-to-managed-settings.json>`        | Verified path used by `managed-settings verify`.  |
| `<path-to-managed-mcp.json>`             | Verified MCP payload path.                        |
| `<finding-id>`                           | Finding identifier for API-shaped steps.          |

The generator routes every finding-derived string (product name,
signal labels) through `shadow.stripSecretMarkers` and a
printable-ASCII filter so a malicious uploader cannot inject `sk-…`
tokens, bearer headers, or terminal escapes into the operator-facing
output.

## Backup, preview, destructive flags

Steps that disable or remove unmanaged configuration are always
emitted with all three safety flags set:

```
preview_only=true   requires_backup=true   destructive=true
```

The generator emits a backup step ahead of any disable step so the
ordered list reads `backup → preview → rename`. Dashboards and the
CLI render the flags so operators see the gate at every render
surface.

## Gateway API

```
POST /api/v1/edge/shadow-agents/{finding_id}/remediation
X-Tenant-ID: <tenant-id>
Content-Type: application/json

{
  "audience": "dev|enterprise|both",
  "omit_commands": false
}
```

Response (200 OK):

```json
{
  "finding_id": "edge_shadow_…",
  "tenant_id": "tenant-alpha",
  "remediation": {
    "audience": "dev",
    "severity": "medium",
    "action_kind": "use_cordumctl_edge_claude",
    "summary": "Bring claude-code configuration under Cordum Edge management.",
    "risk_explanation": "…",
    "recommended_action": "Launch Claude Code via `cordumctl edge claude` …",
    "safety_notes": ["All steps are advisory …"],
    "steps": [
      {
        "id": "use_cordumctl_edge_claude.dev.launch",
        "title": "Launch Claude Code via cordumctl edge claude",
        "kind": "use_cordumctl_edge_claude",
        "command": "cordumctl edge claude --gateway <gateway-url> --tenant <tenant-id> --principal <principal-id>",
        "docs_url": "docs/edge/cordumctl-edge-claude.md",
        "conditions": ["requires cordum-agentd in PATH on the host"]
      }
    ],
    "generator_version": "1.0.0",
    "generated_at": "2026-05-17T16:00:00Z",
    "advisory_only": true
  }
}
```

Error mapping (uses the standard Edge `{code, message, request_id, details}` envelope):

| Status | Code              | When                                              |
|--------|-------------------|---------------------------------------------------|
| 400    | invalid_request   | Unknown audience, malformed body, generator validation. |
| 400    | invalid_json      | Body decode failure.                              |
| 400    | missing_path_param| `{finding_id}` absent.                            |
| 401    | unauthorized      | Auth context missing.                             |
| 403    | access_denied     | Caller lacks `audit.read` or `admin` role.        |
| 404    | not_found         | Finding missing or cross-tenant.                  |
| 503    | store_unavailable | Shadow finding store offline.                     |
| 500    | internal_error    | Catch-all; details suppressed.                    |

## CLI

```
cordumctl shadow remediate --finding-file <path|-> [--audience dev|enterprise|both] [--json] [--omit-commands]
```

The CLI is offline by default — it reads a single finding JSON from a
file or stdin (`-`) and emits the plan as either deterministic
human-readable text (default) or JSON (`--json`). No Cordum Gateway
calls, no API keys required.

Exit codes: `0` success, `2` parse/validation/unsupported flag.

Both finding shapes are accepted:

* EDGE-141 lifecycle records emitted by
  `GET /api/v1/edge/shadow-agents/{finding_id}`.
* EDGE-140 scanner observations emitted by
  `cordumctl shadow scan` (one JSON object per line). Pipe a single
  JSONL record through the CLI to preview guidance without posting
  the finding to the Gateway:

  ```sh
  cordumctl shadow scan --enable-shadow-scan \
    | head -n1 \
    | cordumctl shadow remediate --finding-file - --audience dev --json
  ```

## Advisory-only limitations

* The generator never executes destructive operations. Backup, disable,
  and rename steps are emitted with `preview_only=true` regardless of
  audience.
* `advisory_only=true` is hard-coded on every emitted plan. A future
  enforcement mode (out of scope for EDGE-142 per the task rail) may
  flip this without changing the type signature.
* The CLI does not contact the Gateway. A future enhancement may add a
  `--finding-id` flag that uses the existing Cordum API client
  helpers; until then operators must pull the finding JSON via
  `cordumctl edge` tools or the API client and pipe it into
  `cordumctl shadow remediate --finding-file -`.
* The plan's `command` and `api_request.body` fields always carry
  placeholders. Operators are responsible for substituting live
  values — do not copy-paste secrets onto the command line; use the
  `<api-key-helper-command>` placeholder as documented in
  [managed-settings-deploy.md](managed-settings-deploy.md).

## EDGE-143.7 — Kubernetes + CI scope templates (design doc §12.1)

EDGE-143.7 extends the EDGE-142 generator with seven additional
template classes for the EDGE-143 family of detectors (K8s, CI). All
seven templates are **operator-executed**: Cordum emits the diff /
patch / config snippet, the operator applies it under their own
cluster / repo / provider credentials. Cordum never mutates customer
state per Q5 enforce-scope-out (binding governor ruling
[`comment-a17f4f1c`](../../) on parent task-de50a293).

The new templates plug into the existing
[classification table](#classification) — their signals are checked
first so a K8s or CI finding can never decay to the EDGE-142
developer-wrapper paths.

### Kubernetes scope

#### `apply_tenant_label` — tenant-label-missing

**Trigger.** `signal_set` contains `namespace_untenanted` (EDGE-143.1
detector, emitted when a namespace contains agent-image pods but is
missing the `cordum.io/tenant-id` label).

**Inputs (§10.1 fields).** `tenant_id`, `cluster_id`, `namespace`.

**Output shape.** Two `kubectl` commands:

1. `kubectl --context <cluster> label namespace <namespace> cordum.io/tenant-id=<tenant-id> --overwrite`
2. `kubectl --context <cluster> -n <namespace> annotate pods --all cordum.io/tenant-source=namespace --overwrite`

#### `adopt_unmanaged_workload` — unmanaged-workload

**Trigger.** `signal_set` contains `unmanaged_workload` (EDGE-143.1
detector, emitted when a Pod's owner workload is not on the
operator's `WorkloadAllowlist` and runs an agent image).

**Inputs.** `cluster_id`, `namespace`, `workload_kind`,
`workload_name`, `pod_uid`.

**Output shape.** Two operator-chosen options:

* **Option A** — append the workload to the operator's
  `WorkloadAllowlist` (config-side, no cluster patch).
* **Option B** — a strategic-merge patch injecting the
  `cordum-agentd` sidecar so the workload becomes Cordum-managed.
  Marked `requires_backup=true` because applying the patch triggers
  a rolling restart.

#### `rebase_agent_image` — untrusted-image

**Trigger.** `signal_set` contains `untrusted_agent_image`
(EDGE-143.1 detector, emitted when an agent image's registry prefix
is not on the operator's `ImageRegistryAllowlist`).

**Inputs.** `cluster_id`, `namespace`, `workload_kind`,
`workload_name`, plus the current image string (parsed from
`evidence_summary`'s `image=…` marker or from
`metadata.container_image`).

**Output shape.** Two steps — `kubectl get -o jsonpath` to confirm
the current image, then `kubectl set image` to rebase onto
`<cordum-allowlisted-registry>/<image-name>:<tag>`. Marked
`requires_backup=true`.

#### `extend_egress_policy` — egress-bypass

**Trigger.** `signal_set` contains `egress_bypass` (EDGE-143.1
detector, emitted when a `NetworkPolicy` egress rule is broader than
the operator's LLM-proxy allowlist, including `0.0.0.0/0` rules).

**Inputs.** `cluster_id`, `namespace`, `workload_name` (the policy
name).

**Output shape.** A `NetworkPolicy` YAML patch with `kubectl diff`
preview (`preview_only=true`) followed by `kubectl apply`. The patch
strictly extends `egress.to[]` to include `<llm-proxy-cidr>` plus a
`namespaceSelector: { matchLabels: { cordum.io/llm-proxy: "true" } }`
entry. Marked `requires_backup=true` and additive — existing rules
are preserved.

### CI scope

#### `add_cordum_edge_attach` — missing-Cordum-attach

**Trigger.** `signal_set` contains `missing_cordum_attach`
(EDGE-143.2 GitHub detector — see
`core/edge/shadow/github/detector_test.go` — emitted when a workflow
invokes an agent action without first attaching cordum-edge).

**Inputs.** `ci_provider`, `repo`, `workflow_id`.

**Output shape.** Per-provider workflow snippet inserting the
cordum-edge-attach step ahead of the agent step:

| Provider          | File                                | Snippet uses                             |
|-------------------|-------------------------------------|------------------------------------------|
| `github_actions`  | `.github/workflows/<workflow>.yml`  | `cordum/cordum-edge-attach@v1`           |
| `gitlab_ci`       | `.gitlab-ci.yml`                    | `include:` block + `variables:`          |
| `jenkins`         | `Jenkinsfile`                       | groovy stage with `withCredentials`      |
| `buildkite`       | `.buildkite/pipeline.yml`           | `cordum/cordum-edge-attach#v1` plugin    |
| `circleci`        | `.circleci/config.yml`              | `cordum/cordum-edge-attach@1.0` orb      |
| _other / unknown_ | `<workflow-file>`                   | generic shell guidance                   |

Plus a verify-attach guidance step pointing at
`/edge/sessions?source_type=ci` for confirmation.

#### `configure_oidc_trust` — unmanaged-OIDC

**Trigger.** `signal_set` contains `unmanaged_oidc` (per Q6 env-var
convention; emitted when the CI provider's OIDC trust root or
audience is not configured in the Cordum Edge service environment).

**Inputs.** `ci_provider`.

**Output shape.** Per-provider env-var block setting:

* `CORDUM_EDGE_SHADOW_OIDC_TRUST_<short>` — pre-filled with the
  provider's well-known issuer URL (`token.actions.githubusercontent.com`
  for GitHub Actions, `gitlab.com` for GitLab CI, `agent.buildkite.com`
  for Buildkite, `oidc.circleci.com/org/<org-id>` for CircleCI, a
  `<jenkins-oidc-issuer-url>` placeholder for Jenkins).
* `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<short>` — defaults to
  `cordum-edge`.

The `<short>` suffix is the lowercase provider key with the
underscore separator stripped (`github`, `gitlab`, `jenkins`,
`buildkite`, `circleci`).

Plus a restart-and-verify guidance step
(`cordumctl edge doctor --gateway <gateway-url>`).

#### `route_ci_sdk_through_proxy` — direct-provider-SDK

**Trigger.** `signal_set` contains `direct_provider_endpoint` **AND**
`source_type=ci` (EDGE-143.2 GitHub detector — see
`core/edge/shadow/github/detector_test.go` — emitted when a CI job
calls a provider SDK directly with no LLM-proxy hop).

**Inputs.** `ci_provider`, `finding_id`, `tenant_id`.

**Output shape.** Two operator-chosen options:

* **Option A** — per-provider env block setting
  `ANTHROPIC_BASE_URL` + `OPENAI_BASE_URL` to `<llm-proxy-url>`. Same
  five providers as the cordum-edge-attach template, with a generic
  shell fallback for `other`.
* **Option B** — file an operator-acked exception via
  EDGE-143.6's `POST /api/v1/edge/shadow/exception` with a JSON body
  template carrying `<finding-id>`, `<tenant-id>`, `<operator-justification>`,
  and a mandatory `<rfc3339-timestamp>` for `expires_at`.

When `audience=enterprise` or `audience=both`, a third step is
emitted recommending the env block be rolled into the platform team's
central CI templates so new repos inherit it.

### Q5 enforce-scope-out — structural guarantee

Every new template builder function takes only `findingFeatures` (the
shape-agnostic projection of the finding) plus an optional audience
flag. No builder takes a Kubernetes client, dynamic client, REST
mapper, GitHub client, GitLab client, or any provider API handle:

```go
func buildTenantLabelMissingSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildUnmanagedWorkloadSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildRebaseAgentImageSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildExtendEgressPolicySteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildAddCordumEdgeAttachSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildConfigureOIDCTrustSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep
func buildRouteCISDKThroughProxySteps(kind RemediationActionKind, f findingFeatures, audience RemediationAudience) []RemediationStep
```

The only mutating API request emitted across all seven templates is
EDGE-143.6's `POST /api/v1/edge/shadow/exception` — an
operator-initiated exception filing, not a cluster / CI mutation.
`TestK8sTemplate_NoMutation` and `TestCITemplate_NoMutation` enforce
this invariant: any mutating `APIRequest` whose path does not end in
`/exception` fails the suite.

Golden-file regression tests for all seven templates live under
`core/edge/shadow/testdata/*.golden`; regenerate with
`go test ./core/edge/shadow/ -run 'TestK8sTemplate_|TestCITemplate_' -update`
after intentional output changes.
