package config

import "errors"

// GovernanceOperation is the type of multi-agent governance operation
// being requested. Each kind maps to a distinct risk profile and a distinct
// set of required-provenance invariants enforced by the governance
// evaluator at core/governance/evaluator.
type GovernanceOperation string

const (
	GovernanceOpDelegation         GovernanceOperation = "delegation"
	GovernanceOpHandoff            GovernanceOperation = "handoff"
	GovernanceOpSharedContextWrite GovernanceOperation = "shared_context_write"
	GovernanceOpApprovalBypass     GovernanceOperation = "approval_bypass"
	GovernanceOpResourceAllocation GovernanceOperation = "resource_allocation"
	GovernanceOpTrustAssertion     GovernanceOperation = "trust_assertion"
)

// SharedMemoryWriteKind classifies what KIND of state a shared-memory
// write touches. Plain raw/chat writes stay backward-compatible and run
// through the existing context engine path. Writes that mutate policy/
// trust/directive state MUST carry a verified writer and verified
// provenance; the evaluator fails closed if either is absent.
type SharedMemoryWriteKind string

const (
	SharedMemoryWriteRaw               SharedMemoryWriteKind = "raw"
	SharedMemoryWriteChat              SharedMemoryWriteKind = "chat"
	SharedMemoryWriteSharedPolicyState SharedMemoryWriteKind = "shared_policy_state"
	SharedMemoryWriteSharedTrustState  SharedMemoryWriteKind = "shared_trust_state"
	SharedMemoryWriteSharedDirective   SharedMemoryWriteKind = "shared_directive"
)

// Reserved label prefixes that clients MAY NOT set. The gateway rejects
// any client-supplied label whose key starts with one of these prefixes
// (see core/controlplane/gateway/helpers.go::stripReservedLabels).
// Listed here so the gateway and the evaluator share a single source of
// truth for what's spoofable.
const (
	GovernanceReservedLabelPrefix = "_governance."
	MultiAgentReservedLabelPrefix = "_ma."
)

// Stable rule IDs emitted by the governance evaluator. They surface in
// audit/SIEM and HTTP error envelopes; changing them is a breaking
// change.
const (
	GovernanceRuleUnverifiedIssuer            = "ma_unverified_issuer"
	GovernanceRuleUnverifiedTrustAssertion    = "ma_unverified_trust_assertion"
	GovernanceRuleChildBypass                 = "ma_child_bypass"
	GovernanceRuleApprovalBypassMissingRecord = "ma_approval_bypass_missing_record"
	GovernanceRuleIssuerChainStale            = "ma_issuer_chain_stale"
	GovernanceRuleCrossTenant                 = "ma_cross_tenant"
	GovernanceRuleScopeEscalation             = "ma_scope_escalation"
	GovernanceRuleResourceEscalation          = "ma_resource_escalation"
	GovernanceRuleSharedContextUnverifiedWriter = "ma_shared_context_unverified_writer"
	// GovernanceRuleIssuerRootNotAllowed denies an issuer chain whose
	// root is not in the operator-configured allowlist. Distinct from
	// GovernanceRuleUnverifiedIssuer (which fires when the issuer
	// signature itself cannot be verified) so SIEM/audit/error envelopes
	// can route remediation correctly.
	GovernanceRuleIssuerRootNotAllowed = "ma_issuer_root_not_allowed"
)

// AgentIdentity is the typed identity of a parent or child agent
// participating in a multi-agent governance operation. AgentID and Tenant
// MUST be non-empty; both are populated server-side from authenticated
// records, NOT from client labels.
type AgentIdentity struct {
	AgentID string `yaml:"agent_id" json:"agent_id"`
	Tenant  string `yaml:"tenant"   json:"tenant"`
}

// IssuerChainEntry is one step in a delegation/issuer chain proving how
// trust flowed from a root anchor to the current operation. Each entry
// MUST have non-empty IssuerRoot and Issuer; IssuedAt/ExpiresAt are
// unix seconds (ExpiresAt=0 means no expiry).
type IssuerChainEntry struct {
	IssuerRoot string `yaml:"issuer_root" json:"issuer_root"`
	Issuer     string `yaml:"issuer"      json:"issuer"`
	JTI        string `yaml:"jti"         json:"jti"`
	IssuedAt   int64  `yaml:"issued_at"   json:"issued_at"`
	ExpiresAt  int64  `yaml:"expires_at"  json:"expires_at"`
}

// ResourceDelta describes a requested delta against the parent agent's
// resource allocation. Scope MUST be non-empty; Amount MUST be >= 0
// (deltas cannot remove resources, only request more). Capability is
// optional — e.g. a Scope="cpu",Amount=2 entry requests two more CPU
// units while Scope="mem",Capability="rw" requests a new read/write
// capability on memory.
type ResourceDelta struct {
	Scope      string `yaml:"scope"      json:"scope"`
	Amount     int64  `yaml:"amount"     json:"amount"`
	Capability string `yaml:"capability" json:"capability"`
}

