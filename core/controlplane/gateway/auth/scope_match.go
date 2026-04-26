package auth

import (
	"fmt"
	"net/http"
	"strings"
)

// ScopeError reports that a managed API key is valid but not permitted for the
// canonical resource:verb required by the current HTTP request.
type ScopeError struct {
	Required string
	Granted  []string
}

func (e *ScopeError) Error() string {
	if e == nil {
		return "key scope insufficient"
	}
	if e.Required == "" {
		return "key scope insufficient: unmapped request path"
	}
	return fmt.Sprintf("key scope insufficient: required %s", e.Required)
}

// MatchScopes returns true if any granted API-key scope satisfies the required
// canonical resource:verb scope. Empty granted scopes are intentionally allowed
// for backward compatibility with API keys minted before scope enforcement.
//
// Supported granted forms:
//   - exact resource:verb, e.g. "jobs:read"
//   - resource wildcard, e.g. "jobs:*"
//   - legacy role scopes from the original Settings/Keys UI: read/viewer,
//     write/operator, admin. These preserve existing keys while operators
//     migrate to resource:verb scopes.
func MatchScopes(granted []string, required string) bool {
	_, ok := MatchedScope(granted, required)
	return ok
}

// MatchedScope returns the granted scope that satisfies required. The returned
// scope is safe for logs because it is an operator-authored scope string, not a
// secret. Empty granted scopes return an empty match and ok=true for
// pre-enforcement backward compatibility.
func MatchedScope(granted []string, required string) (string, bool) {
	required = normalizeScope(required)
	normalizedGranted := normalizeGrantedScopes(granted)
	if len(normalizedGranted) == 0 {
		return "", true
	}
	if required == "" {
		return "", false
	}
	requiredResource, requiredVerb, ok := splitScope(required)
	if !ok {
		return "", false
	}

	for _, candidate := range normalizedGranted {
		switch candidate {
		case "admin", "*", "*:*":
			return candidate, true
		case "read", "viewer":
			if requiredVerb == "read" {
				return candidate, true
			}
			continue
		case "write", "operator":
			if requiredVerb == "read" || requiredVerb == "write" {
				return candidate, true
			}
			continue
		}

		resource, verb, ok := splitScope(candidate)
		if !ok || resource != requiredResource {
			continue
		}
		if verb == "*" || verb == requiredVerb {
			return candidate, true
		}
	}
	return "", false
}

func normalizeGrantedScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = normalizeScope(scope)
		if scope != "" {
			out = append(out, scope)
		}
	}
	return out
}

func normalizeScope(scope string) string {
	scope = strings.TrimSpace(strings.ToLower(scope))
	scope = strings.ReplaceAll(scope, " ", "")
	return scope
}

func splitScope(scope string) (resource, verb string, ok bool) {
	resource, verb, found := strings.Cut(scope, ":")
	if !found {
		return "", "", false
	}
	resource = strings.TrimSpace(resource)
	verb = strings.TrimSpace(verb)
	if resource == "" || verb == "" {
		return "", "", false
	}
	return resource, verb, true
}

// PathToScope maps HTTP method + API path to a canonical resource:verb scope.
// It returns ok=false for paths that are intentionally unmapped; scoped API keys
// must deny those paths conservatively while empty-scope legacy keys keep their
// historical role-only behavior.
func PathToScope(method, path string) (scope string, ok bool) {
	resource, ok := resourceForPath(path)
	if !ok {
		return "", false
	}
	verb, ok := verbForMethod(method)
	if !ok {
		return "", false
	}
	return resource + ":" + verb, true
}

func verbForMethod(method string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "read", true
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return "write", true
	default:
		return "", false
	}
}

func resourceForPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}

	for _, candidate := range []struct {
		prefix   string
		resource string
	}{
		{"/api/v1/auth/keys", "apikeys"},
		{"/api/v1/workflow-runs", "workflows"},
		{"/api/v1/workflows", "workflows"},
		{"/api/v1/jobs", "jobs"},
		{"/api/v1/audit", "audit"},
		{"/api/v1/approvals", "approvals"},
		{"/api/v1/agents", "agents"},
		{"/api/v1/delegations", "delegations"},
		{"/api/v1/packs", "packs"},
		{"/api/v1/policy-bundles", "policy"},
		{"/api/v1/policy", "policy"},
		{"/api/v1/topics", "topics"},
		{"/api/v1/schemas", "schemas"},
		{"/api/v1/mcp", "mcp"},
		{"/api/v1/chat", "chat"},
		{"/api/v1/copilot", "copilot"},
		{"/api/v1/config", "config"},
		{"/api/v1/dlq", "dlq"},
		{"/api/v1/workers", "workers"},
		{"/api/v1/memory", "memory"},
		{"/api/v1/artifacts", "artifacts"},
		{"/api/v1/evals", "evals"},
		{"/api/v1/governance", "governance"},
	} {
		if path == candidate.prefix || strings.HasPrefix(path, candidate.prefix+"/") {
			return candidate.resource, true
		}
	}
	return "", false
}
