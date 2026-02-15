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

func TestDecodeMaybeBase64(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("secret"))
	if got := decodeMaybeBase64(raw); string(got) != "secret" {
		t.Fatalf("expected base64 decode")
	}
	if got := decodeMaybeBase64("plain"); string(got) != "plain" {
		t.Fatalf("expected raw bytes")
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

func TestAuthFromClaims_CrossTenantNoIssuerConfigured(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	// No CORDUM_JWT_ISSUER set — cross-tenant should be denied
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub":                "alice",
		"allow_cross_tenant": true,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	ctx, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if ctx.AllowCrossTenant {
		t.Fatal("expected AllowCrossTenant=false when no trusted issuer configured")
	}
}

func TestValidateClaims_MissingIssuerProd(t *testing.T) {
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_PRODUCTION", "true")
	// No CORDUM_JWT_ISSUER or CORDUM_JWT_AUDIENCE in production mode
	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	payload := map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := signHS256(t, "secret", payload)
	_, err = validator.Validate(token)
	if err == nil {
		t.Fatal("expected error in production mode without issuer configured")
	}
	if !strings.Contains(err.Error(), "issuer validation required") {
		t.Fatalf("expected 'issuer validation required' in error, got: %v", err)
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
