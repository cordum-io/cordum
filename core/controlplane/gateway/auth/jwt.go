package auth

import (
	"context"
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
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/infra/env"
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

	warnAudienceOnce sync.Once
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

	issuer := strings.TrimSpace(os.Getenv("CORDUM_JWT_ISSUER"))
	if issuer == "" {
		// Production refuses to start without an explicit issuer:
		// silently defaulting to a well-known name (pre-fix this was
		// "cordum") gave attackers with a leaked HMAC secret a
		// platform-default "iss" claim to forge against. Mirror the
		// audience guard at validateClaims so misconfiguration fails
		// fast at gateway init rather than at first request.
		if env.IsProduction() {
			return nil, false, fmt.Errorf("jwt: CORDUM_JWT_ISSUER required in production")
		}
		issuer = "cordum-dev"
		slog.Info("jwt: using default issuer", "issuer", issuer, "production", false)
	}

	v := &jwtValidator{
		issuer:      issuer,
		audience:    strings.TrimSpace(os.Getenv("CORDUM_JWT_AUDIENCE")),
		defaultRole: strings.TrimSpace(os.Getenv("CORDUM_JWT_DEFAULT_ROLE")),
	}
	if v.defaultRole == "" {
		v.defaultRole = "viewer"
	}
	if rawSkew := strings.TrimSpace(os.Getenv("CORDUM_JWT_CLOCK_SKEW")); rawSkew != "" {
		const maxClockSkew = 5 * time.Minute
		if d, err := time.ParseDuration(rawSkew); err != nil {
			return nil, false, fmt.Errorf("parse jwt clock skew: %w", err)
		} else if d > maxClockSkew {
			return nil, false, fmt.Errorf("jwt clock skew %v exceeds maximum %v", d, maxClockSkew)
		} else if d > 0 {
			v.clockSkew = d
		}
	}

	if secret != "" {
		v.hmacSecret = decodeHMACSecret(secret)
	}
	if pubKey != "" {
		key, err := parseRSAPublicKey([]byte(pubKey))
		if err != nil {
			return nil, false, fmt.Errorf("parse jwt public key: %w", err)
		}
		v.rsaPublic = key
	}

	required := strings.EqualFold(strings.TrimSpace(os.Getenv("CORDUM_JWT_REQUIRED")), "true")
	return v, required, nil
}

