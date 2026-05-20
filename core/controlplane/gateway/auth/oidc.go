package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
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
	"syscall"
	"time"

	"log/slog"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
)

// ===========================================================================
// OIDC Provider
// ===========================================================================

// OIDCConfig holds the OIDC provider configuration.
type OIDCConfig struct {
	Enabled             bool
	IssuerURL           string
	Audience            string
	ClaimTenant         string // JWT claim -> tenant (default "org_id")
	ClaimRole           string // JWT claim -> role (default "cordum_role")
	GroupsClaim         string // JWT claim -> IdP groups (default "groups")
	GroupRoleMapping    map[string]string
	ClientID            string
	ClientSecret        string
	RedirectURI         string
	Scopes              []string
	AuthorizationURL    string
	TokenURL            string
	UserInfoURL         string
	EmailClaim          string
	NameClaim           string
	UsernameClaim       string
	DefaultRole         string
	AutoProvision       bool
	SyncRoles           bool
	JWKSRefreshInterval time.Duration // How often to proactively refresh (default 6h)
	AllowedSigningAlgs  []string      // Restrict algs (default: RS256, RS384, RS512, ES256, ES384, ES512)
}

// OIDCProvider validates JWTs against an OIDC identity provider's JWKS endpoint.
type OIDCProvider struct {
	cfg        OIDCConfig
	jwksURI    string
	httpClient *http.Client

	mu              sync.RWMutex
	rsaKeys         map[string]*rsa.PublicKey
	ecKeys          map[string]*ecdsa.PublicKey
	lastRefresh     time.Time
	lastFullRefresh time.Time // when keys were last fully replaced (grace period anchor)
	allowedAlgs     map[string]struct{}
	redisClient     redis.UniversalClient // optional — used for cross-replica JWKS cache
	refreshCooldown time.Duration         // configurable via OIDC_JWKS_REFRESH_COOLDOWN

	stopCh chan struct{}
	done   chan struct{}
}

