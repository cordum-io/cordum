package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrRuleValidation is the typed error returned by Rule.Validate. The
// Field / Reason fields let HTTP handlers map to a 400 with a precise
// per-field message (the dashboard surfaces these inline next to the
// failing input on the editor form).
type ErrRuleValidation struct {
	Field  string
	Reason string
}

func (e *ErrRuleValidation) Error() string {
	if e.Field == "" {
		return "policy: rule validation: " + e.Reason
	}
	return fmt.Sprintf("policy: rule validation: %s: %s", e.Field, e.Reason)
}

// Validate checks the Rule envelope for the writable fields any client
// must supply. Server-managed fields (ID on Create, Audit, Version) are
// the caller's responsibility to set or unset before calling Validate;
// this function only checks user-authoring concerns.
func (r *Rule) Validate() error {
	if r == nil {
		return &ErrRuleValidation{Reason: "rule is nil"}
	}
	if strings.TrimSpace(r.Name) == "" {
		return &ErrRuleValidation{Field: "name", Reason: "required"}
	}
	if _, err := ParseRuleType(r.Type.String()); err != nil {
		return &ErrRuleValidation{Field: "type", Reason: err.Error()}
	}
	if _, err := ParseRuleScopeKind(r.Scope.Kind.String()); err != nil {
		return &ErrRuleValidation{Field: "scope.kind", Reason: err.Error()}
	}
	if r.Scope.Kind != RuleScopeGlobal && strings.TrimSpace(r.Scope.Value) == "" {
		return &ErrRuleValidation{
			Field:  "scope.value",
			Reason: "required when scope.kind != global",
		}
	}
	if r.Status != "" {
		if _, err := ParseRuleStatus(r.Status.String()); err != nil {
			return &ErrRuleValidation{Field: "status", Reason: err.Error()}
		}
	}
	if len(r.Match) == 0 {
		return &ErrRuleValidation{Field: "match", Reason: "required"}
	}
	if !json.Valid(r.Match) {
		return &ErrRuleValidation{Field: "match", Reason: "must be valid JSON"}
	}
	if len(r.Decide) == 0 {
		return &ErrRuleValidation{Field: "decide", Reason: "required"}
	}
	if !json.Valid(r.Decide) {
		return &ErrRuleValidation{Field: "decide", Reason: "must be valid JSON"}
	}
	return nil
}

// applyServerSideCreateMetadata overwrites the audit + version fields
// of a freshly-created Rule with deterministic server values. Any
// caller-supplied Audit, Version, or Status content is discarded —
// clients cannot fake history.
func applyServerSideCreateMetadata(r *Rule, now time.Time, actor string) {
	r.Version = "v1"
	r.Audit = AuditMetadata{
		CreatedAt: now.UTC(),
		CreatedBy: actor,
		UpdatedAt: now.UTC(),
		UpdatedBy: actor,
	}
	if r.Status == "" {
		r.Status = RuleStatusDraft
	}
}

// applyServerSideUpdateMetadata refreshes the audit + version fields of
// an updated Rule. CreatedAt / CreatedBy are preserved from the prior
// stored Rule (caller passes the previous Audit). Version is bumped via
// bumpVersion which assumes "vN" form and increments N; non-canonical
// version strings get an "v1-derived" suffix to keep monotonicity at
// the cost of readability — handlers should reject non-canonical
// versions before the store sees them.
func applyServerSideUpdateMetadata(r *Rule, prior *Rule, now time.Time, actor string) {
	r.Version = bumpVersion(prior.Version)
	r.Audit = AuditMetadata{
		CreatedAt: prior.Audit.CreatedAt,
		CreatedBy: prior.Audit.CreatedBy,
		UpdatedAt: now.UTC(),
		UpdatedBy: actor,
	}
	// Status is server-managed across the rule lifecycle: clients edit
	// match/decide/scope but lifecycle transitions (draft → published →
	// deprecated) flow through dedicated endpoints. Preserve the prior
	// status when the caller didn't explicitly set one — the
	// MarshalJSON on RuleStatus rejects the empty string, so without
	// this default the downstream marshal would fail.
	if r.Status == "" {
		r.Status = prior.Status
	}
}

// bumpVersion increments a "vN" Rule.Version by 1 and returns "v(N+1)".
// Non-canonical inputs return "<input>-r1" to preserve monotonicity
// without generating a duplicate; handlers reject non-canonical inputs
// at the HTTP boundary so this branch is defensive only.
func bumpVersion(current string) string {
	if !strings.HasPrefix(current, "v") {
		return current + "-r1"
	}
	rest := strings.TrimPrefix(current, "v")
	n, err := strconv.Atoi(rest)
	if err != nil {
		return current + "-r1"
	}
	return fmt.Sprintf("v%d", n+1)
}

// computeRuleAuditHash returns a SHA-256 hex digest over the Rule's
// canonical JSON. The hash is recomputed on every write so the client
// reload-banner can compare against the stored value to detect drift.
// Stable across re-encoding because Go's encoding/json emits map keys
// in sorted order for typed structs and the Rule envelope only uses
// typed fields (Match/Decide are json.RawMessage so callers control
// their internal ordering — that's a deliberate trade-off for free-form
// payloads).
func computeRuleAuditHash(r *Rule) string {
	if r == nil {
		return ""
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
