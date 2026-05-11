// Package legacyshim translates between the legacy job-policy /
// edge-policy authoring shapes (`core/infra/config.InputPolicyRule`,
// `OutputPolicyRule`, `PolicyRule` with `VelocityConfig`,
// `PolicyDecision`, `core/edge.EdgeDecision`) and the unified
// Policy Studio shapes defined in `core/policy` (`Rule`, `Decision`,
// `Bundle`, `RuleScope`, `BundleMetadata`).
//
// The package is the bridge that lets pre-cut-over consumers — the
// existing dashboard `/govern/*` surfaces, `cordumctl`, partner SDKs —
// keep speaking their old request/response shapes while the gateway
// internally migrates onto the unified evaluator. The translation
// helpers are pure (no I/O, no globals) and round-trip preserving:
// `RuleToInputPolicyRule(InputPolicyRuleToRule(x))` reconstructs the
// authored fields bit-for-bit, validated by golden fixtures in
// legacyshim_test.go.
//
// During the transition window every shimmed call is counted by the
// `cordum_legacy_policy_api_calls_total{endpoint,shape}` Prometheus
// counter (metrics.go). The sunset criterion — and the trigger for
// the follow-up task that removes this package — is "metric reads zero
// for 30 consecutive days post cut-over".
package legacyshim
