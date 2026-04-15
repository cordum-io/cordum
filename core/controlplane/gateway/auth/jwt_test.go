package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestJWTValidatorHS256(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "issuer-1")
	t.Setenv("CORDUM_JWT_AUDIENCE", "aud-1")
	t.Setenv("CORDUM_JWT_DEFAULT_ROLE", "viewer")
	validator, required, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	if validator == nil {
		t.Fatalf("expected validator")
	}
	if required {
		t.Fatalf("expected jwt not required by default")
	}

	payload := map[string]any{
		"sub":  "alice",
		"role": "admin",
		"iss":  "issuer-1",
		"aud":  "aud-1",
		"exp":  time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	ctx, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if ctx.PrincipalID != "alice" || ctx.Role != "admin" {
		t.Fatalf("unexpected auth ctx: %#v", ctx)
	}
}

func TestJWTValidatorExpired(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	payload := map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	if _, err := validator.Validate(token); err == nil {
		t.Fatalf("expected expired token error")
	}
}

func TestJWTValidatorIssuerMismatch(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "issuer-1")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	payload := map[string]any{
		"sub": "alice",
		"iss": "issuer-2",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	if _, err := validator.Validate(token); err == nil {
		t.Fatalf("expected issuer mismatch error")
	}
}

func TestParseRSAPublicKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	pkixBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal pkix: %v", err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkixBytes})
	if _, err := parseRSAPublicKey(pemBlock); err != nil {
		t.Fatalf("parse pkix pem: %v", err)
	}

	pkcs1Bytes := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	if _, err := parseRSAPublicKey(pkcs1Bytes); err != nil {
		t.Fatalf("parse pkcs1: %v", err)
	}

	if _, err := parseRSAPublicKey(nil); err == nil {
		t.Fatalf("expected error on empty key")
	}
}

func TestDecodeHMACSecret(t *testing.T) {
	// Explicit base64: prefix triggers decode.
	raw := base64.StdEncoding.EncodeToString([]byte("secret"))
	if got := decodeHMACSecret("base64:" + raw); string(got) != "secret" {
		t.Fatalf("expected base64 decode, got %q", got)
	}
	// Plain string without prefix returns raw bytes (no silent decode).
	if got := decodeHMACSecret("plain"); string(got) != "plain" {
		t.Fatalf("expected raw bytes, got %q", got)
	}
	// Base64-looking value WITHOUT prefix returns raw bytes, not decoded.
	b64 := base64.StdEncoding.EncodeToString([]byte("secret"))
	if got := decodeHMACSecret(b64); string(got) != b64 {
		t.Fatalf("expected raw bytes %q without prefix, got %q", b64, got)
	}
	// Empty returns nil.
	if got := decodeHMACSecret(""); got != nil {
		t.Fatalf("expected nil for empty, got %v", got)
	}
	// Invalid base64 after prefix returns nil.
	if got := decodeHMACSecret("base64:!!!invalid!!!"); got != nil {
		t.Fatalf("expected nil for invalid base64, got %v", got)
	}
}

func TestJWTClockSkewMaxCap(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_CLOCK_SKEW", "1h")
	_, _, err := newJWTValidatorFromEnv()
	if err == nil {
		t.Fatal("expected error for clock skew exceeding maximum")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJWTClockSkewValid(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_CLOCK_SKEW", "30s")
	v, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.clockSkew != 30*time.Second {
		t.Errorf("expected 30s clock skew, got %v", v.clockSkew)
	}
}

func TestJWTClockSkewNegative(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_CLOCK_SKEW", "-5s")
	v, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.clockSkew != 0 {
		t.Errorf("expected zero clock skew for negative value, got %v", v.clockSkew)
	}
}

func TestJWTClockSkewAtMax(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_CLOCK_SKEW", "5m")
	v, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.clockSkew != 5*time.Minute {
		t.Errorf("expected 5m clock skew, got %v", v.clockSkew)
	}
}

func TestClaimBool(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		key    string
		want   bool
	}{
		{"bool true", map[string]any{"k": true}, "k", true},
		{"bool false", map[string]any{"k": false}, "k", false},
		{"string true", map[string]any{"k": "true"}, "k", true},
		{"string TRUE", map[string]any{"k": "TRUE"}, "k", true},
		{"string 1", map[string]any{"k": "1"}, "k", true},
		{"string false", map[string]any{"k": "false"}, "k", false},
		{"string 0", map[string]any{"k": "0"}, "k", false},
		{"float64 1", map[string]any{"k": float64(1)}, "k", true},
		{"float64 0", map[string]any{"k": float64(0)}, "k", false},
		{"unexpected type", map[string]any{"k": []string{"x"}}, "k", false},
		{"missing key", map[string]any{}, "k", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claimBool(tt.claims, tt.key)
			if got != tt.want {
				t.Fatalf("claimBool(%v, %q) = %v, want %v", tt.claims, tt.key, got, tt.want)
			}
		})
	}
}