// GovernanceInput is the typed multi-agent governance input fed to the
// evaluator. It is populated by the gateway/scheduler from
// authenticated records (auth.AuthContext, delegation store, approval
// store, scheduler-owned resource records). Per epic rail #3, every
// field on this struct is populated from a backend-verifiable source —
// NOT from user-claimed text such as "approved by CFO". Client-supplied
// label payloads with the GovernanceReservedLabelPrefix /
// MultiAgentReservedLabelPrefix prefix are rejected before construction.
type GovernanceInput struct {
	Operation             GovernanceOperation   `yaml:"operation"                json:"operation"`
	Parent                AgentIdentity         `yaml:"parent"                   json:"parent"`
	Child                 AgentIdentity         `yaml:"child"                    json:"child"`
	IssuerChain           []IssuerChainEntry    `yaml:"issuer_chain"             json:"issuer_chain"`
	DelegatedScopes       []string              `yaml:"delegated_scopes"         json:"delegated_scopes"`
	HandoffSource         string                `yaml:"handoff_source"           json:"handoff_source"`
	ApprovalRef           string                `yaml:"approval_ref"             json:"approval_ref"`
	ApprovalStatus        string                `yaml:"approval_status"          json:"approval_status"`
	SharedMemoryTargetKey string                `yaml:"shared_memory_target_key" json:"shared_memory_target_key"`
	WriteKind             SharedMemoryWriteKind `yaml:"write_kind"               json:"write_kind"`
	PolicyStateMutation   bool                  `yaml:"policy_state_mutation"    json:"policy_state_mutation"`
	RequestedCapabilities []string              `yaml:"requested_capabilities"   json:"requested_capabilities"`
	ResourceDeltas        []ResourceDelta       `yaml:"resource_deltas"          json:"resource_deltas"`
	Tenant                string                `yaml:"tenant"                   json:"tenant"`
	ProvenanceRef         string                `yaml:"provenance_ref"           json:"provenance_ref"`
	VerifiedAt            int64                 `yaml:"verified_at"              json:"verified_at"`
	FreshnessWindowSec    int64                 `yaml:"freshness_window_sec"     json:"freshness_window_sec"`
}

// GovernancePolicy is the operator-tunable policy expression the
// evaluator consults when deciding ALLOW / DENY / REQUIRE_HUMAN for a
// GovernanceInput. Defaults are fail-closed (same-tenant required, no
// escalation, deny on stale provenance, deny when verified provenance
// is required and the input has none).
type GovernancePolicy struct {
	MaxDelegationDepth        int                          `yaml:"max_delegation_depth"        json:"max_delegation_depth"`
	RequireVerifiedProvenance map[GovernanceOperation]bool `yaml:"require_verified_provenance" json:"require_verified_provenance"`
	SameTenantRequired        bool                         `yaml:"same_tenant_required"        json:"same_tenant_required"`
	AllowedIssuerRoots        []string                     `yaml:"allowed_issuer_roots"        json:"allowed_issuer_roots"`
	AllowScopeEscalation      bool                         `yaml:"allow_scope_escalation"      json:"allow_scope_escalation"`
	AllowResourceEscalation   bool                         `yaml:"allow_resource_escalation"   json:"allow_resource_escalation"`
	StaleProvenanceDecision   string                       `yaml:"stale_provenance_decision"   json:"stale_provenance_decision"`
}

// DefaultGovernancePolicy returns the fail-closed default policy used
// when none is configured. Operators may override individual fields in
// safety.yaml; missing fields fall back to these defaults.
func DefaultGovernancePolicy() GovernancePolicy {
	return GovernancePolicy{
		MaxDelegationDepth: 5,
		RequireVerifiedProvenance: map[GovernanceOperation]bool{
			GovernanceOpDelegation:         true,
			GovernanceOpHandoff:            true,
			GovernanceOpSharedContextWrite: true,
			GovernanceOpApprovalBypass:     true,
			GovernanceOpResourceAllocation: true,
			GovernanceOpTrustAssertion:     true,
		},
		SameTenantRequired:      true,
		AllowScopeEscalation:    false,
		AllowResourceEscalation: false,
		StaleProvenanceDecision: "deny",
	}
}

