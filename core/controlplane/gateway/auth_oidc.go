package gateway

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/logging"
)

// ===========================================================================
// Basic JWT Validator  (was jwt.go)
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
		Role:             normalizeRole(role),
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

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// ===========================================================================
// OIDC Provider  (was oidc_auth.go)
// ===========================================================================

// OIDCConfig holds the OIDC provider configuration.
type OIDCConfig struct {
	Enabled             bool
	IssuerURL           string
	Audience            string
	ClaimTenant         string        // JWT claim -> tenant (default "org_id")
	ClaimRole           string        // JWT claim -> role (default "cordum_role")
	JWKSRefreshInterval time.Duration // How often to proactively refresh (default 6h)
	AllowedSigningAlgs  []string      // Restrict algs (default: RS256, RS384, RS512, ES256, ES384, ES512)
}

// OIDCProvider validates JWTs against an OIDC identity provider's JWKS endpoint.
type OIDCProvider struct {
	cfg        OIDCConfig
	jwksURI    string
	httpClient *http.Client

	mu          sync.RWMutex
	rsaKeys     map[string]*rsa.PublicKey
	ecKeys      map[string]*ecdsa.PublicKey
	lastRefresh time.Time
	allowedAlgs map[string]struct{}

	stopCh chan struct{}
	done   chan struct{}
}

// NewOIDCProvider creates an OIDCProvider by performing OIDC discovery and
// fetching the initial JWKS. Returns an error if discovery or the first key
// fetch fails.
func NewOIDCProvider(cfg OIDCConfig) (*OIDCProvider, error) {
	if cfg.IssuerURL == "" {
		return nil, errors.New("oidc: issuer URL required")
	}
	cfg.IssuerURL = strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	if parsed, err := validateOIDCURL(cfg.IssuerURL); err != nil {
		return nil, err
	} else {
		cfg.IssuerURL = strings.TrimRight(parsed.String(), "/")
	}
	if cfg.ClaimTenant == "" {
		cfg.ClaimTenant = "org_id"
	}
	if cfg.ClaimRole == "" {
		cfg.ClaimRole = "cordum_role"
	}
	if cfg.JWKSRefreshInterval <= 0 {
		cfg.JWKSRefreshInterval = 6 * time.Hour
	}
	if len(cfg.AllowedSigningAlgs) == 0 {
		cfg.AllowedSigningAlgs = defaultOIDCAlgs()
	}
	allowedAlgs := normalizeAllowedAlgs(cfg.AllowedSigningAlgs)
	if len(allowedAlgs) == 0 {
		return nil, errors.New("oidc: allowed signing algs cannot be empty")
	}
	for _, alg := range allowedAlgs {
		if !isSupportedOIDCAlg(alg) {
			return nil, fmt.Errorf("oidc: unsupported signing alg %q", alg)
		}
	}
	cfg.AllowedSigningAlgs = allowedAlgs

	p := &OIDCProvider{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rsaKeys:     make(map[string]*rsa.PublicKey),
		ecKeys:      make(map[string]*ecdsa.PublicKey),
		stopCh:      make(chan struct{}),
		done:        make(chan struct{}),
		allowedAlgs: make(map[string]struct{}, len(cfg.AllowedSigningAlgs)),
	}
	for _, alg := range cfg.AllowedSigningAlgs {
		p.allowedAlgs[alg] = struct{}{}
	}

	// Discover JWKS URI
	jwksURI, err := p.discover()
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	p.jwksURI = jwksURI

	// Fetch initial JWKS
	if err := p.refreshJWKS(); err != nil {
		return nil, fmt.Errorf("oidc jwks fetch: %w", err)
	}

	// Start background refresh
	go p.backgroundRefresh()

	return p, nil
}

