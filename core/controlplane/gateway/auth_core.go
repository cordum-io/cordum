package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/metadata"
)

// ---------------------------------------------------------------------------
// AuthSource type
// ---------------------------------------------------------------------------

// AuthSource identifies the authentication mechanism that validated a request.
type AuthSource string

const (
	AuthSourceAPIKey  AuthSource = "api_key"
	AuthSourceJWT     AuthSource = "jwt"
	AuthSourceOIDC    AuthSource = "oidc"
	AuthSourceSession AuthSource = "session"
)

// ---------------------------------------------------------------------------
// AuthContext & AuthProvider interface  (was auth_provider.go)
// ---------------------------------------------------------------------------

// AuthContext captures request identity for auditing and tenant routing.
type AuthContext struct {
	APIKey           string
	Tenant           string
	PrincipalID      string
	Role             string
	AllowCrossTenant bool
	AuthSource       AuthSource
}

type authContextKey struct{}

// AuthProvider injects auth context and enforces access control.
type AuthProvider interface {
	AuthenticateHTTP(r *http.Request) (*AuthContext, error)
	AuthenticateGRPC(ctx context.Context) (*AuthContext, error)
	RequireRole(r *http.Request, roles ...string) error
	ResolveTenant(r *http.Request, requested, fallback string) (string, error)
	RequireTenantAccess(r *http.Request, tenant string) error
	ResolvePrincipal(r *http.Request, requested string) (string, error)
}

// UserStoreProvider is implemented by auth providers that hold a UserStore.
type UserStoreProvider interface {
	UserStore() UserStore
}

func authFromContext(ctx context.Context) *AuthContext {
	if ctx == nil {
		return nil
	}
	if raw := ctx.Value(authContextKey{}); raw != nil {
		if auth, ok := raw.(*AuthContext); ok {
			return auth
		}
	}
	return nil
}

func authFromRequest(r *http.Request) *AuthContext {
	if r == nil {
		return nil
	}
	return authFromContext(r.Context())
}

// ---------------------------------------------------------------------------
// BasicAuthProvider  (was basic_auth.go)
// ---------------------------------------------------------------------------

