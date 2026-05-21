// Package actiongates implements Cordum's deterministic pre-dispatch
// action-layer gates.
//
// Each gate is a small, single-purpose evaluator that inspects the
// structured ActionDescriptor on a PolicyInput and returns an
// ActionGateDecision. The pipeline runs gates in a fixed order
// (tenant -> file -> url -> mcp -> mutation -> provenance) and
// short-circuits on the first non-ALLOW. When ActionDescriptor is
// nil or its Kind/Verb is unrelated to the gate, the gate returns
// ALLOW and the pipeline moves on.
//
// Design rules (enforced by code review, not by parsers):
//
//   - Gates MUST consume structured fields only. They MUST NOT regex
//     over free-form prompts or otherwise approximate a content
//     classifier — that path lives upstream.
//   - Tenant identity is sourced from auth (AuthContext.Tenant), never
//     from the request body. Body-claimed tenant is for diagnostics only.
//   - Approval claims are untrusted text. Only a backend EdgeApproval
//     resolved via ApprovalLookup grants destructive actions.
//   - Failures in side-channels (approval store, audit chain) MUST fail
//     closed with Code=internal_error or service_unavailable, never
//     silently ALLOW.
//
// Output mapping at the HTTP boundary follows the existing edge error
// envelope: unauthorized->401, access_denied->403, not_found->404,
// conflict->409, internal_error->500, service_unavailable->503. The
// require_human Code is informational at the simulate endpoint (200)
// and triggers the existing inline-approval workflow at the edge.
package actiongates