// NewOIDCProviderFromEnv creates an OIDCProvider from environment variables.
// Returns (nil, nil) if OIDC is not enabled.
func NewOIDCProviderFromEnv() (*OIDCProvider, error) {
	if !envBool("CORDUM_OIDC_ENABLED") {
		return nil, nil
	}
	cfg := OIDCConfig{
		Enabled:     true,
		IssuerURL:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_ISSUER")),
		Audience:    strings.TrimSpace(os.Getenv("CORDUM_OIDC_AUDIENCE")),
		ClaimTenant: strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLAIM_TENANT")),
		ClaimRole:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLAIM_ROLE")),
	}
	if rawAlgs := strings.TrimSpace(os.Getenv("CORDUM_OIDC_ALLOWED_ALGS")); rawAlgs != "" {
		algs := normalizeAllowedAlgs(splitCSV(rawAlgs))
		if len(algs) == 0 {
			return nil, errors.New("oidc: CORDUM_OIDC_ALLOWED_ALGS must contain at least one supported algorithm")
		}
		cfg.AllowedSigningAlgs = algs
	}
	if rawInterval := strings.TrimSpace(os.Getenv("CORDUM_OIDC_JWKS_REFRESH_INTERVAL")); rawInterval != "" {
		d, err := time.ParseDuration(rawInterval)
		if err != nil {
			return nil, fmt.Errorf("oidc: parse CORDUM_OIDC_JWKS_REFRESH_INTERVAL: %w", err)
		}
		cfg.JWKSRefreshInterval = d
	}
	return NewOIDCProvider(cfg)
}

// Close stops the background JWKS refresh goroutine.
func (p *OIDCProvider) Close() {
	close(p.stopCh)
	<-p.done
}

// ValidateJWT parses and validates a JWT token string against the cached JWKS.
// Returns an AuthContext with identity claims mapped to tenant/role/principal.
func (p *OIDCProvider) ValidateJWT(tokenString string) (*AuthContext, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc: invalid jwt format")
	}

	headerRaw, err := decodeSegment(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode header: %w", err)
	}
	payloadRaw, err := decodeSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode payload: %w", err)
	}
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode signature: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, fmt.Errorf("oidc: parse header: %w", err)
	}
	alg := strings.ToUpper(strings.TrimSpace(header.Alg))
	if alg == "" || alg == "NONE" {
		return nil, errors.New("oidc: unsupported alg")
	}
	if !p.isAlgAllowed(alg) {
		return nil, fmt.Errorf("oidc: alg %q not allowed", alg)
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	if err := p.verifySignature(alg, header.Kid, signingInput, sig); err != nil {
		return nil, err
	}

	// Parse and validate claims
	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	if err := p.validateClaims(claims); err != nil {
		return nil, err
	}

	return p.authFromClaims(claims), nil
}

// discover fetches the OpenID Configuration and returns the jwks_uri.
func (p *OIDCProvider) discover() (string, error) {
	url := p.cfg.IssuerURL + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.httpClient.Do(req) // #nosec -- issuer URL is validated during provider initialization.
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read discovery: %w", err)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
		Issuer  string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("oidc: discovery document missing jwks_uri")
	}
	// Validate issuer matches config
	if doc.Issuer != "" && doc.Issuer != p.cfg.IssuerURL {
		return "", fmt.Errorf("oidc: issuer mismatch: discovery=%q config=%q", doc.Issuer, p.cfg.IssuerURL)
	}
	parsedJWKS, err := validateOIDCURL(doc.JWKSURI)
	if err != nil {
		return "", fmt.Errorf("oidc: invalid jwks_uri: %w", err)
	}
	return parsedJWKS.String(), nil
}

// refreshJWKS fetches keys from the JWKS endpoint and caches them.
func (p *OIDCProvider) refreshJWKS() error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, p.jwksURI, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req) // #nosec -- JWKS URL is validated during discovery.
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read jwks: %w", err)
	}

	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("parse jwks: %w", err)
	}

	rsaKeys := make(map[string]*rsa.PublicKey)
	ecKeys := make(map[string]*ecdsa.PublicKey)

	for _, raw := range jwks.Keys {
		var key struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			// RSA fields
			N string `json:"n"`
			E string `json:"e"`
			// EC fields
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		}
		if err := json.Unmarshal(raw, &key); err != nil {
			continue
		}
		// Only use signing keys
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		switch key.Kty {
		case "RSA":
			pub, err := parseJWKRSA(key.N, key.E)
			if err != nil {
				logging.Error("oidc", "skip bad RSA key", "kid", key.Kid, "error", err)
				continue
			}
			rsaKeys[key.Kid] = pub
		case "EC":
			pub, err := parseJWKEC(key.Crv, key.X, key.Y)
			if err != nil {
				logging.Error("oidc", "skip bad EC key", "kid", key.Kid, "error", err)
				continue
			}
			ecKeys[key.Kid] = pub
		}
	}

	p.mu.Lock()
	p.rsaKeys = rsaKeys
	p.ecKeys = ecKeys
	p.lastRefresh = time.Now()
	p.mu.Unlock()

	logging.Info("oidc", "jwks refreshed", "rsa_keys", len(rsaKeys), "ec_keys", len(ecKeys))
	return nil
}

