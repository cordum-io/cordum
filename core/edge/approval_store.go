package edge

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ApprovalRefPrefix = "edge_appr_"
	// DefaultApprovalMaxTTL is the production safety cap for an approval's
	// hold-creation ExpiresAt. Operator overrides may shorten/lengthen this
	// cap, but they must remain positive.
	DefaultApprovalMaxTTL     = 30 * time.Minute
	defaultApprovalTTL        = 5 * time.Minute
	approvalRefRandomBytes    = 18
	maxApprovalFieldBytes     = 2048
	maxApprovalReasonBytes    = 4096
	maxApprovalMetadataFields = 64
	// maxEventsPerApprovalValidation caps how many AgentActionEvents the
	// approval-store loadEventFromTx pulls under WATCH/MULTI/EXEC when
	// validating an EnqueueApproval. EDGE-058 closed an unbounded LRange(0,-1)
	// DoS vector at approval_store_redis.go:692 — an agent looping AppendEvent
	// could fan an execution's event list well beyond what fits in a TX read,
	// pinning gateway memory and breaking the EXEC for healthy executions
	// sharing the same connection. 500 is the operator-facing ceiling: in
	// practice, an EnqueueApproval scoped to one Claude tool action needs to
	// match a single recent event, so a few hundred recent entries suffice
	// for the existing PolicySnapshot/InputHash binding check at
	// validateApprovalEventBinding.
	maxEventsPerApprovalValidation = 500
)

// ErrApprovalConflict marks a fail-closed lifecycle conflict that API
// handlers should surface as HTTP 409 without leaking internal Redis details.
// Callers needing the specific failure family use errors.As against
// *ApprovalConflictError to extract Kind.
var ErrApprovalConflict = errors.New("edge approval: conflict")

// ApprovalConflictKind is the discriminator on an approval-lifecycle
// failure. The MCP entry-path layer maps Kind directly to the
// JSON-RPC -32096 error.data.kind enum, so the snake_case values are
// part of the wire contract.
type ApprovalConflictKind string

const (
	ApprovalConflictKindUnknown        ApprovalConflictKind = ""
	ApprovalConflictKindNotFound       ApprovalConflictKind = "not_found"
	ApprovalConflictKindRejected       ApprovalConflictKind = "rejected"
	ApprovalConflictKindExpired        ApprovalConflictKind = "expired"
	ApprovalConflictKindConsumed       ApprovalConflictKind = "consumed"
	ApprovalConflictKindArgsMismatch   ApprovalConflictKind = "args_mismatch"
	ApprovalConflictKindPolicyMismatch ApprovalConflictKind = "policy_mismatch"
	ApprovalConflictKindTupleMismatch  ApprovalConflictKind = "tuple_mismatch"
	ApprovalConflictKindSelfApproval   ApprovalConflictKind = "self_approval"
	ApprovalConflictKindCrossTenant    ApprovalConflictKind = "cross_tenant"
)

// ApprovalConflictError is the typed wrapper around ErrApprovalConflict
// carrying the specific failure family. errors.Is on the sentinel still
// works for backward compatibility (the wrap chain includes
// ErrApprovalConflict); new callers can errors.As to extract Kind.
type ApprovalConflictError struct {
	Kind   ApprovalConflictKind
	Reason string
}

func (e *ApprovalConflictError) Error() string {
	if e == nil {
		return "edge approval: conflict"
	}
	if e.Reason == "" {
		return fmt.Sprintf("edge approval: conflict (kind=%s)", e.Kind)
	}
	return fmt.Sprintf("edge approval: conflict (kind=%s): %s", e.Kind, e.Reason)
}

func (e *ApprovalConflictError) Unwrap() error { return ErrApprovalConflict }

// newApprovalConflict returns a typed conflict error chained to
// ErrApprovalConflict so existing errors.Is callers keep working.
func newApprovalConflict(kind ApprovalConflictKind, reason string) error {
	return &ApprovalConflictError{Kind: kind, Reason: reason}
}

// ErrEventListTooLarge is returned by EnqueueApproval (via loadEventFromTx)
// when the parent execution's event list exceeds maxEventsPerApprovalValidation
// at the moment the approval is validated under WATCH/MULTI/EXEC. EDGE-058
// closed an unbounded LRange DoS vector by gating the inline read on LLEN;
// callers (gateway handlers) MUST map this to a stable 4xx code (422
// edge_event_list_too_large) so an attacker-induced fan-out cannot surface
// as a generic 500.
var ErrEventListTooLarge = errors.New("edge approval: execution event list exceeds inline-validation cap")

// EdgeApprovalRequest is the validated input for creating one action-scoped
// approval. It intentionally stores only hashes/snapshots/redacted metadata and
// never carries raw tool payloads.
type EdgeApprovalRequest struct {
	TenantID       string
	SessionID      string
	ExecutionID    string
	EventID        string
	PrincipalID    string
	Requester      string
	Reason         string
	RuleID         string
	PolicySnapshot string
	ActionHash     string
	InputHash      string
	ExpiresAt      time.Time
	TTL            time.Duration
	Labels         Labels
	Metadata       Metadata
}

