# Output Policy Operator Guide

This guide documents Cordum output safety scanning for operator workflows. Output policy evaluates job results after execution and can allow, redact, or quarantine output before release.

## 1. Overview

Input policy protects execution-time boundaries. Output policy protects result-time boundaries. A job that was safe to run can still return:

- secret material (API keys, private keys, auth tokens)
- PII (emails, SSNs, payment card numbers)
- unsafe payloads (SQL/shell/prompt injection strings)
- oversized outputs that should be blocked or redacted

Primary references:

- gRPC contract: `core/protocol/proto/v1/output_policy.proto`
- scheduler model/types: `core/controlplane/scheduler/types.go`
- scheduler result flow: `core/controlplane/scheduler/engine.go`
- safety policy schema: `core/infra/config/safety_policy.go`
- JSON schema: `core/infra/config/schema/safety_policy.schema.json`

## 2. Architecture (Sync + Async)

Output safety is a two-phase model:

```text
Worker -> sys.job.result -> Scheduler.handleJobResult
                              |
                              | (sync, hot path)
                              +-> CheckOutputMeta(res, req)
                                      |-- ALLOW      -> job stays SUCCEEDED
                                      |-- REDACT     -> prefer redacted_ptr, stay SUCCEEDED
                                      `-- QUARANTINE -> OUTPUT_QUARANTINED + DLQ (output_quarantined)
                              |
                              | (only if still succeeded)
                              `-> background CheckOutputContent(ctx, res, req) [30s timeout]
                                      `-- QUARANTINE -> retroactive SUCCEEDED -> OUTPUT_QUARANTINED
                                                         + DLQ (output_quarantined_async)
```

Operational characteristics from current scheduler code:

- sync checks are fail-open on checker error/unavailable (result flow continues)
- async checks are best-effort and non-blocking
- output safety record is persisted for API/dashboard retrieval

## 3. Enabling Output Scanning

### 3.1 Feature flag

`OUTPUT_POLICY_ENABLED` is parsed into `Config.OutputPolicyEnabled` in `core/infra/config/config.go`.

```bash
OUTPUT_POLICY_ENABLED=true
```

### 3.2 Fail mode

Current scheduler behavior is effectively `fail_mode: open`:

- if sync check fails, scheduler increments `output_policy_skipped` and does not block result completion

`fail_mode: closed` is not currently implemented as a first-class runtime toggle in the scheduler path.

### 3.3 Per-topic override pattern

Per-topic control is expressed in `output_rules[].match.topics` using topic globs.

```yaml
output_rules:
  - id: quarantine-sensitive-reports
    match:
      topics: ["job.reports.*"]
      detectors: ["secret_leak", "pii"]
    decision: quarantine
    reason: "Sensitive report outputs require quarantine review."

  - id: allow-low-risk-healthchecks
    match:
      topics: ["job.healthcheck.*"]
    decision: allow
    reason: "Healthcheck output is low-risk."
```

### 3.4 Default rule set in `config/safety.yaml`

Cordum ships a baseline output rule set (disabled by default through `output_policy.enabled: false`):

| Rule ID | Default state | Match intent | Decision | Severity |
| --- | --- | --- | --- | --- |
| `output-secret-leak` | enabled | Secret scanners on `job.*` output | `quarantine` | `critical` |
| `output-pii-leak` | disabled | PII scanners on `job.*` output | `quarantine` | `high` |
| `output-injection-detected` | enabled | Injection scanners on `job.*` output | `quarantine` | `high` |
| `output-size-limit` | enabled | Outputs larger than 10 MiB (`output_size_gt`) | `quarantine` | `medium` |
| `output-error-secret-leak` | enabled | Secret scanners when `has_error: true` | `quarantine` | `high` |

### 3.5 Per-topic overrides in Dashboard

`/settings/output-safety` includes a **Per-Topic Overrides** table for operational exceptions. Each override entry contains:

- `topic_pattern`
- `enabled`
- `fail_mode` (`open` or `closed`)
- `scanners` (multi-select)

When saved from dashboard, these overrides are persisted under `output_policy.topic_overrides` and take precedence over global defaults for matching topics.

## 4. Rule Format (`output_rules`)

`output_rules` are part of safety policy YAML and validated by `safety_policy.schema.json`.

```yaml
output_rules:
  - id: <string>
    enabled: <bool>            # optional, default true
    severity: low|medium|high|critical
    description: <string>
    match:
      tenants: [<tenant_id>, ...]
      topics: [<topic_glob>, ...]
      capabilities: [<capability>, ...]
      risk_tags: [<risk_tag>, ...]
      scanners: [secret|pii|injection, ...]
      content_patterns: [<regex_string>, ...]
      detectors: [secret_leak|pii|code_injection|custom, ...]
      output_size_gt: <int>
      max_output_bytes: <int>
      has_error: <bool>
    decision: allow|deny|quarantine|redact
    reason: <string>