// backgroundRefresh periodically refreshes the JWKS cache.
func (p *OIDCProvider) backgroundRefresh() {
	defer close(p.done)
	ticker := time.NewTicker(p.cfg.JWKSRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			if err := p.refreshJWKS(); err != nil {
				logging.Error("oidc", "background jwks refresh failed", "error", err)
			}
		}
	}
}

// refreshIfUnknownKid attempts an on-demand JWKS refresh if the kid is not
// found in cache. Rate-limited to at most once per minute.
func (p *OIDCProvider) refreshIfUnknownKid(kid string) bool {
	p.mu.RLock()
	_, hasRSA := p.rsaKeys[kid]
	_, hasEC := p.ecKeys[kid]
	lastRefresh := p.lastRefresh
	p.mu.RUnlock()

	if hasRSA || hasEC {
		return true
	}
	// Rate limit: at most one refresh per minute
	if time.Since(lastRefresh) < time.Minute {
		return false
	}
	if err := p.refreshJWKS(); err != nil {
		logging.Error("oidc", "on-demand jwks refresh failed", "kid", kid, "error", err)
		return false
	}

	p.mu.RLock()
	_, hasRSA = p.rsaKeys[kid]
	_, hasEC = p.ecKeys[kid]
	p.mu.RUnlock()
	return hasRSA || hasEC
}

func (p *OIDCProvider) verifySignature(alg, kid, signingInput string, sig []byte) error {
	p.mu.RLock()
	rsaKey := p.rsaKeys[kid]
	ecKey := p.ecKeys[kid]
	p.mu.RUnlock()

	// If key not found, try on-demand refresh
	if rsaKey == nil && ecKey == nil {
		if !p.refreshIfUnknownKid(kid) {
			return fmt.Errorf("oidc: unknown kid %q", kid)
		}
		p.mu.RLock()
		rsaKey = p.rsaKeys[kid]
		ecKey = p.ecKeys[kid]
		p.mu.RUnlock()
	}

	switch alg {
	case "RS256":
		return verifyRSA(rsaKey, crypto.SHA256, signingInput, sig)
	case "RS384":
		return verifyRSA(rsaKey, crypto.SHA384, signingInput, sig)
	case "RS512":
		return verifyRSA(rsaKey, crypto.SHA512, signingInput, sig)
	case "ES256":
		return verifyEC(ecKey, sha256.New, 32, signingInput, sig)
	case "ES384":
		return verifyEC(ecKey, sha512.New384, 48, signingInput, sig)
	case "ES512":
		return verifyEC(ecKey, sha512.New, 66, signingInput, sig)
	default:
		return fmt.Errorf("oidc: unsupported alg %q", alg)
	}
}

