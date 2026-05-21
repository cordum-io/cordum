package auth

import (
	"strings"
	"testing"
)

// TestJWTValidator_RefusesToStartInProdWithoutIssuer locks the PR #276
// audit fix: when running in production mode with HMAC/RSA configured
// but CORDUM_JWT_ISSUER unset, newJWTValidatorFromEnv MUST refuse to
// construct the validator. Pre-fix the constructor silently defaulted
// to "cordum", which allowed an attacker with knowledge of the
// platform default (or a leaked HMAC secret) to forge tokens with a
// known issuer and any tenant/role claims.
func TestJWTValidator_RefusesToStartInProdWithoutIssuer(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "true")
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "shared-secret")
	t.Setenv("CORDUM_JWT_ISSUER", "")

	validator, _, err := newJWTValidatorFromEnv()
	if err == nil {
		t.Fatalf("newJWTValidatorFromEnv returned err=nil; want refuse-to-start when prod + missing issuer (got validator=%v)", validator)
	}
	if validator != nil {
		t.Fatalf("newJWTValidatorFromEnv returned validator=%v; want nil on prod+missing-issuer", validator)
	}
	if !strings.Contains(err.Error(), "CORDUM_JWT_ISSUER") {
		t.Fatalf("error %q does not name CORDUM_JWT_ISSUER (operator-facing message must cite the missing env var)", err)
	}
}

// TestJWTValidator_ProdModeViaCordumEnv covers the env-mode form of
// production detection. env.IsProduction() honors both
// CORDUM_PRODUCTION=true and CORDUM_ENV=production; the refuse-to-start
// must trip on either path.
func TestJWTValidator_ProdModeViaCordumEnv(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "shared-secret")
	t.Setenv("CORDUM_JWT_ISSUER", "")

	validator, _, err := newJWTValidatorFromEnv()
	if err == nil {
		t.Fatalf("err=nil with CORDUM_ENV=production + missing issuer; want refuse-to-start (validator=%v)", validator)
	}
	if validator != nil {
		t.Fatalf("validator=%v, want nil", validator)
	}
}

// TestJWTValidator_NonProdStillDefaultsIssuer is the regression
// control: in non-production mode the dev-default issuer fallback
// remains in place so local development does not require operators to
// set CORDUM_JWT_ISSUER manually.
func TestJWTValidator_NonProdStillDefaultsIssuer(t *testing.T) {
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "")
	t.Setenv("CORDUM_JWT_HMAC_SECRET", "secret")
	t.Setenv("CORDUM_JWT_ISSUER", "")

	validator, _, err := newJWTValidatorFromEnv()
	if err != nil {
		t.Fatalf("non-prod default issuer should still construct: %v", err)
	}
	if validator == nil || validator.issuer == "" {
		t.Fatalf("validator=%v, issuer=%q — non-prod must still set a default issuer", validator, validatorIssuerOrEmpty(validator))
	}
}

func validatorIssuerOrEmpty(v *jwtValidator) string {
	if v == nil {
		return ""
	}
	return v.issuer
}
