package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Auth registers the auth surface.
//
//	POST /api/v1/auth/login     login
//	GET  /api/v1/auth/session   getSession (bearer-gated)
//	GET  /api/v1/auth/config    getAuthConfig (api-key or bearer)
func Auth(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Tenant   string `json:"tenant"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			engine.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not JSON", nil)
			return
		}
		if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
			engine.WriteError(w, http.StatusBadRequest, "validation_failed", "username and password required", map[string]any{
				"field_errors": map[string][]string{
					"username": {"required"},
					"password": {"required"},
				},
			})
			return
		}
		if req.Tenant == "" {
			req.Tenant = "default"
		}
		// Deterministic session token derived from the username so
		// re-running the fixture yields the same string. Fixtures
		// mask the value with $any$ but stability keeps debugging
		// simple.
		sum := sha256.Sum256([]byte("sess:" + req.Username + ":" + req.Tenant))
		token := "sess_" + hex.EncodeToString(sum[:12])
		sess := &engine.Session{
			Token:        token,
			SessionToken: token,
			UserID:       req.Username,
			Principal:    req.Username,
			Tenant:       req.Tenant,
			ExpiresAt:    eng.Timestamp(900),
		}
		eng.Mu().Lock()
		eng.Sessions[token] = sess
		eng.Mu().Unlock()
		engine.WriteJSON(w, http.StatusOK, sess)
	})
	mux.HandleFunc("/api/v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		eng.Mu().Lock()
		sess, ok := eng.Sessions[ac.Bearer]
		eng.Mu().Unlock()
		if !ok {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid session", nil)
			return
		}
		engine.WriteJSON(w, http.StatusOK, sess)
	})
	mux.HandleFunc("/api/v1/auth/config", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		engine.WriteJSON(w, http.StatusOK, map[string]any{
			"providers":           []string{"api_key", "session"},
			"default_tenant":      "default",
			"session_ttl_seconds": 900,
		})
	})
}