func TestJWTAllowCrossTenantStringClaim(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "trusted-issuer")
	t.Setenv("CORDUM_JWT_DEFAULT_ROLE", "viewer")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub":                "alice",
		"iss":                "trusted-issuer",
		"allow_cross_tenant": "true",
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	ctx, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !ctx.AllowCrossTenant {
		t.Fatal("expected AllowCrossTenant=true when issuer matches trusted issuer")
	}
}

func TestValidateClaims_MissingExp(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		// No exp claim — must be rejected
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error for missing exp claim")
	}
	if !strings.Contains(err.Error(), "missing exp") {
		t.Fatalf("expected 'missing exp' in error, got: %v", err)
	}
}

func TestAuthFromClaims_CrossTenantUntrustedIssuer(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "trusted-issuer")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// Token from a different issuer claiming allow_cross_tenant
	payload := map[string]any{
		"sub":                "mallory",
		"iss":                "untrusted-issuer",
		"allow_cross_tenant": true,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	// This will fail with issuer mismatch since CORDUM_JWT_ISSUER is set
	if err == nil {
		t.Fatal("expected issuer mismatch error")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("expected 'issuer' in error, got: %v", err)
	}
}

func TestAuthFromClaims_CrossTenantWrongIssuer(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "trusted-platform")
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// Token with matching issuer — cross-tenant honored.
	payload := map[string]any{
		"sub":                "alice",
		"iss":                "trusted-platform",
		"allow_cross_tenant": true,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	ctx, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !ctx.AllowCrossTenant {
		t.Fatal("expected AllowCrossTenant=true when issuer matches trusted issuer")
	}

	// Token with wrong issuer — rejected at validation level.
	payloadBad := map[string]any{
		"sub":                "alice",
		"iss":                "untrusted-external",
		"allow_cross_tenant": true,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}
	badToken := signHS256(t, "secret", payloadBad)
	_, err = validator.Validate(badToken)
	if err == nil {
		t.Fatal("expected validation failure for wrong issuer")
	}
}

func TestValidateClaims_DefaultIssuerProd(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "cordum")
	t.Setenv("CORDUM_JWT_AUDIENCE", "cordum-api")
	t.Setenv("CORDUM_PRODUCTION", "true")
	// Production defaults issuer to "cordum" — dev tokens must be rejected.
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// Token with dev issuer — rejected in production.
	payload := map[string]any{
		"sub": "alice",
		"iss": "cordum-dev",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error: dev-issued token should be rejected in production")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Fatalf("expected 'issuer mismatch' in error, got: %v", err)
	}

	// Token with correct production issuer — accepted.
	payloadOK := map[string]any{
		"sub": "alice",
		"iss": "cordum",
		"aud": "cordum-api",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	goodToken := signHS256(t, "secret", payloadOK)
	_, err = validator.Validate(goodToken)
	if err != nil {
		t.Fatalf("expected production token with matching issuer to pass, got: %v", err)
	}
}

