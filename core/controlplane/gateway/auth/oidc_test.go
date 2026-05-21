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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
)

// ---------- test helpers ----------

// mockOIDCServer creates an httptest.Server that serves OIDC discovery and JWKS.
type mockOIDCServer struct {
	*httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

func newMockOIDCServer(t *testing.T) *mockOIDCServer {
	t.Helper()
	// Mock OIDC server binds to 127.0.0.1 via httptest. The OIDC provider
	// refuses private/loopback issuer URLs by default in all envs; opt
	// the test scope into the dev/test escape hatch so the fixture works.
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	m := &mockOIDCServer{key: key, kid: "test-kid-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]string{
			"issuer":   m.issuer,
			"jwks_uri": m.issuer + "/keys",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Fatalf("encode discovery: %v", err)
		}
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		jwks := m.buildJWKS()
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(jwks); err != nil {
			t.Fatalf("write jwks: %v", err)
		}
	})

	m.Server = httptest.NewServer(mux)
	m.issuer = m.URL
	return m
}

func (m *mockOIDCServer) buildJWKS() []byte {
	n := base64.RawURLEncoding.EncodeToString(m.key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes())
	jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`,
		m.kid, n, e)
	return []byte(jwks)
}

func (m *mockOIDCServer) signJWT(claims map[string]any) string {
	return m.signJWTWithKid(claims, m.kid)
}

func (m *mockOIDCServer) signJWTWithKid(claims map[string]any, kid string) string {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	h := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, h[:])
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

func (m *mockOIDCServer) validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":         m.issuer,
		"aud":         "cordum-api",
		"sub":         "service-account@corp.com",
		"exp":         float64(now.Add(time.Hour).Unix()),
		"iat":         float64(now.Unix()),
		"nbf":         float64(now.Add(-time.Minute).Unix()),
		"org_id":      "acme-corp",
		"cordum_role": "admin",
	}
}

type closeTrackingTransport struct {
	closed atomic.Bool
}

func (t *closeTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected test RoundTrip")
}

func (t *closeTrackingTransport) CloseIdleConnections() {
	t.closed.Store(true)
}

// ---------- OIDCProvider tests ----------

func TestOIDCProviderCloseClosesIdleHTTPConnections(t *testing.T) {
	transport := &closeTrackingTransport{}
	provider := &OIDCProvider{
		httpClient: &http.Client{Transport: transport},
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}
	go func() {
		<-provider.stopCh
		close(provider.done)
	}()

	provider.Close()

	if !transport.closed.Load() {
		t.Fatal("Close did not close idle OIDC HTTP connections")
	}
}

func TestOIDC_ValidJWT(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	token := m.signJWT(m.validClaims())
	authCtx, err := provider.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if authCtx.PrincipalID != "service-account@corp.com" {
		t.Errorf("PrincipalID = %q, want service-account@corp.com", authCtx.PrincipalID)
	}
	if authCtx.Tenant != "acme-corp" {
		t.Errorf("Tenant = %q, want acme-corp", authCtx.Tenant)
	}
	if authCtx.Role != "admin" {
		t.Errorf("Role = %q, want admin", authCtx.Role)
	}
	if authCtx.AuthSource != "oidc" {
		t.Errorf("AuthSource = %q, want oidc", authCtx.AuthSource)
	}
}

func TestOIDC_ExpiredJWT(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	claims["exp"] = float64(time.Now().Add(-time.Hour).Unix())
	token := m.signJWT(claims)

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %q, want 'expired'", err.Error())
	}
}

func TestOIDC_WrongAudience(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	claims["aud"] = "wrong-audience"
	token := m.signJWT(claims)

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error = %q, want 'audience'", err.Error())
	}
}

func TestOIDC_WrongIssuer(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	claims["iss"] = "https://evil.example.com"
	token := m.signJWT(claims)

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Errorf("error = %q, want 'issuer'", err.Error())
	}
}

func TestOIDC_InvalidSignature(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Sign with a different key
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherServer := &mockOIDCServer{key: otherKey, kid: m.kid, issuer: m.issuer}
	token := otherServer.signJWT(m.validClaims())

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
	if !strings.Contains(err.Error(), "signature invalid") {
		t.Errorf("error = %q, want 'signature invalid'", err.Error())
	}
}

func TestOIDC_UnknownKidTriggersRefresh(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Force lastRefresh to be old so on-demand refresh is allowed
	provider.mu.Lock()
	provider.lastRefresh = time.Now().Add(-2 * time.Minute)
	provider.mu.Unlock()

	// Update the server's kid — simulates key rotation
	m.kid = "rotated-kid-2"
	token := m.signJWTWithKid(m.validClaims(), "rotated-kid-2")

	// Should trigger on-demand refresh and find the new key
	authCtx, err := provider.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT after key rotation: %v", err)
	}
	if authCtx.PrincipalID != "service-account@corp.com" {
		t.Errorf("PrincipalID = %q after rotation", authCtx.PrincipalID)
	}
}

func TestOIDC_CustomClaimMapping(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL:   m.issuer,
		Audience:    "cordum-api",
		ClaimTenant: "custom_tenant",
		ClaimRole:   "custom_role",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	claims["custom_tenant"] = "my-tenant"
	claims["custom_role"] = "operator"
	token := m.signJWT(claims)

	authCtx, err := provider.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if authCtx.Tenant != "my-tenant" {
		t.Errorf("Tenant = %q, want my-tenant", authCtx.Tenant)
	}
	// operator maps to admin via normalizeRole
	if authCtx.Role != "admin" {
		t.Errorf("Role = %q, want admin (operator normalizes to admin)", authCtx.Role)
	}
}

func TestOIDC_GroupRoleMapping(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL:   m.issuer,
		Audience:    "cordum-api",
		DefaultRole: "viewer",
		GroupsClaim: "groups",
		GroupRoleMapping: map[string]string{
			"cordum-admins":    "admin",
			"cordum-operators": "operator",
			"cordum-viewers":   "viewer",
		},
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	tests := []struct {
		name       string
		mutate     func(map[string]any)
		wantRole   string
		wantTenant string
	}{
		{
			name: "groups only resolves admin",
			mutate: func(claims map[string]any) {
				delete(claims, "cordum_role")
				claims["groups"] = []any{"cordum-admins"}
			},
			wantRole:   "admin",
			wantTenant: "acme-corp",
		},
		{
			name: "claim only preserves legacy role path when groups absent",
			mutate: func(claims map[string]any) {
				claims["cordum_role"] = "viewer"
			},
			wantRole:   "viewer",
			wantTenant: "acme-corp",
		},
		{
			name: "groups win over conflicting role claim",
			mutate: func(claims map[string]any) {
				claims["groups"] = []any{"cordum-viewers"}
				claims["cordum_role"] = "admin"
			},
			wantRole:   "viewer",
			wantTenant: "acme-corp",
		},
		{
			name: "unknown non empty groups use default role without claim fallback",
			mutate: func(claims map[string]any) {
				claims["groups"] = []any{"unknown-admins"}
				claims["cordum_role"] = "admin"
			},
			wantRole:   "viewer",
			wantTenant: "acme-corp",
		},
		{
			name: "empty groups fall through to claim role",
			mutate: func(claims map[string]any) {
				claims["groups"] = []any{}
				claims["cordum_role"] = "admin"
			},
			wantRole:   "admin",
			wantTenant: "acme-corp",
		},
		{
			name: "group names match case insensitively",
			mutate: func(claims map[string]any) {
				delete(claims, "cordum_role")
				claims["groups"] = []any{"Cordum-Admins"}
			},
			wantRole:   "admin",
			wantTenant: "acme-corp",
		},
		{
			name: "multiple groups pick highest privilege",
			mutate: func(claims map[string]any) {
				delete(claims, "cordum_role")
				claims["groups"] = []any{"cordum-viewers", "cordum-operators", "cordum-admins"}
			},
			wantRole:   "admin",
			wantTenant: "acme-corp",
		},
		{
			name: "operator group mapping preserves operator role",
			mutate: func(claims map[string]any) {
				delete(claims, "cordum_role")
				claims["groups"] = []any{"cordum-operators"}
			},
			wantRole:   "operator",
			wantTenant: "acme-corp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := m.validClaims()
			tt.mutate(claims)
			token := m.signJWT(claims)

			authCtx, err := provider.ValidateJWT(token)
			if err != nil {
				t.Fatalf("ValidateJWT: %v", err)
			}
			if authCtx.Role != tt.wantRole {
				t.Fatalf("Role = %q, want %q", authCtx.Role, tt.wantRole)
			}
			if authCtx.Tenant != tt.wantTenant {
				t.Fatalf("Tenant = %q, want %q", authCtx.Tenant, tt.wantTenant)
			}
		})
	}
}

func TestOIDC_GroupRoleMappingEnv(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	t.Setenv("CORDUM_OIDC_ENABLED", "true")
	t.Setenv("CORDUM_OIDC_ISSUER", m.issuer)
	t.Setenv("CORDUM_OIDC_AUDIENCE", "cordum-api")
	t.Setenv("CORDUM_OIDC_GROUPS_CLAIM", "okta_groups")
	t.Setenv("CORDUM_OIDC_GROUP_ROLE_MAPPING", `{"Cordum-Admins":"admin","cordum-operators":"operator","cordum-viewers":"viewer"}`)

	provider, err := NewOIDCProviderFromEnv()
	if err != nil {
		t.Fatalf("NewOIDCProviderFromEnv: %v", err)
	}
	if provider == nil {
		t.Fatal("NewOIDCProviderFromEnv returned nil provider")
	}
	defer provider.Close()

	cfg := provider.Config()
	if cfg.GroupsClaim != "okta_groups" {
		t.Fatalf("GroupsClaim = %q, want okta_groups", cfg.GroupsClaim)
	}
	want := map[string]string{
		"cordum-admins":    "admin",
		"cordum-operators": "operator",
		"cordum-viewers":   "viewer",
	}
	if len(cfg.GroupRoleMapping) != len(want) {
		t.Fatalf("GroupRoleMapping len = %d, want %d: %#v", len(cfg.GroupRoleMapping), len(want), cfg.GroupRoleMapping)
	}
	for group, role := range want {
		if got := cfg.GroupRoleMapping[group]; got != role {
			t.Fatalf("GroupRoleMapping[%q] = %q, want %q", group, got, role)
		}
	}
}

func TestOIDC_GroupRoleMappingEnvValidation(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name:    "invalid json",
			raw:     `{"cordum-admins":`,
			wantErr: "CORDUM_OIDC_GROUP_ROLE_MAPPING",
		},
		{
			name:    "invalid role",
			raw:     `{"cordum-admins":"superadmin"}`,
			wantErr: "invalid role",
		},
		{
			name:    "duplicate case insensitive group",
			raw:     `{"Cordum-Admins":"admin"," cordum-admins ":"viewer"}`,
			wantErr: "duplicate group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CORDUM_OIDC_ENABLED", "true")
			t.Setenv("CORDUM_OIDC_ISSUER", m.issuer)
			t.Setenv("CORDUM_OIDC_AUDIENCE", "cordum-api")
			t.Setenv("CORDUM_OIDC_GROUP_ROLE_MAPPING", tt.raw)

			provider, err := NewOIDCProviderFromEnv()
			if provider != nil {
				provider.Close()
			}
			if err == nil {
				t.Fatalf("NewOIDCProviderFromEnv error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewOIDCProviderFromEnv error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestOIDC_NoAudienceConfig(t *testing.T) {
	// Skip in production mode (audience is required)
	t.Setenv("CORDUM_PRODUCTION", "false")
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "", // no audience restriction
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	claims["aud"] = "anything"
	token := m.signJWT(claims)

	_, err = provider.ValidateJWT(token)
	if err != nil {
		t.Fatalf("ValidateJWT with no audience config: %v", err)
	}
}

func TestOIDCValidateClaims_MissingExp(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	claims := m.validClaims()
	delete(claims, "exp") // remove exp claim
	token := m.signJWT(claims)

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for missing exp claim")
	}
	if !strings.Contains(err.Error(), "missing exp") {
		t.Fatalf("expected 'missing exp' in error, got: %v", err)
	}
}

func TestOIDCValidateClaims_MissingAudienceProd(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "", // no audience — should fail in production
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	token := m.signJWT(m.validClaims())
	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error in production mode without audience configured")
	}
	if !strings.Contains(err.Error(), "audience validation required") {
		t.Fatalf("expected 'audience validation required' in error, got: %v", err)
	}
}

func TestOIDC_InvalidTokenFormat(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	_, err = provider.ValidateJWT("not-a-jwt")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestOIDC_NoneAlgRejected(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Manually craft a token with alg=none
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"evil"}`))
	token := header + "." + payload + "."

	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for alg=none")
	}
}

