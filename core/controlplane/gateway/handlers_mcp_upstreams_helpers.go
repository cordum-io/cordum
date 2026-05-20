package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

func (s *server) mcpUpstreamRegistryOrUnavailable(w http.ResponseWriter, r *http.Request) edgecore.MCPUpstreamRegistry {
	if s == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "mcp upstream registry unavailable", nil)
		return nil
	}
	s.mcpUpstreamRegistryMu.Lock()
	registry := s.mcpUpstreamRegistry
	if registry == nil && s.jobStore != nil && s.jobStore.Client() != nil {
		registry = edgecore.NewRedisMCPUpstreamRegistryFromClient(s.jobStore.Client())
		s.mcpUpstreamRegistry = registry
	}
	s.mcpUpstreamRegistryMu.Unlock()
	if registry != nil {
		return registry
	}
	writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "mcp upstream registry unavailable", nil)
	return nil
}

func filterMCPUpstreamsByEnabledQuery(r *http.Request, items []edgecore.UpstreamServer) []edgecore.UpstreamServer {
	raw := strings.TrimSpace(r.URL.Query().Get("enabled"))
	if raw == "" {
		return items
	}
	wanted, err := strconv.ParseBool(raw)
	if err != nil {
		return items
	}
	out := make([]edgecore.UpstreamServer, 0, len(items))
	for _, item := range items {
		if item.Enabled == wanted {
			out = append(out, item)
		}
	}
	return out
}

func writeMCPUpstreamStoreError(w http.ResponseWriter, r *http.Request, err error, op, tenantID, name string) {
	status, code, message := http.StatusInternalServerError, edgeErrCodeInternalError, "mcp upstream registry error"
	switch {
	case errors.Is(err, edgecore.ErrUpstreamNotFound):
		status, code, message = http.StatusNotFound, edgeErrCodeNotFound, "mcp upstream not found"
	case errors.Is(err, edgecore.ErrUpstreamAlreadyExists):
		status, code, message = http.StatusConflict, edgeErrCodeConflict, "mcp upstream already exists"
	case errors.Is(err, edgecore.ErrUpstreamLimitExceeded):
		status, code, message = http.StatusTooManyRequests, edgeErrCodeConflict, "mcp upstream tenant cap reached"
	case errors.Is(err, edgecore.ErrUpstreamNotAllowlisted):
		status, code, message = http.StatusForbidden, edgeErrCodeAccessDenied, "mcp upstream not allowlisted"
	case errors.Is(err, edgecore.ErrInvalidUpstream), errors.Is(err, edgecore.ErrInvalidTransport), errors.Is(err, edgecore.ErrUnsafeEndpoint), errors.Is(err, edgecore.ErrSecretMustUseRef), errors.Is(err, edgecore.ErrShellMetacharsRejected):
		status, code, message = http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid mcp upstream"
	}
	logMCPUpstreamOutcome(op, tenantID, name, "deny", message)
	writeEdgeError(w, r, status, code, message, nil)
}

func writeMCPUpstreamValidationError(w http.ResponseWriter, r *http.Request, err error, tenantID, name string) {
	if isMCPUpstreamValidateOnly(r) {
		writeJSON(w, mcpUpstreamValidationResponse{Valid: false, Reason: mcpUpstreamReason(err)})
		logMCPUpstreamOutcome("validate", tenantID, name, "deny", mcpUpstreamReason(err))
		return
	}
	writeMCPUpstreamStoreError(w, r, err, "validate", tenantID, name)
}

func validateMCPUpstreamTenant(r *http.Request, headerTenant, bodyTenant string) error {
	bodyTenant = strings.TrimSpace(bodyTenant)
	if bodyTenant == "" || bodyTenant == headerTenant {
		return nil
	}
	ctx := auth.FromRequest(r)
	if bodyTenant == "*" && ctx != nil && ctx.AllowCrossTenant && headerTenant == "*" {
		return nil
	}
	return fmt.Errorf("edge tenant body/header mismatch")
}

// mcpUpstreamRejectsCallerPolicyParams returns a non-nil error if the caller
// supplied any of the policy-related query params that previously could
// downgrade strict validation or inject an allowlist. Policy now comes ONLY
// from trusted tenant/server config; caller overrides are rejected with 400
// invalid_request rather than silently ignored (avoids confused-deputy where
// the caller believes they overrode strict mode).
func mcpUpstreamRejectsCallerPolicyParams(r *http.Request) error {
	q := r.URL.Query()
	for _, name := range []string{"policy_mode", "allowed_upstream", "allowed_upstreams", "allow_plain_http"} {
		if _, present := q[name]; present {
			return fmt.Errorf("%s must be configured in tenant settings, not query string", name)
		}
	}
	return nil
}

