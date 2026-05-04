# Edge Classifier Trust Boundary

EDGE-069 documents the contract between Cordum Edge's deterministic
classifier and the policy evaluator. The classifier OWNS a set of
reserved label namespaces; a request body that tries to set a label in
one of those namespaces has the value silently dropped at the policy-
mapper trust boundary, with a per-namespace observability counter
firing on every drop.

## Why this contract exists

Claude tool input (and any future hook adapter's payload) is partially
agent/user-controlled. Without a trust boundary, an attacker or a
naive client could:

- Claim `risk_tags=[]` on a `Bash rm -rf /` action so policy never
  sees the destructive flag.
- Set `labels.path.class=file` on a `Read .env` so `claude-code.deny-
  secret-reads` never fires.
- Set `labels.command.class=safe` on a destructive shell command so a
  "safe" allow rule misfires.
- Set `labels.unknown.impact=low` to downgrade an unknown high-risk
  action below the deny-unknown-high-risk rule.

The EDGE-069 fix closes these vectors by:

1. The CLASSIFIER (a deterministic Go function in
   `core/edge/classifier.go`) computes ALL classifier-owned labels +
   risk_tags + capability from the request body fields the user
   actually controls (`tool_name`, `command`, `file_path`, etc).
2. The POLICY MAPPER (`core/edge/policy_mapper.go mapLabelsForPolicy`)
   merges request-body labels (untrusted) with classifier-emitted
   labels (trusted), dropping any untrusted label whose key starts
   with a classifier-owned namespace.
3. The DROP path emits
   `cordum_edge_request_labels_stripped_total{namespace=...}` so
   operators can alert on a sustained non-zero rate (a buggy or
   malicious client).

## Reserved namespaces

A label key whose prefix matches any of the namespaces below is
classifier-owned. `reservedPolicyLabelPrefixes` in
`core/edge/policy_mapper.go` is the canonical list.

| Namespace      | Owner       | Set by                                                  |
|----------------|-------------|---------------------------------------------------------|
| `edge.*`       | classifier  | `baseClassificationLabels` + `mapLabelsForPolicy` for `edge.session_id`, `edge.execution_id`, `edge.event_id`, `edge.layer`, `edge.kind`, `edge.action_name` |
| `hook.*`       | classifier  | `baseClassificationLabels` for `hook.event`, `hook.tool_name`. |
| `mcp.*`        | classifier  | `classifyMCPEvent` for `mcp.server`, `mcp.tool`, `mcp.action`. |
| `llm.*`        | classifier  | `classifyLLMEvent` for `llm.provider`, `llm.model`. |
| `runtime.*`    | classifier  | `classifyRuntimeEvent` for `runtime.event`, `runtime.process`, `runtime.host`. |
| `agent.*`      | classifier  | `baseClassificationLabels` for `agent.product`. |
| `path.*`       | classifier  | `addPathLabels` for `path.class`, `path.traversal`, `path.sensitive_area`. |
| `command.*`    | classifier  | `classifyBashCommand` for `command.class`, `command.family`. |
| `unknown.*`    | classifier  | Hook default-case for `unknown.impact`. |
| `action.*`     | classifier  | Reserved for future use; defensive — pre-EDGE-069 leak vector. |
| `classifier.*` | classifier  | EDGE-069 step 5 — `classifier.complete`, `classifier.missing_fields` for partial-classification fail-closed. |

Anything OUTSIDE these namespaces is request-controlled and passes
through untouched (subject to length/UTF-8/secret-string sanitisation
in `safeLabelValue`).

## What happens when a request tries to set a reserved label

A request body label whose key matches any of the reserved-namespace
prefixes is dropped. Two side-effects:

1. The `cordum_edge_request_labels_stripped_total{namespace=<prefix>}`
   counter is incremented. The label value is the bare prefix
   (`path` not `path.`) for cleaner alert routing.
2. The policy input the Safety Kernel receives carries ONLY the
   classifier-emitted value for that key — or no value at all, if
   the classifier didn't emit one for this event type.

The classifier-emitted values ALWAYS win when both exist (the
`putPolicyLabel(..., trusted=true)` second pass overwrites). When the
classifier didn't emit a value, the request's value is dropped, NOT
substituted — there's no "default" for missing classifier output. This
is deliberate fail-closed behaviour: a Bash event has no `path.class`
because there's no path to classify, and a malicious request claiming
`path.class=secret` does not gain a policy-relevant label by leakage.

## The 7 invariants (EDGE-069 DoD)

| # | Invariant | Test |
|---|-----------|------|
| (a) | Unknown classifier output → never silent ALLOW | `TestClassifier_UnknownToolFailsToHigherTier` (capability=edge.unknown + risk_tags=[review_required, unknown]) |
| (b) | Empty classifier output → fail closed | `TestClassifier_EmptyClassificationFailsClosed` + `TestClassifier_PartialClassificationDenied` (Complete=false + MissingFields populated) |
| (c) | Tenant from auth, never from labels | EDGE-008.7 already enforces this in `handlers_edge_evaluate.go` (verified by `handlers_edge_evaluate_test.go`) |
| (d) | User-controlled risk_tags cannot remove classifier-emitted tags | `TestClassifier_RiskTagsAreOwnedByClassifierNotRequest` |
| (e) | User-controlled reserved labels cannot poison classifier | `TestClassifier_ReservedLabelsCannotBePoisonedByRequest` + 3 namespace-specific subtests |
| (f) | Partial classification → flagged for fail-closed | `TestClassifier_PartialClassificationDenied` (3 subtests, one per missing field) |
| (g) | Audit decision cites rule_id | Tracked in `core/controlplane/safetykernel/...` package tests; safetykernel always populates `RuleID` in `PolicyCheckResponse`. The `<default>` sentinel for empty rule_id is a follow-up. |

## Adding a new classifier-owned label

When a future change adds a new label key emitted by the classifier:

1. Add the namespace prefix to `reservedPolicyLabelPrefixes` in
   `core/edge/policy_mapper.go`.
2. Add a row to the namespace table above.
3. Add a regression test in `classifier_trust_boundary_test.go`
   asserting that a request body trying to set the new label has the
   value dropped + the metric incremented for that namespace.

If the new label is in an EXISTING reserved namespace (e.g. you add
`edge.foo` to `baseClassificationLabels`), no `reservedPolicyLabelPrefixes`
update is required — the prefix list is namespace-granular.

## Cross-references

- `core/edge/classifier.go` — `ActionClassification` (Complete +
  MissingFields), `ClassifyEvent`, `computeClassificationCompleteness`.
- `core/edge/policy_mapper.go` — `mapLabelsForPolicy`, `putPolicyLabel`,
  `reservedPolicyLabelPrefix`, `reservedPolicyLabelPrefixes`.
- `core/edge/policy_mapper_metrics.go` —
  `edgeRequestLabelsStrippedTotal` Prometheus counter.
- `core/edge/classifier_trust_boundary_test.go` — invariant tests.
- `docs/edge.md` — Edge architecture overview (cross-link target).
- EDGE-008.7 — established the principal-from-auth precedent.
- EDGE-031 — Edge security/threat model (this finding lives there).
- EDGE-064 — path-class normalisation (UNC + tilde) — subset of (e).
- EDGE-066 — hook-kind coverage — subset of (a).