func TestOIDC_AllowedSigningAlgs(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL:          m.issuer,
		Audience:           "cordum-api",
		AllowedSigningAlgs: []string{"RS512"},
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	token := m.signJWT(m.validClaims())
	_, err = provider.ValidateJWT(token)
	if err == nil {
		t.Fatal("expected error for disallowed alg")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %q, want 'not allowed'", err.Error())
	}
}

// ---------- CompositeAuthProvider tests ----------

func TestComposite_FallsThroughToOIDC(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	oidcProvider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer oidcProvider.Close()

	// Create a BasicAuthProvider that requires API key
	testKeys := map[string]apiKeyMeta{"test-key": {Role: "admin"}}
	basic := &BasicAuthProvider{
		defaultTenant: "default",
		keys:          testKeys,
		keyHashes:     buildKeyHashes(testKeys),
		requireAPIKey: true,
	}

	oidcAdapter := NewOIDCAuthAdapter(oidcProvider, "default")
	composite, err := NewCompositeAuthProvider(basic, oidcAdapter)
	if err != nil {
		t.Fatalf("NewCompositeAuthProvider: %v", err)
	}

	// Test 1: API key auth succeeds via basic
	req1 := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	req1.Header.Set("X-API-Key", "test-key")
	authCtx, err := composite.AuthenticateHTTP(req1)
	if err != nil {
		t.Fatalf("API key auth: %v", err)
	}
	if authCtx.AuthSource != "api_key" {
		t.Errorf("API key AuthSource = %q, want api_key", authCtx.AuthSource)
	}

	// Test 2: OIDC Bearer token succeeds via adapter
	token := m.signJWT(m.validClaims())
	req2 := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	authCtx, err = composite.AuthenticateHTTP(req2)
	if err != nil {
		t.Fatalf("OIDC auth: %v", err)
	}
	if authCtx.AuthSource != "oidc" {
		t.Errorf("OIDC AuthSource = %q, want oidc", authCtx.AuthSource)
	}
	if authCtx.PrincipalID != "service-account@corp.com" {
		t.Errorf("OIDC PrincipalID = %q", authCtx.PrincipalID)
	}
}

