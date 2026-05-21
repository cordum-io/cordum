# Managed settings deployment automation

This page closes the EDGE-031 gap where the managed-settings template existed
but fleet rollout, drift detection, and rollback were not part of P0. It
documents how to push `managed-settings.json` and `managed-mcp.json` to
managed Claude Code workstations, verify the deployment, and recover from
drift — for at least one MDM channel per supported platform.

The CLI surface is `cordumctl edge managed-settings <export|verify|rollback-template>`
plus the `managed_settings_compliance` doctor check. Both are operator-facing;
Cordum never calls Jamf, Intune, or any other MDM API itself. Operators or
their MDM scripts invoke the CLI; the CLI produces and verifies file content
only.

> **Wrapper is not enterprise enforcement.** The developer-mode
> `cordumctl edge claude` wrapper alone cannot stop a user from running raw
> `claude`. Fleet enforcement requires the managed-settings file installed via
> MDM or equivalent endpoint configuration management, plus the binary trust
> + bootstrap controls described in [docs/edge/README.md](README.md) §
> Enterprise managed boundary.

## 1. Overview

The end-to-end deployment loop is:

```text
operator workstation
  └─ cordumctl edge managed-settings export --output ./payload/
       ├─ managed-settings.json   (managed Claude Code policy)
       └─ managed-mcp.json        (managed MCP server policy)
                │
                ▼
MDM payload (Jamf Configuration Profile / Intune Settings Catalog / Ansible)
                │
                ▼
managed Claude Code workstation
  └─ /Library/Application Support/ClaudeCode/managed-settings.json   (macOS)
  └─ C:\ProgramData\ClaudeCode\managed-settings.json                 (Windows)
  └─ /etc/claude-code/managed-settings.json                          (Linux/WSL)
                │
                ▼
cordumctl edge doctor --managed-settings-path <path>
  └─ managed_settings_compliance check
       ├─ ok      : 14 invariants satisfied
       ├─ skip    : path not configured (non-enterprise host)
       └─ fail    : drift, missing file, or parse error
```

`cordumctl edge managed-settings export` is byte-stable: re-running with the
same flag inputs produces identical files. This makes the output suitable for
checking into a managed-config Git repository alongside a SHA reference,
which the MDM uploader can verify before pushing.

## 2. Prerequisites

| Item | Value / location |
| --- | --- |
| `cordumctl` | Same version as the managed Claude fleet. Older `cordumctl` may not embed the latest invariants. |
| MDM channel | Jamf Pro, Microsoft Intune, or Ansible/Puppet/Chef for Linux. |
| Operator credentials | API key for your MDM. **Never** pass any provider/Cordum API key on the `cordumctl edge managed-settings` command line. |
| `cordum-hook` install path on workstations | Default `/opt/cordum/bin/cordum-hook` (Linux/macOS), `C:\Program Files\Cordum\cordum-hook.exe` (Windows). Adjust via `--hook-command`. |
| `cordum-agentd` URL | Loopback only: `http://127.0.0.1:8765/v1/edge/hooks/claude`. |
| LLM proxy / MCP gateway | Your Cordum cluster URLs. Required flags. |

Reject sensitive inputs at the operator workstation: the export command
verifies that `--hook-command`, `--api-key-helper-command`, `--mcp-gateway-url`,
and `--llm-proxy-base-url` do not contain secret markers (`sk-…`, `ghp_…`,
bearer tokens, API key fragments). A failure here means a secret leaked into
a flag value — fix the upstream source, do not bypass the check.

## 3. macOS / Jamf playbook (worked example)

Jamf is the canonical worked example because it is the most common managed
macOS channel and exercises both file-deployment and post-deploy verification.

### 3.1 Generate the payload

```bash
cordumctl edge managed-settings export \
  --output ./jamf-payload/ \
  --mcp-gateway-url https://mcp.cordum.example/mcp \
  --llm-proxy-base-url https://llm-proxy.cordum.example \
  --api-key-helper-command "/opt/cordum/bin/cordum-agentd claude api-key-helper" \
  --hook-command /opt/cordum/bin/cordum-hook \
  --platform darwin
```

Re-run with `--force` to overwrite an existing payload directory; the CLI
refuses to overwrite by default to protect a checked-in copy.