// Config returns the OIDC configuration.
func (p *OIDCProvider) Config() OIDCConfig {
	if p == nil {
		return OIDCConfig{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	cfg := p.cfg
	cfg.Scopes = append([]string(nil), p.cfg.Scopes...)
	cfg.AllowedSigningAlgs = append([]string(nil), p.cfg.AllowedSigningAlgs...)
	cfg.GroupRoleMapping = cloneStringMap(p.cfg.GroupRoleMapping)
	return cfg
}

// UpdateGroupRoleMapping validates and updates the live OIDC groups-claim
// mapping. It does not change issuer/client/JWKS state.
func (p *OIDCProvider) UpdateGroupRoleMapping(groupsClaim string, mapping map[string]string) (OIDCConfig, error) {
	if p == nil {
		return OIDCConfig{}, errors.New("oidc: provider unavailable")
	}
	groupsClaim = strings.TrimSpace(groupsClaim)
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	normalizedMapping, err := normalizeOIDCGroupRoleMapping(mapping)
	if err != nil {
		return OIDCConfig{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg.GroupsClaim = groupsClaim
	p.cfg.GroupRoleMapping = normalizedMapping
	cfg := p.cfg
	cfg.Scopes = append([]string(nil), p.cfg.Scopes...)
	cfg.AllowedSigningAlgs = append([]string(nil), p.cfg.AllowedSigningAlgs...)
	cfg.GroupRoleMapping = cloneStringMap(p.cfg.GroupRoleMapping)
	return cfg, nil
}

// WithRedis attaches an optional Redis client for cross-replica JWKS caching.
// Must be called before the first background refresh tick (safe if called
// immediately after NewOIDCProvider, since the first tick fires after
// JWKSRefreshInterval which defaults to 6h).
func (p *OIDCProvider) WithRedis(rdb redis.UniversalClient) {
	p.mu.Lock()
	p.redisClient = rdb
	p.mu.Unlock()
}

// issuerCacheKey returns the Redis key for the JWKS cache: cordum:auth:jwks:<hash>.
func (p *OIDCProvider) issuerCacheKey() string {
	h := sha256.Sum256([]byte(p.cfg.IssuerURL))
	return fmt.Sprintf("cordum:auth:jwks:%x", h[:16])
}

// cryptoRandJitter returns a random duration in [0, maxSeconds) using crypto/rand.
func cryptoRandJitter(maxSeconds int) time.Duration {
	if maxSeconds <= 0 {
		return 0
	}
	n, err := crand.Int(crand.Reader, big.NewInt(int64(maxSeconds)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64()) * time.Second
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
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	groupRoleMapping, err := normalizeOIDCGroupRoleMapping(cfg.GroupRoleMapping)
	if err != nil {
		return nil, err
	}
	cfg.GroupRoleMapping = groupRoleMapping
	if cfg.EmailClaim == "" {
		cfg.EmailClaim = "email"
	}
	if cfg.NameClaim == "" {
		cfg.NameClaim = "name"
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "viewer"
	}
	cfg.DefaultRole = normalizeOIDCDefaultRole(cfg.DefaultRole)
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "viewer"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	} else {
		cfg.Scopes = normalizeOIDCScopes(cfg.Scopes)
	}
	if !cfg.AutoProvision {
		cfg.AutoProvision = !envBool("CORDUM_OIDC_DISABLE_AUTO_PROVISION")
	}
	if !cfg.SyncRoles {
		cfg.SyncRoles = !envBool("CORDUM_OIDC_DISABLE_ROLE_SYNC")
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

	cooldown := time.Minute
	if v := os.Getenv("OIDC_JWKS_REFRESH_COOLDOWN"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cooldown = d
		} else {
			slog.Warn("invalid OIDC_JWKS_REFRESH_COOLDOWN, using default", "value", v, "default", cooldown)
		}
	}

	p := &OIDCProvider{
		cfg:             cfg,
		httpClient:      newOIDCHTTPClient(),
		rsaKeys:         make(map[string]*rsa.PublicKey),
		ecKeys:          make(map[string]*ecdsa.PublicKey),
		stopCh:          make(chan struct{}),
		done:            make(chan struct{}),
		allowedAlgs:     make(map[string]struct{}, len(cfg.AllowedSigningAlgs)),
		refreshCooldown: cooldown,
	}
	for _, alg := range cfg.AllowedSigningAlgs {
		p.allowedAlgs[alg] = struct{}{}
	}

	// Discover provider endpoints.
	discoveryDoc, err := p.discover()
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	p.jwksURI = discoveryDoc.JWKSURI
	p.cfg.AuthorizationURL = discoveryDoc.AuthorizationEndpoint
	p.cfg.TokenURL = discoveryDoc.TokenEndpoint
	p.cfg.UserInfoURL = discoveryDoc.UserInfoEndpoint

	// Fetch initial JWKS with bounded startup context
	initCtx, initCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer initCancel()
	if err := p.refreshJWKS(initCtx); err != nil {
		return nil, fmt.Errorf("oidc jwks fetch: %w", err)
	}

	// Start background refresh
	go p.backgroundRefresh()

	return p, nil
}

// NewOIDCProviderFromEnv creates an OIDCProvider from environment variables.
// Returns (nil, nil) if OIDC is not enabled.
func NewOIDCProviderFromEnv() (*OIDCProvider, error) {
	enabled := envBool("CORDUM_OIDC_ENABLED")
	clientID := strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLIENT_ID"))
	if !enabled && clientID == "" {
		return nil, nil
	}
	cfg := OIDCConfig{
		Enabled:       true,
		IssuerURL:     strings.TrimSpace(os.Getenv("CORDUM_OIDC_ISSUER")),
		Audience:      strings.TrimSpace(os.Getenv("CORDUM_OIDC_AUDIENCE")),
		ClaimTenant:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLAIM_TENANT")),
		ClaimRole:     strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLAIM_ROLE")),
		ClientID:      clientID,
		ClientSecret:  strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLIENT_SECRET")),
		RedirectURI:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_REDIRECT_URI")),
		GroupsClaim:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_GROUPS_CLAIM")),
		EmailClaim:    strings.TrimSpace(os.Getenv("CORDUM_OIDC_EMAIL_CLAIM")),
		NameClaim:     strings.TrimSpace(os.Getenv("CORDUM_OIDC_NAME_CLAIM")),
		UsernameClaim: strings.TrimSpace(os.Getenv("CORDUM_OIDC_USERNAME_CLAIM")),
		DefaultRole:   strings.TrimSpace(os.Getenv("CORDUM_OIDC_DEFAULT_ROLE")),
		AutoProvision: !envBool("CORDUM_OIDC_DISABLE_AUTO_PROVISION"),
		SyncRoles:     !envBool("CORDUM_OIDC_DISABLE_ROLE_SYNC"),
	}
	if rawScopes := strings.TrimSpace(os.Getenv("CORDUM_OIDC_SCOPES")); rawScopes != "" {
		cfg.Scopes = normalizeOIDCScopes(splitCSV(rawScopes))
	}
	if rawMapping := strings.TrimSpace(os.Getenv("CORDUM_OIDC_GROUP_ROLE_MAPPING")); rawMapping != "" {
		mapping, err := parseOIDCGroupRoleMapping(rawMapping)
		if err != nil {
			return nil, err
		}
		cfg.GroupRoleMapping = mapping
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
	closeOIDCIdleConnections(p.httpClient)
}

func closeOIDCIdleConnections(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if closer, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// ValidateJWT parses and validates a JWT token string against the cached JWKS.
// Returns an AuthContext with identity claims mapped to tenant/role/principal.
func (p *OIDCProvider) ValidateJWT(tokenString string) (*AuthContext, error) {
	authCtx, _, err := p.validateJWT(tokenString, p.cfg.Audience)
	return authCtx, err
}

// ValidateJWTWithClaims parses and validates a JWT token string and returns the raw claims.
func (p *OIDCProvider) ValidateJWTWithClaims(tokenString string) (*AuthContext, map[string]any, error) {
	return p.validateJWT(tokenString, p.cfg.Audience)
}

func (p *OIDCProvider) validateJWT(tokenString, expectedAudience string) (*AuthContext, map[string]any, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("oidc: invalid jwt format")
	}

	headerRaw, err := decodeSegment(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("oidc: decode header: %w", err)
	}
	payloadRaw, err := decodeSegment(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("oidc: decode payload: %w", err)
	}
	sig, err := decodeSegment(parts[2])
	if err != nil {
		return nil, nil, fmt.Errorf("oidc: decode signature: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, nil, fmt.Errorf("oidc: parse header: %w", err)
	}
	alg := strings.ToUpper(strings.TrimSpace(header.Alg))
	if alg == "" || alg == "NONE" {
		return nil, nil, errors.New("oidc: unsupported alg")
	}
	if !p.isAlgAllowed(alg) {
		return nil, nil, fmt.Errorf("oidc: alg %q not allowed", alg)
	}

	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	if err := p.verifySignature(alg, header.Kid, signingInput, sig); err != nil {
		return nil, nil, err
	}

	// Parse and validate claims
	var claims map[string]any
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, nil, fmt.Errorf("oidc: parse claims: %w", err)
	}
	if err := p.validateClaims(claims, expectedAudience); err != nil {
		return nil, nil, err
	}

	return p.authFromClaims(claims), claims, nil
}

type oidcDiscoveryDocument struct {
	JWKSURI               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

// discover fetches the OpenID Configuration and returns the validated provider endpoints.
func (p *OIDCProvider) discover() (*oidcDiscoveryDocument, error) {
	url := p.cfg.IssuerURL + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req) // #nosec -- issuer URL is validated during provider initialization.
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read discovery: %w", err)
	}

	var doc oidcDiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return nil, errors.New("oidc: discovery document missing jwks_uri")
	}
	// Validate issuer matches config
	if doc.Issuer != "" && doc.Issuer != p.cfg.IssuerURL {
		return nil, fmt.Errorf("oidc: issuer mismatch: discovery=%q config=%q", doc.Issuer, p.cfg.IssuerURL)
	}
	parsedJWKS, err := validateOIDCURL(doc.JWKSURI)
	if err != nil {
		return nil, fmt.Errorf("oidc: invalid jwks_uri: %w", err)
	}
	doc.JWKSURI = parsedJWKS.String()
	for label, raw := range map[string]string{
		"authorization_endpoint": doc.AuthorizationEndpoint,
		"token_endpoint":         doc.TokenEndpoint,
		"userinfo_endpoint":      doc.UserInfoEndpoint,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		parsed, err := validateOIDCURL(raw)
		if err != nil {
			return nil, fmt.Errorf("oidc: invalid %s: %w", label, err)
		}
		switch label {
		case "authorization_endpoint":
			doc.AuthorizationEndpoint = parsed.String()
		case "token_endpoint":
			doc.TokenEndpoint = parsed.String()
		case "userinfo_endpoint":
			doc.UserInfoEndpoint = parsed.String()
		}
	}
	return &doc, nil
}

// refreshJWKS fetches keys from the JWKS endpoint and caches them.
// When a Redis client is configured, it checks the cross-replica cache first
// and only falls back to the IdP HTTP call on cache miss.
func (p *OIDCProvider) refreshJWKS(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.RLock()
	rdb := p.redisClient
	p.mu.RUnlock()

	var body []byte

	// Try Redis cache first (cross-replica dedup).
	if rdb != nil {
		cacheKey := p.issuerCacheKey()
		cacheCtx, cacheCancel := context.WithTimeout(ctx, 2*time.Second)
		cached, err := rdb.Get(cacheCtx, cacheKey).Bytes()
		cacheCancel()
		if err == nil && len(cached) > 0 {
			body = cached
			slog.Debug("jwks cache hit", "key", cacheKey)
		}
	}

	// If no cache hit, fetch from IdP.
	if body == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.jwksURI, nil) // #nosec G704 -- jwksURI from OIDC discovery of admin-configured issuer, not user input
		if err != nil {
			return err
		}
		resp, err := p.httpClient.Do(req) // #nosec -- JWKS URL is validated during discovery.
		if err != nil {
			return fmt.Errorf("fetch jwks: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
		}

		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			return fmt.Errorf("read jwks: %w", readErr)
		}

		// Write to Redis cache (best effort, 1h TTL).
		if rdb != nil {
			cacheKey := p.issuerCacheKey()
			setCtx, setCancel := context.WithTimeout(ctx, 2*time.Second)
			if err := rdb.Set(setCtx, cacheKey, body, time.Hour).Err(); err != nil {
				slog.Error("jwks cache write failed", "error", err)
			}
			setCancel()
		}
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
				slog.Error("skip bad RSA key", "kid", key.Kid, "error", err)
				continue
			}
			rsaKeys[key.Kid] = pub
		case "EC":
			pub, err := parseJWKEC(key.Crv, key.X, key.Y)
			if err != nil {
				slog.Error("skip bad EC key", "kid", key.Kid, "error", err)
				continue
			}
			ecKeys[key.Kid] = pub
		}
	}

	p.mu.Lock()
	if len(rsaKeys) == 0 && len(ecKeys) == 0 {
		// Empty JWKS response: preserve existing cache, don't evict working keys.
		slog.Warn("JWKS response contained no valid keys, preserving existing cache")
	} else if time.Since(p.lastFullRefresh) > time.Hour {
		// Grace period expired: fully replace cache with latest JWKS keys.
		// This ensures revoked/rotated IdP keys are eventually removed.
		p.rsaKeys = rsaKeys
		p.ecKeys = ecKeys
		p.lastFullRefresh = time.Now()
		slog.Info("JWKS cache fully replaced after grace period")
	} else {
		// Within grace period: merge new keys with existing to allow
		// graceful key rotation without breaking in-flight tokens.
		for kid, key := range rsaKeys {
			p.rsaKeys[kid] = key
		}
		for kid, key := range ecKeys {
			p.ecKeys[kid] = key
		}
	}
	p.lastRefresh = time.Now()
	p.mu.Unlock()

	slog.Info("jwks refreshed", "rsa_keys", len(rsaKeys), "ec_keys", len(ecKeys))
	return nil
}

