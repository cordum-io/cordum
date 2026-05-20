package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
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
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if strings.Contains(rec.Body.String(), "user") || strings.Contains(rec.Body.String(), "email") {
		t.Fatalf("auth failure body leaks identity hint: %s", rec.Body.String())
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
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
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
	authCtx := &auth.AuthContext{
		APIKey:      "session-key",
		Tenant:      "default",
		PrincipalID: "bob",
		Role:        "viewer",
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
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
	user := &auth.User{
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
	user := &auth.User{
		ID:       "user-1",
		Username: "test",
		Tenant:   "default",
	}
	original := rand.Reader
	rand.Reader = failingReader{}
	t.Cleanup(func() { rand.Reader = original })

	resp, err := buildUserLoginResponse(context.Background(), user)
	if err == nil {
		t.Fatal("expected error on rand failure")
	}
	// The error sentinel is intentionally opaque so the handler layer can
	// translate it to a generic 500 without leaking entropy-source details
	// (which can carry kernel or driver diagnostics) to the HTTP body.
	if !errors.Is(err, errSessionTokenEntropy) {
		t.Fatalf("expected errSessionTokenEntropy sentinel, got %v", err)
	}
	// No partial response may be returned — a zero-value struct keeps the
	// caller from accidentally emitting a session cookie / response body
	// backed by a zero-filled token buffer.
	if resp.Token != "" || resp.ExpiresAt != "" || resp.User.ID != "" {
		t.Fatalf("expected zero-value response on entropy failure, got %+v", resp)
	}
}

// TestHandleLogin_EntropyFailureReturns500 drives the full handleLogin HTTP
// path with a user-store-backed login that should mint a session token, but
// with the crypto/rand source failing. The handler must return 500 with a
// generic body that does NOT leak the underlying reader error, must not emit
// a Set-Cookie header, and must not write a token field to the response body.
func TestHandleLogin_EntropyFailureReturns500(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), auth.BcryptCostFromEnv())
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}
	us := &timingUserStore{
		user: &auth.User{
			ID:           "u-entropy",
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

	original := rand.Reader
	rand.Reader = failingReader{}
	t.Cleanup(func() { rand.Reader = original })

	body := `{"username":"exists","password":"correct-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on entropy failure, got %d: %s", rec.Code, rec.Body.String())
	}
	// Body must not carry the raw reader error (security rail).
	if strings.Contains(rec.Body.String(), "entropy exhausted") ||
		strings.Contains(rec.Body.String(), "crypto/rand") {
		t.Fatalf("response leaked entropy error detail: %s", rec.Body.String())
	}
	// No session token may be minted — response body must contain no token.
	if strings.Contains(rec.Body.String(), "session-") {
		t.Fatalf("response leaked a session token on entropy failure: %s", rec.Body.String())
	}
	// No Set-Cookie header may be emitted for a failed session.
	if cookies := rec.Result().Cookies(); len(cookies) != 0 { //nolint:bodyclose // httptest.ResponseRecorder
		t.Fatalf("expected zero cookies on entropy failure, got %d: %+v", len(cookies), cookies)
	}
}

// timingUserStore returns a user with a bcrypt hash for "exists" and
// ErrUserNotFound for anything else, so login tests can exercise bcrypt-backed
// password validation without Redis.
type timingUserStore struct {
	user *auth.User
}

func (s *timingUserStore) GetByUsername(_ context.Context, username, _ string) (*auth.User, error) {
	if username == "exists" {
		return s.user, nil
	}
	return nil, auth.ErrUserNotFound
}
func (s *timingUserStore) GetByEmail(_ context.Context, _, _ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *timingUserStore) GetByID(_ context.Context, _ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *timingUserStore) Create(_ context.Context, _ *auth.User, _ string) error { return nil }
func (s *timingUserStore) List(_ context.Context, _ string) ([]*auth.User, error) { return nil, nil }
func (s *timingUserStore) Update(_ context.Context, _ *auth.User) error           { return nil }
func (s *timingUserStore) Delete(_ context.Context, _ string) error               { return nil }
func (s *timingUserStore) UpdatePassword(_ context.Context, _, _ string) error    { return nil }
func (s *timingUserStore) ValidatePassword(_ context.Context, u *auth.User, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}
func (s *timingUserStore) Close() error { return nil }

// ---- Login integration tests with RedisUserStore ----

func setupLoginIntegration(t *testing.T) (*server, *auth.RedisUserStore) {
	t.Helper()
	store, _ := newTestUserStore(t)
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"fallback-api-key","role":"admin","principal_id":"api-admin","tenant":"default"}]`,
	})
	provider.SetUserStore(store)
	s := &server{auth: provider, tenant: "default"}
	return s, store
}