type apiKeyEntry struct {
	Key              string `json:"key"`
	Tenant           string `json:"tenant,omitempty"`
	Role             string `json:"role,omitempty"`
	PrincipalID      string `json:"principal_id,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	AllowCrossTenant bool   `json:"allow_cross_tenant,omitempty"`
}

type apiKeyMeta struct {
	Tenant           string
	Role             string
	PrincipalID      string
	AllowCrossTenant bool
	ExpiresAt        time.Time
}

type BasicAuthProvider struct {
	defaultTenant        string
	keys                 map[string]apiKeyMeta
	requireAPIKey        bool
	allowAnonymous       bool
	allowHeaderPrincipal bool
	keysPath             string
	keysModTime          time.Time
	keysMu               sync.RWMutex
	jwt                  *jwtValidator
	jwtRequired          bool
	userStore            UserStore
	keyStore             KeyStore
	usageWG              sync.WaitGroup
	usageCtx             context.Context
}

func newBasicAuthProvider(defaultTenant string) (*BasicAuthProvider, error) {
	keys, requireKey, keysPath, keysModTime, allowHeaderPrincipal, err := loadBasicAPIKeys()
	if err != nil {
		return nil, err
	}
	allowAnonymous := env.Bool("CORDUM_ALLOW_INSECURE_NO_AUTH")
	jwtValidator, jwtRequired, err := newJWTValidatorFromEnv()
	if err != nil {
		return nil, err
	}
	if env.IsProduction() && jwtValidator != nil && !jwtRequired {
		jwtRequired = true
	}
	if env.IsProduction() && allowAnonymous {
		return nil, errors.New("insecure auth disabled in production")
	}
	if env.IsProduction() && len(keys) == 0 {
		return nil, errors.New("api key required in production")
	}
	if len(keys) == 0 && jwtValidator == nil && !allowAnonymous {
		return nil, errors.New("auth not configured: set CORDUM_API_KEYS, CORDUM_API_KEY, or JWT")
	}
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	return &BasicAuthProvider{
		defaultTenant:        defaultTenant,
		keys:                 keys,
		requireAPIKey:        requireKey,
		allowAnonymous:       allowAnonymous,
		allowHeaderPrincipal: allowHeaderPrincipal,
		keysPath:             keysPath,
		keysModTime:          keysModTime,
		jwt:                  jwtValidator,
		jwtRequired:          jwtRequired,
		usageCtx:             context.Background(),
	}, nil
}

// SetUserStore sets the user store for user/password authentication.
func (b *BasicAuthProvider) SetUserStore(store UserStore) {
	b.userStore = store
}

// UserStore returns the user store if configured.
func (b *BasicAuthProvider) UserStore() UserStore {
	return b.userStore
}

// SetKeyStore sets the managed key store for runtime API key authentication.
func (b *BasicAuthProvider) SetKeyStore(ks KeyStore) {
	b.keyStore = ks
}

// SetUsageContext sets the base context for usage recording goroutines.
// When nil, it falls back to a background context.
func (b *BasicAuthProvider) SetUsageContext(ctx context.Context) {
	if b == nil {
		return
	}
	if ctx == nil {
		b.usageCtx = context.Background()
		return
	}
	b.usageCtx = ctx
}

func (b *BasicAuthProvider) usageContext() context.Context {
	if b == nil || b.usageCtx == nil {
		return context.Background()
	}
	return b.usageCtx
}

// DrainUsage waits for all pending API key usage recordings to complete.
func (b *BasicAuthProvider) DrainUsage() {
	if b == nil {
		return
	}
	b.usageWG.Wait()
}

func basicAuthProvider(auth AuthProvider) *BasicAuthProvider {
	if auth == nil {
		return nil
	}
	switch provider := auth.(type) {
	case *BasicAuthProvider:
		return provider
	case *CompositeAuthProvider:
		for _, candidate := range provider.providers {
			if basic, ok := candidate.(*BasicAuthProvider); ok {
				return basic
			}
		}
	}
	return nil
}

// ManagedKeyStore returns the key store if configured.
func (b *BasicAuthProvider) ManagedKeyStore() KeyStore {
	return b.keyStore
}

func (b *BasicAuthProvider) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	if r == nil {
		return nil, errors.New("request required")
	}
	b.maybeReloadKeys()
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		// Check session tokens before JWT
		if strings.HasPrefix(token, "session-") && b.userStore != nil {
			if redisStore, ok := b.userStore.(*RedisUserStore); ok {
				authCtx, err := redisStore.ValidateSession(r.Context(), token)
				if err == nil {
					authCtx.AuthSource = AuthSourceSession
					return authCtx, nil
				}
			}
		}
		if b.jwt == nil {
			return nil, errors.New("jwt auth not configured")
		}
		ctx, err := b.jwt.Validate(token)
		if err != nil {
			return nil, err
		}
		if ctx.Tenant == "" {
			ctx.Tenant = b.defaultTenant
		}
		ctx.AuthSource = AuthSourceJWT
		return ctx, nil
	}
	if b.jwtRequired {
		return nil, errors.New("jwt required")
	}
	key := normalizeAPIKey(r.Header.Get("X-API-Key"))
	if key == "" && (websocket.IsWebSocketUpgrade(r) || strings.TrimSpace(r.Header.Get("Sec-WebSocket-Protocol")) != "") {
		key = normalizeAPIKey(apiKeyFromWebSocket(r))
	}
	return b.authenticate(r.Context(), key, headerValue(r, "X-Principal-Id"))
}

func (b *BasicAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	b.maybeReloadKeys()
	key := ""
	principalID := ""
	jwtToken := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if raw := md.Get("authorization"); len(raw) > 0 {
			jwtToken = bearerToken(raw[0])
		}
		if jwtToken == "" {
			if raw := md.Get("Authorization"); len(raw) > 0 {
				jwtToken = bearerToken(raw[0])
			}
		}
		if raw := md.Get("x-api-key"); len(raw) > 0 {
			key = normalizeAPIKey(raw[0])
		}
		if key == "" {
			if raw := md.Get("api-key"); len(raw) > 0 {
				key = normalizeAPIKey(raw[0])
			}
		}
		if raw := md.Get("x-principal-id"); len(raw) > 0 {
			principalID = strings.TrimSpace(raw[0])
		}
	}
	if jwtToken != "" {
		if b.jwt == nil {
			return nil, errors.New("jwt auth not configured")
		}
		authCtx, err := b.jwt.Validate(jwtToken)
		if err != nil {
			return nil, err
		}
		if authCtx.Tenant == "" {
			authCtx.Tenant = b.defaultTenant
		}
		authCtx.AuthSource = AuthSourceJWT
		return authCtx, nil
	}
	if b.jwtRequired {
		return nil, errors.New("jwt required")
	}
	return b.authenticate(ctx, key, principalID)
}

func (b *BasicAuthProvider) authenticate(ctx context.Context, key, principalID string) (*AuthContext, error) {
	if b == nil {
		return &AuthContext{}, nil
	}
	if key == "" {
		if !b.allowAnonymous {
			return nil, errors.New("api key required")
		}
		if !b.allowHeaderPrincipal {
			principalID = ""
		}
		return &AuthContext{
			Tenant:      b.defaultTenant,
			PrincipalID: strings.TrimSpace(principalID),
			Role:        "anonymous",
		}, nil
	}
	// Check session tokens (from user/password login)
	if strings.HasPrefix(key, "session-") && b.userStore != nil {
		if redisStore, ok := b.userStore.(*RedisUserStore); ok {
			authCtx, err := redisStore.ValidateSession(ctx, key)
			if err == nil {
				authCtx.AuthSource = AuthSourceSession
				return authCtx, nil
			}
			// Session not found or invalid — fall through to API key check
		}
	}
	meta, ok := b.lookupKey(key)
	if !ok {
		// Fall back to managed key store (runtime-created keys in Redis)
		if b.keyStore != nil {
			mk, err := b.keyStore.ValidateKey(ctx, key)
			if err == nil {
				role := "user"
				for _, scope := range mk.Scopes {
					if scope == "admin" {
						role = "admin"
						break
					}
				}
				tenant := strings.TrimSpace(mk.Tenant)
				if tenant == "" {
					tenant = b.defaultTenant
				}
				b.usageWG.Add(1)
				go func(keyID string) {
					defer b.usageWG.Done()
					bgCtx, cancel := context.WithTimeout(b.usageContext(), 2*time.Second)
					defer cancel()
					if err := b.keyStore.RecordUsage(bgCtx, keyID); err != nil {
						slog.Warn("failed to record api key usage", "key_id", keyID, "error", err) // #nosec -- key id is validated and safe for logs.
					}
				}(mk.ID)
				return &AuthContext{
					APIKey:      key,
					Tenant:      tenant,
					PrincipalID: strings.TrimSpace(principalID),
					Role:        role,
					AuthSource:  AuthSourceAPIKey,
				}, nil
			}
		}
		return nil, errors.New("invalid api key")
	}
	if !meta.ExpiresAt.IsZero() && time.Now().After(meta.ExpiresAt) {
		return nil, errors.New("api key expired")
	}
	if meta.PrincipalID != "" {
		principalID = meta.PrincipalID
	} else if !b.allowHeaderPrincipal {
		principalID = ""
	}
	tenant := strings.TrimSpace(meta.Tenant)
	if tenant == "" {
		tenant = b.defaultTenant
	}
	role := normalizeRole(meta.Role)
	if role == "" {
		role = "admin"
	}
	return &AuthContext{
		APIKey:           key,
		Tenant:           tenant,
		PrincipalID:      strings.TrimSpace(principalID),
		Role:             role,
		AllowCrossTenant: meta.AllowCrossTenant,
		AuthSource:       AuthSourceAPIKey,
	}, nil
}

func (b *BasicAuthProvider) IsPublicPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/api/v1/auth/config" || path == "/api/v1/auth/login"
}

// AuthConfig returns the auth configuration for the dashboard.
// Implements AuthConfigProvider interface.
func (b *BasicAuthProvider) AuthConfig() AuthConfig {
	b.keysMu.RLock()
	hasKeys := len(b.keys) > 0
	b.keysMu.RUnlock()
	return AuthConfig{
		PasswordEnabled:  hasKeys,
		UserAuthEnabled:  b.userStore != nil,
		SAMLEnabled:      false,
		SAMLEnterprise:   true, // SSO is always an enterprise feature
		SessionTTL:       "24h",
		RequireRBAC:      false,
		RequirePrincipal: false,
		DefaultTenant:    b.defaultTenant,
	}
}

func (b *BasicAuthProvider) RequireRole(r *http.Request, roles ...string) error {
	if b == nil {
		return nil
	}
	if len(roles) == 0 {
		return nil
	}
	auth := authFromRequest(r)
	if auth == nil {
		return nil
	}
	role := normalizeRole(auth.Role)
	if role == "" {
		return errors.New("role required")
	}
	for _, candidate := range roles {
		if normalizeRole(candidate) == role {
			return nil
		}
	}
	return fmt.Errorf("role %s not permitted", role)
}

func (b *BasicAuthProvider) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	auth := authFromRequest(r)
	requested = strings.TrimSpace(requested)
	authTenant := ""
	if auth != nil {
		authTenant = strings.TrimSpace(auth.Tenant)
		if requested != "" && !auth.AllowCrossTenant && authTenant != "" && requested != authTenant {
			return "", errors.New("tenant access denied")
		}
	}
	if requested == "" {
		if authTenant != "" {
			return authTenant, nil
		}
		if b.defaultTenant != "" {
			return b.defaultTenant, nil
		}
		return strings.TrimSpace(fallback), nil
	}
	// SECURITY: At this point, either requested==authTenant
	// or AllowCrossTenant is true. Safe to return the requested tenant.
	if authTenant != "" {
		return requested, nil
	}
	if b.defaultTenant != "" && requested != b.defaultTenant {
		return "", errors.New("tenant access denied")
	}
	return requested, nil
}

func (b *BasicAuthProvider) RequireTenantAccess(r *http.Request, tenant string) error {
	auth := authFromRequest(r)
	tenant = strings.TrimSpace(tenant)
	if b == nil {
		return nil
	}
	if tenant == "" {
		return errors.New("tenant required")
	}
	if auth != nil {
		if auth.AllowCrossTenant {
			return nil
		}
		authTenant := strings.TrimSpace(auth.Tenant)
		if authTenant != "" && tenant != authTenant {
			return errors.New("tenant access denied")
		}
		if authTenant != "" {
			return nil
		}
	}
	if b.defaultTenant != "" && tenant != b.defaultTenant {
		return errors.New("tenant access denied")
	}
	return nil
}

func (b *BasicAuthProvider) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	if auth := authFromRequest(r); auth != nil {
		if auth.PrincipalID != "" {
			return auth.PrincipalID, nil
		}
	}
	if b.allowHeaderPrincipal {
		return headerValue(r, "X-Principal-Id"), nil
	}
	return "", nil
}

func loadBasicAPIKeys() (map[string]apiKeyMeta, bool, string, time.Time, bool, error) {
	keys := map[string]apiKeyMeta{}
	requireKey := false
	keysPath := strings.TrimSpace(os.Getenv("CORDUM_API_KEYS_PATH"))
	keysModTime := time.Time{}
	allowHeaderPrincipal := !env.IsProduction() || env.Bool("CORDUM_ALLOW_HEADER_PRINCIPAL")

	raw := strings.TrimSpace(os.Getenv("CORDUM_API_KEYS"))
	if raw != "" {
		entries, err := parseAPIKeys(raw)
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if err := mergeAPIKeyEntries(keys, entries); err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		requireKey = true
	}

	single := normalizeAPIKey(os.Getenv("CORDUM_API_KEY"))
	if single == "" {
		single = normalizeAPIKey(os.Getenv("API_KEY"))
		if single != "" {
			slog.Warn("API_KEY env var is deprecated, use CORDUM_API_KEY instead")
		}
	}
	if single != "" {
		keys[single] = apiKeyMeta{Role: "admin"}
		requireKey = true
	}

	if keysPath != "" {
		info, err := os.Stat(keysPath) // #nosec -- API keys path is configured by the operator.
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, fmt.Errorf("read api keys path: %w", err)
		}
		keysModTime = info.ModTime()
		rawFile, err := os.ReadFile(keysPath) // #nosec -- API keys path is configured by the operator.
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, fmt.Errorf("read api keys: %w", err)
		}
		entries, err := parseAPIKeys(string(rawFile))
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if err := mergeAPIKeyEntries(keys, entries); err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if len(entries) > 0 {
			requireKey = true
		}
	}

	return keys, requireKey, keysPath, keysModTime, allowHeaderPrincipal, nil
}

func parseAPIKeys(raw string) ([]apiKeyEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var entries []apiKeyEntry
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, fmt.Errorf("parse CORDUM_API_KEYS: %w", err)
		}
		return entries, nil
	}
	if strings.HasPrefix(raw, "{") {
		entries := map[string]apiKeyEntry{}
		if err := json.Unmarshal([]byte(raw), &entries); err == nil {
			out := make([]apiKeyEntry, 0, len(entries))
			for key, entry := range entries {
				entry.Key = key
				out = append(out, entry)
			}
			return out, nil
		}
		var wrapped struct {
			Keys []apiKeyEntry `json:"keys"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
			return nil, fmt.Errorf("parse CORDUM_API_KEYS: %w", err)
		}
		return wrapped.Keys, nil
	}
	parts := strings.Split(raw, ",")
	entries := make([]apiKeyEntry, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		chunks := strings.Split(part, ":")
		entry := apiKeyEntry{}
		switch len(chunks) {
		case 1:
			entry.Key = strings.TrimSpace(chunks[0])
		case 2:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
		case 3:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
			entry.Role = strings.TrimSpace(chunks[2])
		default:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
			entry.Role = strings.TrimSpace(chunks[2])
			entry.PrincipalID = strings.TrimSpace(chunks[3])
		}
		if entry.Key != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func headerValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(name))
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "secops" || role == "operator" {
		return "admin"
	}
	return role
}