func TestValidateClaims_MissingAudienceProd(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "trusted-issuer")
	t.Setenv("CORDUM_PRODUCTION", "true")
	// No CORDUM_JWT_AUDIENCE in production mode
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		"iss": "trusted-issuer",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error in production mode without audience configured")
	}
	if !strings.Contains(err.Error(), "audience validation required") {
		t.Fatalf("expected 'audience validation required' in error, got: %v", err)
	}
}

func signHS256(t *testing.T, secret string, payload map[string]any) string {
	t.Helper()
	header := map[string]any{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	segment := func(data []byte) string {
		return base64.RawURLEncoding.EncodeToString(data)
	}
	headerSeg := segment(headerJSON)
	payloadSeg := segment(payloadJSON)
	signingInput := headerSeg + "." + payloadSeg
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	signature := segment(mac.Sum(nil))
	return signingInput + "." + signature
}

func TestReloadableJWTValidator(t *testing.T) {
	// Create with key A
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "key-A")
	t.Setenv("CORDUM_JWT_ISSUER", "test-issuer")
	t.Setenv("CORDUM_JWT_AUDIENCE", "test-aud")

	rv, _, err := NewReloadableJWTValidator()
	if err != nil {
		t.Fatalf("new reloadable: %v", err)
	}
	if rv == nil {
		t.Fatal("expected reloadable validator")
	}

	// Token signed with key A should pass
	tokenA := signHS256(t, "key-A", map[string]any{
		"sub": "user1", "iss": "test-issuer", "aud": "test-aud",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	if _, err := rv.Validate(tokenA); err != nil {
		t.Fatalf("expected valid with key A: %v", err)
	}

	// Token signed with key B should fail
	tokenB := signHS256(t, "key-B", map[string]any{
		"sub": "user1", "iss": "test-issuer", "aud": "test-aud",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	if _, err := rv.Validate(tokenB); err == nil {
		t.Fatal("expected invalid with key B before reload")
	}

	// Reload with key B
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "key-B")
	if err := rv.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Now token B should pass
	if _, err := rv.Validate(tokenB); err != nil {
		t.Fatalf("expected valid with key B after reload: %v", err)
	}

	// Token A should now fail
	if _, err := rv.Validate(tokenA); err == nil {
		t.Fatal("expected invalid with key A after reload to key B")
	}
}

func TestReloadableJWTValidator_NilWhenNoConfig(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "")
	t.Setenv("CORDUM_JWT_PUBLIC_KEY", "")
	t.Setenv("CORDUM_JWT_PUBLIC_KEY_PATH", "")

	rv, _, err := NewReloadableJWTValidator()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rv != nil {
		t.Fatal("expected nil when no JWT config")
	}
}

// ---------------------------------------------------------------------------
// JWT issuer validation — always enforced
// ---------------------------------------------------------------------------

// TestJWTIssuer_MissingIssuer_Rejected verifies that a token without an iss
// claim is rejected even in non-production mode.
func TestJWTIssuer_MissingIssuer_Rejected(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	// No CORDUM_JWT_ISSUER — defaults to "cordum-dev" in non-production.
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		// No "iss" claim.
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error: token without iss should be rejected")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Fatalf("expected 'issuer mismatch' error, got: %v", err)
	}
}

// TestJWTIssuer_DefaultDevIssuer_Accepted verifies that a token with the
// default dev issuer ("cordum-dev") is accepted when no CORDUM_JWT_ISSUER is set.
func TestJWTIssuer_DefaultDevIssuer_Accepted(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	// No CORDUM_JWT_ISSUER — defaults to "cordum-dev".
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		"iss": "cordum-dev",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	ctx, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("expected token with default dev issuer to pass, got: %v", err)
	}
	if ctx.PrincipalID != "alice" {
		t.Fatalf("expected principal alice, got %s", ctx.PrincipalID)
	}
}

// TestJWTIssuer_DevToken_RejectedInProd verifies that a token with iss="cordum-dev"
// is rejected when the configured issuer is "cordum" (production default).
func TestJWTIssuer_DevToken_RejectedInProd(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "cordum")

	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		"iss": "cordum-dev",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error: dev-issued token must be rejected by production issuer")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Fatalf("expected 'issuer mismatch' error, got: %v", err)
	}
}
