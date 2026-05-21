package actiongates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// MutationGate enforces approval + provenance discipline for destructive
// verbs. Non-destructive verbs (read/write) and non-mutation kinds short-
// circuit to the zero decision so the pipeline continues.
//
// The gate trusts neither ApprovalClaim.ClaimText ("approved by CFO") nor
// any tenant claim on the action body. Authorization is granted only when
// the backend resolves the caller-supplied ApprovalClaim.ApprovalRef to
// that exact EdgeApproval, and the resolved record (a) belongs to the
// auth tenant, (b) is not self-approved, (c) is in
// status "approved" with decision "approve", (d) has not been consumed,
// (e) has not expired, and (f) carries an ActionHash matching the
// canonical hash of the current descriptor.
//
// Evaluate is side-effect free: it never consumes the approval. The
// execute-time single-use transition belongs to the Edge approval
// ClaimApproval CAS path after the caller presents the bound ref.
type MutationGate struct {
	approvals ApprovalLookup
	resources ResourceLookup
}

// MutationGateOptions configures the gate. Approvals SHOULD be wired to a
// Cordum approval store in production; Resources is optional and skipped
// when nil (callers that cannot afford a synchronous existence check
// accept the downstream error path).
type MutationGateOptions struct {
	Approvals ApprovalLookup
	Resources ResourceLookup
}

// NewMutationGate returns a mutation gate bound to the provided lookups.
// A nil Approvals lookup causes every destructive action to fail closed
// with internal_error; this matches the "no silent allow" rule even when
// integration is mis-wired in test/dev.
func NewMutationGate(opts MutationGateOptions) *MutationGate {
	return &MutationGate{approvals: opts.Approvals, resources: opts.Resources}
}

func (g *MutationGate) ID() string { return GateIDMutation }

// destructiveVerbs is the set of action verbs the gate fires on. Adding a
// new destructive verb requires updating both this set and the matching
// public ActionVerb const in core/infra/config/safety_policy.go.
var destructiveVerbs = map[config.ActionVerb]struct{}{
	config.ActionVerbDelete:        {},
	config.ActionVerbDrop:          {},
	config.ActionVerbTruncate:      {},
	config.ActionVerbExport:        {},
	config.ActionVerbPayment:       {},
	config.ActionVerbAdminGrant:    {},
	config.ActionVerbAdminRevoke:   {},
	config.ActionVerbRoleAssign:    {},
	config.ActionVerbRoleRemove:    {},
	config.ActionVerbLicenseCreate: {},
	config.ActionVerbLicenseRevoke: {},
	config.ActionVerbLicenseChange: {},
	config.ActionVerbKeyRotate:     {},
	config.ActionVerbKeyDelete:     {},
	config.ActionVerbSecretsWrite:  {},
	config.ActionVerbSecretsDelete: {},
	config.ActionVerbConfigWrite:   {},
	config.ActionVerbConfigDelete:  {},
	config.ActionVerbBackupRestore: {},
	config.ActionVerbTenantCreate:  {},
	config.ActionVerbTenantDelete:  {},
}

// IsDestructiveVerb reports whether v is in the set of destructive verbs.
// Exported for use by sibling gates (provenance) that share the policy.
func IsDestructiveVerb(v config.ActionVerb) bool {
	_, ok := destructiveVerbs[v]
	return ok
}

