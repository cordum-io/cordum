package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
)

type policyBundleVersionRequest struct {
	Version      string        `json:"version"`
	RuleSnapshot []policy.Rule `json:"rule_snapshot"`
	DeployedAt   time.Time     `json:"deployed_at"`
	AuditHash    string        `json:"audit_hash"`
}

type policyBundleDeployRequest struct {
	Version string           `json:"version"`
	Scope   policy.RuleScope `json:"scope"`
}

type policyBundleRollbackRequest struct {
	Scope policy.RuleScope `json:"scope"`
}

const policyBundleRoutePrefix = "/api/v1/policy/bundles/"

func (s *server) handlePolicyBundleLifecycleSubroutes(w http.ResponseWriter, r *http.Request) {
	subpath := strings.Trim(strings.TrimPrefix(r.URL.Path, policyBundleRoutePrefix), "/")
	if subpath == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(subpath, "/")
	switch {
	case len(parts) == 1 && parts[0] == "deployments":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleListPolicyBundleDeployments(w, r)
	case len(parts) == 2 && parts[0] == "deployments" && parts[1] == "rollback":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleRollbackPolicyBundleDeployment(w, r)
	case len(parts) == 2 && parts[1] == "versions":
		r.SetPathValue("id", parts[0])
		s.handlePolicyBundleVersionsByMethod(w, r)
	case len(parts) == 3 && parts[1] == "versions":
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		r.SetPathValue("id", parts[0])
		r.SetPathValue("version", parts[2])
		s.handleGetPolicyBundleVersion(w, r)
	case len(parts) == 2 && parts[1] == "deploy":
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		r.SetPathValue("id", parts[0])
		s.handleDeployPolicyBundleVersion(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) handlePolicyBundleVersionsByMethod(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListPolicyBundleVersions(w, r)
	case http.MethodPost:
		s.handleCreatePolicyBundleVersion(w, r)
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *server) handleCreatePolicyBundleVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	bundleID := strings.TrimSpace(r.PathValue("id"))
	var body policyBundleVersionRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	version := policy.BundleVersion{
		Version:      strings.TrimSpace(body.Version),
		RuleSnapshot: append([]policy.Rule{}, body.RuleSnapshot...),
		DeployedAt:   body.DeployedAt,
		AuditHash:    strings.TrimSpace(body.AuditHash),
	}
	if version.DeployedAt.IsZero() {
		version.DeployedAt = time.Now().UTC()
	}
	if err := validatePolicyBundleIDVersion(bundleID, version.Version); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.policyBundleStore.CreateBundleVersion(r.Context(), bundleID, &version); err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"bundle_id": bundleID, "version": version})
}

func (s *server) handleListPolicyBundleVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyRead) {
		return
	}
	bundleID := strings.TrimSpace(r.PathValue("id"))
	if bundleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id required")
		return
	}
	versions, err := s.policyBundleStore.ListBundleVersions(r.Context(), bundleID)
	if err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"bundle_id": bundleID, "items": versions})
}

func (s *server) handleGetPolicyBundleVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyRead) {
		return
	}
	bundleID := strings.TrimSpace(r.PathValue("id"))
	version := strings.TrimSpace(r.PathValue("version"))
	if err := validatePolicyBundleIDVersion(bundleID, version); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	got, err := s.policyBundleStore.GetBundleVersion(r.Context(), bundleID, version)
	if err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"bundle_id": bundleID, "version": got})
}

func (s *server) handleDeployPolicyBundleVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	bundleID := strings.TrimSpace(r.PathValue("id"))
	var body policyBundleDeployRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if !s.requirePolicyBundleScopeAccess(w, r, body.Scope) {
		return
	}
	deployment, err := s.deployPolicyBundleVersion(r, bundleID, body)
	if err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"deployment": deployment})
}

func (s *server) handleListPolicyBundleDeployments(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyRead) {
		return
	}
	scope, err := policyBundleScopeFromQuery(r)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.requirePolicyBundleScopeAccess(w, r, scope) {
		return
	}
	history, err := s.policyBundleStore.ListDeploymentHistory(r.Context(), scope, policyBundleHistoryLimit(r))
	if err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"scope": scope, "items": history})
}

func (s *server) handleRollbackPolicyBundleDeployment(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	var body policyBundleRollbackRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if !s.requirePolicyBundleScopeAccess(w, r, body.Scope) {
		return
	}
	deployment, err := s.policyBundleStore.RollbackDeployment(r.Context(), body.Scope)
	if err != nil {
		writePolicyBundleStoreError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"deployment": deployment})
}

func (s *server) deployPolicyBundleVersion(r *http.Request, bundleID string, body policyBundleDeployRequest) (*policy.Deployment, error) {
	version := strings.TrimSpace(body.Version)
	if err := validatePolicyBundleIDVersion(bundleID, version); err != nil {
		return nil, err
	}
	if err := validatePolicyBundleScope(body.Scope); err != nil {
		return nil, err
	}
	return s.policyBundleStore.DeployVersionToScope(r.Context(), bundleID, version, body.Scope)
}

func (s *server) requirePolicyBundleStore(w http.ResponseWriter, r *http.Request, permission string) bool {
	return s.requireStoreAndPermissionOrRole(w, r, permission, []string{"admin"}, s.policyBundleStore)
}

func (s *server) requirePolicyBundleScopeAccess(w http.ResponseWriter, r *http.Request, scope policy.RuleScope) bool {
	if err := validatePolicyBundleScope(scope); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return false
	}
	if scope.Kind == policy.RuleScopeTenant {
		if err := s.requireTenantAccess(r, scope.Value); err != nil {
			writeForbidden(w, r, err)
			return false
		}
	}
	return true
}

func policyBundleScopeFromQuery(r *http.Request) (policy.RuleScope, error) {
	scope := policy.RuleScope{
		Kind:  policy.RuleScopeKind(strings.TrimSpace(r.URL.Query().Get("scope_kind"))),
		Value: strings.TrimSpace(r.URL.Query().Get("scope_value")),
	}
	return scope, validatePolicyBundleScope(scope)
}

func validatePolicyBundleScope(scope policy.RuleScope) error {
	if _, err := policy.ParseRuleScopeKind(scope.Kind.String()); err != nil {
		return err
	}
	if scope.Kind != policy.RuleScopeGlobal && strings.TrimSpace(scope.Value) == "" {
		return errors.New("scope value required")
	}
	return nil
}

func validatePolicyBundleIDVersion(bundleID, version string) error {
	if strings.TrimSpace(bundleID) == "" {
		return errors.New("bundle id required")
	}
	if strings.TrimSpace(version) == "" {
		return errors.New("version required")
	}
	return nil
}

func policyBundleHistoryLimit(r *http.Request) int {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < limit {
			limit = parsed
		}
	}
	return limit
}

func writePolicyBundleStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrBundleExists), errors.Is(err, policy.ErrBundleVersionExists):
		writeErrorJSON(w, http.StatusConflict, err.Error())
	case errors.Is(err, policy.ErrBundleNotFound), errors.Is(err, policy.ErrBundleVersionNotFound),
		errors.Is(err, policy.ErrNoDeploymentForScope), errors.Is(err, policy.ErrNoRollbackTarget):
		writeErrorJSON(w, http.StatusNotFound, err.Error())
	case strings.Contains(err.Error(), "required"):
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
	default:
		writeInternalError(w, r, "policy bundle lifecycle", err)
	}
}
