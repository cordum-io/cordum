package gateway

import (
	"errors"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
)

// ErrSpoofedGovernanceLabel is returned when a client-supplied label
// uses one of the reserved governance / multi-agent prefixes (only the
// gateway may set those — never the client). Cf. stripReservedLabels,
// which silently strips ALL "_*" labels; this stricter check fires
// BEFORE strip so a spoofing attempt is loud (HTTP 400 + audit event)
// rather than swallowed.
var ErrSpoofedGovernanceLabel = errors.New("client may not set reserved governance/multi-agent labels")

// rejectReservedGovernanceLabels returns ErrSpoofedGovernanceLabel if
// any key in labels begins with config.GovernanceReservedLabelPrefix
// ("_governance.") or config.MultiAgentReservedLabelPrefix ("_ma.").
// Other underscored labels (e.g. "_delegation.*", "_content.*") are
// out of scope here — they are stripped silently by stripReservedLabels.
func rejectReservedGovernanceLabels(labels map[string]string) error {
	for k := range labels {
		if strings.HasPrefix(k, config.GovernanceReservedLabelPrefix) ||
			strings.HasPrefix(k, config.MultiAgentReservedLabelPrefix) {
			return ErrSpoofedGovernanceLabel
		}
	}
	return nil
}

// BuildGovernanceInputParams collects the verified inputs needed to
// construct a config.GovernanceInput. Every field on this struct is
// expected to originate from a backend-verifiable source (auth context,
// delegation token verifier, approval store lookup, scheduler-owned
// resource records). Client-supplied claims must NOT be passed through
// without verification.
type BuildGovernanceInputParams struct {
	Op              config.GovernanceOperation
	AuthCtx         *auth.AuthContext
	DelegCtx        *config.DelegationContext
	ChildAgentID    string
	ChildTenant     string
	DelegatedScopes []string
	HandoffSource   string
	ApprovalRef     string
	ApprovalStatus  string
	SharedMemKey    string
	WriteKind       config.SharedMemoryWriteKind
	PolicyMutation  bool
	Capabilities    []string
	ResourceDeltas  []config.ResourceDelta
	ProvenanceRef   string
	VerifiedAt      int64
	FreshnessSec    int64
}

// BuildGovernanceInput assembles a typed multi-agent governance input
// from server-verified records. Per epic rail #3, every field is
// populated from a backend-verifiable source — NOT from user-claimed
// text or client labels. Crucially:
//
//   - Tenant always comes from auth.AuthContext.Tenant. This overrides
//     any body-claimed tenant on the request shape; a client cannot
//     escalate cross-tenant by lying in the JSON body.
//   - Parent.AgentID comes from auth.AuthContext.PrincipalID (the
//     authenticated submitter).
//   - IssuerChain comes from the verified delegation chain projection
//     (projectGovernanceIssuerChain); empty if no delegation was used.
//   - ApprovalRef/ApprovalStatus come from the Cordum approval store
//     lookup performed by the caller (not from client text).
//
// Returns nil if AuthCtx is nil. The returned struct still needs to
// pass config.ValidateGovernanceInput before evaluator dispatch.
func BuildGovernanceInput(p BuildGovernanceInputParams) *config.GovernanceInput {
	if p.AuthCtx == nil {
		return nil
	}
	parentTenant := strings.TrimSpace(p.AuthCtx.Tenant)
	childTenant := strings.TrimSpace(p.ChildTenant)
	if childTenant == "" {
		childTenant = parentTenant
	}
	in := &config.GovernanceInput{
		Operation: p.Op,
		Parent: config.AgentIdentity{
			AgentID: strings.TrimSpace(p.AuthCtx.PrincipalID),
			Tenant:  parentTenant,
		},
		Child: config.AgentIdentity{
			AgentID: strings.TrimSpace(p.ChildAgentID),
			Tenant:  childTenant,
		},
		Tenant:                parentTenant,
		DelegatedScopes:       append([]string(nil), p.DelegatedScopes...),
		HandoffSource:         strings.TrimSpace(p.HandoffSource),
		ApprovalRef:           strings.TrimSpace(p.ApprovalRef),
		ApprovalStatus:        strings.TrimSpace(p.ApprovalStatus),
		SharedMemoryTargetKey: strings.TrimSpace(p.SharedMemKey),
		WriteKind:             p.WriteKind,
		PolicyStateMutation:   p.PolicyMutation,
		RequestedCapabilities: append([]string(nil), p.Capabilities...),
		ResourceDeltas:        append([]config.ResourceDelta(nil), p.ResourceDeltas...),
		ProvenanceRef:         strings.TrimSpace(p.ProvenanceRef),
		VerifiedAt:            p.VerifiedAt,
		FreshnessWindowSec:    p.FreshnessSec,
	}
	if p.DelegCtx != nil {
		in.IssuerChain = projectGovernanceIssuerChain(p.DelegCtx)
	}
	return in
}

// projectGovernanceIssuerChain converts the gateway's
// config.DelegationContext (verified JWT projection) into the
// structured config.IssuerChainEntry slice consumed by the governance
// evaluator. Each chain link becomes an entry whose IssuerRoot is the
// chain's verified root anchor and whose Issuer is the link's agent
// identifier. JTI is recorded on the first entry only — the verified
// JWT carries one JTI for the whole chain — so the evaluator can
// replay-protect on it without duplication.
func projectGovernanceIssuerChain(d *config.DelegationContext) []config.IssuerChainEntry {
	if d == nil || len(d.IssuerChain) == 0 {
		return nil
	}
	entries := make([]config.IssuerChainEntry, 0, len(d.IssuerChain))
	for i, link := range d.IssuerChain {
		entry := config.IssuerChainEntry{
			IssuerRoot: d.RootIssuer,
			Issuer:     link,
		}
		if i == 0 {
			entry.JTI = d.JTI
		}
		entries = append(entries, entry)
	}
	return entries
}