// ListApprovalsQuery pages Edge approvals within one tenant. PrincipalID scopes
// list visibility for non-operator callers. Status uses the tenant/status or
// principal/status index; the tuple fields use the action tuple index. If tuple
// filters and status/principal filters are combined, the implementation returns
// the intersection after bounded reads.
type ListApprovalsQuery struct {
	TenantID    string
	PrincipalID string
	Status      ApprovalStatus
	SessionID   string
	ExecutionID string
	ActionHash  string
	Cursor      string
	Limit       int
}

// ApprovalPage is one bounded page of approvals.
type ApprovalPage struct {
	Items      []EdgeApproval
	NextCursor string
}

// ApprovalResolution describes a human resolver's approve/reject action.
type ApprovalResolution struct {
	TenantID    string
	ApprovalRef string
	ResolverID  string
	ResolvedBy  string
	Reason      string
	ResolvedAt  time.Time
}

// ApprovalClaimRequest is the agent-side consume-once check. The action tuple
// and policy snapshot are revalidated inside the store transition.
//
// CallerAgentID is the authenticated identity of the agent presenting the
// claim. When non-empty, the store-level CAS adds a defense-in-depth
// self-approval check: a claim where CallerAgentID matches the approval's
// ResolverID (the principal who issued /approve) is refused with
// ApprovalConflictError{Kind:ApprovalConflictKindSelfApproval}. In real MCP
// retry semantics the caller is the same principal as the original Requester
// (the agent re-issues the tool call with _approval_ref), so the Requester
// field is NOT a comparison target — only ResolverID is. The approve-time
// requesterMatchesApprover guard enforces Requester != ResolverID at the
// /approve API surface; this store-level check is defense-in-depth backing
// that guard so a refactor that bypasses the API-layer check still fails
// closed at the store.
type ApprovalClaimRequest struct {
	TenantID       string
	ApprovalRef    string
	SessionID      string
	ExecutionID    string
	EventID        string
	ActionHash     string
	InputHash      string
	PolicySnapshot string
	ConsumedAt     time.Time
	CallerAgentID  string
}

// GenerateApprovalRef returns a crypto-random, URL-safe approval handle.
func GenerateApprovalRef() (string, error) {
	buf := make([]byte, approvalRefRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return ApprovalRefPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func (r EdgeApprovalRequest) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"tenant_id", r.TenantID},
		{"session_id", r.SessionID},
		{"execution_id", r.ExecutionID},
		{"event_id", r.EventID},
		{"principal_id", r.PrincipalID},
		{"requester", r.Requester},
		{"policy_snapshot", r.PolicySnapshot},
		{"action_hash", r.ActionHash},
		{"input_hash", r.InputHash},
	} {
		if err := requireApprovalString(field.name, field.value, maxApprovalFieldBytes); err != nil {
			return err
		}
	}
	if len([]byte(strings.TrimSpace(r.Reason))) > maxApprovalReasonBytes {
		return fmt.Errorf("reason exceeds %d bytes", maxApprovalReasonBytes)
	}
	if len([]byte(strings.TrimSpace(r.RuleID))) > maxApprovalFieldBytes {
		return fmt.Errorf("rule_id exceeds %d bytes", maxApprovalFieldBytes)
	}
	if r.ExpiresAt.IsZero() && r.TTL < 0 {
		return fmt.Errorf("ttl must be non-negative")
	}
	if err := validateLabels("labels", r.Labels); err != nil {
		return err
	}
	if err := validateMetadata("metadata", r.Metadata); err != nil {
		return err
	}
	if len(r.Metadata) > maxApprovalMetadataFields {
		return fmt.Errorf("metadata exceeds %d fields", maxApprovalMetadataFields)
	}
	return nil
}

func (r ApprovalResolution) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"tenant_id", r.TenantID},
		{"approval_ref", r.ApprovalRef},
		{"resolver_id", r.ResolverID},
		{"resolved_by", r.ResolvedBy},
	} {
		if err := requireApprovalString(field.name, field.value, maxApprovalFieldBytes); err != nil {
			return err
		}
	}
	if len([]byte(strings.TrimSpace(r.Reason))) > maxApprovalReasonBytes {
		return fmt.Errorf("resolution_reason exceeds %d bytes", maxApprovalReasonBytes)
	}
	return nil
}

func (r ApprovalClaimRequest) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"tenant_id", r.TenantID},
		{"approval_ref", r.ApprovalRef},
		{"session_id", r.SessionID},
		{"execution_id", r.ExecutionID},
		{"event_id", r.EventID},
		{"action_hash", r.ActionHash},
		{"policy_snapshot", r.PolicySnapshot},
	} {
		if err := requireApprovalString(field.name, field.value, maxApprovalFieldBytes); err != nil {
			return err
		}
	}
	return nil
}

func requireApprovalString(field, value string, maxBytes int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if maxBytes > 0 && len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxBytes)
	}
	return nil
}
