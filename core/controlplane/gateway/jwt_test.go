package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
		"sub": "alice",
		"role": "admin",
		"iss": "issuer-1",
		"aud": "aud-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
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