### 3.2 Pin the payload SHA in source control

```bash
shasum -a 256 ./jamf-payload/managed-settings.json
shasum -a 256 ./jamf-payload/managed-mcp.json

git add jamf-payload/managed-settings.json jamf-payload/managed-mcp.json
git commit -m "managed-settings: rev <SHA-prefix>"
```

The SHA pin is what your MDM uploader compares against before push, so an
operator typo cannot ship a different payload than the one approved.

### 3.3 Upload as a Configuration Profile

In Jamf Pro:

1. **Computers → Configuration Profiles → New**.
2. **Application & Custom Settings → External Applications**.
3. Source: **Custom Schema**.
4. Preference Domain: `com.anthropic.claudecode` (Custom Settings payload).
5. Upload `managed-settings.json` as the JSON payload.
6. Add a second **External Applications** payload for `com.anthropic.claudecode.mcp`
   carrying `managed-mcp.json`.
7. Scope to the managed-Claude smart group.
8. Save and let the next Jamf check-in apply the profile.

Equivalent file-based deployment (when the Custom Settings payload is not
viable for your tenancy):

- **macOS file path:** `/Library/Application Support/ClaudeCode/managed-settings.json`
  and `/Library/Application Support/ClaudeCode/managed-mcp.json`.
- Use a Jamf **Files and Processes** policy or a configuration management tool
  (Munki, Chef, Puppet) to copy the files with mode `0644` and root ownership.

### 3.4 Post-deploy verification on a workstation

```bash
cordumctl edge doctor \
  --managed-settings-path "/Library/Application Support/ClaudeCode/managed-settings.json"
```

The `managed_settings_compliance` row reports `ok` when the 14 invariants
are satisfied. Operators can also run the standalone verifier without
contacting the gateway:

```bash
cordumctl edge managed-settings verify \
  --path "/Library/Application Support/ClaudeCode/managed-settings.json"
```

Exit `0` = ok, `1` = drift detected (each drifting field printed), `2` =
missing or unparseable file.

## 4. Windows / Intune playbook

Three deployable shapes are supported on Windows:

| Shape | Where the file lands | When to use |
| --- | --- | --- |
| Settings Catalog | Intune-managed registry/file via the catalog UI | Modern Intune-managed devices with the catalog enabled. |
| OMA-URI custom policy | `HKLM\SOFTWARE\Policies\ClaudeCode` | Mixed estates that need a fallback custom policy. |
| File-based | `C:\Program Files\Cordum\managed-settings.json` (per-machine) or `%PROGRAMDATA%\ClaudeCode\managed-settings.json` (per-tenant) | When you ship via a Win32 app / configuration management. |

### 4.1 Generate the payload

```powershell
cordumctl edge managed-settings export `
  --output .\intune-payload `
  --mcp-gateway-url https://mcp.cordum.example/mcp `
  --llm-proxy-base-url https://llm-proxy.cordum.example `
  --api-key-helper-command "C:\Program Files\Cordum\cordum-agentd.exe claude api-key-helper" `
  --hook-command "C:\Program Files\Cordum\cordum-hook.exe" `
  --platform windows
```

The `--platform windows` flag preserves backslash hook paths verbatim;
Cordum tests cover Windows paths with spaces (e.g. `C:\Program Files\Cordum\cordum-hook.exe`).

### 4.2 Push via Intune Settings Catalog

1. **Microsoft Intune admin centre → Devices → Configuration profiles → Create**.
2. Platform: **Windows 10 and later** → Profile type: **Settings catalog**.
3. Add settings under **Anthropic Claude Code → Managed Settings JSON** (the
   schema name your tenant exposes — the catalog identifier may evolve).
4. Paste the contents of `managed-settings.json`.
5. Repeat for `managed-mcp.json` under **Managed MCP Servers JSON**.
6. Assign to the managed Claude Code device group.

OMA-URI fallback (when the catalog node is not available):

- OMA-URI: `./Vendor/MSFT/Policy/Config/AnthropicClaudeCode/ManagedSettings`.
- Data type: String.
- Value: full contents of `managed-settings.json`.

### 4.3 Post-deploy verification

```powershell
cordumctl edge doctor `
  --managed-settings-path "C:\Program Files\Cordum\managed-settings.json"
```

