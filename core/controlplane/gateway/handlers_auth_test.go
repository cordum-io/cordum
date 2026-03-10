package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestHandleLogin_ValidAPIKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key","role":"admin","principal_id":"alice","tenant":"default"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"alice","password":"test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	// Token should be masked for security - verify it's not the full key
	if resp.Token == "test-key" {
		t.Fatalf("expected token to be masked, but got full key")
	}
	// Verify it contains mask characters
	if resp.Token != "test********" {
		t.Fatalf("expected masked token test********, got %q", resp.Token)
	}
	if resp.User.Tenant != "default" {
		t.Fatalf("expected tenant default, got %q", resp.User.Tenant)
	}
	if resp.User.ID != "alice" {
		t.Fatalf("expected user ID alice, got %q", resp.User.ID)
	}
	if len(resp.User.Roles) == 0 || resp.User.Roles[0] != "admin" {
		t.Fatalf("expected role admin, got %v", resp.User.Roles)
	}
}

func TestHandleLogin_InvalidAPIKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"valid-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"user","password":"invalid-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_EmptyPassword(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"user","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `not valid json`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSession_ValidSession(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"session-key","role":"viewer","principal_id":"bob"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	authCtx := &AuthContext{
		APIKey:      "session-key",
		Tenant:      "default",
		PrincipalID: "bob",
		Role:        "viewer",
	}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.ID != "bob" {
		t.Fatalf("expected user ID bob, got %q", resp.User.ID)
	}
	if len(resp.User.Roles) == 0 || resp.User.Roles[0] != "viewer" {
		t.Fatalf("expected role viewer, got %v", resp.User.Roles)
	}
}

func TestHandleSession_NoAuthContext(t *testing.T) {
	s := &server{tenant: "default"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	rec := httptest.NewRecorder()

	s.handleSession(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogout_Success(t *testing.T) {
	s := &server{tenant: "default"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()

	s.handleLogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestLoginIsPublicPath(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})

	if !provider.IsPublicPath("/api/v1/auth/login") {
		t.Fatal("expected /api/v1/auth/login to be public")
	}
	if !provider.IsPublicPath("/api/v1/auth/config") {
		t.Fatal("expected /api/v1/auth/config to be public")
	}
	if provider.IsPublicPath("/api/v1/auth/session") {
		t.Fatal("expected /api/v1/auth/session to NOT be public")
	}
	if provider.IsPublicPath("/api/v1/jobs") {
		t.Fatal("expected /api/v1/jobs to NOT be public")
	}
}

func TestBasicAuthProvidesAuthConfig(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})

	cfg := provider.AuthConfig()
	if !cfg.PasswordEnabled {
		t.Fatal("expected password_enabled to be true")
	}
	if cfg.SessionTTL != "24h" {
		t.Fatalf("expected session_ttl 24h, got %q", cfg.SessionTTL)
	}
	if cfg.DefaultTenant != "default" {
		t.Fatalf("expected default tenant, got %q", cfg.DefaultTenant)
	}
}

func TestBasicAuthProvidesAuthConfig_NoKeys(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ALLOW_INSECURE_NO_AUTH": "1",
	})

	cfg := provider.AuthConfig()
	if cfg.PasswordEnabled {
		t.Fatal("expected password_enabled to be false when no keys")
	}
}