// decodeHMACSecret parses the HMAC secret string. If the value starts with
// "base64:" the remainder is decoded as standard base64. Otherwise the raw
// string bytes are used verbatim. This replaces the old decodeMaybeBase64
// behaviour which silently decoded any valid-looking base64, changing the
// effective key bytes without the operator's knowledge.
func decodeHMACSecret(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	const prefix = "base64:"
	if strings.HasPrefix(raw, prefix) {
		decoded, err := base64.StdEncoding.DecodeString(raw[len(prefix):])
		if err != nil {
			slog.Error("jwt: invalid base64 in HMAC secret after 'base64:' prefix", "err", err)
			return nil
		}
		return decoded
	}
	// Warn if the value looks like base64 but is missing the prefix —
	// helps operators migrate from the old silent-decode behaviour.
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) > 0 && len(decoded) != len(raw) {
		slog.Warn("jwt: HMAC secret looks like base64 but missing 'base64:' prefix — using raw bytes. "+
			"Add 'base64:' prefix if you intended base64 decoding.",
			"hint", "CORDUM_JWT_HMAC_SECRET=base64:"+raw)
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
	exp, ok := numericClaim(claims, "exp")
	if !ok {
		return errors.New("jwt missing exp claim")
	}
	if now.After(exp.Add(v.clockSkew)) {
		return errors.New("jwt expired")
	}
	if nbf, ok := numericClaim(claims, "nbf"); ok {
		if now.Add(v.clockSkew).Before(nbf) {
			return errors.New("jwt not active yet")
		}
	}
	if iss, _ := claims["iss"].(string); iss != v.issuer {
		return fmt.Errorf("jwt issuer mismatch: got %q, want %q", iss, v.issuer)
	}
	if v.audience != "" {
		if !audienceMatches(claims["aud"], v.audience) {
			return errors.New("jwt audience mismatch")
		}
	} else {
		if env.IsProduction() {
			return errors.New("jwt: audience validation required in production — set CORDUM_JWT_AUDIENCE")
		}
		v.warnAudienceOnce.Do(func() {
			slog.Warn("jwt: audience validation disabled — CORDUM_JWT_AUDIENCE not configured")
		})
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
	tenant := claimString(claims, "tenant") // #nosec G706 -- structured slog logging, no format string injection
	if tenant == "" {
		tenant = claimString(claims, "tenant_id")
	}
	principal := claimString(claims, "sub")
	if principal == "" {
		principal = claimString(claims, "principal_id")
	}
	// Only honor allow_cross_tenant from tokens signed by the platform's own
	// trusted issuer. Self-asserted cross-tenant from arbitrary JWTs is a
	// privilege escalation vector.
	var allowCrossTenant bool
	if claimBool(claims, "allow_cross_tenant") {
		iss := claimString(claims, "iss")
		if v.issuer != "" && iss == v.issuer {
			allowCrossTenant = true
		} else {
			slog.Warn("jwt: ignoring self-asserted allow_cross_tenant — issuer not trusted",
				"iss", iss, "trusted_issuer", v.issuer)
		}
	}

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

func claimBool(claims map[string]any, key string) bool {
	raw, exists := claims[key]
	if !exists {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	case float64:
		return v != 0
	default:
		slog.Warn("jwt: unexpected type for claim", "key", key, "type", fmt.Sprintf("%T", raw))
		return false
	}
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

// ===========================================================================
// Reloadable JWT Validator — supports key rotation without restart
// ===========================================================================

// ReloadableJWTValidator wraps a jwtValidator with atomic swap for hot-reload.
// Use Reload() to re-read env vars and swap the inner validator atomically.
// Call WatchLoop to poll for key file changes every interval.
type ReloadableJWTValidator struct {
	inner    atomic.Pointer[jwtValidator]
	required atomic.Bool
	keyPath  string // CORDUM_JWT_PUBLIC_KEY_PATH, empty if not file-based
}

// NewReloadableJWTValidator creates a reloadable JWT validator from env vars.
// Returns (nil, false, nil) if no JWT config is present.
func NewReloadableJWTValidator() (*ReloadableJWTValidator, bool, error) {
	v, required, err := newJWTValidatorFromEnv()
	if err != nil {
		return nil, false, err
	}
	if v == nil {
		return nil, false, nil
	}
	r := &ReloadableJWTValidator{
		keyPath: strings.TrimSpace(os.Getenv("CORDUM_JWT_PUBLIC_KEY_PATH")),
	}
	r.inner.Store(v)
	r.required.Store(required)
	return r, required, nil
}

// Validate delegates to the current inner validator.
func (r *ReloadableJWTValidator) Validate(token string) (*AuthContext, error) {
	v := r.inner.Load()
	if v == nil {
		return nil, errors.New("jwt validator not configured")
	}
	return v.Validate(token)
}

// IsRequired reports whether JWT auth is required.
func (r *ReloadableJWTValidator) IsRequired() bool {
	return r.required.Load()
}

// Reload re-reads JWT config from environment variables and atomically
// swaps the inner validator. Existing in-flight validations continue
// with the old validator; new validations use the new one.
func (r *ReloadableJWTValidator) Reload() error {
	v, required, err := newJWTValidatorFromEnv()
	if err != nil {
		return fmt.Errorf("jwt reload: %w", err)
	}
	if v == nil {
		slog.Warn("jwt reload: no JWT config found in env — keeping existing validator")
		return nil
	}
	r.inner.Store(v)
	r.required.Store(required)
	slog.Info("jwt config reloaded",
		"has_hmac", len(v.hmacSecret) > 0,
		"has_rsa", v.rsaPublic != nil,
		"issuer", v.issuer,
		"required", required,
	)
	return nil
}

// WatchLoop polls the JWT public key file (if configured via
// CORDUM_JWT_PUBLIC_KEY_PATH) for changes and reloads automatically.
// For HMAC-only configs without a key file, this is a no-op.
// Blocks until ctx is cancelled.
func (r *ReloadableJWTValidator) WatchLoop(ctx context.Context, interval time.Duration) {
	if r.keyPath == "" {
		// No file to watch — HMAC secret from env only, use SIGHUP to reload.
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	var lastMod time.Time
	if info, err := os.Stat(r.keyPath); err == nil {
		lastMod = info.ModTime()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(r.keyPath)
			if err != nil {
				continue
			}
			if info.ModTime().Equal(lastMod) {
				continue
			}
			if err := r.Reload(); err != nil {
				slog.Error("jwt key file reload failed", "path", r.keyPath, "error", err)
				continue
			}
			lastMod = info.ModTime()
		}
	}
}
