package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Server authorize helpers
// ---------------------------------------------------------------------------

func (s *server) requireRole(r *http.Request, roles ...string) error {
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireRole(r, roles...)
}

func (s *server) resolveTenant(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	headerTenant := headerValue(r, "X-Tenant-ID")
	// Fall back to auth context tenant (e.g. from session token)
	if headerTenant == "" {
		if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" {
			headerTenant = authCtx.Tenant
		}
	}
	if headerTenant == "" {
		return "", errors.New("tenant id required")
	}
	if requested == "" {
		requested = headerTenant
	} else if requested != headerTenant {
		return "", errors.New("tenant header mismatch")
	}
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolveTenant(r, requested, s.tenant)
}

func (s *server) requireTenantAccess(r *http.Request, tenant string) error {
	tenant = strings.TrimSpace(tenant)
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireTenantAccess(r, tenant)
}

func (s *server) resolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolvePrincipal(r, requested)
}

// ---------------------------------------------------------------------------
// AuthConfig handler
// ---------------------------------------------------------------------------

func (s *server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	defaultTenant := strings.TrimSpace(s.tenant)
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	resp := AuthConfig{
		PasswordEnabled:  false,
		SAMLEnabled:      false,
		SessionTTL:       "0s",
		RequireRBAC:      false,
		RequirePrincipal: false,
		DefaultTenant:    defaultTenant,
	}
	if provider, ok := s.auth.(AuthConfigProvider); ok {
		resp = provider.AuthConfig()
	}
	if strings.TrimSpace(resp.DefaultTenant) == "" {
		resp.DefaultTenant = defaultTenant
	}
	if strings.TrimSpace(resp.SessionTTL) == "" {
		resp.SessionTTL = "0s"
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// ---------------------------------------------------------------------------
// Login / session / logout
// ---------------------------------------------------------------------------

// loginTimingDummyHash is a pre-computed bcrypt hash used to equalize
// response time between user-exists and user-not-found login paths,
// preventing username enumeration via timing side-channel.
//
//nolint:gosec // G101: this is not a credential, it's a timing-equalization dummy.
var loginTimingDummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-pad"), bcrypt.DefaultCost)

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
	Password string `json:"password"` // #nosec G117 -- field name, not a credential
	Tenant   string `json:"tenant,omitempty"`
}

// AuthLoginResponse is the response for successful login/session.
type AuthLoginResponse struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	User      AuthUser `json:"user"`
}

