// EDGE-143.6 — operator-defined exception declarations for ShadowAgent
// findings. Per §10.3 of docs/edge/kubernetes-ci-shadow-detector-design.md
// + Q8 binding governor ruling on task-de50a293 (comment-a17f4f1c).
//
// An exception is a tenant-scoped, time-bounded, operator-signed
// declaration that "findings matching this scope are intentional". The
// shadow store joins exceptions to incoming findings via a scope
// predicate (source_type + source_id + risk_level + signal_set) at
// CreateFinding emit time; matching findings are stamped with
// exception_id + false_positive_reason="operator_exception" and flipped
// to FindingStatusManagedSkip, then excluded from default-filter list
// queries.
//
// risk=high exception CREATION (and revoke-of-high) is gated by a
// step-up auth analogue to auth.PermDelegationImpersonate. The actor
// and the step-up factor that satisfied the gate are recorded in the
// shadow_agent.exception_created / .exception_revoked / .exception_applied
// audit events. The detector NEVER auto-creates exceptions.
package shadow

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FindingStatusManagedSkip is the lifecycle disposition for a finding
// suppressed by a matching active operator exception (§10.3). It is
// terminal in the same sense as resolved/suppressed: no further
// transitions are accepted. Default list queries hide managed_skip
// findings; ?include_managed_skip=true returns them.
const FindingStatusManagedSkip FindingStatus = "managed_skip"

// FalsePositiveReasonOperatorException stamps a finding suppressed via
// an operator-defined exception (§10.3). Other false-positive reasons
// remain reserved for future detector-self-suppression paths.
const FalsePositiveReasonOperatorException = "operator_exception"

// ExceptionStatus is the lifecycle disposition of an Exception record.
// Terminal states (revoked, expired) freeze the record; only Status,
// RevokedBy, RevokedAt, and RevocationReason mutate on revoke.
type ExceptionStatus string

const (
	ExceptionStatusActive  ExceptionStatus = "active"
	ExceptionStatusRevoked ExceptionStatus = "revoked"
	ExceptionStatusExpired ExceptionStatus = "expired"
)

// StepUpFactor records which form of elevated authentication satisfied
// the Q8 step-up gate at exception-create time. Persisted on the
// Exception record and copied into every audit event emitted on its
// behalf. "none" indicates the gate was not required (medium/low risk
// scope).
type StepUpFactor string

const (
	// StepUpFactorSignedAdminToken — the caller satisfied the gate via
	// the legacy "admin" role fallback (no explicit RBAC permission
	// match). Mapped to "signed admin token" because the admin role
	// itself is the strongest in-band identity assertion Cordum has.
	StepUpFactorSignedAdminToken StepUpFactor = "signed_admin_token"
	// StepUpFactorMFARecent — the caller satisfied the gate via the
	// explicit PermShadowExceptionHighRisk RBAC permission. Operators
	// granted this permission directly are presumed to have completed
	// the workflow's recent-MFA step before the grant.
	StepUpFactorMFARecent StepUpFactor = "mfa_recent"
	// StepUpFactorNone — the gate was not required (scope risk is
	// medium or low). Audit events still record "none" so SIEM rules
	// can pivot on the presence of the field without nil checks.
	StepUpFactorNone StepUpFactor = "none"
)

// exceptionIDPrefix mirrors findingIDPrefix — a stable opaque prefix
// applied to synthetic exception ids so downstream consumers can
// recognise shadow exceptions without parsing the suffix.
const exceptionIDPrefix = "shadow_exc_"

// Exception scope-predicate + size limits.
const (
	// maxExceptionExpiresAhead caps creation-time expires_at to 90 days
	// from now (§10.3 "max 90 days; longer requires re-affirmation").
	maxExceptionExpiresAhead = 90 * 24 * time.Hour
	// maxExceptionReasonBytes caps the operator-supplied free-text
	// rationale. Audit and dashboard surfaces echo the reason verbatim;
	// bounding it prevents Redis bloat from a malicious operator account.
	maxExceptionReasonBytes = 512
	// maxExceptionScopeSignals caps the per-exception signal_set length.
	// Mirror of maxShadowSignalSetEntries.
	maxExceptionScopeSignals = 16
	// maxExceptionsPerTenant is the soft cap on active exceptions per
	// tenant. MatchActiveExceptions scans the tenant index linearly, so
	// keep it bounded; the gateway list endpoint returns paginated.
	maxExceptionsPerTenant = 1000
	// validExceptionSignalRe — same alphabet as the finding signal regex.
	exceptionSignalPattern = `^[a-z0-9_]{1,32}$`
)

