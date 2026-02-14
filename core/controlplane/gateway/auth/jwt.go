package auth

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ===========================================================================
// Basic JWT Validator
// ===========================================================================

type jwtValidator struct {
	hmacSecret  []byte
	rsaPublic   *rsa.PublicKey
	issuer      string
	audience    string
	clockSkew   time.Duration
	defaultRole string
}

func newJWTValidatorFromEnv() (*jwtValidator, bool, error) {
	secret := strings.TrimSpace(os.Getenv("CORDUM_JWT_HMAC_SECRET"))
	pubKey := strings.TrimSpace(os.Getenv("CORDUM_JWT_PUBLIC_KEY"))
	pubKeyPath := strings.TrimSpace(os.Getenv("CORDUM_JWT_PUBLIC_KEY_PATH"))
	if pubKeyPath != "" {
		// #nosec G304,G703 -- public key path is configured by the operator.
		data, err := os.ReadFile(pubKeyPath)
		if err != nil {
			return nil, false, fmt.Errorf("read jwt public key: %w", err)
		}
		pubKey = string(data)
	}

	if secret == "" && pubKey == "" {
		return nil, false, nil
	}

	v := &jwtValidator{
		issuer:      strings.TrimSpace(os.Getenv("CORDUM_JWT_ISSUER")),
		audience:    strings.TrimSpace(os.Getenv("CORDUM_JWT_AUDIENCE")),
		defaultRole: strings.TrimSpace(os.Getenv("CORDUM_JWT_DEFAULT_ROLE")),
	}
	if v.defaultRole == "" {
		v.defaultRole = "viewer"
	}
	if rawSkew := strings.TrimSpace(os.Getenv("CORDUM_JWT_CLOCK_SKEW")); rawSkew != "" {
		if d, err := time.ParseDuration(rawSkew); err != nil {
			return nil, false, fmt.Errorf("parse jwt clock skew: %w", err)
		} else if d > 0 {
			v.clockSkew = d
		}
	}

	if secret != "" {
		v.hmacSecret = decodeMaybeBase64(secret)
	}
	if pubKey != "" {
		key, err := parseRSAPublicKey([]byte(pubKey))
		if err != nil {
			return nil, false, fmt.Errorf("parse jwt public key: %w", err)
		}
		v.rsaPublic = key
	}

	required := false
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CORDUM_JWT_REQUIRED")), "true") {
		required = true
	}
	return v, required, nil
}

func decodeMaybeBase64(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && len(decoded) > 0 {
		return decoded
	}
	return []byte(raw)
}

func parseRSAPublicKey(raw []byte) (*rsa.PublicKey, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty public key")
	}
	if block, _ := pem.Decode(raw); block != nil {
		raw = block.Bytes
	}
	if pub, err := x509.ParsePKIXPublicKey(raw); err == nil {
		if key, ok := pub.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(raw); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported public key")
}

func (v *jwtValidator) Validate(token string) (*AuthContext, error) {
	if v == nil {
		return nil, errors.New("jwt validator not configured")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	headerRaw, err := decodeSegment(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode jwt header: %w", err)
	}
	payloadRaw, err := decodeSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode jwt payload: %w", err)
	}
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode jwt signature: %w", err)
	}

	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, fmt.Errorf("jwt header json: %w", err)
	}
	alg, _ := header["alg"].(string)
	if alg == "" || strings.EqualFold(alg, "none") {
		return nil, errors.New("jwt alg unsupported")
	}

	signingInput := parts[0] + "." + parts[1]
	if err := v.verifySignature(alg, signingInput, sig); err != nil {
		return nil, err
	}

	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, fmt.Errorf("jwt claims json: %w", err)
	}
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	return v.authFromClaims(claims), nil
}

func (v *jwtValidator) verifySignature(alg, signingInput string, sig []byte) error {
	switch alg {
	case "HS256":
		if len(v.hmacSecret) == 0 {
			return errors.New("jwt hmac secret not configured")
		}
		mac := hmac.New(sha256.New, v.hmacSecret)
		_, _ = mac.Write([]byte(signingInput))
		expected := mac.Sum(nil)
		if !hmac.Equal(expected, sig) {
			return errors.New("jwt signature invalid")
		}
		return nil
	case "RS256":
		if v.rsaPublic == nil {
			return errors.New("jwt public key not configured")
		}
		sum := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(v.rsaPublic, crypto.SHA256, sum[:], sig); err != nil {
			return errors.New("jwt signature invalid")
		}
		return nil
	default:
		return fmt.Errorf("jwt alg %s unsupported", alg)
	}
}

func (v *jwtValidator) validateClaims(claims map[string]any) error {
	now := time.Now()
	if exp, ok := numericClaim(claims, "exp"); ok {
		if now.After(exp.Add(v.clockSkew)) {
			return errors.New("jwt expired")
		}
	}
	if nbf, ok := numericClaim(claims, "nbf"); ok {
		if now.Add(v.clockSkew).Before(nbf) {
			return errors.New("jwt not active yet")
		}
	}
	if v.issuer != "" {
		if iss, _ := claims["iss"].(string); iss != v.issuer {
			return errors.New("jwt issuer mismatch")
		}
	}
	if v.audience != "" {
		if !audienceMatches(claims["aud"], v.audience) {
			return errors.New("jwt audience mismatch")
		}
	}
	return nil
}

func (v *jwtValidator) authFromClaims(claims map[string]any) *AuthContext {
	role := claimString(claims, "role")
	if role == "" {
		if roles, ok := claims["roles"].([]any); ok && len(roles) > 0 {
			if s, ok := roles[0].(string); ok {
				role = s
			}
		}
	}
	if role == "" {
		role = v.defaultRole
	}
	tenant := claimString(claims, "tenant")
	if tenant == "" {
		tenant = claimString(claims, "tenant_id")
	}
	principal := claimString(claims, "sub")
	if principal == "" {
		principal = claimString(claims, "principal_id")
	}
	allowCrossTenant, _ := claims["allow_cross_tenant"].(bool)

	return &AuthContext{
		Tenant:           strings.TrimSpace(tenant),
		PrincipalID:      strings.TrimSpace(principal),
		Role:             NormalizeRole(role),
		AllowCrossTenant: allowCrossTenant,
	}
}

// ===========================================================================
// Shared JWT helpers
// ===========================================================================

func decodeSegment(seg string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(seg)
}

func numericClaim(claims map[string]any, key string) (time.Time, bool) {
	raw, ok := claims[key]
	if !ok {
		return time.Time{}, false
	}
	switch v := raw.(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return time.Unix(i, 0), true
		}
	}
	return time.Time{}, false
}

func claimString(claims map[string]any, key string) string {
	if raw, ok := claims[key]; ok {
		if s, ok := raw.(string); ok {
			return s
		}
	}
	return ""
}

func audienceMatches(raw any, expected string) bool {
	switch v := raw.(type) {
	case string:
		return v == expected
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}