// TestLoginHandler_BruteForceCollapsesTo401NoOracle asserts that throttling a
// known user does NOT surface 429 to the caller — collapsing into the same
// 401 / AUTH_INVALID_CREDENTIALS shape as unknown-user / wrong-password closes
// the rate-limit user-enumeration oracle. Server-side audit retains the real
// reason via emitAuthFailure → slog.Warn for ops debugging.
func TestLoginHandler_BruteForceCollapsesTo401NoOracle(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	user := &auth.User{Username: "bruteforce-target", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := 0; i < maxLoginAttempts(); i++ {
		body := `{"username":"bruteforce-target","password":"wrong-password"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleLogin(rec, req)
	}

	body := `{"username":"bruteforce-target","password":"wrong-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (no rate-limit oracle), got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if strings.Contains(rec.Body.String(), "bruteforce-target") ||
		strings.Contains(rec.Body.String(), "rate") ||
		strings.Contains(rec.Body.String(), "throttle") {
		t.Fatalf("throttled response leaked rate-limit oracle: %s", rec.Body.String())
	}
}

// TestLoginHandler_DisabledUserCollapsesTo401NoOracle asserts that a disabled
// user attempting login yields the same 401 / AUTH_INVALID_CREDENTIALS shape
// as unknown-user / wrong-password. Returning a distinct 403 / AUTH_USER_DISABLED
// would let an attacker enumerate valid-but-disabled accounts.
func TestLoginHandler_DisabledUserCollapsesTo401NoOracle(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	user := &auth.User{Username: "disabled-user", Tenant: "default", Role: "user", Disabled: true}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"username":"disabled-user","password":"SecurePass1!xy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (no user-disabled oracle), got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if strings.Contains(rec.Body.String(), "disabled-user") ||
		strings.Contains(strings.ToLower(rec.Body.String()), "disabled") {
		t.Fatalf("disabled-user response body leaked username or disabled hint: %s", rec.Body.String())
	}
}

func TestLoginHandler_DisabledUserRecordsThrottleAttempt(t *testing.T) {
	t.Setenv("MAX_LOGIN_ATTEMPTS", "1")
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	user := &auth.User{Username: "disabled-throttle", Tenant: "default", Role: "user", Disabled: true}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create disabled user: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"username":"disabled-throttle","password":"wrong-password"}`))
	req.RemoteAddr = "203.0.113.55:4567"
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if err := store.CheckLoginThrottle(ctx, "disabled-throttle", clientIP(req)); !errors.Is(err, auth.ErrLoginThrottled) {
		t.Fatalf("disabled-user login must record failed throttle attempt, got %v", err)
	}
}

// TestHandleAuthLogin_NoUserEnumerationViaResponseShape verifies that all four
// auth-failure cases — unknown user, disabled user, rate-limited known user,
// wrong password — return byte-identical status + body so an attacker cannot
// distinguish "user exists" / "user exists but disabled" / "user exists but
// throttled" / "user does not exist" via response variance.
func TestHandleAuthLogin_NoUserEnumerationViaResponseShape(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	if err := store.Create(ctx, &auth.User{Username: "active-user", Tenant: "default", Role: "user"}, "SecurePass1!xy"); err != nil {
		t.Fatalf("create active user: %v", err)
	}
	if err := store.Create(ctx, &auth.User{Username: "disabled-user", Tenant: "default", Role: "user", Disabled: true}, "SecurePass1!xy"); err != nil {
		t.Fatalf("create disabled user: %v", err)
	}
	if err := store.Create(ctx, &auth.User{Username: "throttled-user", Tenant: "default", Role: "user"}, "SecurePass1!xy"); err != nil {
		t.Fatalf("create throttled user: %v", err)
	}
	// Burn the throttled-user's attempt budget under the IP we'll reuse below.
	const throttleIP = "192.0.2.7"
	for i := 0; i < maxLoginAttempts()+2; i++ {
		store.RecordFailedLogin(ctx, "throttled-user", throttleIP)
	}
	if err := store.CheckLoginThrottle(ctx, "throttled-user", throttleIP); err == nil {
		t.Fatalf("expected throttle to fire after burning budget")
	}

	cases := []struct {
		name     string
		username string
	}{
		{"unknown_user", "nonexistent-user"},
		{"disabled_user", "disabled-user"},
		{"rate_limited", "throttled-user"},
		{"wrong_password", "active-user"},
	}

	bodies := make([]string, len(cases))
	for i, c := range cases {
		body := `{"username":"` + c.username + `","password":"wrong-pass"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		// Align RemoteAddr to the throttle IP so CheckLoginThrottle fires for
		// the rate-limited case and stays inert for the others.
		req.RemoteAddr = throttleIP + ":1234"
		rec := httptest.NewRecorder()
		s.handleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d body=%s", c.name, rec.Code, rec.Body.String())
		}
		requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
		bodies[i] = rec.Body.String()
	}

	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("response body diverged across failure cases\n  %s: %s\n  %s: %s",
				cases[0].name, bodies[0], cases[i].name, bodies[i])
		}
	}
}