// backgroundRefresh periodically refreshes the JWKS cache with jitter to
// prevent thundering-herd requests to the IdP across N gateway replicas.
func (p *OIDCProvider) backgroundRefresh() {
	defer close(p.done)

	// Initial jitter: 0-10s to desynchronize replicas that start together.
	// Kept short to minimize auth cold-start latency; collision risk is low with 2-6 replicas.
	if jitter := cryptoRandJitter(10); jitter > 0 {
		select {
		case <-p.stopCh:
			return
		case <-time.After(jitter):
		}
	}

	ticker := time.NewTicker(p.cfg.JWKSRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Per-tick jitter: 0-15s to stagger subsequent refreshes.
			if jitter := cryptoRandJitter(15); jitter > 0 {
				select {
				case <-p.stopCh:
					return
				case <-time.After(jitter):
				}
			}
			refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := p.refreshJWKS(refreshCtx); err != nil {
				slog.Error("background jwks refresh failed", "error", err)
			}
			refreshCancel()
		}
	}
}

// refreshIfUnknownKid attempts an on-demand JWKS refresh if the kid is not
// found in cache. Uses double-checked locking to prevent thundering herd:
// the rate-limit check is re-verified under the write lock so concurrent
// goroutines coalesce into a single refresh.
func (p *OIDCProvider) refreshIfUnknownKid(kid string) bool {
	// Fast path: check under read lock.
	p.mu.RLock()
	_, hasRSA := p.rsaKeys[kid]
	_, hasEC := p.ecKeys[kid]
	p.mu.RUnlock()

	if hasRSA || hasEC {
		return true
	}

	// Slow path: acquire write lock and re-check everything.
	// This prevents the TOCTOU race where N goroutines all pass the
	// rate-limit check and trigger N concurrent JWKS refreshes.
	p.mu.Lock()
	// Re-check kid under write lock — another goroutine may have refreshed.
	_, hasRSA = p.rsaKeys[kid]
	_, hasEC = p.ecKeys[kid]
	if hasRSA || hasEC {
		p.mu.Unlock()
		return true
	}
	// Re-check rate limit under write lock — prevents thundering herd.
	if time.Since(p.lastRefresh) < p.refreshCooldown {
		p.mu.Unlock()
		return false
	}
	// Mark refresh in progress so concurrent goroutines see the updated timestamp.
	p.lastRefresh = time.Now()
	p.mu.Unlock()

	// Bound on-demand refresh to prevent request-path pile-up under IdP slowness.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.refreshJWKS(ctx); err != nil {
		slog.Error("on-demand jwks refresh failed", "kid", kid, "error", err)
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

func (p *OIDCProvider) validateClaims(claims map[string]any, expectedAudience string) error {
	now := time.Now()
	// Validate exp — required to prevent tokens without expiry from granting permanent access
	exp, ok := numericClaim(claims, "exp")
	if !ok {
		return errors.New("oidc: token missing exp claim")
	}
	if now.After(exp.Add(30 * time.Second)) { // 30s clock skew
		return errors.New("oidc: token expired")
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
	if strings.TrimSpace(expectedAudience) != "" {
		if !audienceMatches(claims["aud"], expectedAudience) {
			return errors.New("oidc: audience mismatch")
		}
	} else if env.IsProduction() {
		return errors.New("oidc: audience validation required in production — set CORDUM_OIDC_AUDIENCE")
	}
	return nil
}

func (p *OIDCProvider) isAlgAllowed(alg string) bool {
	_, ok := p.allowedAlgs[alg]
	return ok
}

func (p *OIDCProvider) authFromClaims(claims map[string]any) *AuthContext {
	cfg := p.Config()

	// Extract tenant from configurable claim
	tenant := claimString(claims, cfg.ClaimTenant)
	if tenant == "" {
		tenant = claimString(claims, "tenant")
		if tenant == "" {
			tenant = claimString(claims, "tenant_id")
		}
	}

	// Extract role from IdP groups when a group mapping is configured. Empty
	// mappings keep existing OIDC deployments on the legacy ClaimRole path even
	// when their IdP already emits an unrelated "groups" claim.
	role := ""
	roleFromGroupPath := false
	if len(cfg.GroupRoleMapping) > 0 {
		groups, present, malformed := claimStringSlice(claims, cfg.GroupsClaim)
		if present {
			if len(groups) > 0 {
				role = roleFromGroups(groups, cfg.GroupRoleMapping)
				if role == "" {
					role = cfg.DefaultRole
				}
				roleFromGroupPath = true
			} else if malformed {
				role = cfg.DefaultRole
				roleFromGroupPath = true
			}
		}
	}

	// Extract role from configurable claim
	if role == "" {
		role = claimString(claims, cfg.ClaimRole)
	}
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
		role = cfg.DefaultRole
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
		Role:        normalizeOIDCResolvedRole(role, roleFromGroupPath),
		AuthSource:  AuthSourceOIDC,
	}
}

func roleFromGroups(groups []string, mapping map[string]string) string {
	bestRole := ""
	bestRank := 0
	for _, group := range groups {
		role := mapping[canonicalOIDCGroup(group)]
		if role == "" {
			continue
		}
		if rank := oidcRoleRank(role); rank > bestRank {
			bestRole = role
			bestRank = rank
		}
	}
	return bestRole
}

func normalizeOIDCResolvedRole(role string, preserveOperator bool) string {
	if preserveOperator {
		if normalized, ok := normalizeOIDCStrictRole(role); ok {
			return normalized
		}
	}
	if normalized, ok := normalizeOIDCStrictRole(role); ok && normalized != "operator" {
		return normalized
	}
	return NormalizeRole(role)
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

func normalizeOIDCScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes)+1)
	normalized := make([]string, 0, len(scopes)+1)
	appendScope := func(scope string) {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return
		}
		if _, ok := seen[scope]; ok {
			return
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	appendScope("openid")
	for _, scope := range scopes {
		appendScope(scope)
	}
	return normalized
}

