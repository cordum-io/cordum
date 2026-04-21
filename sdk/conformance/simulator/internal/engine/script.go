package engine

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ScriptHeader is the name of the opt-in per-request header fixtures
// use to coax the simulator into specific failure modes.
const ScriptHeader = "X-Conformance-Script"

// IdempotencyHeader is the request header name clients MUST set to
// flag a request as retry-safe. The simulator honors it under the
// idempotency fixtures.
const IdempotencyHeader = "Idempotency-Key"

// WriteError renders the gateway's canonical error envelope for the
// given status code so every handler produces bit-identical bodies
// for the same failure mode. Fixtures lock this envelope via
// `schema/error_envelope.schema.json`.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if details != nil {
		body["error"].(map[string]any)["details"] = details
	}
	_ = json.NewEncoder(w).Encode(body)
}

// WriteJSON encodes v as JSON with the given HTTP status. Content-Type
// is set once so handlers never forget it.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ScriptForRequest returns the script name declared by the caller via
// the X-Conformance-Script request header, or "" when absent.
func ScriptForRequest(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get(ScriptHeader))
}

// RouteKey builds the per-request key used by Engine.ShouldFire and
// SeenIdempotencyKey. Using method+path keeps different fixtures on
// the same operation independent of each other.
func RouteKey(r *http.Request) string {
	return r.Method + " " + r.URL.Path
}

// AuthContext models the authenticated caller. The simulator honors
// two auth schemes: API key (X-API-Key) and bearer token
// (Authorization: Bearer <token>). Anonymous callers get an empty
// AuthContext and are rejected by 401-returning handlers.
type AuthContext struct {
	Principal string
	Tenant    string
	APIKey    string
	Bearer    string
}

// AuthFromRequest extracts the caller's auth material. Unknown bearer
// tokens are treated as anonymous (no error) so the 401 fixture can
// assert its own failure mode instead of the simulator pre-empting.
func (e *Engine) AuthFromRequest(r *http.Request) AuthContext {
	ac := AuthContext{}
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		ac.APIKey = apiKey
		ac.Principal = "apikey:" + apiKey
	}
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(raw, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		ac.Bearer = token
		// Look up the session so a follow-up call after login can
		// behave identically to an api-key call.
		e.mu.Lock()
		if sess, ok := e.Sessions[token]; ok {
			ac.Principal = sess.Principal
			ac.Tenant = sess.Tenant
		}
		e.mu.Unlock()
	}
	if tenant := strings.TrimSpace(r.Header.Get("X-Tenant-Id")); tenant != "" {
		ac.Tenant = tenant
	}
	return ac
}

// Authenticated reports whether the caller has ANY credential. Most
// handlers use this as a blunt "is this request anonymous?" check —
// the conformance suite does NOT exercise tenant-scoped authorization
// logic beyond the api-key-present/api-key-absent split.
func (ac AuthContext) Authenticated() bool {
	return ac.APIKey != "" || ac.Bearer != "" || ac.Principal != ""
}