type deterministicLoginTimingStore struct {
	users         map[string]*auth.User
	validateCalls int64
}

func newDeterministicLoginTimingStore() *deterministicLoginTimingStore {
	return &deterministicLoginTimingStore{
		users: map[string]*auth.User{
			"blocked-user": {ID: "u-blocked", Username: "blocked-user", Tenant: "default", Role: "user", Disabled: true},
			"good-user":    {ID: "u-good", Username: "good-user", Tenant: "default", Role: "user"},
		},
	}
}

func (s *deterministicLoginTimingStore) GetByUsername(_ context.Context, username, _ string) (*auth.User, error) {
	if user, ok := s.users[username]; ok {
		return user, nil
	}
	return nil, auth.ErrUserNotFound
}
func (s *deterministicLoginTimingStore) GetByEmail(_ context.Context, _, _ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *deterministicLoginTimingStore) GetByID(_ context.Context, _ string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *deterministicLoginTimingStore) Create(_ context.Context, _ *auth.User, _ string) error {
	return nil
}
func (s *deterministicLoginTimingStore) List(_ context.Context, _ string) ([]*auth.User, error) {
	return nil, nil
}
func (s *deterministicLoginTimingStore) Update(_ context.Context, _ *auth.User) error { return nil }
func (s *deterministicLoginTimingStore) Delete(_ context.Context, _ string) error     { return nil }
func (s *deterministicLoginTimingStore) UpdatePassword(_ context.Context, _, _ string) error {
	return nil
}
func (s *deterministicLoginTimingStore) ValidatePassword(_ context.Context, _ *auth.User, _ string) bool {
	atomic.AddInt64(&s.validateCalls, 1)
	return false
}
func (s *deterministicLoginTimingStore) Close() error { return nil }

type loginTimingHarness struct {
	server       *server
	store        *deterministicLoginTimingStore
	timingCalls  int64
	lastPassword string
}

