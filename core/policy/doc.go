// Package policy holds the unified Rule, Decision, and Bundle shapes that
// subsume Cordum's previously split job-policy (input/output/velocity) and
// edge-policy authoring surfaces. The shapes are storage- and
// evaluator-agnostic: core/safetykernel and core/edge consume Rule and emit
// Decision; the bundle store keys by Bundle.ID.
package policy
