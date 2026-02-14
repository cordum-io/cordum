# ADR-005: Output Policy Architecture

- Status: Accepted
- Date: 2026-02-13

## Context
Cordum already enforces input policy before dispatch. That protects execution boundaries, but does not guarantee safe result payloads. Successful jobs can still return secrets, PII, or unsafe generated content.

We need an output policy architecture that:
- fits scheduler hot-path latency requirements
- supports stronger scans when needed
- preserves auditability and operator controls

## Decision
Adopt a two-phase output policy model with a dedicated output policy contract.

1. Phase 1 (sync): metadata-oriented check in scheduler result path.
2. Phase 2 (async): deeper content scanning on dereferenced payloads.

Decision outcomes:
- `ALLOW`: release result
- `REDACT`: release sanitized pointer
- `QUARANTINE`: terminal `OUTPUT_QUARANTINED` state + DLQ workflow

Contract location:
- `core/protocol/proto/v1/output_policy.proto`

Policy config extension:
- `output_rules` in safety policy YAML + JSON schema.

## Why Not Single-Phase Only
A single deep scan on hot path increases p99 latency and risks scheduler throughput regression. Splitting phases keeps fast-path bounded while still enabling strong scans.

## Why Not Reuse Input Policy Objects Directly
Input policy rules match request intent (topic/capability/risk labels) and pre-dispatch constraints. Output checks require result-specific artifacts (content pointers, findings, scanners, matched patterns, redaction pointers). A dedicated output contract avoids overloading input semantics.

## Why Fail-Open on Checker Errors
Result processing is part of core control-plane reliability path. Blocking all completions on transient checker errors creates systemic availability risk. Scheduler therefore degrades to skip-on-error and records observability signals (`output_policy_skipped`).

## Why QUARANTINE Instead of DENY
`DENY` semantics are request-time and binary. Output incidents need operational review workflows, evidence preservation, and optional release/remediation actions. `QUARANTINE` provides a terminal, auditable hold state without conflating it with pre-execution denial.

## Why Keep Output Proto Local
The output contract is an implementation-specific control-plane interface tightly coupled to Cordum scheduler + dashboard behavior. Keeping it local allows rapid iteration while preserving CAP wire compatibility for core job bus messages.

## Consequences
Positive:
- clear separation between input and output safety
- bounded hot-path latency
- richer audit/debug payloads for operators
- dashboard-ready quarantine and findings metadata

Tradeoffs:
- additional policy surface (`output_rules`)
- checker implementations must maintain detector quality and threshold tuning
- asynchronous deep-scan orchestration remains an implementation responsibility
