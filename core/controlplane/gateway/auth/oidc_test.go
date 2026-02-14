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
	"testing"
	"time"

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
	m.issuer = m.Server.URL
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

// ---------- OIDCProvider tests ----------

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

func TestOIDC_NoAudienceConfig(t *testing.T) {
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
	basic := &BasicAuthProvider{
		defaultTenant: "default",
		keys:          map[string]apiKeyMeta{"test-key": {Role: "admin"}},
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

	basic := &BasicAuthProvider{
		defaultTenant: "default",
		keys:          map[string]apiKeyMeta{"real-key": {Role: "admin"}},
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