func parseOIDCGroupRoleMapping(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("oidc: parse CORDUM_OIDC_GROUP_ROLE_MAPPING: %w", err)
	}
	mapping, err := normalizeOIDCGroupRoleMapping(parsed)
	if err != nil {
		return nil, fmt.Errorf("oidc: CORDUM_OIDC_GROUP_ROLE_MAPPING %w", err)
	}
	return mapping, nil
}

func normalizeOIDCGroupRoleMapping(raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for group, role := range raw {
		key := canonicalOIDCGroup(group)
		if key == "" {
			return nil, errors.New("contains an empty group name")
		}
		normalizedRole, ok := normalizeOIDCStrictRole(role)
		if !ok {
			return nil, fmt.Errorf("contains invalid role %q for group %q", role, group)
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("contains duplicate group %q after case-insensitive normalization", key)
		}
		out[key] = normalizedRole
	}
	return out, nil
}

func normalizeOIDCDefaultRole(role string) string {
	if normalized, ok := normalizeOIDCStrictRole(role); ok {
		return normalized
	}
	return NormalizeRole(role)
}

func normalizeOIDCStrictRole(role string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin":
		return "admin", true
	case "operator":
		return "operator", true
	case "viewer":
		return "viewer", true
	default:
		return "", false
	}
}