func newLoginTimingHarness(t *testing.T) *loginTimingHarness {
	t.Helper()
	store := newDeterministicLoginTimingStore()
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"fallback-api-key","role":"admin","principal_id":"api-admin","tenant":"default"}]`,
	})
	provider.SetUserStore(store)
	h := &loginTimingHarness{store: store}
	h.server = &server{auth: provider, tenant: "default", loginTimingCompare: h.compareTimingHash}
	return h
}

func (h *loginTimingHarness) compareTimingHash(hash, password []byte) error {
	atomic.AddInt64(&h.timingCalls, 1)
	h.lastPassword = string(password)
	if !bytes.Equal(hash, loginTimingDummyHash) {
		panic("unexpected login timing hash")
	}
	return bcrypt.ErrMismatchedHashAndPassword
}

func (h *loginTimingHarness) reset() {
	atomic.StoreInt64(&h.timingCalls, 0)
	atomic.StoreInt64(&h.store.validateCalls, 0)
	h.lastPassword = ""
}

func (h *loginTimingHarness) login(username, password string) *httptest.ResponseRecorder {
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.server.handleLogin(rec, req)
	return rec
}

func (h *loginTimingHarness) requireInvalidLogin(t *testing.T, username, password string) string {
	t.Helper()
	rec := h.login(username, password)
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	return rec.Body.String()
}

// TestHandleAuthLogin_TimingResistantUnderEarlyReturn proves the disabled-user
// early-return path still executes the timing-equalization hash without relying
// on shared-runner wall-clock measurements.
func TestHandleAuthLogin_TimingResistantUnderEarlyReturn(t *testing.T) {
	harness := newLoginTimingHarness(t)

	harness.reset()
	blockedBody := harness.requireInvalidLogin(t, "blocked-user", "wrong-pass")
	if calls := atomic.LoadInt64(&harness.timingCalls); calls != 1 {
		t.Fatalf("disabled-user path must burn exactly one dummy hash, got %d", calls)
	}
	if calls := atomic.LoadInt64(&harness.store.validateCalls); calls != 0 {
		t.Fatalf("disabled-user path must not validate password, got %d calls", calls)
	}
	if harness.lastPassword != "wrong-pass" {
		t.Fatalf("dummy hash saw password %q, want wrong-pass", harness.lastPassword)
	}

	harness.reset()
	goodBody := harness.requireInvalidLogin(t, "good-user", "wrong-pass")
	if calls := atomic.LoadInt64(&harness.timingCalls); calls != 0 {
		t.Fatalf("good-user wrong-password path should use ValidatePassword, got %d dummy hashes", calls)
	}
	if calls := atomic.LoadInt64(&harness.store.validateCalls); calls != 1 {
		t.Fatalf("good-user wrong-password path must validate exactly once, got %d", calls)
	}
	if blockedBody != goodBody {
		t.Fatalf("disabled-user and wrong-password responses diverged\nblocked=%s\ngood=%s", blockedBody, goodBody)
	}

	harness.reset()
	harness.requireInvalidLogin(t, "missing-user", "wrong-pass")
	if calls := atomic.LoadInt64(&harness.timingCalls); calls != 1 {
		t.Fatalf("missing-user path must burn exactly one dummy hash, got %d", calls)
	}

	harness.reset()
	harness.requireInvalidLogin(t, "good-user", "")
	if calls := atomic.LoadInt64(&harness.timingCalls); calls != 1 {
		t.Fatalf("empty-password path must burn exactly one dummy hash, got %d", calls)
	}
}

func TestHandleChangePasswordInvalidCurrentPasswordReturnsStableCode(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	user := &auth.User{Username: "change-user", Tenant: "default", Role: "user"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/change-password",
		bytes.NewBufferString(`{"current_password":"wrong","new_password":"NewSecurePass1!xy"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: user.ID,
		Role:        "user",
	}))
	rec := httptest.NewRecorder()
	s.handleChangePassword(rec, req)

	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if strings.Contains(rec.Body.String(), "change-user") || strings.Contains(rec.Body.String(), user.ID) {
		t.Fatalf("password-change failure body leaked user identity: %s", rec.Body.String())
	}
}

type changePasswordInternalErrorStore struct {
	user *auth.User
}

