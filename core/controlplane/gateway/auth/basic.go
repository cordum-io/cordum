package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// BasicAuthProvider
// ---------------------------------------------------------------------------

// BasicAuthProvider implements AuthProvider using static API keys, JWT, sessions,
// and managed keys (in Redis).
type BasicAuthProvider struct {
	defaultTenant        string
	keys                 map[string]apiKeyMeta
	keyHashes            map[string]apiKeyMeta // SHA-256(key) → meta for timing-safe lookup
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

// NewBasicAuthProvider creates a BasicAuthProvider configured from environment variables.
func NewBasicAuthProvider(defaultTenant string) (*BasicAuthProvider, error) {
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
		keyHashes:            buildKeyHashes(keys),
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

// ExtractBasicAuth extracts the BasicAuthProvider from an AuthProvider,
// looking through CompositeAuthProvider wrappers as needed.
func ExtractBasicAuth(auth AuthProvider) *BasicAuthProvider {
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
	if token := BearerToken(r.Header.Get("Authorization")); token != "" {
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
	key := NormalizeAPIKey(r.Header.Get("X-API-Key"))
	if key == "" && (websocket.IsWebSocketUpgrade(r) || strings.TrimSpace(r.Header.Get("Sec-WebSocket-Protocol")) != "") {
		key = NormalizeAPIKey(APIKeyFromWebSocket(r))
	}
	return b.authenticate(r.Context(), key, HeaderValue(r, "X-Principal-Id"))
}

func (b *BasicAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	b.maybeReloadKeys()
	key := ""
	principalID := ""
	jwtToken := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if raw := md.Get("authorization"); len(raw) > 0 {
			jwtToken = BearerToken(raw[0])
		}
		if jwtToken == "" {
			if raw := md.Get("Authorization"); len(raw) > 0 {
				jwtToken = BearerToken(raw[0])
			}
		}
		if raw := md.Get("x-api-key"); len(raw) > 0 {
			key = NormalizeAPIKey(raw[0])
		}
		if key == "" {
			if raw := md.Get("api-key"); len(raw) > 0 {
				key = NormalizeAPIKey(raw[0])
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

// roleFromScopes maps API key scopes to a role, using the highest privilege
// scope present. Order: admin > operator > viewer. Keys with no scopes
// default to viewer (principle of least privilege).
func roleFromScopes(scopes []string) string {
	role := "viewer"
	for _, scope := range scopes {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "admin":
			return "admin" // highest — short-circuit
		case "write", "operator":
			role = "operator"
		case "read", "viewer":
			// only upgrade from default viewer
		}
	}
	return role
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
				role := roleFromScopes(mk.Scopes)
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
	role := NormalizeRole(meta.Role)
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
	auth := FromRequest(r)
	if auth == nil {
		return errors.New("authentication required")
	}
	role := NormalizeRole(auth.Role)
	if role == "" {
		return errors.New("role required")
	}
	for _, candidate := range roles {
		if NormalizeRole(candidate) == role {
			return nil
		}
	}
	return fmt.Errorf("role %s not permitted", role)
}

func (b *BasicAuthProvider) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	auth := FromRequest(r)
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
	auth := FromRequest(r)
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
	if auth := FromRequest(r); auth != nil {
		if auth.PrincipalID != "" {
			return auth.PrincipalID, nil
		}
	}
	if b.allowHeaderPrincipal {
		return HeaderValue(r, "X-Principal-Id"), nil
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// API key loading
// ---------------------------------------------------------------------------

func loadBasicAPIKeys() (map[string]apiKeyMeta, bool, string, time.Time, bool, error) {
	keys := map[string]apiKeyMeta{}
	requireKey := false
	keysPath := strings.TrimSpace(os.Getenv("CORDUM_API_KEYS_PATH"))
	keysModTime := time.Time{}
	allowHeaderPrincipal := !env.IsProduction() || env.Bool("CORDUM_ALLOW_HEADER_PRINCIPAL")

	raw := strings.TrimSpace(os.Getenv("CORDUM_API_KEYS"))
	if raw != "" {
		entries, err := ParseAPIKeys(raw)
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if err := MergeAPIKeyEntries(keys, entries); err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		requireKey = true
	}

	single := NormalizeAPIKey(os.Getenv("CORDUM_API_KEY"))
	if single == "" {
		single = NormalizeAPIKey(os.Getenv("API_KEY"))
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
		entries, err := ParseAPIKeys(string(rawFile))
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if err := MergeAPIKeyEntries(keys, entries); err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, err
		}
		if len(entries) > 0 {
			requireKey = true
		}
	}

	return keys, requireKey, keysPath, keysModTime, allowHeaderPrincipal, nil
}

// hashAPIKey returns a hex-encoded SHA-256 digest of the given API key.
// Used to build a timing-safe lookup index: the map key is the hash,
// so Go's map lookup timing correlates with hash values (opaque) rather
// than the original secret. This is NOT password hashing — API keys are
// high-entropy random tokens; SHA-256 is appropriate for indexing them.
func hashAPIKey(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken)) // #nosec G703 -- SHA-256 for lookup index, not password storage
	return hex.EncodeToString(h[:])
}

// buildKeyHashes creates the hash-indexed lookup map from the raw key map.
func buildKeyHashes(keys map[string]apiKeyMeta) map[string]apiKeyMeta {
	hashes := make(map[string]apiKeyMeta, len(keys))
	for k, meta := range keys {
		hashes[hashAPIKey(k)] = meta
	}
	return hashes
}

func (b *BasicAuthProvider) lookupKey(key string) (apiKeyMeta, bool) {
	b.keysMu.RLock()
	defer b.keysMu.RUnlock()
	// SECURITY: Look up by SHA-256 hash of the key rather than the raw key.
	// This prevents timing side-channels from leaking information about
	// which key prefixes exist in the map.
	meta, ok := b.keyHashes[hashAPIKey(key)]
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
	b.keyHashes = buildKeyHashes(keys)
	b.requireAPIKey = requireKey
	b.keysPath = keysPath
	b.keysModTime = keysModTime
	b.allowHeaderPrincipal = allowHeaderPrincipal
	b.keysMu.Unlock()
}