For PowerShell pipelines that need a non-zero exit gate, use `cordumctl edge
managed-settings verify --path <…> --json` and parse the `ok` field.

## 5. Linux / WSL playbook

The canonical Linux file location is `/etc/claude-code/managed-settings.json`,
owned by root with mode `0644`. Cordum recommends an Ansible playbook (or
equivalent Puppet/Chef recipe) keyed to the managed-Claude inventory group.

### 5.1 Generate the payload

```bash
cordumctl edge managed-settings export \
  --output ./ansible-payload/ \
  --mcp-gateway-url https://mcp.cordum.example/mcp \
  --llm-proxy-base-url https://llm-proxy.cordum.example \
  --api-key-helper-command "/opt/cordum/bin/cordum-agentd claude api-key-helper" \
  --hook-command /opt/cordum/bin/cordum-hook \
  --platform linux
```

### 5.2 Ansible deployment

```yaml
- name: Cordum managed Claude Code settings
  hosts: managed_claude
  become: true
  tasks:
    - name: Ensure /etc/claude-code exists
      ansible.builtin.file:
        path: /etc/claude-code
        state: directory
        owner: root
        group: root
        mode: "0755"

    - name: Install managed-settings.json
      ansible.builtin.copy:
        src: ansible-payload/managed-settings.json
        dest: /etc/claude-code/managed-settings.json
        owner: root
        group: root
        mode: "0644"

    - name: Install managed-mcp.json
      ansible.builtin.copy:
        src: ansible-payload/managed-mcp.json
        dest: /etc/claude-code/managed-mcp.json
        owner: root
        group: root
        mode: "0644"

    - name: Verify managed-settings invariants
      ansible.builtin.command: >
        cordumctl edge managed-settings verify
        --path /etc/claude-code/managed-settings.json
      changed_when: false
```

The final `verify` task aborts the playbook run if a workstation ends up with
a drifted file — usually because of a manual edit between rollouts.

### 5.3 WSL note

Inside WSL, the Linux file path applies (`/etc/claude-code/...`). Push via
the same Ansible playbook from your management host or via a Win32 app on the
host that drops the WSL path.

## 6. Verification reference

Every supported channel ends with the same verifier:

```bash
cordumctl edge managed-settings verify --path <path>
```

The verifier enforces the **17 invariants** baked into the template:

| # | Invariant | Severity |
| --- | --- | --- |
| 1 | `allowManagedHooksOnly == true` | critical |
| 2 | `allowManagedMcpServersOnly == true` | critical |
| 3 | `disableBypassPermissionsMode == "disable"` | critical |
| 4 | `allowedHttpHookUrls == []`; HTTP hooks are spike-only and must not be part of production enforcement | critical |
| 5 | `allowedMcpServers == [{ "serverName": "cordum-edge" }]` with no extra/missing/case-variant entries | critical |
| 6 | `hooks.PreToolUse` present with a canonical `cordum-hook claude pre-tool-use` command | critical |
| 7 | `hooks.PostToolUse` present with a canonical `cordum-hook claude post-tool-use` command | critical |
| 8 | `hooks.PostToolUseFailure` present with a canonical `cordum-hook claude post-tool-use-failure` command | critical |
| 9 | `hooks.UserPromptSubmit` present with a canonical `cordum-hook claude user-prompt-submit` command | critical |
| 10 | `hooks.ConfigChange` present with a canonical `cordum-hook claude config-change` command | critical |
| 11 | `hooks.FileChanged` present with a canonical `cordum-hook claude file-changed` command | critical |
| 12 | `env.CORDUM_AGENTD_FAIL_CLOSED == "true"` | critical |
| 13 | `env.CORDUM_EDGE_MANAGED_POLICY_MODE == "enterprise-strict"` | critical |
| 14 | `env.CORDUM_EDGE_MANAGED_HOOKS_ONLY == "true"` | critical |
| 15 | `env.CORDUM_AGENTD_URL` non-empty + no query string | critical |
| 16 | The hook command boundary is local `cordum-hook` -> local `cordum-agentd`; arbitrary shell/python/curl commands are rejected | critical |
| 17 | No nonce/key markers in env or serialised form: `CORDUM_AGENTD_HOOK_NONCE`, `ANTHROPIC_API_KEY`, `sk-…` provider keys, `ghp_…` GitHub tokens, `AKIA…` AWS access keys, or `Authorization` headers carrying bearer tokens. | high |