func (s *changePasswordInternalErrorStore) GetByUsername(context.Context, string, string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *changePasswordInternalErrorStore) GetByEmail(context.Context, string, string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *changePasswordInternalErrorStore) GetByID(context.Context, string) (*auth.User, error) {
	return s.user, nil
}
func (s *changePasswordInternalErrorStore) Create(context.Context, *auth.User, string) error {
	return nil
}
func (s *changePasswordInternalErrorStore) List(context.Context, string) ([]*auth.User, error) {
	return nil, nil
}
func (s *changePasswordInternalErrorStore) Update(context.Context, *auth.User) error {
	return nil
}
func (s *changePasswordInternalErrorStore) Delete(context.Context, string) error {
	return nil
}
func (s *changePasswordInternalErrorStore) UpdatePassword(context.Context, string, string) error {
	return fmt.Errorf("redis set user: internal topology leaked")
}
func (s *changePasswordInternalErrorStore) ValidatePassword(context.Context, *auth.User, string) bool {
	return true
}
func (s *changePasswordInternalErrorStore) Close() error { return nil }

func TestHandleChangePassword_NoInternalErrorLeak(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key","role":"admin","principal_id":"admin","tenant":"default"}]`,
	})
	provider.SetUserStore(&changePasswordInternalErrorStore{
		user: &auth.User{ID: "user-1", Username: "change-user", Tenant: "default", Role: "admin"},
	})
	s := &server{auth: provider, tenant: "default"}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/auth/password",
		bytes.NewBufferString(`{"current_password":"SecurePass1!xy","new_password":"NewSecurePass1!xy"}`)),
		&auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "user-1"})
	rec := httptest.NewRecorder()
	s.handleChangePassword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for storage failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "redis set user") || strings.Contains(rec.Body.String(), "internal topology") {
		t.Fatalf("change-password response leaked internal error: %s", rec.Body.String())
	}
}

func TestHandleChangePassword_ValidationErrorStaysAuthPasswordInvalid(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()
	user := &auth.User{Username: "policy-user", Tenant: "default", Role: "admin"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/auth/password",
		bytes.NewBufferString(`{"current_password":"SecurePass1!xy","new_password":"short"}`)),
		&auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: user.ID})
	rec := httptest.NewRecorder()
	s.handleChangePassword(rec, req)

	requireStableErrorCode(t, rec, http.StatusBadRequest, "AUTH_PASSWORD_INVALID")
	if !strings.Contains(rec.Body.String(), "password must be at least") {
		t.Fatalf("expected validator policy message, got %s", rec.Body.String())
	}
}

func TestLoginHandler_SessionTokenCreated(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	// Create a user.
	user := &auth.User{Username: "session-user", Tenant: "default", Role: "admin"}
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
	user := &auth.User{Username: "some-user", Tenant: "default", Role: "user"}
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

type loginUserStoreFailure struct{ timingUserStore }

func (s *loginUserStoreFailure) GetByUsername(context.Context, string, string) (*auth.User, error) {
	return nil, fmt.Errorf("redis get user: internal topology leaked")
}
func (s *loginUserStoreFailure) GetByEmail(context.Context, string, string) (*auth.User, error) {
	return nil, fmt.Errorf("redis get email: internal topology leaked")
}

func TestLoginHandler_NoAPIKeyFallbackAfterUserStoreFailure(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"fallback-api-key","role":"admin","principal_id":"api-admin","tenant":"default"}]`,
	})
	provider.SetUserStore(&loginUserStoreFailure{})
	s := &server{auth: provider, tenant: "default"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"username":"some-user","password":"fallback-api-key"}`))
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for user-store failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "api_key") || strings.Contains(rec.Body.String(), "redis get user") {
		t.Fatalf("login failure leaked fallback auth source or storage details: %s", rec.Body.String())
	}
}