func (g *MutationGate) Evaluate(ctx context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if act.Kind != config.ActionKindMutation || !IsDestructiveVerb(act.Verb) {
		return ActionGateDecision{}
	}

	actx := auth.FromContext(ctx)
	if actx == nil || strings.TrimSpace(actx.Tenant) == "" {
		return mutDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeUnauthorized,
			"authentication required", "missing_auth")
	}

	if act.ApprovalClaim == nil || strings.TrimSpace(act.ApprovalClaim.ApprovalRef) == "" {
		return mutDecision(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, act, CodeRequireHuman,
			"destructive action requires human approval", "missing_approval")
	}

	approval, failure := bindApprovalRef(ctx, g.approvals, actx, act, act.ApprovalClaim.ApprovalRef)
	if failure.failed() {
		return mutDecision(failure.Decision, act, failure.Code, failure.Reason, failure.SubReason)
	}

	if g.resources != nil && act.TargetResource != nil && strings.TrimSpace(act.TargetResource.ID) != "" {
		exists, rerr := g.resources.ResourceExists(ctx, actx.Tenant, *act.TargetResource)
		if rerr != nil {
			return mutDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeServiceUnavailable,
				"target lookup unavailable", "resource_lookup_failed:"+sanitizeErr(rerr))
		}
		if !exists {
			return mutDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeNotFound,
				"target resource not found", "resource_not_found")
		}
	}

	extra := mutExtra(act, "approved")
	extra["approval_ref"] = approval.ApprovalRef
	extra["single_use"] = "true"
	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_ALLOW,
		GateID:    GateIDMutation,
		Reason:    "destructive action authorized by backend approval",
		SubReason: "approved",
		Extra:     extra,
	}
}

func mutDecision(decision pb.DecisionType, act *config.ActionDescriptor, code, reason, sub string) ActionGateDecision {
	return ActionGateDecision{
		Decision:  decision,
		GateID:    GateIDMutation,
		Code:      code,
		Reason:    reason,
		SubReason: sub,
		Extra:     mutExtra(act, sub),
	}
}

func mutExtra(act *config.ActionDescriptor, sub string) map[string]string {
	out := map[string]string{
		"gate":       GateIDMutation,
		"sub_reason": sub,
		"verb":       string(act.Verb),
	}
	if act.TargetResource != nil && act.TargetResource.Type != "" {
		out["target_type"] = act.TargetResource.Type
	}
	return out
}

// sanitizeErr strips newlines from an error message so the sub_reason
// stays single-line for SIEM grep'ability.
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// CanonicalActionHash returns a stable hex-encoded SHA-256 of the action's
// authorization-relevant fields. Map ordering is normalized so equivalent
// payloads hash identically regardless of insertion order. nil returns "".
//
// The hash binds an approval to a specific action shape: when the resolved
// approval.ActionHash differs from this value, the gate raises an
// approval_mismatch conflict — the approval was granted for a different
// payload.
func CanonicalActionHash(act *config.ActionDescriptor) string {
	if act == nil {
		return ""
	}
	payload := canonicalPayload(act)
	buf, err := json.Marshal(payload)
	if err != nil {
		// All entries in payload are primitives/strings/maps/slices that
		// json.Marshal can encode; this branch is defensive.
		return ""
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// canonicalPayload builds a deterministic representation of the action
// suitable for hashing. encoding/json sorts map keys, so map[string]X
// values produce stable bytes. Slices that are conceptually sets
// (Wildcards, RiskTags) are sorted before being embedded.
func canonicalPayload(act *config.ActionDescriptor) map[string]any {
	payload := map[string]any{
		"kind": string(act.Kind),
		"verb": string(act.Verb),
	}
	if act.Server != "" {
		payload["server"] = act.Server
	}
	if act.Tool != "" {
		payload["tool"] = act.Tool
	}
	if act.TargetPath != "" {
		payload["target_path"] = act.TargetPath
	}
	if act.TargetURL != "" {
		payload["target_url"] = act.TargetURL
	}
	if act.TargetResource != nil {
		payload["target_resource"] = map[string]any{
			"type":         act.TargetResource.Type,
			"id":           act.TargetResource.ID,
			"owner_tenant": act.TargetResource.OwnerTenant,
		}
	}
	if len(act.Filters) > 0 {
		payload["filters"] = act.Filters
	}
	if len(act.Wildcards) > 0 {
		payload["wildcards"] = sortedCopy(act.Wildcards)
	}
	if len(act.Args) > 0 {
		payload["args"] = act.Args
	}
	if len(act.RiskTags) > 0 {
		payload["risk_tags"] = sortedCopy(act.RiskTags)
	}
	return payload
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}