```

Fields mapped from code:

- `id`: rule identifier
- `enabled`: optional rule toggle
- `severity`: rule severity label (`low|medium|high|critical`)
- `description`: operator-facing rule description
- `match.tenants`: tenant selector
- `match.topics`: topic globs
- `match.capabilities`: capability filter
- `match.risk_tags`: risk-tag filter
- `match.scanners`: scanner aliases (`secret`, `pii`, `injection`)
- `match.content_patterns`: regex/pattern list for output content checks
- `match.detectors`: scanner aliases/synonyms (`secret_leak`, `code_injection`, etc.)
- `match.output_size_gt`: size threshold comparator (rule matches when output size is greater than this value)
- `match.max_output_bytes`: output size ceiling
- `match.has_error`: restrict rule to outputs with/without worker error payload
- `decision`: `allow|deny|quarantine|redact`
- `reason`: operator-facing decision explanation

## 5. YAML Examples for Common Rule Types

```yaml
output_rules:
  # 1) Secret leak rule
  - id: out-secret-aws
    match:
      topics: ["job.*"]
      detectors: ["secret_leak"]
      content_patterns: ["AKIA[0-9A-Z]{16}", "gh[pousr]_[A-Za-z0-9]{20,}"]
    decision: quarantine
    reason: "Potential cloud credential or token in output."

  # 2) PII rule
  - id: out-pii-critical
    match:
      topics: ["job.customer.*"]
      detectors: ["pii"]
    decision: quarantine
    reason: "PII detected in customer workflow output."

  # 3) Injection rule
  - id: out-injection-patterns
    match:
      topics: ["job.codegen.*", "job.agent.*"]
      detectors: ["code_injection"]
      content_patterns:
        - "(?i)(union\\s+select|drop\\s+table)"
        - "(?i)(ignore\\s+previous\\s+instructions|jailbreak)"
    decision: redact
    reason: "Potential injection payload detected; redact before release."

  # 4) Size-limit rule
  - id: out-size-limit
    enabled: true
    severity: medium
    description: Quarantine oversized export payloads.
    match:
      topics: ["job.export.*"]
      output_size_gt: 1048576
    decision: quarantine
    reason: "Output exceeds 1 MiB policy limit."

  # 5) Error-path leak rule
  - id: out-error-secret-leak
    enabled: true
    severity: high
    description: Scan failure/error payloads for secret leakage.
    match:
      topics: ["job.*"]
      scanners: ["secret"]
      has_error: true
    decision: quarantine
    reason: "Secret-like data found in error output."
```

## 6. Scanner Types

Built-in scanner implementations currently live in `core/controlplane/safetykernel/scanners.go`.

| Scanner name | Detection target | Example patterns in code | Typical severity |
| --- | --- | --- | --- |
| `secret_leak` / `secret` | Credentials and key material | AWS access key id, GitHub token, private key header, generic `api_key/token/password` assignment | `critical`, `high` |
| `pii` | Personal data | email, SSN, phone; payment card with Luhn validation | `high` |
| `code_injection` / `injection` | Injection and exploit fragments | SQL (`union select`, `drop table`), shell (`rm -rf`, `curl|sh`), prompt injection phrases | `high`, `medium` |
| `max_output_bytes` rule match | Oversized output | metadata size threshold | policy-defined |

## 7. Scanner Pattern Definitions (`config/output_scanners.yaml`)

Cordum ships scanner definitions in `config/output_scanners.yaml`. Safety Kernel loads this file at startup and falls back to built-in scanners if parsing fails.

```yaml
version: v1