func TestEmitAuthFailureRedactsPathSecrets(t *testing.T) {
	sink := &testAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/keys/ck_live_sensitive_secret", nil)

	s.emitAuthFailure(req, "user@example.com", "apikey", "invalid_credentials")

	if sink.Len() != 1 {
		t.Fatalf("expected one audit event, got %d", sink.Len())
	}
	path := sink.Get(0).Extra["path"]
	if strings.Contains(path, "ck_live_sensitive_secret") {
		t.Fatalf("auth failure audit path leaked key id: %q", path)
	}
	if path != "/api/v1/auth/keys/{id}" {
		t.Fatalf("unexpected sanitized path %q", path)
	}
}

type updateUserCaptureStore struct {
	existing *auth.User
	updated  *auth.User
}

func (s *updateUserCaptureStore) GetByUsername(context.Context, string, string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *updateUserCaptureStore) GetByEmail(context.Context, string, string) (*auth.User, error) {
	return nil, auth.ErrUserNotFound
}
func (s *updateUserCaptureStore) GetByID(context.Context, string) (*auth.User, error) {
	return s.existing, nil
}
func (s *updateUserCaptureStore) Create(context.Context, *auth.User, string) error { return nil }
func (s *updateUserCaptureStore) List(context.Context, string) ([]*auth.User, error) {
	return nil, nil
}
func (s *updateUserCaptureStore) Update(_ context.Context, user *auth.User) error {
	s.updated = user
	if strings.TrimSpace(user.Username) == "" || strings.TrimSpace(user.Tenant) == "" {
		return fmt.Errorf("username and tenant required")
	}
	return nil
}
func (s *updateUserCaptureStore) Delete(context.Context, string) error { return nil }
func (s *updateUserCaptureStore) UpdatePassword(context.Context, string, string) error {
	return nil
}
func (s *updateUserCaptureStore) ValidatePassword(context.Context, *auth.User, string) bool {
	return true
}
func (s *updateUserCaptureStore) Close() error { return nil }

func TestHandleUpdateUserPassesExistingUsernameToStore(t *testing.T) {
	store := &updateUserCaptureStore{
		existing: &auth.User{ID: "user-1", Username: "existing-user", Tenant: "default", Role: "viewer"},
	}
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key","role":"admin","principal_id":"admin","tenant":"default"}]`,
	})
	provider.SetUserStore(store)
	s, _, _ := newTestGateway(t)
	s.auth = provider
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/users/user-1",
		bytes.NewBufferString(`{"display_name":"Updated User"}`)),
		&auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin"})
	req.SetPathValue("id", "user-1")
	rec := httptest.NewRecorder()

	s.handleUpdateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.updated == nil || store.updated.Username != "existing-user" || store.updated.Tenant != "default" {
		t.Fatalf("Update received incomplete user: %+v", store.updated)
	}
}

type concurrentOIDCAuth struct {
	active int32
	max    int32
}