func mergeAPIKeyEntries(keys map[string]apiKeyMeta, entries []apiKeyEntry) error {
	for _, entry := range entries {
		if entry.Key == "" {
			continue
		}
		meta := apiKeyMeta{
			Tenant:           strings.TrimSpace(entry.Tenant),
			Role:             normalizeRole(entry.Role),
			PrincipalID:      strings.TrimSpace(entry.PrincipalID),
			AllowCrossTenant: entry.AllowCrossTenant,
		}
		if entry.ExpiresAt != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.ExpiresAt))
			if err != nil {
				return fmt.Errorf("parse api key expiry: %w", err)
			}
			meta.ExpiresAt = ts
		}
		if meta.Role == "" {
			meta.Role = "admin"
		}
		keys[entry.Key] = meta
	}
	return nil
}

func (b *BasicAuthProvider) lookupKey(key string) (apiKeyMeta, bool) {
	b.keysMu.RLock()
	defer b.keysMu.RUnlock()
	meta, ok := b.keys[key]
	return meta, ok
}

func (b *BasicAuthProvider) maybeReloadKeys() {
	if b == nil || b.keysPath == "" {
		return
	}
	info, err := os.Stat(b.keysPath)
	if err != nil {
		log.Printf("auth: failed to stat api keys: %v", err)
		return
	}
	mod := info.ModTime()
	b.keysMu.RLock()
	needsReload := mod.After(b.keysModTime)
	b.keysMu.RUnlock()
	if !needsReload {
		return
	}
	keys, requireKey, keysPath, keysModTime, allowHeaderPrincipal, err := loadBasicAPIKeys()
	if err != nil {
		log.Printf("auth: failed to reload api keys: %v", err)
		return
	}
	b.keysMu.Lock()
	b.keys = keys
	b.requireAPIKey = requireKey
	b.keysPath = keysPath
	b.keysModTime = keysModTime
	b.allowHeaderPrincipal = allowHeaderPrincipal
	b.keysMu.Unlock()
}

