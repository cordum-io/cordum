package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
)

// requirePolicyRuleStore mirrors requirePolicyBundleStore — it gates a
// handler on the auth permission and confirms the rule store is wired.
func (s *server) requirePolicyRuleStore(w http.ResponseWriter, r *http.Request, permission string) bool {
	return s.requireStoreAndPermissionOrRole(w, r, permission, []string{"admin"}, s.policyRuleStore)
}

// handleCreatePolicyRule implements POST /api/v1/policy/rules. Accepts
// a unified Rule body (without ID — server assigns) and returns the
// persisted Rule with server-set Version=v1, Audit.CreatedAt/UpdatedAt,
// Status=draft. Reject client-supplied audit/version/status fields with
// 400 — clients cannot fake history. Validation errors map to 400 with
// a precise per-field message; duplicate ID maps to 409.
func (s *server) handleCreatePolicyRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyRuleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	var body policy.Rule
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if err := rejectClientManagedFieldsOnCreate(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "id required")
		return
	}
	created, err := s.policyRuleStore.CreateRule(r.Context(), &body)
	if err != nil {
		writePolicyRuleStoreError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/policy/rules/"+created.ID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

// handleUpdatePolicyRule implements PUT /api/v1/policy/rules/{id}.
// Optimistic concurrency: requires `If-Match: <Rule.Version>` header
// (412 Precondition Required when missing). Stale version returns 409
// with body `{ "current_version": ..., "current_audit_hash": ... }` so
// the dashboard can render a reload-banner without re-fetching.
// Server overwrites client-supplied audit/version on every write.
func (s *server) handleUpdatePolicyRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyRuleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "id required")
		return
	}
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" {
		writeErrorJSON(w, http.StatusPreconditionRequired, "If-Match header required")
		return
	}
	var body policy.Rule
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	body.ID = id // path id wins over body id; clients cannot rename via PUT.
	updated, err := s.policyRuleStore.UpdateRule(r.Context(), &body, ifMatch)
	if err != nil {
		writePolicyRuleStoreError(w, r, err)
		return
	}
	writeJSON(w, updated)
}

// rejectClientManagedFieldsOnCreate fails the request when the client
// tries to set fields the server owns: ID is allowed (callers pick the
// id; the server SETNX-checks), but Version, Audit timestamps, and
// Audit actors must be empty. Status is permitted because some flows
// create rules in non-draft state (e.g. seed scripts); the store
// defaults Status=draft when empty.
func rejectClientManagedFieldsOnCreate(r *policy.Rule) error {
	if r.Version != "" {
		return errors.New("rule.version is server-managed; omit on create")
	}
	if !r.Audit.CreatedAt.IsZero() || !r.Audit.UpdatedAt.IsZero() {
		return errors.New("rule.audit timestamps are server-managed; omit on create")
	}
	if r.Audit.CreatedBy != "" || r.Audit.UpdatedBy != "" {
		return errors.New("rule.audit actors are server-managed; omit on create")
	}
	return nil
}

// writePolicyRuleStoreError maps RuleStore typed errors to HTTP
// statuses. Mirrors writePolicyBundleStoreError but adds the 409+body
// path for *ErrRuleStaleVersion since the dashboard's reload-banner
// contract depends on the current_version + current_audit_hash fields.
func writePolicyRuleStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if stale, ok := policy.IsStaleVersionError(err); ok {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":              "stale_version",
			"current_version":    stale.CurrentVersion,
			"current_audit_hash": stale.CurrentAuditHash,
		})
		return
	}
	var validation *policy.ErrRuleValidation
	if errors.As(err, &validation) {
		writeErrorJSON(w, http.StatusBadRequest, validation.Error())
		return
	}
	switch {
	case errors.Is(err, policy.ErrRuleExists):
		writeErrorJSON(w, http.StatusConflict, err.Error())
	case errors.Is(err, policy.ErrRuleNotFound):
		writeErrorJSON(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "required"):
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
	default:
		writeInternalError(w, r, "policy rule store", err)
	}
}

// writeJSONStatus writes a JSON body with an explicit HTTP status code.
// Mirrors writeJSON but lets the caller control the status (writeJSON
// always emits 200 implicitly via WriteHeader-not-called).
func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, body)
}