func (a *concurrentOIDCAuth) AuthenticateHTTP(*http.Request) (*auth.AuthContext, error) {
	return nil, errors.New("not used")
}
func (a *concurrentOIDCAuth) AuthenticateGRPC(context.Context) (*auth.AuthContext, error) {
	return nil, errors.New("not used")
}
func (a *concurrentOIDCAuth) RequireRole(*http.Request, ...string) error { return nil }
func (a *concurrentOIDCAuth) ResolveTenant(_ *http.Request, requested, fallback string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	return fallback, nil
}
func (a *concurrentOIDCAuth) RequireTenantAccess(*http.Request, string) error { return nil }
func (a *concurrentOIDCAuth) ResolvePrincipal(*http.Request, string) (string, error) {
	return "admin", nil
}
func (a *concurrentOIDCAuth) AuthConfig() auth.AuthConfig {
	return auth.AuthConfig{OIDCGroupsClaim: "groups", OIDCGroupRoleMapping: map[string]string{"old": "viewer"}}
}
func (a *concurrentOIDCAuth) UpdateOIDCGroupRoleMapping(groupsClaim string, mapping map[string]string) (auth.AuthConfig, error) {
	n := atomic.AddInt32(&a.active, 1)
	defer atomic.AddInt32(&a.active, -1)
	for {
		old := atomic.LoadInt32(&a.max)
		if n <= old || atomic.CompareAndSwapInt32(&a.max, old, n) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	return auth.AuthConfig{OIDCGroupsClaim: groupsClaim, OIDCGroupRoleMapping: mapping}, nil
}

func TestHandleUpdateOIDCGroupRoleMappingSerializesSnapshotAndUpdate(t *testing.T) {
	s, _, _ := newTestGateway(t)
	provider := &concurrentOIDCAuth{}
	s.auth = provider
	const requests = 8
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"oidc_groups_claim":"groups","oidc_group_role_mapping":{"group-%d":"admin"}}`, i)
			req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/auth/oidc/group-role-mapping", strings.NewReader(body)),
				&auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin"})
			rec := httptest.NewRecorder()
			s.handleUpdateOIDCGroupRoleMapping(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("request %d status = %d body=%s", i, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&provider.max); got > 1 {
		t.Fatalf("OIDC mapping updates overlapped max concurrency=%d; snapshot/update must be serialized", got)
	}
}

// ---- Revoke key handler tests ----

// stubKeyStore implements KeyStore for testing handleRevokeKey.
type stubKeyStore struct {
	revokeErr error
}

func (s *stubKeyStore) List(_ context.Context, _ string) ([]*auth.ManagedKey, error) { return nil, nil }
func (s *stubKeyStore) Create(_ context.Context, _ *auth.ManagedKey, _ string) error { return nil }
func (s *stubKeyStore) Revoke(_ context.Context, _ string, _ string) error           { return s.revokeErr }
func (s *stubKeyStore) ValidateKey(_ context.Context, _ string) (*auth.ManagedKey, error) {
	return nil, auth.ErrKeyNotFound
}
func (s *stubKeyStore) RecordUsage(_ context.Context, _ string) error { return nil }

func TestHandleRevokeKeyNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.keyStore = &stubKeyStore{revokeErr: auth.ErrKeyNotFound}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/keys/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
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
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
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
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()
	s.handleRevokeKey(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer revoking keys, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogin_SetsHttpOnlyCookie(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"cookie-test-key","role":"admin","principal_id":"alice","tenant":"default"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"alice","password":"cookie-test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be set on login")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected SameSite=Lax, got %v", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/" {
		t.Fatalf("expected cookie path /, got %q", sessionCookie.Path)
	}
	if sessionCookie.Value == "" {
		t.Fatal("session cookie value must not be empty")
	}
}

func TestLogin_UserAuth_SetsHttpOnlyCookie(t *testing.T) {
	s, store := setupLoginIntegration(t)
	ctx := context.Background()

	user := &auth.User{Username: "cookie-user", Tenant: "default", Role: "admin"}
	if err := store.Create(ctx, user, "SecurePass1!xy"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"username":"cookie-user","password":"SecurePass1!xy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie on user/password login")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if !strings.HasPrefix(sessionCookie.Value, "session-") {
		t.Fatalf("expected session- prefix in cookie value, got %q", sessionCookie.Value)
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	s := &server{tenant: "default"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("X-API-Key", "some-token")
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie to be cleared on logout")
	}
	if sessionCookie.MaxAge != -1 {
		t.Fatalf("expected MaxAge=-1 (delete), got %d", sessionCookie.MaxAge)
	}
	if sessionCookie.Value != "" {
		t.Fatalf("expected empty cookie value on logout, got %q", sessionCookie.Value)
	}
}

func TestCSPHeader(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected Content-Security-Policy header")
	}
	if !strings.Contains(csp, "default-src") {
		t.Fatalf("CSP missing default-src directive: %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP missing frame-ancestors 'none': %q", csp)
	}
}

func TestCORSAllowCredentials(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	creds := rec.Header().Get("Access-Control-Allow-Credentials")
	if creds != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials: true, got %q", creds)
	}
}