Canonical hook paths are `/opt/cordum/bin/cordum-hook`, `/usr/local/bin/cordum-hook`, `/Applications/Cordum Edge/cordum-hook`, and `C:\Program Files\Cordum\cordum-hook.exe`. The verifier has no settings-file override for HTTP hook URLs, alternate MCP servers, or arbitrary hook commands. A future power-user exception must be a separately verified, signed trusted-config input; managed settings bytes alone are not trusted to weaken the production boundary.

The same checks run inside `cordumctl edge doctor` when you pass
`--managed-settings-path` (or set `CORDUM_EDGE_MANAGED_SETTINGS_PATH`).

`--json` envelope schema for `cordumctl edge managed-settings verify`:

```json
{
  "ok": false,
  "drifts": [
    {
      "field": "env.CORDUM_AGENTD_FAIL_CLOSED",
      "got": "false",
      "want": "true",
      "severity": "critical"
    }
  ],
  "source": "/etc/claude-code/managed-settings.json"
}
```

## 7. Drift detection and monitoring

Run the verifier on a schedule (`cron`, Windows Task Scheduler, `systemd
timer`, or the MDM's compliance-rule executor) and ship its non-zero exit
into your existing alerting channel. Two recommended cadences:

- **Hourly per workstation** — catches a local administrator overriding the
  file or a partial MDM sync.
- **Daily per fleet** — sample a percentage of devices and aggregate the
  `--json` envelopes for a single drift report.

Cordum does not ship the alert pipeline; this is intentional — the existing
SIEM/observability tools at every Cordum customer already handle exit-code
based monitoring.

## 8. Rollback

> **Production rollback is MDM-orchestrated.** The `cordumctl edge
> managed-settings rollback-template` subcommand exists for synthetic test
> fixtures only and is **not** a production rollback path.

### 8.1 Production rollback (the right path)

| Channel | Action |
| --- | --- |
| Jamf | Re-upload the prior Configuration Profile from Git, scoped to the same smart group. Jamf's profile-version history is the audit trail. |
| Intune | Reassign the previous Settings Catalog / OMA-URI policy version. Intune's audit log captures the swap. |
| Linux (Ansible) | Replace `ansible-payload/managed-settings.json` with the previous version from Git, re-run the playbook, observe the verifier task. |

In every case, the previous revision lives in your config-management Git
history. The CLI is not in the production rollback loop.

### 8.2 Synthetic rollback for tests

`cordumctl edge managed-settings rollback-template --path <file>` regenerates
a fresh template at `<file>` (atomic temp-file + rename, mode `0600`) and
re-runs the verifier defensively. The integration test
`TestManagedSettingsFullRollbackCycle` exercises the full
`export → drift → verify-fails → rollback → verify-passes` loop with no
network calls. Use this in your CI for the Cordum CLI itself or for a local
sanity check after editing the template generator.

## 9. Upgrade

When upgrading the Cordum cluster (e.g. new MCP gateway URL or a hook command
relocation):

1. Bump the operator's local `cordumctl` and re-run `export` with the new
   flags into the same payload directory using `--force`.
2. Diff against the prior committed payload (`git diff jamf-payload/`).
3. Pin the new SHA, push through the standard MDM channel above.
4. Run the verifier on a sample workstation before broad rollout.
5. Monitor the scheduled drift report to confirm fleet adoption.

The 14 invariants do not change between minor Cordum releases; if they
materially change, the upgrade notes will say so explicitly. The
`cordumctl edge managed-settings verify` exit code is the operator's
shippable signal that a fleet has converged.

## 10. Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `--mcp-gateway-url … contains sensitive value` on export | A flag value or env var carries a secret marker. | Replace with the plain Cordum URL; do not embed bearer tokens. |
| `verify` exits `2` with `parse managed-settings.json` | Hand-edited JSON corrupted the file. | Re-run `export` to a temp dir, diff against the deployed file, re-push the clean version. |
| `verify` exits `1` with `env.CORDUM_AGENTD_URL got=…?token=…` | A workstation administrator added a query string. | Re-deploy via MDM; the verifier rejects URL queries by design. |
| `verify` exits `1` with `serialized.sensitive_marker` | A debug env var or hook command leaked a secret marker. | Audit recent edits; the secret never belongs in managed settings. |
| `doctor` shows `managed_settings_compliance: skip` on a managed host | `--managed-settings-path` not configured. | Set `CORDUM_EDGE_MANAGED_SETTINGS_PATH` system-wide via MDM, or pass `--managed-settings-path` in your scheduled doctor run. |
| `doctor` shows `managed-settings.json not found` | The MDM push has not landed yet (or the path is wrong for the platform). | Check MDM sync status; confirm the platform-specific path matches §3-5 above. |

## 11. GDPR / UK-DPA processing record (EDGE-143.4)

This template documents the data-processing record operators MUST
maintain when running the Cordum Edge **network-signal aggregator**
(see [shadow-scanner.md §9.3](shadow-scanner.md)). The aggregator
classifies `principal_id` (derived from `github.actor` or equivalent
CI usernames) as **pseudonymous personal data** per GDPR Art. 4(1) /
UK-DPA equivalent. The Q2 governor ruling
([comment-a17f4f1c on task-de50a293](kubernetes-ci-shadow-detector-design.md))
mandates this record alongside the
`CORDUM_EDGE_SHADOW_PII_MODE=pseudonymize|hash|drop` env flag.

Treat the following as a copy-and-fill checklist. Replace each
placeholder with operator-specific values BEFORE production rollout.

### 11.1 Data categories collected

The collected fields are the **§9.1 lawful metadata catalog** — a
closed set. Any extension requires a fresh ADR + governor re-rule.

| Field            | Source                          | Privacy classification |
|------------------|---------------------------------|------------------------|
| `hostname`       | Operator egress / proxy log     | Non-personal — provider endpoint name. |
| `category`       | Derived (`hostname → category`) | Non-personal — `anthropic_api` / `openai_api` / `google_api`. |
| `count_bucket`   | Derived (bucketized count)      | Non-personal — order-of-magnitude. Exact request rate NOT retained. |
| `workload_id`    | Operator log (post-PII)         | **Pseudonymous personal data** under the active PII mode. |
| `principal_id`   | Derived (`workload_id` post-PII) | **Pseudonymous personal data**. |
| `endpoint_hash`  | Operator log (opaque)            | Non-personal if operator follows the SHA-based hashing convention. |

Fields **NEVER** collected (enforced by `enforceCatalog` at the
detector boundary):

- Full URLs with query strings (would leak `api_key=...`).
- IP addresses (geolocation / network-attribution risk).
- Bearer tokens / API keys (secret-shape regex enforced).
- Request / response bodies (content classification risk).
- User-Agent strings (de-anonymization vector).

### 11.2 Lawful basis

- **GDPR Art. 6(1)(f)** / **UK-DPA s.35** — legitimate interests in
  **security operations**: identifying unauthorized direct LLM
  provider traffic from CI runners and developer workstations is
  necessary to enforce the operator's data-handling and tenant-
  isolation policies.
- A balancing test SHOULD be documented per Art. 6(1)(f)(ii). The
  pseudonymize / hash / drop env flag is the proportionality control:
  operators choose the minimum identity strength their security
  posture requires.

### 11.3 Retention

- **Default**: 90 days terminal-retention TTL (`shadow_default` class
  via `CORDUM_EDGE_SHADOW_RETENTION_DEFAULT`).
- **Shortened**: `shadow_short` (7 days) for low-stakes deployments.
- **Extended**: `shadow_long` (365 days) for regulated industries.
- `detected` findings remain until operator triage (no auto-TTL); only
  terminal states (`resolved` / `suppressed`) carry the per-class TTL.
- See [shadow-scanner.md §9.1](shadow-scanner.md) for the env-var
  override syntax (`time.ParseDuration`, e.g. `168h` for 7 days).

### 11.4 Controller / Processor split

- **Operator = data controller.** The operator decides which logs
  feed the aggregator, which workload identities map to which
  tenants, and what PII mode to apply. The operator's DPO owns DSAR
  responses.
- **Cordum = data processor.** Cordum reads the operator-supplied
  log stream (NG7: no Cordum-side traffic capture), applies the
  configured PII mode, persists findings to the operator's own
  Redis-backed shadow store, and never exfiltrates findings to
  Cordum-controlled infrastructure.
- A processor agreement under GDPR Art. 28 / UK-DPA s.59 SHOULD
  document this split. Cordum publishes no SaaS variant of this
  pipeline; processor obligations apply only to the open-source
  library code.

### 11.5 DSAR contact

Operators MUST publish a Data Subject Access Request contact route.
Recommended template:

```
DSAR Contact:
  Email: dpo@<operator-domain>
  Acknowledgement SLA: 5 business days
  Response SLA: 30 calendar days (GDPR Art. 12(3))
  Verification: <operator-defined identity proof>
  Scope: principal_id values matching the requester's CI identity
         (e.g. github.actor=<requester-handle>) across all tenants
         the requester is associated with.
  Erasure: Operator runs `cordumctl edge shadow resolve <finding_id>
           --reason='gdpr_erasure'` for each matching finding;
           terminal-retention TTL applies thereafter.
```

The Cordum library itself does not expose a DSAR endpoint — operators
serve DSAR requests from their own controller-side tooling.

### 11.6 Cross-reference

- `CORDUM_EDGE_SHADOW_PII_MODE` env flag — see
  [shadow-scanner.md §9.3](shadow-scanner.md) for the three modes
  and the worked SHA-256 example.
- Q2 binding governor ruling lives on the design-doc parent task
  ([comment-a17f4f1c on task-de50a293](kubernetes-ci-shadow-detector-design.md)).

### 11.7 Shadow CI detector OIDC trust config (EDGE-143.3, Q6 binding)

The CI shadow detector library (`core/edge/shadow/ci/`) reads
per-provider OIDC trust roots from the operator environment. Per Q6
([comment-a17f4f1c on task-de50a293](kubernetes-ci-shadow-detector-design.md)),
Cordum ships a default issuer ONLY for GitLab.com SaaS; every other
provider is operator-only.

| Env var | Default | Behavior |
| --- | --- | --- |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab` | `https://gitlab.com` when `GitLabBaseURL` host is `gitlab.com`; otherwise NONE | Self-hosted GitLab (any non-`gitlab.com` host) requires operator override; absent override sets `OIDCConfig.Disabled=true` and falls back to §6.3 tier-2 `OrgRepoMap`. Literal `disabled` forces fallback. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_gitlab` | `cordum-edge` | Expected JWT `aud` claim. |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_jenkins` | NONE (operator-only per Q6) | OIDC support varies by Jenkins plugin. Absent operator override sets `Disabled=true` and falls back to §6.3 tier-2. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_jenkins` | `cordum-edge` | Expected JWT `aud` claim when OIDC is enabled. |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_buildkite` | NONE (operator-only per Q6) | Buildkite OIDC issuer is `https://agent.buildkite.com`; operators MUST set this explicitly. Absent override falls back to §6.3 tier-2. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_buildkite` | `cordum-edge` | Expected JWT `aud` claim when OIDC is enabled. |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_circleci` | NONE (operator-only per Q6) | CircleCI OIDC issuer is org-scoped (`https://oidc.circleci.com/org/<org-id>`); operators MUST set their org-specific issuer. Absent override falls back to §6.3 tier-2. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_circleci` | `cordum-edge` | Expected JWT `aud` claim when OIDC is enabled. |

No new managed-settings JSON invariants are introduced — the OIDC
trust config lives in the operator's environment / fleet config, not
in `managed-settings.json`. Cordum's verifier checks the 14 invariants
in §6 above; CI OIDC config is read by the detector at boot and
re-checked at every Detector instantiation via `LoadOIDCConfigFromEnv`.

## 12. Related docs

- [Managed settings template (synthetic excerpt)](managed-settings-template.md)
- [cordumctl edge doctor — local diagnostics](cordumctl-edge-doctor.md)
- [Cordum Edge index](README.md)
- [cordumctl edge claude](cli.md)
- [Edge configuration](configuration.md)
- [Shadow scanner — EDGE-143.4 network aggregator §9.3](shadow-scanner.md)