scanners:
  secret:
    finding_type: secret_leak
    patterns:
      - name: aws_access_key
        severity: critical
        regex: "AKIA[0-9A-Z]{16}"
      - name: github_token
        regex: "gh[pousr]_[A-Za-z0-9]{20,}"
      - name: db_connection_string_password
        regex: "(?i)(postgres(?:ql)?|mysql|mariadb|mongodb|sqlserver):\\/\\/[^\\s:@]{1,64}:[^\\s@]{4,}@[^\\s]+"
      - name: base64_high_entropy_secret
        regex: "(?i)(secret|token|password|private)[^\\n]{0,32}[=:][^\\n]{0,16}[A-Za-z0-9+/]{64,}={0,2}"
        context_required: true

  pii:
    finding_type: pii
    patterns:
      - name: email_address
        regex: "\\b[A-Za-z0-9._%+\\-]+@[A-Za-z0-9.\\-]+\\.[A-Za-z]{2,}\\b"
      - name: ssn
        regex: "\\b\\d{3}-\\d{2}-\\d{4}\\b"
      - name: credit_card
        regex: "\\b(?:\\d[ -]*?){13,19}\\b"
        context_required: true

  injection:
    finding_type: code_injection
    patterns:
      - name: shell_command
        regex: "(?i)(\\brm\\s+-rf\\b|\\bcurl\\b[^\\n]{0,80}\\|\\s*(sh|bash)\\b|\\bwget\\b[^\\n]{0,80}\\|\\s*(sh|bash)\\b)"
      - name: sql_injection
        regex: "(?i)union\\s+select"
      - name: prompt_injection
        regex: "(?i)(ignore\\s+previous\\s+instructions|reveal\\s+system\\s+prompt|jailbreak)"