var validExceptionSignal = regexp.MustCompile(exceptionSignalPattern)

// Exception is the persisted operator-signed exception declaration. All
// scope fields are tenant-scoped; cross-tenant access returns
// ErrNotFound for parity with ShadowAgentFinding probing defense.
type Exception struct {
	ExceptionID      string          `json:"exception_id"`
	TenantID         string          `json:"tenant_id"`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
	ExpiresAt        time.Time       `json:"expires_at"`
	Reason           string          `json:"reason,omitempty"`
	ScopeSourceType  string          `json:"scope_source_type"`
	ScopeSourceID    string          `json:"scope_source_id,omitempty"`
	ScopeSignalSet   []string        `json:"scope_signal_set,omitempty"`
	ScopeRiskLevel   FindingRisk     `json:"scope_risk_level"`
	Status           ExceptionStatus `json:"status"`
	StepUpFactor     StepUpFactor    `json:"step_up_factor"`
	RevokedBy        string          `json:"revoked_by,omitempty"`
	RevokedAt        *time.Time      `json:"revoked_at,omitempty"`
	RevocationReason string          `json:"revocation_reason,omitempty"`
}

// CreateExceptionRequest is the validated input used to mint a new
// Exception record. CreatedBy and StepUpFactor are stamped by the
// handler from the authenticated principal + auth-gate outcome — they
// are NOT trusted from the wire body.
type CreateExceptionRequest struct {
	TenantID        string
	CreatedBy       string
	ExpiresAt       time.Time
	Reason          string
	ScopeSourceType string
	ScopeSourceID   string
	ScopeSignalSet  []string
	ScopeRiskLevel  FindingRisk
	StepUpFactor    StepUpFactor
}

// ListExceptionsQuery is the bounded filter set accepted by
// ListExceptions. Empty fields mean "no filter on this dimension".
// TenantID is REQUIRED; the store refuses cross-tenant scans.
type ListExceptionsQuery struct {
	TenantID        string
	Status          ExceptionStatus
	ScopeSourceType string
	ScopeRiskLevel  FindingRisk
	Limit           int
	Cursor          string
}

