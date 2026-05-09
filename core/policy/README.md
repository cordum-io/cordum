# core/policy

Unified Rule, Decision, and Bundle shapes for Cordum's Policy Studio surface
(epic-d9a6c0a1). Subsumes the previously split job-side
(input/output/velocity) and edge-side authoring surfaces under one envelope
with a `RuleType` discriminator and per-type `Match`/`Decide` payloads.

## Consumers

- `core/controlplane/safetykernel/` — consumes `Rule` through adapter
  functions and emits job-source `Decision` values alongside the legacy Safety
  Kernel responses during Backend 3's transition window.
- `core/edge/` — consumes `Rule{Type: edge}` through an adapter, keeps legacy
  `EdgeDecision` persistence for compatibility, and emits
  `Decision{Source: edge}` into the shared audit-chain stream (Backend 4).
- `core/controlplane/gateway/` — exposes `/policies/*` HTTP routes (Backend 5).

## Match / Decide contract

`Rule.Match` and `Rule.Decide` are `json.RawMessage`. The per-`RuleType`
shape is documented in `docs/specs/policy-studio-rewrite.md` and mirrors
today's `InputPolicyMatch` / `OutputPolicyMatch` / `PolicyMatch` +
`VelocityConfig` / `ActionClassification` surfaces. The raw-message carrier
preserves bit-for-bit fidelity and maps cleanly onto the proto-side
`google.protobuf.Struct` and orval `Record<string, unknown>` types.

## Decision wire values

`DecisionType` mirrors the seven values in the wire-format
`cap/proto/cordum/agent/v1/safety.proto` enum: `allow` / `deny` /
`require_human` / `throttle` / `allow_with_constraints` / `quarantine` /
`redact`. The last two were appended for the unified surface per CAP's
append-only protobuf-evolution rule.