```

The default scanner set directly covers:

- AWS credentials and secret key material
- private key headers
- API tokens/keys (`ghp_`, `xoxb-`, `sk-`, generic key/token/password assignments)
- DB connection strings with embedded credentials
- common PII (`email`, `ssn`, payment-card-like number patterns)
- high-entropy base64 secret heuristics
- shell/SQL/prompt-injection fragments

## 8. Job Lifecycle Impact

Output policy impacts lifecycle in `handleJobResult`:

- terminal state can become `OUTPUT_QUARANTINED` (`JobStateQuarantined`)
- DLQ reason codes used by scheduler:
  - `output_quarantined` (sync phase)
  - `output_quarantined_async` (async phase)
- output safety record is persisted and returned via job APIs:
  - `decision`, `reason`, `rule_id`, `policy_snapshot`
  - `findings[]`, `phase`, `redacted_ptr`, `original_ptr`

Persistence path:

- dedicated Redis key: `job:<job_id>:output_decision`
- metadata field: `job:meta:<job_id>` -> `output_safety`

## 9. Dashboard Integration

Dashboard workflows for output policy:

- **Settings / Output Safety** (`/settings/output-safety`): operators can enable/disable scanning, set fail mode (`open|closed`), configure scan timeout/payload limits, and review runtime status/denials from one page.
  - Screenshot placeholder: `docs/assets/output-safety-settings.png` (capture pending).
- **Policy authoring flow (Policy Studio / Output Rules UX)**: operators define `output_rules` (topics, scanners, severity, decision, reason) and publish policy bundles; authoring and review UX is specified in `dashboard/src/components/jobs/OUTPUT_QUARANTINE_UX.md`.
  - Policy Studio now exposes an **Output Rules** section in `/policies/rules` with per-rule enabled toggles and a drawer-style rule detail view (config JSON, scanners/patterns, and recent findings from `GET /api/v1/policy/audit?type=output&rule_id=<id>`).
- **Jobs and Quarantine monitoring**: `output_quarantined` appears in job filters and state machine views (`JobFiltersBar`, `JobStateMachine`) so analysts can triage blocked outputs.
- **DLQ triage UX**: DLQ adds `Result Type = Quarantined` filtering, an orange quarantine shield badge, inline finding details (matched rule + original pointer), and a **Release Output** action for false positives (internally uses DLQ retry).
- **DLQ review actions**:
  - list quarantined: `GET /api/v1/jobs?state=OUTPUT_QUARANTINED`
  - inspect findings: `GET /api/v1/jobs/{jobId}`
  - release false positive: `POST /api/v1/dlq/{jobId}/retry`
  - confirm quarantine: `DELETE /api/v1/dlq/{jobId}`

## 10. Operator Runbook

### 10.1 Review quarantined outputs

1. List quarantined jobs:
   `GET /api/v1/jobs?state=OUTPUT_QUARANTINED`
2. Inspect a job:
   `GET /api/v1/jobs/{job_id}`
3. Review `output_safety`:
   - `decision`, `rule_id`, `reason`
   - `findings` (type/severity/scanner/pattern/confidence)
   - `phase` (`sync` or `async`)

### 10.2 Release false positives

If quarantine is false positive and output is acceptable:

- `POST /api/v1/dlq/{job_id}/retry`
- Dashboard path: **DLQ** -> `Result Type = Quarantined` -> open entry -> **Release Output**

### 10.3 Confirm quarantine

If quarantine is valid and you want to keep the item handled in DLQ workflow:

- `DELETE /api/v1/dlq/{job_id}`

### 10.4 Tuning guidance

- narrow `topics` scope first, then add `capabilities` / `risk_tags`
- prefer targeted `content_patterns` over broad regex
- quarantine only high-confidence/high-impact findings
- use `redact` for partial-sensitive payloads where sanitized output is acceptable
- monitor false positive rate and adjust pattern confidence/severity

## 11. Environment Variables

| Variable | Component | Purpose |
| --- | --- | --- |
| `OUTPUT_POLICY_ENABLED` | scheduler config loader | Enables output policy feature flag parsing (`true`/`1`) in `Config.OutputPolicyEnabled`. |
| `SAFETY_KERNEL_ADDR` | scheduler | gRPC endpoint for safety kernel checks. |
| `SAFETY_KERNEL_TLS_CA` | scheduler + gateway | CA bundle used for TLS verification when dialing safety kernel. |
| `SAFETY_KERNEL_TLS_REQUIRED` | scheduler + gateway | Requires TLS when connecting to safety kernel. |
| `SAFETY_KERNEL_INSECURE` | scheduler + gateway | Allows insecure transport in non-production/testing scenarios. |
| `SAFETY_KERNEL_TLS_CERT` | safety kernel server | Server certificate path for TLS listener. |
| `SAFETY_KERNEL_TLS_KEY` | safety kernel server | Server key path for TLS listener. |
| `OUTPUT_SCANNERS_PATH` | safety kernel server | Optional path to scanner regex definitions (`config/output_scanners.yaml` by default). |
| `SAFETY_POLICY_PATH` | safety kernel | Local policy bundle path (includes `output_rules`). |
| `SAFETY_POLICY_URL` | safety kernel | Remote policy URL (overrides path when set). |
| `SAFETY_POLICY_URL_ALLOWLIST` | safety kernel | Comma-separated allowed hosts for policy URL fetch. |
| `SAFETY_POLICY_URL_ALLOW_PRIVATE` | safety kernel | Allows private/loopback hosts for policy fetch (disabled by default). |
| `SAFETY_POLICY_MAX_BYTES` | safety kernel | Max policy bundle size for file/URL loading. |
| `SAFETY_POLICY_SIGNATURE_REQUIRED` | safety kernel | Require signed policy bundle. |
| `SAFETY_POLICY_PUBLIC_KEY` | safety kernel | Ed25519 public key for signature verification. |
| `SAFETY_POLICY_SIGNATURE` | safety kernel | Inline signature (base64/hex). |
| `SAFETY_POLICY_SIGNATURE_PATH` | safety kernel | Path to detached signature file. |
| `SAFETY_POLICY_RELOAD_INTERVAL` | safety kernel | Hot-reload interval for policy refresh. |
| `SAFETY_POLICY_CONFIG_DISABLE` | safety kernel | Disable config-service policy loading path. |
| `SAFETY_POLICY_CONFIG_SCOPE` | safety kernel | Config-service scope (`system`, `org`, `team`, etc.). |
| `SAFETY_POLICY_CONFIG_ID` | safety kernel | Config-service object id for policy fetch. |
| `SAFETY_POLICY_CONFIG_KEY` | safety kernel | Config-service key for policy payload. |

## 12. Cross-References

- [API Overview](./api.md)
- [System Overview](./system_overview.md)
- [Scheduler Pool Spec](./SCHEDULER_POOL_SPEC.md)
- [Output Safety Overview](./output-safety.md)
- [ADR-005 Output Policy Architecture](./adr/005-output-policy-architecture.md)
- Safety kernel implementation: `core/controlplane/safetykernel/kernel.go`
