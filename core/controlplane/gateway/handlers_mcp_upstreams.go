package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

type mcpUpstreamListResponse struct {
	Items []edgecore.UpstreamServer `json:"items"`
}

type mcpUpstreamValidationResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

func (s *server) handleListMCPUpstreams(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermMCPRead, "admin", "user", "viewer") {
		return
	}
	registry := s.mcpUpstreamRegistryOrUnavailable(w, r)
	if registry == nil {
		return
	}
	tenantID, ok := s.requireMCPUpstreamTenant(w, r)
	if !ok {
		return
	}
	items, err := registry.List(r.Context(), tenantID)
	if err != nil {
		writeMCPUpstreamStoreError(w, r, err, "list", tenantID, "")
		return
	}
	items = filterMCPUpstreamsByEnabledQuery(r, items)
	logMCPUpstreamOutcome("list", tenantID, "", "allow", "ok")
	writeJSON(w, mcpUpstreamListResponse{Items: items})
}

func (s *server) handleGetMCPUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermMCPRead, "admin", "user", "viewer") {
		return
	}
	registry := s.mcpUpstreamRegistryOrUnavailable(w, r)
	if registry == nil {
		return
	}
	tenantID, name, ok := s.mcpUpstreamTenantAndName(w, r)
	if !ok {
		return
	}
	upstream, found, err := registry.Get(r.Context(), tenantID, name)
	if err != nil {
		writeMCPUpstreamStoreError(w, r, err, "get", tenantID, name)
		return
	}
	if !found || upstream == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "mcp upstream not found", nil)
		return
	}
	logMCPUpstreamOutcome("get", tenantID, name, "allow", "ok")
	writeJSON(w, upstream)
}

func (s *server) handleCreateMCPUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	registry := s.mcpUpstreamRegistryOrUnavailable(w, r)
	if registry == nil {
		return
	}
	tenantID, ok := s.requireMCPUpstreamTenant(w, r)
	if !ok {
		return
	}
	upstream, opts, ok := s.decodeMCPUpstreamRequest(w, r, tenantID, "")
	if !ok {
		return
	}
	if s.handleMCPUpstreamValidateOnly(w, r, upstream) {
		return
	}
	if err := registry.Create(edgecore.WithMCPUpstreamValidationOptions(r.Context(), opts), upstream); err != nil {
		writeMCPUpstreamStoreError(w, r, err, "create", tenantID, upstream.Name)
		return
	}
	logMCPUpstreamOutcome("create", tenantID, upstream.Name, "allow", "created")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, upstream)
}

func (s *server) handleUpdateMCPUpstream(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	registry := s.mcpUpstreamRegistryOrUnavailable(w, r)
	if registry == nil {
		return
	}
	tenantID, name, ok := s.mcpUpstreamTenantAndName(w, r)
	if !ok {
		return
	}
	upstream, opts, ok := s.decodeMCPUpstreamRequest(w, r, tenantID, name)
	if !ok {
		return
	}
	if err := registry.Update(edgecore.WithMCPUpstreamValidationOptions(r.Context(), opts), upstream); err != nil {
		writeMCPUpstreamStoreError(w, r, err, "update", tenantID, name)
		return
	}
	logMCPUpstreamOutcome("update", tenantID, name, "allow", "updated")
	writeJSON(w, upstream)
}

func (s *server) handleDisableMCPUpstream(w http.ResponseWriter, r *http.Request) {
	s.handleSetMCPUpstreamEnabled(w, r, false)
}

func (s *server) handleEnableMCPUpstream(w http.ResponseWriter, r *http.Request) {
	s.handleSetMCPUpstreamEnabled(w, r, true)
}

func (s *server) handleSetMCPUpstreamEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	registry := s.mcpUpstreamRegistryOrUnavailable(w, r)
	if registry == nil {
		return
	}
	tenantID, name, ok := s.mcpUpstreamTenantAndName(w, r)
	if !ok {
		return
	}
	var err error
	if enabled {
		err = registry.Enable(r.Context(), tenantID, name)
	} else {
		err = registry.Disable(r.Context(), tenantID, name)
	}
	if err != nil {
		writeMCPUpstreamStoreError(w, r, err, "set-enabled", tenantID, name)
		return
	}
	upstream, _, _ := registry.Get(r.Context(), tenantID, name)
	logMCPUpstreamOutcome("set-enabled", tenantID, name, "allow", fmt.Sprintf("enabled=%t", enabled))
	writeJSON(w, upstream)
}

// decodeMCPUpstreamRequest decodes and validates an upstream. It also returns
// the trusted validation options it used, for the registry's own re-check.
func (s *server) decodeMCPUpstreamRequest(w http.ResponseWriter, r *http.Request, tenantID, pathName string) (*edgecore.UpstreamServer, edgecore.MCPUpstreamValidationOptions, bool) {
	var upstream edgecore.UpstreamServer
	var opts edgecore.MCPUpstreamValidationOptions
	if err := decodeJSONBody(w, r, &upstream); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid mcp upstream request")
		return nil, opts, false
	}
	if err := validateMCPUpstreamTenant(r, tenantID, upstream.TenantID); err != nil {
		writeEdgeForbidden(w, r, err)
		return nil, opts, false
	}
	if pathName != "" && upstream.Name != "" && strings.TrimSpace(upstream.Name) != pathName {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "mcp upstream name mismatch", nil)
		return nil, opts, false
	}
	if pathName != "" {
		upstream.Name = pathName
	}
	upstream.TenantID = tenantID
	// Reject caller-supplied policy_mode/allowed_upstream(s) query params
	// (SSRF/policy-downgrade vector). Trusted tenant/server config is the
	// ONLY policy source.
	if err := mcpUpstreamRejectsCallerPolicyParams(r); err != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, err.Error(), nil)
		return nil, opts, false
	}
	settings := s.mcpUpstreamPolicySettings(r, tenantID)
	opts.AllowPlainHTTP = settings.allowPlainHTTP
	if err := edgecore.ValidateMCPUpstreamWithOptions(r.Context(), &upstream, settings.policyMode, settings.allowlist, opts); err != nil {
		writeMCPUpstreamValidationError(w, r, err, tenantID, upstream.Name)
		return nil, opts, false
	}
	return &upstream, opts, true
}

func (s *server) handleMCPUpstreamValidateOnly(w http.ResponseWriter, r *http.Request, upstream *edgecore.UpstreamServer) bool {
	if !isMCPUpstreamValidateOnly(r) {
		return false
	}
	writeJSON(w, mcpUpstreamValidationResponse{Valid: true})
	logMCPUpstreamOutcome("validate", upstream.TenantID, upstream.Name, "allow", "valid")
	return true
}

func (s *server) mcpUpstreamTenantAndName(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	tenantID, ok := s.requireMCPUpstreamTenant(w, r)
	if !ok {
		return "", "", false
	}
	name, ok := requireEdgePathParam(w, r, "name")
	if !ok {
		return "", "", false
	}
	return tenantID, strings.TrimSpace(name), true
}

func (s *server) requireMCPUpstreamTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	if strings.TrimSpace(auth.HeaderValue(r, "X-Tenant-ID")) == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeTenantRequired, "tenant id required", nil)
		return "", false
	}
	return s.edgeTenantFromRequest(w, r, "")
}