// Named errors returned by ValidateGovernanceInput. Tests assert against
// these sentinels via errors.Is, so callers can branch deterministically
// on the kind of validation failure rather than parsing strings.
var (
	ErrGovernanceEmptyOperation             = errors.New("governance: operation is required")
	ErrGovernanceInvalidOperation           = errors.New("governance: invalid operation value")
	ErrGovernanceEmptyTenant                = errors.New("governance: tenant is required")
	ErrGovernanceEmptyParentAgentID         = errors.New("governance: parent.agent_id is required")
	ErrGovernanceEmptyParentTenant          = errors.New("governance: parent.tenant is required")
	ErrGovernanceEmptyChildAgentID          = errors.New("governance: child.agent_id is required")
	ErrGovernanceEmptyChildTenant           = errors.New("governance: child.tenant is required")
	ErrGovernanceEmptyIssuerChain           = errors.New("governance: issuer_chain is required for this operation")
	ErrGovernanceEmptyIssuerChainEntry      = errors.New("governance: issuer_chain entry has empty issuer or issuer_root")
	ErrGovernanceNegativeFreshnessWindow    = errors.New("governance: freshness_window_sec must be > 0 for verified-provenance operations")
	ErrGovernanceInvalidResourceDelta       = errors.New("governance: resource_delta has empty scope or negative amount")
	ErrGovernanceMalformedSharedMemoryWrite = errors.New("governance: shared_context_write requires non-empty target_key + write_kind")
	ErrGovernanceInvalidWriteKind           = errors.New("governance: invalid write_kind value")
)

// ValidateGovernanceInput checks that the input is well-formed for the
// declared operation. Returns a typed error matching one of the
// ErrGovernance* sentinels via errors.Is so callers can branch
// deterministically on the failure mode. Policy-level decisions (cross-
// tenant rejection, escalation invariants, stale provenance) belong on
// the evaluator at core/governance/evaluator; this function only
// enforces input well-formedness.
func ValidateGovernanceInput(in *GovernanceInput) error {
	if in == nil {
		return ErrGovernanceEmptyOperation
	}
	if in.Operation == "" {
		return ErrGovernanceEmptyOperation
	}
	if !isValidGovernanceOperation(in.Operation) {
		return ErrGovernanceInvalidOperation
	}
	if in.Tenant == "" {
		return ErrGovernanceEmptyTenant
	}
	if in.Parent.AgentID == "" {
		return ErrGovernanceEmptyParentAgentID
	}
	if in.Parent.Tenant == "" {
		return ErrGovernanceEmptyParentTenant
	}
	if in.Child.AgentID == "" {
		return ErrGovernanceEmptyChildAgentID
	}
	if in.Child.Tenant == "" {
		return ErrGovernanceEmptyChildTenant
	}
	if err := validateOperationInvariants(in); err != nil {
		return err
	}
	for _, d := range in.ResourceDeltas {
		if d.Scope == "" || d.Amount < 0 {
			return ErrGovernanceInvalidResourceDelta
		}
	}
	return nil
}

// validateOperationInvariants enforces per-operation invariants that
// depend on the operation enum value. Split out so ValidateGovernanceInput
// stays small and the switch is in one place.
func validateOperationInvariants(in *GovernanceInput) error {
	switch in.Operation {
	case GovernanceOpDelegation, GovernanceOpHandoff, GovernanceOpApprovalBypass, GovernanceOpResourceAllocation, GovernanceOpTrustAssertion:
		if in.FreshnessWindowSec <= 0 {
			return ErrGovernanceNegativeFreshnessWindow
		}
		if requiresIssuerChain(in.Operation) {
			if len(in.IssuerChain) == 0 {
				return ErrGovernanceEmptyIssuerChain
			}
			for _, e := range in.IssuerChain {
				if e.Issuer == "" || e.IssuerRoot == "" {
					return ErrGovernanceEmptyIssuerChainEntry
				}
			}
		}
	case GovernanceOpSharedContextWrite:
		if in.SharedMemoryTargetKey == "" || in.WriteKind == "" {
			return ErrGovernanceMalformedSharedMemoryWrite
		}
		if !isValidSharedMemoryWriteKind(in.WriteKind) {
			return ErrGovernanceInvalidWriteKind
		}
		if in.PolicyStateMutation && in.FreshnessWindowSec <= 0 {
			return ErrGovernanceNegativeFreshnessWindow
		}
	}
	return nil
}

// requiresIssuerChain reports whether the operation kind needs a
// non-empty issuer chain to be well-formed. Shared-memory writes carry
// provenance via writer_agent_id + provenance_ref on the proto message,
// not via an issuer chain, so they are excluded here.
func requiresIssuerChain(op GovernanceOperation) bool {
	switch op {
	case GovernanceOpDelegation, GovernanceOpHandoff, GovernanceOpApprovalBypass, GovernanceOpResourceAllocation, GovernanceOpTrustAssertion:
		return true
	default:
		return false
	}
}

func isValidGovernanceOperation(op GovernanceOperation) bool {
	switch op {
	case GovernanceOpDelegation, GovernanceOpHandoff, GovernanceOpSharedContextWrite,
		GovernanceOpApprovalBypass, GovernanceOpResourceAllocation, GovernanceOpTrustAssertion:
		return true
	default:
		return false
	}
}

func isValidSharedMemoryWriteKind(k SharedMemoryWriteKind) bool {
	switch k {
	case SharedMemoryWriteRaw, SharedMemoryWriteChat, SharedMemoryWriteSharedPolicyState,
		SharedMemoryWriteSharedTrustState, SharedMemoryWriteSharedDirective:
		return true
	default:
		return false
	}
}
