package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// AuthUser represents the authenticated user info returned to clients.
type AuthUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Tenant      string   `json:"tenant"`
	Roles       []string `json:"roles,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
	Source      string   `json:"source,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	LastLoginAt string   `json:"last_login_at,omitempty"`
}

// AuthLoginRequest is the request body for login.
type AuthLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"` // API key is passed as password
	Tenant   string `json:"tenant,omitempty"`
}

// AuthLoginResponse is the response for successful login/session.
type AuthLoginResponse struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	User      AuthUser `json:"user"`
}

const defaultSessionTTL = 24 * time.Hour

// handleLogin authenticates using API key passed as password.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req AuthLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Treat password as API key
	apiKey := strings.TrimSpace(req.Password)
	if apiKey == "" {
		http.Error(w, "password required", http.StatusUnauthorized)
		return
	}

	// Create a mock request with the API key header for authentication
	authReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "/", nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	authReq.Header.Set("X-API-Key", apiKey)
	if req.Tenant != "" {
		authReq.Header.Set("X-Tenant-ID", req.Tenant)
	}

	// Authenticate using existing provider
	authCtx, err := s.auth.AuthenticateHTTP(authReq)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// Build response
	resp := buildLoginResponse(authCtx, apiKey)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSession validates current session via X-API-Key header.
func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	// Get auth context from middleware (already validated)
	authCtx := authFromRequest(r)
	if authCtx == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	apiKey := strings.TrimSpace(authCtx.APIKey)
	resp := buildLoginResponse(authCtx, apiKey)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleLogout is a no-op for stateless auth (API key based).
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// buildLoginResponse creates the AuthLoginResponse from auth context.
func buildLoginResponse(authCtx *AuthContext, token string) AuthLoginResponse {
	now := time.Now()
	expiresAt := now.Add(defaultSessionTTL)

	// Use principal ID or generate from API key prefix
	userID := authCtx.PrincipalID
	if userID == "" {
		userID = "user-" + safePrefix(token, 8)
	}

	// Username from principal ID or "api-user"
	username := authCtx.PrincipalID
	if username == "" {
		username = "api-user"
	}

	var roles []string
	if authCtx.Role != "" {
		roles = append(roles, authCtx.Role)
	}

	return AuthLoginResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: AuthUser{
			ID:          userID,
			Username:    username,
			Tenant:      authCtx.Tenant,
			Roles:       roles,
			Source:      "api_key",
			LastLoginAt: now.Format(time.RFC3339),
		},
	}
}

// safePrefix returns first n chars of s, or s if shorter.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
