// Package policyreplay hosts shared primitives that sit on top of the
// policy engine but aren't part of the engine itself. They support two
// sibling pipelines without code duplication:
//
//   - Policy replay (gateway.handlePolicyReplay): iterates historical
//     jobs, re-evaluates them against a candidate bundle, and reports
//     drift direction per job.
//   - Eval runner (core/evals/runner): iterates curated dataset
//     entries, evaluates against current or candidate policy, and
//     classifies each entry as pass / fail / regression.
//
// The three primitives in policyreplay.go (DecisionSeverity,
// CompareDecisions, ProtoDecisionToString) cover the decision-compare
// + proto-stringify logic both pipelines share. A richer
// EvaluateJobRequest wrapper that threads a *pb.JobRequest through
// policybundles.EvaluatePolicyCheck is intentionally NOT included in
// this package right now because core/controlplane/gateway/policybundles
// transitively depends on core/infra/store and core/audit, both of
// which are in an incomplete sibling-task state as of 2026-04-20.
// When those dependencies are green again, the wrapper can move here
// (it's a ~30-line function); until then, callers should call
// policybundles.EvaluatePolicyCheck directly.
//
// Package deliberately does NOT re-export policybundles' already-public
// BuildPolicyFromBundles / CloneBundleMap — callers should import
// policybundles directly.
package policyreplay