func TestSessionTokenCryptoRandom(t *testing.T) {
	user := &User{
		ID:       "user-1",
		Username: "test",
		Tenant:   "default",
	}

	resp1, err := buildUserLoginResponse(context.Background(), user)
	if err != nil {
		t.Fatalf("buildUserLoginResponse: %v", err)
	}
	resp2, err := buildUserLoginResponse(context.Background(), user)
	if err != nil {
		t.Fatalf("buildUserLoginResponse: %v", err)
	}

	// Tokens must differ even for the same user at the same instant.
	if resp1.Token == resp2.Token {
		t.Fatal("expected different session tokens, got identical")
	}

	// Tokens must start with session- prefix.
	if !strings.HasPrefix(resp1.Token, "session-") {
		t.Fatalf("token missing session- prefix: %s", resp1.Token)
	}
	if !strings.HasPrefix(resp2.Token, "session-") {
		t.Fatalf("token missing session- prefix: %s", resp2.Token)
	}

	// Token length: "session-" (8) + base64url(32 bytes) = 8 + 43 = 51 chars.
	const expectedLen = 8 + 43
	if len(resp1.Token) != expectedLen {
		t.Fatalf("expected token length %d, got %d (%s)", expectedLen, len(resp1.Token), resp1.Token)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy exhausted")
}

func TestBuildUserLoginResponseRandFailure(t *testing.T) {
	user := &User{
		ID:       "user-1",
		Username: "test",
		Tenant:   "default",
	}
	original := rand.Reader
	rand.Reader = failingReader{}
	t.Cleanup(func() { rand.Reader = original })

	if _, err := buildUserLoginResponse(context.Background(), user); err == nil {
		t.Fatal("expected error on rand failure")
	}
}

// timingUserStore returns a user with a bcrypt hash for "exists" and
// ErrUserNotFound for anything else, so we can measure bcrypt timing.
type timingUserStore struct {
	user *User
}

func (s *timingUserStore) GetByUsername(_ context.Context, username, _ string) (*User, error) {
	if username == "exists" {
		return s.user, nil
	}
	return nil, ErrUserNotFound
}
func (s *timingUserStore) GetByEmail(_ context.Context, _, _ string) (*User, error) {
	return nil, ErrUserNotFound
}
func (s *timingUserStore) GetByID(_ context.Context, _ string) (*User, error) {
	return nil, ErrUserNotFound
}
func (s *timingUserStore) Create(_ context.Context, _ *User, _ string) error   { return nil }
func (s *timingUserStore) List(_ context.Context, _ string) ([]*User, error)   { return nil, nil }
func (s *timingUserStore) Update(_ context.Context, _ *User) error             { return nil }
func (s *timingUserStore) Delete(_ context.Context, _ string) error            { return nil }
func (s *timingUserStore) UpdatePassword(_ context.Context, _, _ string) error { return nil }
func (s *timingUserStore) ValidatePassword(_ context.Context, u *User, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}
func (s *timingUserStore) Close() error { return nil }

func TestLoginTimingEqualization(t *testing.T) {
	// Create a user with the same bcrypt cost as the timing dummy hash
	// to ensure timing equalization works correctly.
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcryptCostFromEnv())
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}
	us := &timingUserStore{
		user: &User{
			ID:           "u-timing",
			Username:     "exists",
			Tenant:       "default",
			PasswordHash: string(hash),
		},
	}

	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"fallback-key"}]`,
	})
	provider.SetUserStore(us)
	s := &server{auth: provider, tenant: "default"}

	// Measure: existing user + wrong password (bcrypt in ValidatePassword).
	existsBody := `{"username":"exists","password":"wrong-password"}`
	start := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(existsBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	existsDuration := time.Since(start)

	// Measure: non-existent user (should now do dummy bcrypt for timing equalization).
	missingBody := `{"username":"missing","password":"wrong-password"}`
	start = time.Now()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(missingBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	s.handleLogin(rec, req)
	missingDuration := time.Since(start)

	// Both paths should take roughly the same time (bcrypt dominates).
	// Allow up to 3x difference to account for system load variance.
	ratio := float64(existsDuration) / float64(missingDuration)
	if ratio > 3.0 || ratio < 0.33 {
		t.Fatalf("timing oracle detected: exists=%v missing=%v ratio=%.2f (want 0.33-3.0)",
			existsDuration, missingDuration, ratio)
	}

	// Both should take at least 30ms (bcrypt default cost is ~100ms).
	if missingDuration < 30*time.Millisecond {
		t.Fatalf("missing-user path too fast (%v) — timing equalization may not be working", missingDuration)
	}
}

// ---- Login integration tests with RedisUserStore ----

func setupLoginIntegration(t *testing.T) (*server, *RedisUserStore) {
	t.Helper()
	store, _ := newTestUserStore(t)
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"fallback-api-key","role":"admin","principal_id":"api-admin","tenant":"default"}]`,
	})
	provider.SetUserStore(store)
	s := &server{auth: provider, tenant: "default"}
	return s, store
}

