package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type stubMemStore struct {
	context map[string][]byte
	result  map[string][]byte
}

func (s *stubMemStore) PutContext(ctx context.Context, key string, data []byte) error {
	if s.context == nil {
		s.context = make(map[string][]byte)
	}
	s.context[key] = data
	return nil
}

func (s *stubMemStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	val, ok := s.context[key]
	if !ok {
		return nil, redis.Nil
	}
	return val, nil
}

func (s *stubMemStore) PutResult(ctx context.Context, key string, data []byte) error {
	if s.result == nil {
		s.result = make(map[string][]byte)
	}
	s.result[key] = data
	return nil
}

func (s *stubMemStore) GetResult(ctx context.Context, key string) ([]byte, error) {
	val, ok := s.result[key]
	if !ok {
		return nil, redis.Nil
	}
	return val, nil
}

func (s *stubMemStore) Close() error { return nil }

func TestHandleGetMemoryFetchesContextByPointer(t *testing.T) {
	s := &server{
		memStore: &stubMemStore{
			context: map[string][]byte{
				"ctx:job-1": []byte(`{"prompt":"hi"}`),
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/v1/memory?ptr="+url.QueryEscape("redis://ctx:job-1"), nil)
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleGetMemory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp["kind"] != "context" {
		t.Fatalf("expected kind=context got=%v", resp["kind"])
	}
	if resp["key"] != "ctx:job-1" {
		t.Fatalf("expected key=ctx:job-1 got=%v", resp["key"])
	}
	jsonVal, ok := resp["json"].(map[string]any)
	if !ok {
		t.Fatalf("expected json object got=%T", resp["json"])
	}
	if jsonVal["prompt"] != "hi" {
		t.Fatalf("expected json.prompt=hi got=%v", jsonVal["prompt"])
	}
}

func TestHandleAuthConfigDefaults(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	rr := httptest.NewRecorder()
	s.handleAuthConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp AuthConfig
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.PasswordEnabled || resp.SAMLEnabled {
		t.Fatalf("expected password/saml disabled, got password=%v saml=%v", resp.PasswordEnabled, resp.SAMLEnabled)
	}
	if resp.DefaultTenant != "default" {
		t.Fatalf("expected default_tenant=default got=%q", resp.DefaultTenant)
	}
	if resp.SessionTTL == "" {
		t.Fatalf("expected session_ttl to be set")
	}
}

func TestApiKeyUnaryInterceptor(t *testing.T) {
	t.Setenv("CORDUM_API_KEYS", "secret")
	provider, err := newBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("auth init: %v", err)
	}
	interceptor := apiKeyUnaryInterceptor(provider)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "secret"))
	_, err = interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "bad"))
	if _, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	}); err == nil {
		t.Fatalf("expected auth error")
	}
}

func TestHandleGetMemoryReturnsNotFoundForMissingKey(t *testing.T) {
	s := &server{memStore: &stubMemStore{}}

	req := httptest.NewRequest("GET", "/api/v1/memory?ptr="+url.QueryEscape("redis://res:missing"), nil)
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleGetMemory(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got=%d body=%s", rr.Code, rr.Body.String())
	}
}

type stubUserStore struct {
	closed bool
}

func (s *stubUserStore) GetByUsername(_ context.Context, _, _ string) (*User, error) {
	if s.closed {
		return nil, ErrUserNotFound
	}
	return &User{ID: "u1", Username: "admin"}, nil
}

func (s *stubUserStore) GetByEmail(_ context.Context, _, _ string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *stubUserStore) GetByID(_ context.Context, _ string) (*User, error) {
	return nil, ErrUserNotFound
}

func (s *stubUserStore) Create(_ context.Context, _ *User, _ string) error { return nil }

func (s *stubUserStore) List(_ context.Context, _ string) ([]*User, error) { return nil, nil }

func (s *stubUserStore) Update(_ context.Context, _ *User) error { return nil }

func (s *stubUserStore) Delete(_ context.Context, _ string) error { return nil }

func (s *stubUserStore) UpdatePassword(_ context.Context, _, _ string) error { return nil }

func (s *stubUserStore) ValidatePassword(_ context.Context, _ *User, _ string) bool { return false }

func (s *stubUserStore) Close() error {
	s.closed = true
	return nil
}

func TestServerCloseClosesUserStore(t *testing.T) {
	us := &stubUserStore{}
	s := &server{userStore: us}

	// Store should be functional before Close.
	if _, err := us.GetByUsername(context.Background(), "admin", "default"); err != nil {
		t.Fatalf("expected user store to be open, got err=%v", err)
	}

	s.Close()

	if !us.closed {
		t.Fatal("expected userStore.Close() to have been called")
	}
}

