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
				return authCtx, nil
			}
			// Session not found or invalid — fall through to API key check
		}
	}
	meta, ok := b.lookupKey(key)
	if !ok {
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
		info, err := os.Stat(keysPath)
		if err != nil {
			return nil, false, "", time.Time{}, allowHeaderPrincipal, fmt.Errorf("read api keys path: %w", err)
		}
		keysModTime = info.ModTime()
		// #nosec G304 -- API keys path is configured by the operator.
		rawFile, err := os.ReadFile(keysPath)
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