func TestLoginHandler_BruteForce429(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	// Create a user to trigger the user-auth path.
	user := &User{Username: "bruteforce-target", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Send maxLoginAttempts() failed logins to trigger throttle.
	for i := 0; i < maxLoginAttempts(); i++ {
		body := `{"username":"bruteforce-target","password":"wrong-password"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleLogin(rec, req)
		// These should either succeed (wrong password → falls to API key path → 401)
		// or fail, but NOT be 429 yet.
	}

	// Next attempt should be throttled → 429.
	body := `{"username":"bruteforce-target","password":"wrong-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginHandler_DisabledUser403(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	// Create a disabled user.
	user := &User{Username: "disabled-user", Tenant: "default", Role: "user", Disabled: true}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"username":"disabled-user","password":"SecurePass1!xy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginHandler_SessionTokenCreated(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	// Create a user.
	user := &User{Username: "session-user", Tenant: "default", Role: "admin"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"username":"session-user","password":"SecurePass1!xy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Session token should start with "session-" prefix.
	if !strings.HasPrefix(resp.Token, "session-") {
		t.Fatalf("expected session- token prefix, got %q", resp.Token)
	}
	if resp.User.Source != "user" {
		t.Fatalf("expected source=user, got %q", resp.User.Source)
	}
	if resp.User.Username != "session-user" {
		t.Fatalf("expected username=session-user, got %q", resp.User.Username)
	}
}

func TestLoginHandler_APIKeyFallback(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	// Create a user (different password) so the user-auth path runs but fails.
	user := &User{Username: "some-user", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Login with wrong user password but pass the API key as password — should fall through.
	body := `{"username":"unknown-user","password":"fallback-api-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 via API key fallback, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.User.Source != "api_key" {
		t.Fatalf("expected source=api_key, got %q", resp.User.Source)
	}
}

// ---- Revoke key handler tests ----

// stubKeyStore implements KeyStore for testing handleRevokeKey.
type stubKeyStore struct {
	revokeErr error
}

func (s *stubKeyStore) List(_ context.Context, _ string) ([]*ManagedKey, error) { return nil, nil }
func (s *stubKeyStore) Create(_ context.Context, _ *ManagedKey, _ string) error { return nil }
func (s *stubKeyStore) Revoke(_ context.Context, _ string, _ string) error      { return s.revokeErr }
func (s *stubKeyStore) ValidateKey(_ context.Context, _ string) (*ManagedKey, error) {
	return nil, ErrKeyNotFound
}
func (s *stubKeyStore) RecordUsage(_ context.Context, _ string) error { return nil }

func TestHandleRevokeKeyNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.keyStore = &stubKeyStore{revokeErr: ErrKeyNotFound}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/keys/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, &AuthContext{
		Role:   "admin",
		Tenant: "default",
	}))
	rec := httptest.NewRecorder()
	s.handleRevokeKey(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing key, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRevokeKeyInternalError(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.keyStore = &stubKeyStore{revokeErr: errors.New("redis connection refused")}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/keys/some-id", nil)
	req.SetPathValue("id", "some-id")
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, &AuthContext{
		Role:   "admin",
		Tenant: "default",
	}))
	rec := httptest.NewRecorder()
	s.handleRevokeKey(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for internal error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRevokeKeyViewerDenied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.keyStore = &stubKeyStore{}
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer","tenant":"default"}]`,
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/keys/some-id", nil)
	req.SetPathValue("id", "some-id")
	req.Header.Set("X-API-Key", "viewer-key")
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handleRevokeKey(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer revoking keys, got %d: %s", rec.Code, rec.Body.String())
	}
}