func TestSeedDefaultAdminUserRejectsEmptyPassword(t *testing.T) {
	t.Setenv("CORDUM_ADMIN_USERNAME", "admin")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "")
	us := &stubUserStore{}
	err := seedDefaultAdminUser(context.Background(), us, "default")
	if err == nil {
		t.Fatal("expected error for empty admin password")
	}
	if !strings.Contains(err.Error(), "CORDUM_ADMIN_PASSWORD") {
		t.Fatalf("error should mention CORDUM_ADMIN_PASSWORD, got: %v", err)
	}
}

func TestServerCloseNilUserStoreNoPanic(t *testing.T) {
	s := &server{}
	// Should not panic when userStore is nil.
	s.Close()
}

// apiLimiterMu guards mutation of the global rate limiters in tests.
var apiLimiterMu sync.Mutex

func TestAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv("CORDUM_ALLOWED_ORIGINS", "")
	t.Setenv("CORDUM_CORS_ALLOW_ORIGINS", "")
	t.Setenv("CORS_ALLOW_ORIGINS", "")
	allowed, allowAll := allowedOriginsFromEnv()
	if allowAll || allowed != nil {
		t.Fatalf("expected no allowed origins")
	}

	t.Setenv("CORDUM_ALLOWED_ORIGINS", "*")
	allowed, allowAll = allowedOriginsFromEnv()
	if !allowAll || allowed != nil {
		t.Fatalf("expected allow all origins")
	}

	t.Setenv("CORDUM_ALLOWED_ORIGINS", "https://example.com, http://localhost:3000")
	allowed, allowAll = allowedOriginsFromEnv()
	if allowAll {
		t.Fatalf("unexpected allow all")
	}
	if _, ok := allowed["https://example.com"]; !ok {
		t.Fatalf("missing example.com origin")
	}
	if _, ok := allowed["http://localhost:3000"]; !ok {
		t.Fatalf("missing localhost origin")
	}
}

func TestRequestHostname(t *testing.T) {
	if requestHostname("") != "" {
		t.Fatalf("expected empty hostname")
	}
	if requestHostname("example.com:8080") != "example.com" {
		t.Fatalf("expected host without port")
	}
	if requestHostname("example.com") != "example.com" {
		t.Fatalf("expected host unchanged")
	}
}

func TestAddrFromEnv(t *testing.T) {
	t.Setenv("TEST_ADDR", "")
	if got := addrFromEnv("TEST_ADDR", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback addr")
	}
	t.Setenv("TEST_ADDR", "127.0.0.1:9999")
	if got := addrFromEnv("TEST_ADDR", "fallback"); got != "127.0.0.1:9999" {
		t.Fatalf("expected env addr")
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	if got := clientIP(req); got != "192.0.2.10" {
		t.Fatalf("expected host without port, got %q", got)
	}
	req.RemoteAddr = "10.0.0.2"
	if got := clientIP(req); got != "10.0.0.2" {
		t.Fatalf("expected raw addr, got %q", got)
	}
	if clientIP(nil) != "" {
		t.Fatalf("expected empty for nil request")
	}
}

type flushWriter struct {
	http.ResponseWriter
	flushed bool
}

func (f *flushWriter) Flush() {
	f.flushed = true
}

func TestStatusRecorderWriteHeaderAndFlush(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr}
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusTeapot {
		t.Fatalf("expected recorded status")
	}

	flusher := &flushWriter{ResponseWriter: rr}
	rec = &statusRecorder{ResponseWriter: flusher}
	rec.Flush()
	if !flusher.flushed {
		t.Fatalf("expected flush to be forwarded")
	}
}

func TestCorsMiddleware(t *testing.T) {
	t.Setenv("CORDUM_ALLOWED_ORIGINS", "http://allowed.com")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Origin", "http://allowed.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
		t.Fatalf("expected cors allow origin header")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Origin", "http://blocked.com")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d", rr.Code)
	}
}

func TestCorsMiddlewareAllowsPUT(t *testing.T) {
	t.Setenv("CORDUM_ALLOWED_ORIGINS", "http://allowed.com")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Preflight for PUT should succeed and include PUT in allowed methods
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/policy/bundles/test-id", nil)
	req.Header.Set("Origin", "http://allowed.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS preflight, got %d", rr.Code)
	}
	methods := rr.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "PUT") {
		t.Fatalf("expected PUT in Access-Control-Allow-Methods, got %q", methods)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	apiLimiterMu.Lock()
	origAPI := apiLimiter
	origPublic := publicLimiter
	defer func() {
		apiLimiter = origAPI
		publicLimiter = origPublic
		apiLimiterMu.Unlock()
	}()

	apiLimiter = newKeyedRateLimiter(1, 1)
	publicLimiter = newKeyedRateLimiter(1, 1)
	auth := newBasicAuthForTest(t, nil)
	handler := apiKeyMiddleware(auth, rateLimitMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-API-Key", "test-api-key")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit response, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected health response, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected health rate limit response, got %d", rr.Code)
	}
}

