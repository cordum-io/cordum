# core/policy

Unified Rule, Decision, and Bundle shapes for Cordum's Policy Studio surface
(epic-d9a6c0a1). Subsumes the previously split job-side
(input/output/velocity) and edge-side authoring surfaces under one envelope
with a `RuleType` discriminator and per-type `Match`/`Decide` payloads.

## Consumers

- `core/controlplane/safetykernel/` — consumes `Rule` through adapter
  functions and emits job-source `Decision` values alongside the legacy Safety
  Kernel responses during Backend 3's transition window.
- `core/edge/` and Gateway `/edge/evaluate` — consume bound bundle
  `Rule{Type: edge}` snapshots through the Edge adapter, keep legacy
  `EdgeDecision` persistence for compatibility, and emit
  `Decision{Source: edge}` into the shared audit-chain stream (Backend 4).
- `core/controlplane/gateway/` — exposes the unified evaluator
  `POST /api/v1/policy/evaluate`, gRPC `PolicyEvaluator.EvaluateUnified`, and
  BundleStore lifecycle HTTP routes (Backend 5). See
  [`docs/policy-evaluate-api.md`](../../docs/policy-evaluate-api.md).
- `BundleRedisStore` (`bundle_store_redis.go`) — Redis-backed shared
  bundle storage for job + edge consumers; Backend 5+ wires this into
  the unified evaluator entry-point. Schema documented in
  [`docs/policy-bundle-store.md`](../../docs/policy-bundle-store.md).

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

## Evaluator contract

The gateway evaluator accepts exactly one rule source and one context:

- Inline `Rule`, or `bundle_id` + `RuleScope` active deployment.
- `job_context`, or `edge_context`.

Dispatch is driven only by `Rule.Type`:

| Rule type | Context | Consumer |
| --- | --- | --- |
| `input` | job | Safety Kernel input adapter |
| `output` | job | Safety Kernel output adapter |
| `velocity` | job | Safety Kernel velocity adapter |
| `edge` | edge | Edge classifier adapter |

Requests that mix `RuleTypeEdge` with job context, or job-side rule types with
Edge context, fail validation before downstream dispatch. Bundle evaluation
loads the active deployment for the requested `RuleScope`, verifies the active
bundle matches `bundle_id`, scans the active version's published
`RuleSnapshot`, and stamps the returned `Decision` with `BundleID` +
`BundleVersion`.
