package gateway

import (
	"encoding/json"
	"errors"
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

// handleLogin authenticates using user/password or API key.
// Supports two authentication methods:
// 1. User/password: If user store is configured, authenticates against stored users
// 2. API key: For programmatic access (scripts, CI/CD), the password field accepts API keys
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req AuthLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	password := strings.TrimSpace(req.Password)
	if password == "" {
		writeErrorJSON(w, http.StatusUnauthorized, "password required")
		return
	}

	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = s.tenant
	}

	// Try user/password authentication first if user store is configured
	if basicAuth, ok := s.auth.(*BasicAuthProvider); ok && basicAuth.UserStore() != nil {
		userStore := basicAuth.UserStore()
		username := strings.TrimSpace(req.Username)

		// Try to find user by username or email
		user, err := userStore.GetByUsername(r.Context(), username, tenant)
		if errors.Is(err, ErrUserNotFound) && strings.Contains(username, "@") {
			user, err = userStore.GetByEmail(r.Context(), username, tenant)
		}

		if err == nil && user != nil {
			if user.Disabled {
				writeErrorJSON(w, http.StatusForbidden, "user is disabled")
				return
			}

			if userStore.ValidatePassword(r.Context(), user, password) {
				// User/password authentication successful
				resp := buildUserLoginResponse(user)
				// Store session token in Redis for subsequent request validation
				if redisStore, ok := userStore.(*RedisUserStore); ok {
					if err := redisStore.StoreSession(r.Context(), resp.Token, user, defaultSessionTTL); err != nil {
						writeErrorJSON(w, http.StatusInternalServerError, "failed to create session")
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w,resp)
				return
			}
		}
	}

	// API key authentication (for programmatic access)
	apiKey := password

	// Create a mock request with the API key header for authentication
	authReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "/", nil)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	authReq.Header.Set("X-API-Key", apiKey)
	if req.Tenant != "" {
		authReq.Header.Set("X-Tenant-ID", req.Tenant)
	}

	// Authenticate using existing provider
	authCtx, err := s.auth.AuthenticateHTTP(authReq)
	if err != nil {
		writeErrorJSON(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Build response
	resp := buildLoginResponse(authCtx, apiKey)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w,resp)
}

// handleSession validates current session via X-API-Key header.
func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	// Get auth context from middleware (already validated)
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	apiKey := strings.TrimSpace(authCtx.APIKey)
	resp := buildLoginResponse(authCtx, apiKey)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w,resp)
}

// handleLogout invalidates the current session token.
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Extract the session token from the auth context
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" {
		if tok := bearerToken(r.Header.Get("Authorization")); tok != "" {
			key = tok
		}
	}
	if strings.HasPrefix(key, "session-") && s.userStore != nil {
		if redisStore, ok := s.userStore.(*RedisUserStore); ok {
			_ = redisStore.DeleteSession(r.Context(), key)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// buildLoginResponse creates the AuthLoginResponse from auth context.
// SECURITY: Token is masked to prevent API key leakage in responses.
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

	// Mask the token to prevent API key leakage
	// Only show first 8 chars and last 4 chars with asterisks in between
	maskedToken := maskToken(token)

	return AuthLoginResponse{
		Token:     maskedToken,
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

// buildUserLoginResponse creates the AuthLoginResponse for user/password auth.
// For user auth, we generate a session token rather than exposing the password.
func buildUserLoginResponse(user *User) AuthLoginResponse {
	now := time.Now()
	expiresAt := now.Add(defaultSessionTTL)

	var roles []string
	if user.Role != "" {
		roles = append(roles, user.Role)
	}

	// Generate a session token for user auth
	// In a production system, this would be a JWT or opaque session token
	sessionToken := "session-" + user.ID + "-" + safePrefix(now.Format(time.RFC3339Nano), 16)

	return AuthLoginResponse{
		Token:     sessionToken,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: AuthUser{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			Tenant:      user.Tenant,
			Roles:       roles,
			Source:      "user",
			CreatedAt:   user.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   user.UpdatedAt.Format(time.RFC3339),
			LastLoginAt: now.Format(time.RFC3339),
		},
	}
}

// maskToken returns a masked version of the token.
// Shows first 8 and last 4 characters, with asterisks in between.
// For tokens shorter than 16 chars, shows first 4 chars with asterisks.
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 12 {
		// Short tokens: show first 4 chars + asterisks
		return safePrefix(token, 4) + "********"
	}
	// Longer tokens: show first 8 + asterisks + last 4
	return token[:8] + "********" + token[len(token)-4:]
}

// safePrefix returns first n chars of s, or s if shorter.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// handleChangePassword handles password change for authenticated users.
// POST /api/v1/auth/password
func (s *server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.CurrentPassword) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "current_password required")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "new_password required")
		return
	}

	userStore := basicAuth.UserStore()

	// Get user by principal ID
	user, err := userStore.GetByID(r.Context(), authCtx.PrincipalID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	// Validate current password
	if !userStore.ValidatePassword(r.Context(), user, req.CurrentPassword) {
		writeErrorJSON(w, http.StatusUnauthorized, "invalid current password")
		return
	}

	// Update password
	if err := userStore.UpdatePassword(r.Context(), user.ID, req.NewPassword); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.appendAuditEntry(r.Context(), "change_password", "user", authCtx.PrincipalID, authCtx.PrincipalID, authCtx.Role, "change password")
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateUser creates a new user (admin only).
// POST /api/v1/users
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Require admin role
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	basicAuth, ok := s.auth.(*BasicAuthProvider)
	if !ok || basicAuth.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Username) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "username required")
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "password required")
		return
	}

	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = authCtx.Tenant
	}

	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "user"
	}

	user := &User{
		Username: strings.TrimSpace(req.Username),
		Email:    strings.TrimSpace(req.Email),
		Tenant:   tenant,
		Role:     role,
	}

	userStore := basicAuth.UserStore()
	if err := userStore.Create(r.Context(), user, req.Password); err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			writeErrorJSON(w, http.StatusConflict, "user already exists")
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.appendAuditEntry(r.Context(), "create", "user", user.ID, authCtx.PrincipalID, authCtx.Role, "create user "+user.Username)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w,AuthUser{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Tenant:    user.Tenant,
		Roles:     []string{user.Role},
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
		UpdatedAt: user.UpdatedAt.Format(time.RFC3339),
	})
}