func sessionTTL() time.Duration {
	const fallback = 24 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("CORDUM_SESSION_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// handleLogin authenticates using user/password or API key.
// Supports two authentication methods:
// 1. User/password: If user store is configured, authenticates against stored users
// 2. API key: For programmatic access (scripts, CI/CD), the password field accepts API keys
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req AuthLoginRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid request body")
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

			// Brute-force protection: check throttle before password validation.
			if redisStore, ok := userStore.(*RedisUserStore); ok {
				if err := redisStore.CheckLoginThrottle(r.Context(), username); err != nil {
					writeErrorJSON(w, http.StatusTooManyRequests, err.Error())
					return
				}
			}

			if userStore.ValidatePassword(r.Context(), user, password) {
				// User/password authentication successful — clear failed counter.
				if redisStore, ok := userStore.(*RedisUserStore); ok {
					redisStore.ClearFailedLogins(r.Context(), username)
				}
				resp, err := buildUserLoginResponse(r.Context(), user)
				if err != nil {
					writeErrorJSON(w, http.StatusInternalServerError, "internal error")
					return
				}
				// Store session token in Redis for subsequent request validation
				if redisStore, ok := userStore.(*RedisUserStore); ok {
					if err := redisStore.StoreSession(r.Context(), resp.Token, user, sessionTTL()); err != nil {
						writeErrorJSON(w, http.StatusInternalServerError, "failed to create session")
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, resp)
				return
			}

			// Password validation failed — record the attempt.
			if redisStore, ok := userStore.(*RedisUserStore); ok {
				redisStore.RecordFailedLogin(r.Context(), username)
			}
		} else if username != "" {
			// Timing equalization: spend bcrypt time even when user is not found,
			// preventing username enumeration via response time side-channel.
			_ = bcrypt.CompareHashAndPassword(loginTimingDummyHash, []byte(password)) //nolint:errcheck
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
	writeJSON(w, resp)
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
	writeJSON(w, resp)
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
	expiresAt := now.Add(sessionTTL())

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
func buildUserLoginResponse(ctx context.Context, user *User) (AuthLoginResponse, error) {
	now := time.Now()
	expiresAt := now.Add(sessionTTL())

	var roles []string
	if user.Role != "" {
		roles = append(roles, user.Role)
	}

	// Generate a cryptographically random session token (256 bits entropy).
	var tokenBytes [32]byte
	if _, err := io.ReadFull(rand.Reader, tokenBytes[:]); err != nil {
		slog.ErrorContext(ctx, "crypto/rand failed", "error", err)
		return AuthLoginResponse{}, fmt.Errorf("crypto/rand: %w", err)
	}
	sessionToken := "session-" + base64.RawURLEncoding.EncodeToString(tokenBytes[:])

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
	}, nil
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

// ---------------------------------------------------------------------------
// Password management
// ---------------------------------------------------------------------------

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
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid request body")
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

	s.appendAuditEntryNamed(r.Context(), "change_password", "user", authCtx.PrincipalID, user.Username, authCtx.PrincipalID, authCtx.Role, "change password")
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// User management (admin only)
// ---------------------------------------------------------------------------

// updateUserRequest is the request body for PUT /api/v1/users/{id}.
type updateUserRequest struct {
	Email       string   `json:"email,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// adminPasswordRequest is the request body for POST /api/v1/users/{id}/password.
type adminPasswordRequest struct {
	// #nosec G117 -- password is required in request payloads.
	Password string `json:"password"`
}

// userResponse maps a User to the frontend-expected JSON shape.
func userResponse(u *User) AuthUser {
	var roles []string
	if u.Role != "" {
		roles = []string{u.Role}
	}
	return AuthUser{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Tenant:      u.Tenant,
		Roles:       roles,
		CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   u.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// handleCreateUser creates a new user (admin only).
// POST /api/v1/users
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
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
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid request body")
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

	s.appendAuditEntryNamed(r.Context(), "create", "user", user.ID, user.Username, authCtx.PrincipalID, authCtx.Role, "create user "+user.Username)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, AuthUser{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Tenant:    user.Tenant,
		Roles:     []string{user.Role},
		CreatedAt: user.CreatedAt.Format(time.RFC3339),
		UpdatedAt: user.UpdatedAt.Format(time.RFC3339),
	})
}

// handleListUsers lists all users for the authenticated tenant (admin only).
// GET /api/v1/users
func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	usp, ok := s.auth.(UserStoreProvider)
	if !ok || usp.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	users, err := usp.UserStore().List(r.Context(), authCtx.Tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	items := make([]AuthUser, 0, len(users))
	for _, u := range users {
		items = append(items, userResponse(u))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

// handleUpdateUser updates a user's mutable fields (admin only).
// PUT /api/v1/users/{id}
func (s *server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	usp, ok := s.auth.(UserStoreProvider)
	if !ok || usp.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := usp.UserStore()

	// Load existing user and verify tenant
	existing, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if existing.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Build update user with only the fields to change
	update := &User{ID: userID}
	if strings.TrimSpace(req.Email) != "" {
		update.Email = strings.TrimSpace(req.Email)
	}
	if strings.TrimSpace(req.DisplayName) != "" {
		update.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	if len(req.Roles) > 0 && strings.TrimSpace(req.Roles[0]) != "" {
		update.Role = strings.TrimSpace(req.Roles[0])
	}

	if err := userStore.Update(r.Context(), update); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Re-fetch for response
	updated, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get updated user")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "update", "user", userID, updated.Username, authCtx.PrincipalID, authCtx.Role, "update user "+updated.Username)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, userResponse(updated))
}

// handleDeleteUser soft-deletes a user (admin only).
// DELETE /api/v1/users/{id}
func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	usp, ok := s.auth.(UserStoreProvider)
	if !ok || usp.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := usp.UserStore()

	// Load user and verify tenant
	user, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	// Prevent self-deletion
	if user.ID == authCtx.PrincipalID {
		writeErrorJSON(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}

	if err := userStore.Delete(r.Context(), userID); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "delete", "user", userID, user.Username, authCtx.PrincipalID, authCtx.Role, "delete user "+user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleChangeUserPassword changes a user's password (admin only).
// POST /api/v1/users/{id}/password
func (s *server) handleChangeUserPassword(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if authCtx.Tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if err := s.auth.RequireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "admin role required")
		return
	}

	usp, ok := s.auth.(UserStoreProvider)
	if !ok || usp.UserStore() == nil {
		writeErrorJSON(w, http.StatusBadRequest, "user authentication not enabled")
		return
	}

	userID := r.PathValue("id")
	if userID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "user id required")
		return
	}

	userStore := usp.UserStore()

	// Load user and verify tenant
	user, err := userStore.GetByID(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "user not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get user")
		return
	}
	if user.Tenant != authCtx.Tenant {
		writeErrorJSON(w, http.StatusNotFound, "user not found")
		return
	}

	var req adminPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	password := strings.TrimSpace(req.Password)
	if len(password) < 8 {
		writeErrorJSON(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	if err := userStore.UpdatePassword(r.Context(), userID, password); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to change password")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "change_password", "user", userID, user.Username, authCtx.PrincipalID, authCtx.Role, "admin changed password for "+user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// API Key management (admin only)
// ---------------------------------------------------------------------------

// Allowed scopes for managed API keys.
var allowedKeyScopes = map[string]struct{}{
	"admin":           {},
	"jobs:read":       {},
	"jobs:write":      {},
	"workflows:read":  {},
	"workflows:write": {},
	"policy:read":     {},
	"policy:write":    {},
}

// apiKeyResponse is the JSON shape returned to the frontend, matching the ApiKey type.
type apiKeyResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	LastUsed   string   `json:"lastUsed,omitempty"`
	UsageCount int64    `json:"usageCount"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
}

type createKeyRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

func managedKeyToResponse(mk *ManagedKey) apiKeyResponse {
	resp := apiKeyResponse{
		ID:         mk.ID,
		Name:       mk.Name,
		Prefix:     mk.Prefix,
		Scopes:     mk.Scopes,
		CreatedAt:  mk.CreatedAt.UTC().Format(time.RFC3339),
		UsageCount: mk.UsageCount,
	}
	if !mk.LastUsed.IsZero() {
		resp.LastUsed = mk.LastUsed.UTC().Format(time.RFC3339)
	}
	if !mk.ExpiresAt.IsZero() {
		resp.ExpiresAt = mk.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if resp.Scopes == nil {
		resp.Scopes = []string{}
	}
	return resp
}

// handleListKeys handles GET /api/v1/auth/keys.
func (s *server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	keys, err := s.keyStore.List(r.Context(), tenant)
	if err != nil {
		slog.Error("list keys failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list keys")
		return
	}

	items := make([]apiKeyResponse, 0, len(keys))
	for _, mk := range keys {
		items = append(items, managedKeyToResponse(mk))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

// handleCreateKey handles POST /api/v1/auth/keys.
func (s *server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	var req createKeyRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "name is required")
		return
	}

	// Validate scopes
	for _, scope := range req.Scopes {
		if _, ok := allowedKeyScopes[scope]; !ok {
			writeErrorJSON(w, http.StatusBadRequest, "invalid scope: "+scope)
			return
		}
	}

	var expiresAt time.Time
	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid expiresAt format, use RFC3339")
			return
		}
		if parsed.Before(time.Now()) {
			writeErrorJSON(w, http.StatusBadRequest, "expiresAt must be in the future")
			return
		}
		expiresAt = parsed
	}

	rawKey, err := GenerateRawKey()
	if err != nil {
		slog.Error("generate key failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to generate key")
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	scopes := req.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	mk := &ManagedKey{
		Name:      req.Name,
		Tenant:    tenant,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}

	if err := s.keyStore.Create(r.Context(), mk, rawKey); err != nil {
		slog.Error("create key failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "create", "api_key", mk.ID, mk.Name, policyActorID(r), policyRole(r), "create api key: "+mk.Name)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"key":    managedKeyToResponse(mk),
		"secret": rawKey,
	})
}

// handleRevokeKey handles DELETE /api/v1/auth/keys/{id}.
func (s *server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing key id")
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	if err := s.keyStore.Revoke(r.Context(), id, tenant); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, "key not found")
			return
		}
		slog.Error("revoke key failed", "error", err, "key_id", id) // #nosec -- key id is validated and safe for logs.
		writeErrorJSON(w, http.StatusInternalServerError, "failed to revoke key")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "revoke", "api_key", id, "", policyActorID(r), policyRole(r), "revoke api key: "+id)
	w.WriteHeader(http.StatusNoContent)
}