func TestRateLimitAuthOrdering(t *testing.T) {
	apiLimiterMu.Lock()
	origAPI := apiLimiter
	origPublic := publicLimiter
	defer func() {
		apiLimiter = origAPI
		publicLimiter = origPublic
		apiLimiterMu.Unlock()
	}()

	apiLimiter = newKeyedRateLimiter(1, 1)
	publicLimiter = newKeyedRateLimiter(1, 1)
	auth := newBasicAuthForTest(t, nil)
	handler := apiKeyMiddleware(auth, rateLimitMiddleware(auth, tenantMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))))

	unauth := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	unauth.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, unauth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", rr.Code)
	}

	authReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	authReq.RemoteAddr = "10.0.0.1:12345"
	authReq.Header.Set("X-API-Key", "test-api-key")
	authReq.Header.Set("X-Tenant-ID", "default")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response after unauthorized request, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authReq)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit response, got %d", rr.Code)
	}
}

func TestRateLimitKeyTenantBased(t *testing.T) {
	// Verify that authenticated requests use tenant-based keys.
	req1 := requestWithAuthContext(&AuthContext{Tenant: "tenant-a"})
	req1.RemoteAddr = "10.0.0.1:12345"

	req2 := requestWithAuthContext(&AuthContext{Tenant: "tenant-b"})
	req2.RemoteAddr = "10.0.0.1:12345"

	key1 := rateLimitKey(req1)
	key2 := rateLimitKey(req2)

	if key1 != "tenant:tenant-a" {
		t.Errorf("key1 = %q, want tenant:tenant-a", key1)
	}
	if key2 != "tenant:tenant-b" {
		t.Errorf("key2 = %q, want tenant:tenant-b", key2)
	}
	if key1 == key2 {
		t.Errorf("different tenants produced same key: %q", key1)
	}

	// Fallback to IP when no auth context is present.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req3.RemoteAddr = "10.0.0.1:12345"
	key3 := rateLimitKey(req3)
	if key3 != "ip:10.0.0.1" {
		t.Errorf("key3 = %q, want ip:10.0.0.1", key3)
	}
}

func TestRateLimitTenantSharedAcrossIPs(t *testing.T) {
	// With rate=1, burst=1: first request succeeds, second from a
	// different IP but the same tenant should still be rate-limited.
	apiLimiterMu.Lock()
	origAPI := apiLimiter
	origPublic := publicLimiter
	defer func() {
		apiLimiter = origAPI
		publicLimiter = origPublic
		apiLimiterMu.Unlock()
	}()

	apiLimiter = newKeyedRateLimiter(1, 1)
	publicLimiter = newKeyedRateLimiter(1, 1)
	auth := newBasicAuthForTest(t, nil)
	handler := apiKeyMiddleware(auth, rateLimitMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// First request for the tenant — should succeed.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	req.Header.Set("X-API-Key", "test-api-key")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rr.Code)
	}

	// Second request from different IP but same tenant — must still be limited.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req2.RemoteAddr = "10.0.0.2:9999"
	req2.Header.Set("X-API-Key", "test-api-key")
	req2.Header.Set("X-Tenant-ID", "default")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request with same tenant: expected 429, got %d", rr2.Code)
	}
}

func TestGatewaySafetyTransportCredentials(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	t.Setenv("SAFETY_KERNEL_INSECURE", "true")
	creds, err := safetyTransportCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("expected insecure credentials")
	}
}

func TestAuthConfigReflectsUserStore(t *testing.T) {
	t.Setenv("CORDUM_API_KEY", "test-key-12345678901234567890123456789012")
	t.Setenv("CORDUM_ALLOW_INSECURE_NO_AUTH", "")
	basic, err := newBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("newBasicAuthProvider: %v", err)
	}

	cfg1 := basic.AuthConfig()
	if cfg1.UserAuthEnabled {
		t.Fatal("expected UserAuthEnabled=false before SetUserStore")
	}

	basic.SetUserStore(&stubUserStore{})

	cfg2 := basic.AuthConfig()
	if !cfg2.UserAuthEnabled {
		t.Fatal("expected UserAuthEnabled=true after SetUserStore")
	}

	// Verify through interface chain (mirrors handleAuthConfig path)
	var provider AuthProvider = basic
	configProvider, ok := provider.(AuthConfigProvider)
	if !ok {
		t.Fatal("BasicAuthProvider does not implement AuthConfigProvider")
	}
	if !configProvider.AuthConfig().UserAuthEnabled {
		t.Fatal("expected UserAuthEnabled=true through interface chain")
	}
}

func TestHandleListJobDecisions(t *testing.T) {
	s, _, _ := newTestGateway(t)
	jobID := "job-decisions-1"
	record := scheduler.SafetyDecisionRecord{
		Decision:    scheduler.SafetyAllow,
		Reason:      "ok",
		Constraints: &pb.PolicyConstraints{RedactionLevel: "low"},
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, record); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/decisions", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", jobID)
	rr := httptest.NewRecorder()
	s.handleListJobDecisions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}
	var out []scheduler.SafetyDecisionRecord
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0].Decision != scheduler.SafetyAllow {
		t.Fatalf("unexpected decisions: %#v", out)
	}
}