// ---------------------------------------------------------------------------
// CompositeAuthProvider & OIDCAuthAdapter  (was composite_auth.go)
// ---------------------------------------------------------------------------

// CompositeAuthProvider tries multiple AuthProvider implementations in order.
// Authentication succeeds if ANY provider accepts the request. Role checks,
// tenant resolution, and principal resolution delegate to the primary provider
// (first in the list) since they operate on the AuthContext already stored in
// the request context.
type CompositeAuthProvider struct {
	providers []AuthProvider
	primary   AuthProvider // first provider — used for non-auth methods
}

// NewCompositeAuthProvider creates a composite that tries providers in order.
// At least one provider is required.
func NewCompositeAuthProvider(providers ...AuthProvider) (*CompositeAuthProvider, error) {
	if len(providers) == 0 {
		return nil, errors.New("composite auth: at least one provider required")
	}
	return &CompositeAuthProvider{
		providers: providers,
		primary:   providers[0],
	}, nil
}

// AuthenticateHTTP tries each provider in order — returns the first success.
func (c *CompositeAuthProvider) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	var lastErr error
	for _, p := range c.providers {
		authCtx, err := p.AuthenticateHTTP(r)
		if err == nil {
			return authCtx, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// AuthenticateGRPC tries each provider in order — returns the first success.
func (c *CompositeAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	var lastErr error
	for _, p := range c.providers {
		authCtx, err := p.AuthenticateGRPC(ctx)
		if err == nil {
			return authCtx, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// RequireRole delegates to the primary provider.
func (c *CompositeAuthProvider) RequireRole(r *http.Request, roles ...string) error {
	return c.primary.RequireRole(r, roles...)
}

// ResolveTenant delegates to the primary provider.
func (c *CompositeAuthProvider) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	return c.primary.ResolveTenant(r, requested, fallback)
}

// RequireTenantAccess delegates to the primary provider.
func (c *CompositeAuthProvider) RequireTenantAccess(r *http.Request, tenant string) error {
	return c.primary.RequireTenantAccess(r, tenant)
}

// ResolvePrincipal delegates to the primary provider.
func (c *CompositeAuthProvider) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	return c.primary.ResolvePrincipal(r, requested)
}

// IsPublicPath delegates to any provider that implements PublicPathProvider.
func (c *CompositeAuthProvider) IsPublicPath(path string) bool {
	for _, p := range c.providers {
		if pp, ok := p.(PublicPathProvider); ok && pp.IsPublicPath(path) {
			return true
		}
	}
	return false
}

// AuthConfig delegates to the first provider that implements AuthConfigProvider.
func (c *CompositeAuthProvider) AuthConfig() AuthConfig {
	for _, p := range c.providers {
		if acp, ok := p.(AuthConfigProvider); ok {
			cfg := acp.AuthConfig()
			// Enrich with OIDC info if any provider is OIDC
			for _, op := range c.providers {
				if oidc, ok := op.(*OIDCAuthAdapter); ok {
					cfg.OIDCEnabled = true
					cfg.OIDCIssuer = oidc.provider.cfg.IssuerURL
					break
				}
			}
			return cfg
		}
	}
	return AuthConfig{}
}

// RegisterRoutes delegates to any provider that implements RouteRegistrar.
func (c *CompositeAuthProvider) RegisterRoutes(mux *http.ServeMux, wrap func(string, http.HandlerFunc) http.HandlerFunc) {
	for _, p := range c.providers {
		if rr, ok := p.(RouteRegistrar); ok {
			rr.RegisterRoutes(mux, wrap)
		}
	}
}

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

func (a *OIDCAuthAdapter) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	token := bearerToken(r.Header.Get("Authorization"))
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
		token = bearerToken(raw[0])
	}
	if token == "" {
		if raw := md.Get("Authorization"); len(raw) > 0 {
			token = bearerToken(raw[0])
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

// ---------------------------------------------------------------------------
// Server authorize helpers  (was authorize.go)
// ---------------------------------------------------------------------------

func (s *server) requireRole(r *http.Request, roles ...string) error {
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireRole(r, roles...)
}

func (s *server) resolveTenant(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	headerTenant := headerValue(r, "X-Tenant-ID")
	// Fall back to auth context tenant (e.g. from session token)
	if headerTenant == "" {
		if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" {
			headerTenant = authCtx.Tenant
		}
	}
	if headerTenant == "" {
		return "", errors.New("tenant id required")
	}
	if requested == "" {
		requested = headerTenant
	} else if requested != headerTenant {
		return "", errors.New("tenant header mismatch")
	}
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolveTenant(r, requested, s.tenant)
}

func (s *server) requireTenantAccess(r *http.Request, tenant string) error {
	tenant = strings.TrimSpace(tenant)
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireTenantAccess(r, tenant)
}

func (s *server) resolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolvePrincipal(r, requested)
}

// ---------------------------------------------------------------------------
// AuthConfig type & handler  (was auth_config.go)
// ---------------------------------------------------------------------------

// AuthConfig describes authentication capabilities for the dashboard.
type AuthConfig struct {
	PasswordEnabled  bool   `json:"password_enabled"`
	UserAuthEnabled  bool   `json:"user_auth_enabled"`
	SAMLEnabled      bool   `json:"saml_enabled"`
	SAMLEnterprise   bool   `json:"saml_enterprise"`
	SAMLLoginURL     string `json:"saml_login_url,omitempty"`
	SAMLMetadataURL  string `json:"saml_metadata_url,omitempty"`
	SessionTTL       string `json:"session_ttl"`
	RequireRBAC      bool   `json:"require_rbac"`
	RequirePrincipal bool   `json:"require_principal"`
	DefaultTenant    string `json:"default_tenant"`
	OIDCEnabled      bool   `json:"oidc_enabled,omitempty"`
	OIDCIssuer       string `json:"oidc_issuer,omitempty"`
}

func (s *server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	defaultTenant := strings.TrimSpace(s.tenant)
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	resp := AuthConfig{
		PasswordEnabled:  false,
		SAMLEnabled:      false,
		SessionTTL:       "0s",
		RequireRBAC:      false,
		RequirePrincipal: false,
		DefaultTenant:    defaultTenant,
	}
	if provider, ok := s.auth.(AuthConfigProvider); ok {
		resp = provider.AuthConfig()
	}
	if strings.TrimSpace(resp.DefaultTenant) == "" {
		resp.DefaultTenant = defaultTenant
	}
	if strings.TrimSpace(resp.SessionTTL) == "" {
		resp.SessionTTL = "0s"
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}