func TestComposite_BothFail(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	oidcProvider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer oidcProvider.Close()

	realKeys := map[string]apiKeyMeta{"real-key": {Role: "admin"}}
	basic := &BasicAuthProvider{
		defaultTenant: "default",
		keys:          realKeys,
		keyHashes:     buildKeyHashes(realKeys),
		requireAPIKey: true,
	}

	oidcAdapter := NewOIDCAuthAdapter(oidcProvider, "default")
	composite, err := NewCompositeAuthProvider(basic, oidcAdapter)
	if err != nil {
		t.Fatalf("NewCompositeAuthProvider: %v", err)
	}

	// No auth header at all
	req := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	_, err = composite.AuthenticateHTTP(req)
	if err == nil {
		t.Fatal("expected error when no auth provided")
	}
}

func TestComposite_RequiresAtLeastOneProvider(t *testing.T) {
	_, err := NewCompositeAuthProvider()
	if err == nil {
		t.Fatal("expected error with no providers")
	}
}

func TestComposite_AuthConfigMergesOIDCGroupRoleMapping(t *testing.T) {
	oidcProvider := &OIDCProvider{cfg: OIDCConfig{
		Enabled:     true,
		IssuerURL:   "https://idp.example.com",
		GroupsClaim: "okta_groups",
		GroupRoleMapping: map[string]string{
			"cordum-admins":    "admin",
			"cordum-operators": "operator",
		},
	}}

	testKeys := map[string]apiKeyMeta{"test-key": {Role: "admin"}}
	basic := &BasicAuthProvider{
		defaultTenant: "default",
		keys:          testKeys,
		keyHashes:     buildKeyHashes(testKeys),
		requireAPIKey: true,
	}
	oidcAdapter := NewOIDCAuthAdapter(oidcProvider, "default")
	composite, err := NewCompositeAuthProvider(basic, oidcAdapter)
	if err != nil {
		t.Fatalf("NewCompositeAuthProvider: %v", err)
	}

	cfg := composite.AuthConfig()
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
}