func verifyRSA(key *rsa.PublicKey, hash crypto.Hash, signingInput string, sig []byte) error {
	if key == nil {
		return errors.New("oidc: no RSA key for kid")
	}
	h := hash.New()
	h.Write([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(key, hash, h.Sum(nil), sig); err != nil {
		return errors.New("oidc: signature invalid")
	}
	return nil
}

func verifyEC(key *ecdsa.PublicKey, hashFn func() hash.Hash, sigSize int, signingInput string, sig []byte) error {
	if key == nil {
		return errors.New("oidc: no EC key for kid")
	}
	h := hashFn()
	h.Write([]byte(signingInput))
	digest := h.Sum(nil)

	// ECDSA signatures in JWTs are r||s concatenated, not ASN.1 DER
	if len(sig) != sigSize*2 {
		return errors.New("oidc: invalid EC signature length")
	}
	r := new(big.Int).SetBytes(sig[:sigSize])
	s := new(big.Int).SetBytes(sig[sigSize:])

	if !ecdsa.Verify(key, digest, r, s) {
		return errors.New("oidc: signature invalid")
	}
	return nil
}

func (p *OIDCProvider) validateClaims(claims map[string]any) error {
	now := time.Now()
	// Validate exp
	if exp, ok := numericClaim(claims, "exp"); ok {
		if now.After(exp.Add(30 * time.Second)) { // 30s clock skew
			return errors.New("oidc: token expired")
		}
	}
	// Validate nbf
	if nbf, ok := numericClaim(claims, "nbf"); ok {
		if now.Add(30 * time.Second).Before(nbf) {
			return errors.New("oidc: token not active yet")
		}
	}
	// Validate iss
	if iss, _ := claims["iss"].(string); iss != p.cfg.IssuerURL {
		return fmt.Errorf("oidc: issuer mismatch: got %q want %q", iss, p.cfg.IssuerURL)
	}
	// Validate aud
	if p.cfg.Audience != "" {
		if !audienceMatches(claims["aud"], p.cfg.Audience) {
			return errors.New("oidc: audience mismatch")
		}
	}
	return nil
}

func (p *OIDCProvider) isAlgAllowed(alg string) bool {
	_, ok := p.allowedAlgs[alg]
	return ok
}

func (p *OIDCProvider) authFromClaims(claims map[string]any) *AuthContext {
	// Extract tenant from configurable claim
	tenant := claimString(claims, p.cfg.ClaimTenant)
	if tenant == "" {
		tenant = claimString(claims, "tenant")
		if tenant == "" {
			tenant = claimString(claims, "tenant_id")
		}
	}

	// Extract role from configurable claim
	role := claimString(claims, p.cfg.ClaimRole)
	if role == "" {
		role = claimString(claims, "role")
	}
	if role == "" {
		if roles, ok := claims["roles"].([]any); ok && len(roles) > 0 {
			if s, ok := roles[0].(string); ok {
				role = s
			}
		}
	}
	if role == "" {
		role = "viewer"
	}

	// Principal from sub claim
	principal := claimString(claims, "sub")
	if principal == "" {
		principal = claimString(claims, "email")
	}

	return &AuthContext{
		Tenant:      strings.TrimSpace(tenant),
		PrincipalID: strings.TrimSpace(principal),
		Role:        normalizeRole(role),
		AuthSource:  AuthSourceOIDC,
	}
}

// ---------- JWK parsing helpers ----------

func parseJWKRSA(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, errors.New("exponent too large")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func parseJWKEC(crv, xB64, yB64 string) (*ecdsa.PublicKey, error) {
	xBytes, err := base64.RawURLEncoding.DecodeString(xB64)
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(yB64)
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	var curve elliptic.Curve
	switch crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve: %s", crv)
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func defaultOIDCAlgs() []string {
	return []string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}
}

func normalizeAllowedAlgs(algs []string) []string {
	out := make([]string, 0, len(algs))
	for _, alg := range algs {
		a := strings.ToUpper(strings.TrimSpace(alg))
		if a == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func splitCSV(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
}

func isSupportedOIDCAlg(alg string) bool {
	switch alg {
	case "RS256", "RS384", "RS512", "ES256", "ES384", "ES512":
		return true
	default:
		return false
	}
}

const (
	envOIDCIssuerAllowlist = "CORDUM_OIDC_ISSUER_ALLOWLIST"
	envOIDCAllowPrivate    = "CORDUM_OIDC_ALLOW_PRIVATE"
	envOIDCAllowHTTP       = "CORDUM_OIDC_ALLOW_HTTP"
)

func validateOIDCURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("oidc: issuer URL required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("oidc: invalid issuer url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("oidc: issuer url must include scheme and host")
	}
	if parsed.Scheme != "https" {
		if parsed.Scheme != "http" || (env.IsProduction() && !envBool(envOIDCAllowHTTP)) {
			return nil, fmt.Errorf("oidc: issuer url must use https")
		}
	}

	host := strings.ToLower(parsed.Hostname())
	allowlist := oidcAllowlist()
	if len(allowlist) > 0 && !hostAllowed(host, allowlist) {
		return nil, fmt.Errorf("oidc: issuer host not allowed: %s", host)
	}
	if env.IsProduction() && !envBool(envOIDCAllowPrivate) {
		if err := ensurePublicHost(host); err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

func oidcAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv(envOIDCIssuerAllowlist))
	if raw == "" {
		return nil
	}
	entries := splitCSV(raw)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		val := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(entry), "."))
		if val != "" {
			out = append(out, val)
		}
	}
	return out
}

func hostAllowed(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.TrimPrefix(entry, ".")
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func ensurePublicHost(host string) error {
	if host == "" {
		return errors.New("oidc: issuer url missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("oidc: issuer host not allowed: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateNet(ip) {
			return fmt.Errorf("oidc: issuer host not allowed: %s", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("oidc: issuer host resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("oidc: issuer host resolve failed: %s", host)
	}
	for _, ip := range ips {
		if isPrivateNet(ip) {
			return fmt.Errorf("oidc: issuer host not allowed: %s", host)
		}
	}
	return nil
}

// envBool returns true if the named env var is set to a truthy value.
func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "true" || v == "1" || v == "yes"
}