// ExceptionPage is the cursor-paginated list response. NextCursor is
// empty when the caller has consumed the index.
type ExceptionPage struct {
	Exceptions []Exception `json:"exceptions"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// RevokeExceptionRequest captures the revoke-side inputs. Idempotent
// when the exception is already revoked AND the inputs are byte-equal;
// otherwise returns ErrTerminalConflict.
type RevokeExceptionRequest struct {
	RevokedBy string
	Reason    string
}

// normalizeAndValidateException converts a CreateExceptionRequest into
// a persistable Exception. now is injected so tests can pin time
// without monkey-patching.
func normalizeAndValidateException(req CreateExceptionRequest, now time.Time, idGen func() string) (*Exception, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrValidation)
	}
	createdBy := strings.TrimSpace(req.CreatedBy)
	if createdBy == "" {
		return nil, fmt.Errorf("%w: created_by is required", ErrValidation)
	}
	if req.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("%w: expires_at is required", ErrValidation)
	}
	if !req.ExpiresAt.After(now) {
		return nil, fmt.Errorf("%w: expires_at must be in the future", ErrValidation)
	}
	if req.ExpiresAt.After(now.Add(maxExceptionExpiresAhead)) {
		return nil, fmt.Errorf("%w: expires_at exceeds 90-day cap", ErrValidation)
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > maxExceptionReasonBytes {
		return nil, fmt.Errorf("%w: reason exceeds %d bytes", ErrValidation, maxExceptionReasonBytes)
	}
	sourceType := strings.ToLower(strings.TrimSpace(req.ScopeSourceType))
	if sourceType == "" {
		return nil, fmt.Errorf("%w: scope.source_type is required", ErrValidation)
	}
	if _, ok := validShadowSourceType[sourceType]; !ok {
		return nil, fmt.Errorf("%w: scope.source_type must be one of local|kubernetes|ci|network", ErrValidation)
	}
	sourceID := strings.TrimSpace(req.ScopeSourceID)
	if len(sourceID) > maxShadowSourceIDBytes {
		return nil, fmt.Errorf("%w: scope.source_id exceeds %d bytes", ErrValidation, maxShadowSourceIDBytes)
	}
	risk := FindingRisk(strings.ToLower(strings.TrimSpace(string(req.ScopeRiskLevel))))
	if _, ok := validFindingRisk[risk]; !ok {
		return nil, fmt.Errorf("%w: scope.risk_level must be one of low|medium|high|critical, got %q", ErrValidation, req.ScopeRiskLevel)
	}
	if len(req.ScopeSignalSet) > maxExceptionScopeSignals {
		return nil, fmt.Errorf("%w: scope.signal_set exceeds %d entries", ErrValidation, maxExceptionScopeSignals)
	}
	signals := make([]string, 0, len(req.ScopeSignalSet))
	seen := make(map[string]struct{}, len(req.ScopeSignalSet))
	for _, sig := range req.ScopeSignalSet {
		s := strings.ToLower(strings.TrimSpace(sig))
		if s == "" {
			continue
		}
		if !validExceptionSignal.MatchString(s) {
			return nil, fmt.Errorf("%w: scope.signal_set entry %q must match %s", ErrValidation, sig, exceptionSignalPattern)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		signals = append(signals, s)
	}

	factor := req.StepUpFactor
	if factor == "" {
		factor = StepUpFactorNone
	}
	switch factor {
	case StepUpFactorMFARecent, StepUpFactorSignedAdminToken, StepUpFactorNone:
	default:
		return nil, fmt.Errorf("%w: step_up_factor must be mfa_recent|signed_admin_token|none", ErrValidation)
	}

	id := idGen()
	if !strings.HasPrefix(id, exceptionIDPrefix) {
		id = exceptionIDPrefix + id
	}

	return &Exception{
		ExceptionID:     id,
		TenantID:        tenantID,
		CreatedBy:       createdBy,
		CreatedAt:       now,
		ExpiresAt:       req.ExpiresAt,
		Reason:          reason,
		ScopeSourceType: sourceType,
		ScopeSourceID:   sourceID,
		ScopeSignalSet:  signals,
		ScopeRiskLevel:  risk,
		Status:          ExceptionStatusActive,
		StepUpFactor:    factor,
	}, nil
}

// ErrExceptionLimitExceeded indicates the per-tenant exception cap was
// reached. Handlers map to HTTP 429.
var ErrExceptionLimitExceeded = errors.New("shadow exception: per-tenant cap reached")

// matchesFinding evaluates the scope predicate. An exception matches
// when the tenant ids agree AND the source_type matches AND
// (scope_source_id is empty OR equals finding.SourceID) AND
// (scope_risk_level == finding.Risk) AND
// (scope_signal_set is empty OR any signal overlaps finding.SignalSet).
// Expired or revoked exceptions never match.
func (e *Exception) matchesFinding(f *ShadowAgentFinding, now time.Time) bool {
	if e == nil || f == nil {
		return false
	}
	if e.Status != ExceptionStatusActive {
		return false
	}
	if !e.ExpiresAt.After(now) {
		return false
	}
	if e.TenantID != f.TenantID {
		return false
	}
	if e.ScopeSourceType != "" && e.ScopeSourceType != f.SourceType {
		return false
	}
	if e.ScopeSourceID != "" && e.ScopeSourceID != f.SourceID {
		return false
	}
	if e.ScopeRiskLevel != "" && e.ScopeRiskLevel != f.Risk {
		return false
	}
	if len(e.ScopeSignalSet) > 0 {
		findingSignals := make(map[string]struct{}, len(f.SignalSet))
		for _, sig := range f.SignalSet {
			findingSignals[sig] = struct{}{}
		}
		var overlap bool
		for _, sig := range e.ScopeSignalSet {
			if _, ok := findingSignals[sig]; ok {
				overlap = true
				break
			}
		}
		if !overlap {
			return false
		}
	}
	return true
}
