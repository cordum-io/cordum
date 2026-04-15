---
sidebar_position: 6
title: "Output Safety"
slug: /concepts/output-safety
---

# Output Safety

## Overview
Output Safety is the post-execution policy layer for Cordum job results.

Input policy decides whether a job is allowed to run. Output policy decides whether its result can be released as-is, must be redacted, or must be quarantined.

## Why It Exists
Input checks cannot fully predict generated output. A safe input can still produce:
- secret leakage
- sensitive PII fragments
- unsafe code or command payloads

Output Safety closes this gap before result release.

## Architecture
Cordum models output checks as a two-phase flow:

1. Sync metadata check on scheduler hot path.
2. Optional deeper content check over dereferenced payloads.

Current scheduler wiring uses `CheckOutputMeta` before finalizing successful results. The scheduler persists output safety metadata for dashboard retrieval.

## API Contract
Proto contract: `core/protocol/proto/v1/output_policy.proto`

Service:
- `OutputPolicyService.CheckOutput(OutputCheckRequest) -> OutputCheckResponse`

Decisions:
- `OUTPUT_DECISION_ALLOW`
- `OUTPUT_DECISION_QUARANTINE`
- `OUTPUT_DECISION_REDACT`

`OutputCheckRequest` includes:
- original topic, labels, tenant
- result pointer and optional inline output content
- capability/risk context (`capabilities`, `risk_tags`)
- principal and pack metadata (`principal_id`, `pack_id`)
- content metadata (`content_type`, `output_size_bytes`, `content_hash`)

## Policy Schema
Output rules are modeled in safety policy under `output_rules`.

Example:

```yaml
output_rules:
  - id: out-secret-1
    decision: quarantine
    reason: "possible cloud credential in output"
    match:
      topics: ["job.*"]
      capabilities: ["code.write"]
      risk_tags: ["secrets"]
      content_patterns: ["AKIA[0-9A-Z]{16}"]
      detectors: ["secret_leak"]
      max_output_bytes: 1048576
```

Supported output decisions:
- `allow`
- `deny`
- `quarantine`
- `redact`

## Scheduler Behavior
For succeeded jobs, scheduler output safety handling is:
- Run `CheckOutputMeta` when output safety checker is configured and enabled.
- Persist `OutputSafetyRecord` into job metadata (`output_safety`) for `GET /api/v1/jobs/{id}`.
- `QUARANTINE`: move job to `OUTPUT_QUARANTINED` and emit DLQ event with reason code `output_quarantined`.
- `REDACT`: keep success state but prefer `redacted_ptr` when returned.
- `ALLOW`: release result as normal.

Output safety failures default to **fail-closed** (quarantine on error). The behavior is configurable:

| Mode | Behavior | When to use |
|------|----------|-------------|
| `closed` (default) | Quarantine job on checker error/timeout | Production, regulated, high-risk tenants |
| `open` | Allow result through, count as skipped | Development, low-risk tenants |

Configure via environment variable `OUTPUT_POLICY_FAIL_MODE` at scheduler startup, or at runtime through the config service (`PUT /api/v1/config` with `{"scheduler":{"output_fail_mode":"open"}}`). Runtime changes are hot-reloaded every 30 seconds.

A Redis-backed circuit breaker (3 failures → open for 30s → half-open probe) protects against cascading failures when the output safety checker is persistently unavailable.

## Dashboard Data Shape
`GET /api/v1/jobs/{id}` now includes optional:

- `output_safety.decision`
- `output_safety.reason`
- `output_safety.rule_id`
- `output_safety.findings[]`
- `output_safety.policy_snapshot`
- `output_safety.redacted_ptr`
- `output_safety.original_ptr`

## Metrics
Scheduler exports output safety metrics:
- `cordum_output_policy_checked_total`
- `cordum_output_policy_quarantined_total`
- `cordum_output_policy_skipped_total`
- `cordum_output_check_latency_seconds{phase="sync|async"}`

## Failure Modes
- Checker unavailable/error: behavior depends on `output_fail_mode` setting. Default (`closed`) quarantines the job; `open` allows the result and increments `cordum_output_policy_skipped_total`.
- Circuit breaker open (3+ consecutive failures): all output checks are blocked until the breaker transitions to half-open after 30 seconds.
- Missing request context for result: check skipped (metadata unavailable, not a checker failure).
- Corrupt stored output safety payload: gateway/store tolerate and return empty record.

## Performance Notes
- Sync check must be low-latency because it runs in result processing.
- Avoid network I/O in sync phase.
- Use deeper content scans in async/background flow where available.

## Tuning False Positives
- Narrow `topics` and `capabilities` in `output_rules`.
- Set detector-appropriate confidence thresholds in checker implementations.
- Prefer targeted `content_patterns` over broad regex.
- Use `REDACT` for acceptable partial masking cases; reserve `QUARANTINE` for high confidence/high impact findings.