func (s *server) mcpUpstreamPolicyInputs(r *http.Request, tenantID string) (string, []string) {
	settings := s.mcpUpstreamPolicySettings(r, tenantID)
	return settings.policyMode, settings.allowlist
}

type mcpUpstreamPolicySettings struct {
	policyMode     string
	allowlist      []string
	allowPlainHTTP bool
}

func (s *server) mcpUpstreamPolicySettings(r *http.Request, tenantID string) mcpUpstreamPolicySettings {
	// Policy mode is NOT caller-controllable; the trusted-config tenant
	// settings (`safety.mcp.policy_mode` + `safety.mcp.allowed_upstreams`)
	// are the only sources. Pre-fix this returned ("", allowlist)
	// unconditionally, collapsing the validator's enterprise-strict
	// branches (HTTPS-only, allowlist gate, fail-closed-on-DNS) into
	// observe-mode semantics for every create/update — the strict
	// branch was dead code on the API surface. Now we read mcp.policy_mode
	// from the same configsvc Effective tree so an operator can opt the
	// tenant in via config without code changes (HIGH audit finding
	// 2026-05-20). When an allowlist is configured but policy_mode is
	// unset, default to enterprise-strict so the operator's declaration
	// of an allowlist actually gates registration (PR #276 audit fix).
	// Plain HTTP is rejected by default in every mode and can only be
	// restored via trusted `safety.mcp.allow_plain_http=true`.
	if s == nil || s.configSvc == nil {
		return mcpUpstreamPolicySettings{}
	}
	effective, err := s.configSvc.Effective(r.Context(), tenantID, "", "", "")
	if err != nil {
		return mcpUpstreamPolicySettings{}
	}
	mode := extractMCPPolicyMode(effective)
	allow := extractStringSlice(effective, "safety", "mcp", "allowed_upstreams")
	if mode == "" && len(allow) > 0 {
		mode = string(edgecore.PolicyModeEnterpriseStrict)
	}
	return mcpUpstreamPolicySettings{
		policyMode:     mode,
		allowlist:      allow,
		allowPlainHTTP: extractBool(effective, "safety", "mcp", "allow_plain_http"),
	}
}

// extractMCPPolicyMode reads `safety.mcp.policy_mode` from the configsvc
// Effective tree. Returns "" when unset so callers stay on the
// non-strict default — the operator must explicitly opt in. Unknown
// strings are passed through verbatim; the validator's isMCPEnterpriseStrict
// is a case-insensitive prefix-of-canonical check so misspellings safely
// fall back to non-strict rather than silently enabling strict mode.
func extractMCPPolicyMode(data map[string]any) string {
	var current any = data
	for _, key := range []string{"safety", "mcp", "policy_mode"} {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	if s, ok := current.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func extractStringSlice(data map[string]any, keys ...string) []string {
	var current any = data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	switch v := current.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func extractBool(data map[string]any, keys ...string) bool {
	var current any = data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = m[key]
	}
	v, ok := current.(bool)
	return ok && v
}

func isMCPUpstreamValidateOnly(r *http.Request) bool {
	q := r.URL.Query()
	return strings.EqualFold(q.Get("validate-only"), "true") || strings.EqualFold(q.Get("validate_only"), "true")
}

func mcpUpstreamReason(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, edgecore.ErrUnsafeEndpoint):
		return "unsafe endpoint"
	case errors.Is(err, edgecore.ErrSecretMustUseRef):
		return "secret references must use secret://"
	case errors.Is(err, edgecore.ErrShellMetacharsRejected):
		return "shell metacharacters rejected"
	case errors.Is(err, edgecore.ErrUpstreamNotAllowlisted):
		return "upstream not allowlisted"
	case errors.Is(err, edgecore.ErrInvalidTransport):
		return "invalid transport"
	default:
		return "invalid upstream"
	}
}

func logMCPUpstreamOutcome(op, tenantID, name, decision, reason string) {
	slog.Info("mcp upstream registry", "event", "mcp-upstream-"+op, "tenant_id", tenantID, "name", name, "decision", decision, "reason", reason)
}