// ---------- OIDCAuthAdapter tests ----------

func TestOIDCAdapter_SkipsSessionTokens(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	oidcProvider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer oidcProvider.Close()

	adapter := NewOIDCAuthAdapter(oidcProvider, "default")

	req := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer session-abc123")
	_, err = adapter.AuthenticateHTTP(req)
	if err == nil {
		t.Fatal("expected error — session tokens should be skipped by OIDC adapter")
	}
}

func TestOIDCAdapter_AuthConfigIncludesGroupRoleMappingWithoutSecret(t *testing.T) {
	oidcProvider := &OIDCProvider{cfg: OIDCConfig{
		Enabled:      true,
		IssuerURL:    "https://idp.example.com",
		ClientID:     "cordum-dashboard",
		ClientSecret: "super-secret-value",
		GroupsClaim:  "okta_groups",
		GroupRoleMapping: map[string]string{
			"cordum-admins":  "admin",
			"cordum-viewers": "viewer",
		},
	}}

	cfg := NewOIDCAuthAdapter(oidcProvider, "default").AuthConfig()
	if cfg.OIDCGroupsClaim != "okta_groups" {
		t.Fatalf("OIDCGroupsClaim = %q, want okta_groups", cfg.OIDCGroupsClaim)
	}
	want := map[string]string{
		"cordum-admins":  "admin",
		"cordum-viewers": "viewer",
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

func TestOIDCAdapter_DefaultTenantFallback(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	oidcProvider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer oidcProvider.Close()

	adapter := NewOIDCAuthAdapter(oidcProvider, "fallback-tenant")

	claims := m.validClaims()
	delete(claims, "org_id") // no tenant claim
	token := m.signJWT(claims)

	req := httptest.NewRequest("GET", "/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	authCtx, err := adapter.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if authCtx.Tenant != "fallback-tenant" {
		t.Errorf("Tenant = %q, want fallback-tenant", authCtx.Tenant)
	}
}

func TestOIDCAdapter_AuthenticateGRPC(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	oidcProvider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer oidcProvider.Close()

	adapter := NewOIDCAuthAdapter(oidcProvider, "fallback-tenant")
	token := m.signJWT(m.validClaims())
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + token,
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	authCtx, err := adapter.AuthenticateGRPC(ctx)
	if err != nil {
		t.Fatalf("AuthenticateGRPC: %v", err)
	}
	if authCtx.AuthSource != "oidc" {
		t.Errorf("AuthSource = %q, want oidc", authCtx.AuthSource)
	}
	if authCtx.PrincipalID != "service-account@corp.com" {
		t.Errorf("PrincipalID = %q, want service-account@corp.com", authCtx.PrincipalID)
	}
}

// ---------- JWK parsing tests ----------

func TestParseJWKRSA(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())

	pub, err := parseJWKRSA(n, e)
	if err != nil {
		t.Fatalf("parseJWKRSA: %v", err)
	}
	if pub.N.Cmp(key.N) != 0 {
		t.Error("N mismatch")
	}
	if pub.E != key.E {
		t.Error("E mismatch")
	}
}

func TestParseJWKRSA_InvalidBase64(t *testing.T) {
	_, err := parseJWKRSA("!!!invalid!!!", "AQAB")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

// ---------- JWKS jitter + Redis cache tests ----------

func TestCryptoRandJitter(t *testing.T) {
	// Verify jitter returns values in [0, maxSeconds).
	for i := 0; i < 20; i++ {
		d := cryptoRandJitter(30)
		if d < 0 || d >= 30*time.Second {
			t.Fatalf("jitter out of range: %v", d)
		}
	}

	// Verify zero/negative maxSeconds returns 0.
	if d := cryptoRandJitter(0); d != 0 {
		t.Fatalf("expected 0 for maxSeconds=0, got %v", d)
	}
	if d := cryptoRandJitter(-1); d != 0 {
		t.Fatalf("expected 0 for maxSeconds=-1, got %v", d)
	}

	// Verify some variance exists (not all the same).
	seen := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		seen[cryptoRandJitter(30)] = true
	}
	if len(seen) < 2 {
		t.Fatal("jitter produced no variance over 50 samples")
	}
}

func TestIssuerCacheKey(t *testing.T) {
	p := &OIDCProvider{cfg: OIDCConfig{IssuerURL: "https://auth.example.com"}}
	key := p.issuerCacheKey()
	if !strings.HasPrefix(key, "cordum:auth:jwks:") {
		t.Fatalf("expected cordum:auth:jwks: prefix, got %q", key)
	}
	// sha256[:16] = 32 hex chars
	suffix := strings.TrimPrefix(key, "cordum:auth:jwks:")
	if len(suffix) != 32 {
		t.Fatalf("expected 32 hex chars after prefix, got %d: %q", len(suffix), suffix)
	}

	// Different issuers produce different keys.
	p2 := &OIDCProvider{cfg: OIDCConfig{IssuerURL: "https://other.example.com"}}
	if p.issuerCacheKey() == p2.issuerCacheKey() {
		t.Fatal("expected different cache keys for different issuers")
	}
}

// mockOIDCServerWithCounter wraps mockOIDCServer and counts JWKS requests.
type mockOIDCServerWithCounter struct {
	*httptest.Server
	key      *rsa.PrivateKey
	kid      string
	issuer   string
	jwksHits atomic.Int64
}

func newMockOIDCServerWithCounter(t *testing.T) *mockOIDCServerWithCounter {
	t.Helper()
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	m := &mockOIDCServerWithCounter{key: key, kid: "test-kid-cache"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]string{
			"issuer":   m.issuer,
			"jwks_uri": m.issuer + "/keys",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		m.jwksHits.Add(1)
		n := base64.RawURLEncoding.EncodeToString(m.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes())
		jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`,
			m.kid, n, e)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	})

	m.Server = httptest.NewServer(mux)
	m.issuer = m.URL
	return m
}

func TestJWKSRedisCacheHit(t *testing.T) {
	// Populate Redis cache, then refresh — should NOT hit the IdP.
	srv := newTestMiniredis(t)
	rdb := newTestRedisClient(t, srv.Addr())

	m := newMockOIDCServerWithCounter(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Initial fetch happened during construction — record the count.
	initialHits := m.jwksHits.Load()
	if initialHits < 1 {
		t.Fatalf("expected at least 1 JWKS fetch during init, got %d", initialHits)
	}

	// Attach Redis and pre-populate the cache.
	provider.WithRedis(rdb)
	cacheKey := provider.issuerCacheKey()

	n := base64.RawURLEncoding.EncodeToString(m.key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes())
	cachedJWKS := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`,
		m.kid, n, e)
	if err := rdb.Set(context.Background(), cacheKey, cachedJWKS, time.Hour).Err(); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Trigger refresh — should hit cache, not IdP.
	if err := provider.refreshJWKS(context.Background()); err != nil {
		t.Fatalf("refreshJWKS: %v", err)
	}

	if m.jwksHits.Load() != initialHits {
		t.Fatalf("expected no additional JWKS fetches (cache hit), got %d total", m.jwksHits.Load())
	}

	// Verify the provider actually loaded the keys from cache.
	provider.mu.RLock()
	hasKey := len(provider.rsaKeys) > 0
	provider.mu.RUnlock()
	if !hasKey {
		t.Fatal("expected RSA keys loaded from cache")
	}
}

func TestJWKSRedisCacheMiss(t *testing.T) {
	// Empty Redis cache — should fetch from IdP and write to Redis.
	srv := newTestMiniredis(t)
	rdb := newTestRedisClient(t, srv.Addr())

	m := newMockOIDCServerWithCounter(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	provider.WithRedis(rdb)
	cacheKey := provider.issuerCacheKey()

	// Verify cache is empty.
	if val, err := rdb.Get(context.Background(), cacheKey).Result(); err == nil {
		t.Fatalf("expected empty cache, got: %s", val)
	}

	hitsBefore := m.jwksHits.Load()

	// Trigger refresh — should fetch from IdP and write cache.
	if err := provider.refreshJWKS(context.Background()); err != nil {
		t.Fatalf("refreshJWKS: %v", err)
	}

	if m.jwksHits.Load() <= hitsBefore {
		t.Fatal("expected IdP JWKS fetch on cache miss")
	}

	// Verify cache was populated.
	cached, err := rdb.Get(context.Background(), cacheKey).Result()
	if err != nil {
		t.Fatalf("expected cache populated after fetch, got error: %v", err)
	}
	if !strings.Contains(cached, "keys") {
		t.Fatalf("cached value doesn't look like JWKS: %s", cached)
	}

	// Verify TTL is approximately 1h.
	ttl := srv.TTL(cacheKey)
	if ttl < 59*time.Minute || ttl > 61*time.Minute {
		t.Fatalf("expected ~1h TTL, got %v", ttl)
	}
}

func TestJWKSRedisFallback(t *testing.T) {
	// Redis is unavailable — should fall back to direct IdP fetch.
	m := newMockOIDCServerWithCounter(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Use a Redis client with fast failure — non-routable address, zero retries,
	// short dial timeout. Avoids port exhaustion on Windows/MSYS.
	brokenRdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1", // unlikely to have a server
		MaxRetries:  0,
		DialTimeout: 100 * time.Millisecond,
	})
	defer func() { _ = brokenRdb.Close() }()

	provider.WithRedis(brokenRdb)

	hitsBefore := m.jwksHits.Load()

	// Should fall back to HTTP fetch despite broken Redis.
	if err := provider.refreshJWKS(context.Background()); err != nil {
		t.Fatalf("refreshJWKS should succeed with broken Redis: %v", err)
	}

	if m.jwksHits.Load() <= hitsBefore {
		t.Fatal("expected IdP fetch as fallback when Redis unavailable")
	}

	// Verify keys were loaded.
	provider.mu.RLock()
	hasKey := len(provider.rsaKeys) > 0
	provider.mu.RUnlock()
	if !hasKey {
		t.Fatal("expected RSA keys loaded via fallback fetch")
	}
}

func TestJWKSWithRedisNil(t *testing.T) {
	// No Redis attached — should work exactly like before (direct fetch).
	m := newMockOIDCServerWithCounter(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	hitsBefore := m.jwksHits.Load()

	if err := provider.refreshJWKS(context.Background()); err != nil {
		t.Fatalf("refreshJWKS: %v", err)
	}

	if m.jwksHits.Load() <= hitsBefore {
		t.Fatal("expected IdP JWKS fetch when no Redis configured")
	}
}

// ---------- Context binding regression tests ----------

// TestRefreshJWKSRespectsContextCancellation verifies that refreshJWKS
// honours context cancellation instead of using an unbounded context.
func TestRefreshJWKSRespectsContextCancellation(t *testing.T) {
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	// Slow server that delays JWKS response by 5 seconds.
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]string{"issuer": issuer, "jwks_uri": issuer + "/keys"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		// Block until request context is done or 5s elapses.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
		}
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"slow-kid","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`, n, e)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuer = srv.URL

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Cancel context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err = provider.refreshJWKS(ctx)
	elapsed := time.Since(start)

	// refreshJWKS should fail quickly due to cancelled context, not block for 5s.
	if elapsed > 2*time.Second {
		t.Fatalf("refreshJWKS took %v — context cancellation not respected", elapsed)
	}
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestRefreshJWKSContextTimeout verifies that refreshJWKS respects a short
// context timeout instead of running unbounded.
func TestRefreshJWKSContextTimeout(t *testing.T) {
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	// Server that never responds to JWKS requests.
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]string{"issuer": issuer, "jwks_uri": issuer + "/keys"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		// Respond immediately on first call (init), then block on subsequent.
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"timeout-kid","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`, n, e)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuer = srv.URL

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Verify that a bounded context is respected (not ignored).
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// This should succeed quickly since the server responds immediately.
	if err := provider.refreshJWKS(ctx); err != nil {
		t.Fatalf("refreshJWKS with timeout: %v", err)
	}
}

// TestRefreshIfUnknownKidUsesBoundedContext verifies that the on-demand
// refresh triggered by an unknown kid uses a bounded context.
func TestRefreshIfUnknownKidUsesBoundedContext(t *testing.T) {
	m := newMockOIDCServer(t)
	defer m.Close()

	provider, err := NewOIDCProvider(OIDCConfig{
		IssuerURL: m.issuer,
		Audience:  "cordum-api",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	defer provider.Close()

	// Force lastRefresh to be old so on-demand refresh is allowed.
	provider.mu.Lock()
	provider.lastRefresh = time.Now().Add(-2 * time.Minute)
	provider.mu.Unlock()

	// Request an unknown kid — triggers on-demand refresh which should
	// complete (pass or fail) within reasonable time, not hang.
	done := make(chan bool, 1)
	go func() {
		result := provider.refreshIfUnknownKid("nonexistent-kid-xyz")
		done <- result
	}()

	select {
	case <-done:
		// Completed within timeout — context bounding works.
	case <-time.After(15 * time.Second):
		t.Fatal("refreshIfUnknownKid blocked beyond 15s — likely unbounded context")
	}
}
