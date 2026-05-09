# core/policy

Unified Rule, Decision, and Bundle shapes for Cordum's Policy Studio surface
(epic-d9a6c0a1). Subsumes the previously split job-side
(input/output/velocity) and edge-side authoring surfaces under one envelope
with a `RuleType` discriminator and per-type `Match`/`Decide` payloads.

## Consumers (in follow-up tasks)

- `core/safetykernel/` — will consume `Rule` and emit `Decision` for job
  evaluation paths (Backend 3 + Backend 5).
- `core/edge/` — same migration; joins the unified audit-chain (Backend 4).
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
