package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

type mockOIDCFlowServer struct {
	*httptest.Server
	mu            sync.Mutex
	key           *rsa.PrivateKey
	kid           string
	issuer        string
	tokenClaims   map[string]any
	userInfo      map[string]any
	tokenError    string
	lastTokenForm url.Values
}

func newMockOIDCFlowServer(t *testing.T) *mockOIDCFlowServer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	m := &mockOIDCFlowServer{key: key, kid: "flow-kid-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 m.issuer,
			"jwks_uri":               m.issuer + "/keys",
			"authorization_endpoint": m.issuer + "/authorize",
			"token_endpoint":         m.issuer + "/token",
			"userinfo_endpoint":      m.issuer + "/userinfo",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(m.buildJWKS())
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.lastTokenForm = r.PostForm
		tokenError := m.tokenError
		claims := cloneClaims(m.tokenClaims)
		m.mu.Unlock()
		if tokenError != "" {
			http.Error(w, tokenError, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-1",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     m.signJWT(claims),
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		userInfo := cloneClaims(m.userInfo)
		m.mu.Unlock()
		if userInfo == nil {
			userInfo = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(userInfo)
	})

	m.Server = httptest.NewServer(mux)
	m.issuer = m.URL
	return m
}

func (m *mockOIDCFlowServer) buildJWKS() []byte {
	n := base64.RawURLEncoding.EncodeToString(m.key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes())
	return []byte(fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`,
		m.kid, n, e))
}

func (m *mockOIDCFlowServer) signJWT(claims map[string]any) string {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": m.kid}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64
	digest := sha256.Sum256([]byte(signingInput))
	signature, _ := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, digest[:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func cloneClaims(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func newTestOIDCFlowAdapter(t *testing.T, resolver *licensing.EntitlementResolver, cfgMutate func(*OIDCConfig), claimMutate func(map[string]any)) (*OIDCFlowAdapter, *RedisUserStore, *mockOIDCFlowServer, func()) {
	t.Helper()

	store, redisSrv := newTestUserStore(t)
	server := newMockOIDCFlowServer(t)
	claims := map[string]any{
		"iss":                server.issuer,
		"aud":                "cordum-dashboard",
		"sub":                "operator@example.com",
		"email":              "operator@example.com",
		"name":               "Cordum Operator",
		"preferred_username": "operator@example.com",
		"exp":                float64(time.Now().Add(time.Hour).Unix()),
		"iat":                float64(time.Now().Unix()),
		"nbf":                float64(time.Now().Add(-time.Minute).Unix()),
		"org_id":             "acme",
		"cordum_role":        "viewer",
		"nonce":              "nonce-1",
	}
	if claimMutate != nil {
		claimMutate(claims)
	}
	server.tokenClaims = cloneClaims(claims)

	cfg := OIDCConfig{
		Enabled:       true,
		IssuerURL:     server.issuer,
		Audience:      "api-audience",
		ClaimTenant:   "org_id",
		ClaimRole:     "cordum_role",
		ClientID:      "cordum-dashboard",
		ClientSecret:  "super-secret-value",
		RedirectURI:   "http://localhost:8081" + OIDCCallbackPath,
		Scopes:        []string{"openid", "profile", "email"},
		DefaultRole:   "viewer",
		AutoProvision: true,
		SyncRoles:     true,
	}
	if cfgMutate != nil {
		cfgMutate(&cfg)
	}

	provider, err := NewOIDCProvider(cfg)
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	adapter, err := NewOIDCFlowAdapter(provider, store, "default", resolver)
	if err != nil {
		provider.Close()
		t.Fatalf("NewOIDCFlowAdapter: %v", err)
	}
	adapter.now = func() time.Time {
		return time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	}
	adapter.newState = func() string { return "state-1" }
	adapter.newNonce = func() string { return "nonce-1" }
	adapter.redirectURL = "http://localhost:8081/login"
	cleanup := func() {
		provider.Close()
		_ = store.Close()
		redisSrv.Close()
		server.Close()
	}
	return adapter, store, server, cleanup
}

func TestOIDCFlowHandlers_EntitlementDisabled(t *testing.T) {
	adapter, _, _, cleanup := newTestOIDCFlowAdapter(t, nil, nil, nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, OIDCLoginPath, nil)
	rr := httptest.NewRecorder()
	adapter.handleLogin(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") {
		t.Fatalf("expected tier_limit_exceeded response, got %s", rr.Body.String())
	}
}

func TestOIDCFlowLoginRedirectsWithExpectedParams(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, _, _, cleanup := newTestOIDCFlowAdapter(t, resolver, nil, nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, OIDCLoginPath+"?redirect="+url.QueryEscape("http://localhost:8081/login?returnUrl=%2Fjobs"), nil)
	rr := httptest.NewRecorder()
	adapter.handleLogin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	location, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	values := location.Query()
	if got := values.Get("client_id"); got != "cordum-dashboard" {
		t.Fatalf("client_id = %q", got)
	}
	if got := values.Get("redirect_uri"); got != "http://localhost:8081/api/v1/auth/sso/oidc/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := values.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q", got)
	}
	if got := values.Get("scope"); got != "openid profile email" {
		t.Fatalf("scope = %q", got)
	}
	if got := values.Get("state"); got != "state-1" {
		t.Fatalf("state = %q", got)
	}
	if got := values.Get("nonce"); got != "nonce-1" {
		t.Fatalf("nonce = %q", got)
	}

	entry, err := adapter.stateStore.Get(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("stateStore.Get: %v", err)
	}
	if entry.Redirect != "http://localhost:8081/login?returnUrl=%2Fjobs" {
		t.Fatalf("redirect = %q", entry.Redirect)
	}
}

func TestOIDCFlowAuthConfigIncludesGroupRoleMapping(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter := &OIDCFlowAdapter{
		enabled:  true,
		loginURL: "http://localhost:8081/api/v1/auth/sso/oidc/login",
		resolver: resolver,
		provider: &OIDCProvider{cfg: OIDCConfig{
			Enabled:      true,
			IssuerURL:    "https://idp.example.com",
			ClientID:     "cordum-dashboard",
			ClientSecret: "super-secret-value",
			GroupsClaim:  "okta_groups",
			GroupRoleMapping: map[string]string{
				"cordum-admins":    "admin",
				"cordum-operators": "operator",
			},
		}},
	}

	cfg := adapter.AuthConfig()
	if !cfg.OIDCEnabled {
		t.Fatal("OIDCEnabled = false, want true")
	}
	if cfg.OIDCGroupsClaim != "okta_groups" {
		t.Fatalf("OIDCGroupsClaim = %q, want okta_groups", cfg.OIDCGroupsClaim)
	}
	want := map[string]string{
		"cordum-admins":    "admin",
		"cordum-operators": "operator",
	}
	if len(cfg.OIDCGroupRoleMapping) != len(want) {
		t.Fatalf("OIDCGroupRoleMapping len = %d, want %d: %#v", len(cfg.OIDCGroupRoleMapping), len(want), cfg.OIDCGroupRoleMapping)
	}
	for group, role := range want {
		if got := cfg.OIDCGroupRoleMapping[group]; got != role {
			t.Fatalf("OIDCGroupRoleMapping[%q] = %q, want %q", group, got, role)
		}
	}
	if cfg.OIDCClientSecretMasked == "super-secret-value" {
		t.Fatal("AuthConfig leaked raw OIDC client secret")
	}
}

func TestOIDCFlowCallbackRejectsInvalidState(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, _, _, cleanup := newTestOIDCFlowAdapter(t, resolver, nil, nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, OIDCCallbackPath+"?state=missing&code=test-code", nil)
	rr := httptest.NewRecorder()
	adapter.handleCallback(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid OIDC state") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestOIDCFlowCallbackCreatesSessionFromMappedClaims(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, store, server, cleanup := newTestOIDCFlowAdapter(t, resolver, nil, nil)
	defer cleanup()

	if err := adapter.stateStore.Put(context.Background(), "state-1", "nonce-1", "http://localhost:8081/login?returnUrl=%2Fjobs", time.Minute); err != nil {
		t.Fatalf("stateStore.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, OIDCCallbackPath+"?state=state-1&code=good-code", nil)
	rr := httptest.NewRecorder()
	adapter.handleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), SessionCookieName+"=session-") {
		t.Fatalf("expected session cookie, got %q", rr.Header().Get("Set-Cookie"))
	}
	location, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if !strings.HasPrefix(location.String(), "http://localhost:8081/login?returnUrl=") {
		t.Fatalf("unexpected redirect %q", location.String())
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil {
		t.Fatalf("ParseQuery fragment: %v", err)
	}
	sessionToken := fragment.Get("token")
	if !strings.HasPrefix(sessionToken, "session-") {
		t.Fatalf("token = %q", sessionToken)
	}
	authCtx, err := store.ValidateSession(context.Background(), sessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if authCtx.Tenant != "acme" {
		t.Fatalf("tenant = %q", authCtx.Tenant)
	}
	if authCtx.Role != "viewer" {
		t.Fatalf("role = %q", authCtx.Role)
	}
	user, err := store.GetByUsername(context.Background(), "operator@example.com", "acme")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user.Email != "operator@example.com" {
		t.Fatalf("email = %q", user.Email)
	}
	if got := fragment.Get("display_name"); got != "Cordum Operator" {
		t.Fatalf("display_name = %q", got)
	}
	server.mu.Lock()
	code := server.lastTokenForm.Get("code")
	redirectURI := server.lastTokenForm.Get("redirect_uri")
	server.mu.Unlock()
	if code != "good-code" {
		t.Fatalf("code exchange used %q", code)
	}
	if redirectURI != "http://localhost:8081/api/v1/auth/sso/oidc/callback" {
		t.Fatalf("redirect_uri exchange = %q", redirectURI)
	}
}

func TestOIDCFlowCallbackCreatesAdminFromGroupsClaim(t *testing.T) {
	cfgMutate := func(cfg *OIDCConfig) {
		cfg.GroupsClaim = "groups"
		cfg.GroupRoleMapping = map[string]string{
			"cordum-admins": "admin",
		}
	}
	claimMutate := func(claims map[string]any) {
		delete(claims, "cordum_role")
		claims["groups"] = []any{"cordum-admins"}
	}

	authCtx, user, fragment := exerciseOIDCFlowCallback(t, cfgMutate, claimMutate)
	if authCtx.Role != "admin" {
		t.Fatalf("session role = %q, want admin", authCtx.Role)
	}
	if user.Role != "admin" {
		t.Fatalf("user role = %q, want admin", user.Role)
	}
	if got := fragment.Get("role"); got != "admin" {
		t.Fatalf("redirect role fragment = %q, want admin", got)
	}
}

func TestOIDCFlowCallbackGroupsWinOverConflictingRoleClaim(t *testing.T) {
	cfgMutate := func(cfg *OIDCConfig) {
		cfg.GroupsClaim = "groups"
		cfg.GroupRoleMapping = map[string]string{
			"cordum-viewers": "viewer",
		}
	}
	claimMutate := func(claims map[string]any) {
		claims["cordum_role"] = "admin"
		claims["groups"] = []any{"cordum-viewers"}
	}

	authCtx, user, fragment := exerciseOIDCFlowCallback(t, cfgMutate, claimMutate)
	if authCtx.Role != "viewer" {
		t.Fatalf("session role = %q, want viewer", authCtx.Role)
	}
	if user.Role != "viewer" {
		t.Fatalf("user role = %q, want viewer", user.Role)
	}
	if got := fragment.Get("role"); got != "viewer" {
		t.Fatalf("redirect role fragment = %q, want viewer", got)
	}
}

func TestOIDCFlowCallbackPreservesOperatorGroupRole(t *testing.T) {
	cfgMutate := func(cfg *OIDCConfig) {
		cfg.GroupsClaim = "groups"
		cfg.GroupRoleMapping = map[string]string{
			"cordum-operators": "operator",
		}
	}
	claimMutate := func(claims map[string]any) {
		delete(claims, "cordum_role")
		claims["groups"] = []any{"cordum-operators"}
	}

	authCtx, user, fragment := exerciseOIDCFlowCallback(t, cfgMutate, claimMutate)
	if authCtx.Role != "operator" {
		t.Fatalf("session role = %q, want operator", authCtx.Role)
	}
	if user.Role != "operator" {
		t.Fatalf("user role = %q, want operator", user.Role)
	}
	if got := fragment.Get("role"); got != "operator" {
		t.Fatalf("redirect role fragment = %q, want operator", got)
	}
}

func exerciseOIDCFlowCallback(t *testing.T, cfgMutate func(*OIDCConfig), claimMutate func(map[string]any)) (*AuthContext, *User, url.Values) {
	t.Helper()

	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, store, _, cleanup := newTestOIDCFlowAdapter(t, resolver, cfgMutate, claimMutate)
	defer cleanup()

	if err := adapter.stateStore.Put(context.Background(), "state-1", "nonce-1", "http://localhost:8081/login?returnUrl=%2Fjobs", time.Minute); err != nil {
		t.Fatalf("stateStore.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, OIDCCallbackPath+"?state=state-1&code=good-code", nil)
	rr := httptest.NewRecorder()
	adapter.handleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	location, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil {
		t.Fatalf("ParseQuery fragment: %v", err)
	}
	sessionToken := fragment.Get("token")
	if !strings.HasPrefix(sessionToken, "session-") {
		t.Fatalf("token = %q", sessionToken)
	}
	authCtx, err := store.ValidateSession(context.Background(), sessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	user, err := store.GetByUsername(context.Background(), "operator@example.com", "acme")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	return authCtx, user, fragment
}

func TestOIDCFlowCallbackRedirectsIdPErrors(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, _, _, cleanup := newTestOIDCFlowAdapter(t, resolver, nil, nil)
	defer cleanup()

	if err := adapter.stateStore.Put(context.Background(), "state-1", "nonce-1", "http://localhost:8081/login", time.Minute); err != nil {
		t.Fatalf("stateStore.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, OIDCCallbackPath+"?state=state-1&error=access_denied&error_description="+url.QueryEscape("SSO access denied"), nil)
	rr := httptest.NewRecorder()
	adapter.handleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	location, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil {
		t.Fatalf("ParseQuery fragment: %v", err)
	}
	if fragment.Get("error") != "access_denied" {
		t.Fatalf("error = %q", fragment.Get("error"))
	}
	if fragment.Get("error_description") != "SSO access denied" {
		t.Fatalf("error_description = %q", fragment.Get("error_description"))
	}
}

func TestOIDCFlowCallbackHonorsCustomClaimMapping(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
	})
	adapter, store, _, cleanup := newTestOIDCFlowAdapter(t, resolver, func(cfg *OIDCConfig) {
		cfg.ClaimTenant = "custom_tenant"
		cfg.ClaimRole = "custom_role"
	}, func(claims map[string]any) {
		delete(claims, "org_id")
		delete(claims, "cordum_role")
		claims["custom_tenant"] = "ops"
		claims["custom_role"] = "operator"
		claims["preferred_username"] = "ops.user"
		claims["sub"] = "ops.user"
		claims["email"] = "ops@example.com"
	})
	defer cleanup()

	if err := adapter.stateStore.Put(context.Background(), "state-1", "nonce-1", "http://localhost:8081/login", time.Minute); err != nil {
		t.Fatalf("stateStore.Put: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, OIDCCallbackPath+"?state=state-1&code=good-code", nil)
	rr := httptest.NewRecorder()
	adapter.handleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusFound, rr.Body.String())
	}
	location, err := rr.Result().Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil {
		t.Fatalf("ParseQuery fragment: %v", err)
	}
	authCtx, err := store.ValidateSession(context.Background(), fragment.Get("token"))
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if authCtx.Tenant != "ops" {
		t.Fatalf("tenant = %q", authCtx.Tenant)
	}
	if authCtx.Role != "admin" {
		t.Fatalf("role = %q", authCtx.Role)
	}
}