func oidcRoleRank(role string) int {
	switch role {
	case "admin":
		return 3
	case "operator":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func canonicalOIDCGroup(group string) string {
	return strings.ToLower(strings.TrimSpace(group))
}

func claimStringSlice(claims map[string]any, key string) (groups []string, present bool, malformed bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, false
	}
	raw, ok := claims[key]
	if !ok {
		return nil, false, false
	}
	switch v := raw.(type) {
	case []string:
		return cleanOIDCGroups(v), true, false
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				malformed = true
				continue
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			malformed = false
		}
		return out, true, malformed
	default:
		return nil, true, true
	}
}

func cleanOIDCGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group != "" {
			out = append(out, group)
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func maskOIDCSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	switch {
	case secret == "":
		return ""
	case len(secret) <= 4:
		return strings.Repeat("*", len(secret))
	case len(secret) <= 8:
		return secret[:2] + strings.Repeat("*", len(secret)-2)
	default:
		return secret[:4] + strings.Repeat("*", len(secret)-8) + secret[len(secret)-4:]
	}
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
	// SSRF defense in all environments: refuse private / loopback /
	// link-local / cloud-metadata issuer hosts unless explicitly opted
	// into via CORDUM_OIDC_ALLOW_PRIVATE=true (dev/test escape hatch).
	// Pre-fix this was gated on env.IsProduction(), so dev / staging
	// deployments accepted issuer URLs that pointed at the cloud
	// metadata IP and leaked discovery + JWKS to the local IMDS.
	if !envBool(envOIDCAllowPrivate) {
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

// isPrivateNet delegates to the shared PrivateIPNets in private_nets.go.
func isPrivateNet(ip net.IP) bool {
	return IsPrivateNet(ip)
}

// newOIDCHTTPClient returns the http.Client used for OIDC discovery and
// JWKS refresh. It installs a net.Dialer.Control hook that inspects the
// resolved address at connect time and refuses dials to private /
// loopback / link-local / multicast / unspecified IPs. This is defense
// in depth against DNS rebinding: even if validateOIDCURL accepted the
// hostname at registration, a hostile DNS server can swap the resolved
// IP between fetches, and refreshJWKS uses the discovery-time URI
// verbatim. CORDUM_OIDC_ALLOW_PRIVATE=true opens the dialer for the
// dev/test escape hatch (parallels validateOIDCURL).
func newOIDCHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if envBool(envOIDCAllowPrivate) {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("oidc: dial refused — unresolved host %q", address)
			}
			if isPrivateNet(ip) {
				return fmt.Errorf("oidc: dial refused — address %s is private / loopback / link-local (DNS rebinding guard)", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// ===========================================================================
// OIDCAuthAdapter
// ===========================================================================

// OIDCAuthAdapter wraps OIDCProvider as an AuthProvider.
// It only handles Bearer token authentication — all other methods return errors
// so the CompositeAuthProvider can fall through to the next provider.
type OIDCAuthAdapter struct {
	provider      *OIDCProvider
	defaultTenant string
}

// NewOIDCAuthAdapter creates an AuthProvider adapter around an OIDCProvider.
func NewOIDCAuthAdapter(provider *OIDCProvider, defaultTenant string) *OIDCAuthAdapter {
	return &OIDCAuthAdapter{provider: provider, defaultTenant: defaultTenant}
}

func (a *OIDCAuthAdapter) AuthConfig() AuthConfig {
	cfg := AuthConfig{
		SessionTTL: authSessionTTLString(),
	}
	if a == nil || a.provider == nil {
		return cfg
	}
	providerCfg := a.provider.Config()
	cfg.OIDCEnabled = providerCfg.Enabled
	cfg.OIDCIssuer = providerCfg.IssuerURL
	cfg.OIDCClientID = providerCfg.ClientID
	cfg.OIDCRedirectURI = providerCfg.RedirectURI
	cfg.OIDCScopes = append([]string(nil), providerCfg.Scopes...)
	cfg.OIDCGroupsClaim = providerCfg.GroupsClaim
	cfg.OIDCGroupRoleMapping = cloneStringMap(providerCfg.GroupRoleMapping)
	cfg.OIDCClientSecretMasked = maskOIDCSecret(providerCfg.ClientSecret)
	return cfg
}

func (a *OIDCAuthAdapter) UpdateOIDCGroupRoleMapping(groupsClaim string, mapping map[string]string) (AuthConfig, error) {
	if a == nil || a.provider == nil {
		return AuthConfig{}, errors.New("oidc: provider unavailable")
	}
	if _, err := a.provider.UpdateGroupRoleMapping(groupsClaim, mapping); err != nil {
		return AuthConfig{}, err
	}
	return a.AuthConfig(), nil
}

func (a *OIDCAuthAdapter) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	token := BearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return nil, errors.New("oidc: no bearer token")
	}
	// Skip session tokens — those belong to BasicAuthProvider
	if len(token) > 8 && token[:8] == "session-" {
		return nil, errors.New("oidc: not an OIDC token")
	}
	authCtx, err := a.provider.ValidateJWT(token)
	if err != nil {
		return nil, err
	}
	if authCtx.Tenant == "" {
		authCtx.Tenant = a.defaultTenant
	}
	return authCtx, nil
}

func (a *OIDCAuthAdapter) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	// OIDC tokens can come via gRPC Authorization header
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("oidc: no metadata")
	}
	token := ""
	if raw := md.Get("authorization"); len(raw) > 0 {
		token = BearerToken(raw[0])
	}
	if token == "" {
		if raw := md.Get("Authorization"); len(raw) > 0 {
			token = BearerToken(raw[0])
		}
	}
	if token == "" {
		return nil, errors.New("oidc: no bearer token")
	}
	// Skip session tokens — those belong to BasicAuthProvider
	if strings.HasPrefix(token, "session-") {
		return nil, errors.New("oidc: not an OIDC token")
	}
	authCtx, err := a.provider.ValidateJWT(token)
	if err != nil {
		return nil, err
	}
	if authCtx.Tenant == "" {
		authCtx.Tenant = a.defaultTenant
	}
	return authCtx, nil
}

func (a *OIDCAuthAdapter) RequireRole(r *http.Request, roles ...string) error {
	return errors.New("oidc: delegate to primary")
}

func (a *OIDCAuthAdapter) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	return "", errors.New("oidc: delegate to primary")
}

func (a *OIDCAuthAdapter) RequireTenantAccess(r *http.Request, tenant string) error {
	return errors.New("oidc: delegate to primary")
}

func (a *OIDCAuthAdapter) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	return "", errors.New("oidc: delegate to primary")
}
