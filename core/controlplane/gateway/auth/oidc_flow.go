package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
)

const (
	OIDCLoginPath      = "/api/v1/auth/sso/oidc/login"
	OIDCCallbackPath   = "/api/v1/auth/sso/oidc/callback"
	oidcStateKeyPrefix = "oidc:state:"
)

var errOIDCStateNotFound = errors.New("oidc state not found")

type oidcStateEntry struct {
	Nonce     string    `json:"nonce,omitempty"`
	Redirect  string    `json:"redirect,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type oidcStateStore interface {
	Put(ctx context.Context, state, nonce, redirect string, ttl time.Duration) error
	Get(ctx context.Context, state string) (oidcStateEntry, error)
	Delete(ctx context.Context, state string) error
}

type redisOIDCStateStore struct {
	client *redis.Client
}

func (s *redisOIDCStateStore) Put(ctx context.Context, state, nonce, redirect string, ttl time.Duration) error {
	entry := oidcStateEntry{Nonce: nonce, Redirect: redirect, CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal oidc state: %w", err)
	}
	return s.client.Set(ctx, oidcStateKeyPrefix+state, data, ttl).Err()
}

func (s *redisOIDCStateStore) Get(ctx context.Context, state string) (oidcStateEntry, error) {
	data, err := s.client.Get(ctx, oidcStateKeyPrefix+state).Bytes()
	if err == redis.Nil {
		return oidcStateEntry{}, errOIDCStateNotFound
	}
	if err != nil {
		return oidcStateEntry{}, fmt.Errorf("get oidc state: %w", err)
	}
	var entry oidcStateEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return oidcStateEntry{}, fmt.Errorf("unmarshal oidc state: %w", err)
	}
	return entry, nil
}

func (s *redisOIDCStateStore) Delete(ctx context.Context, state string) error {
	return s.client.Del(ctx, oidcStateKeyPrefix+state).Err()
}

type memoryOIDCStateStore struct {
	mu      sync.RWMutex
	entries map[string]oidcStateEntry
}

func newMemoryOIDCStateStore() *memoryOIDCStateStore {
	return &memoryOIDCStateStore{entries: make(map[string]oidcStateEntry)}
}

func (s *memoryOIDCStateStore) Put(_ context.Context, state, nonce, redirect string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[state] = oidcStateEntry{Nonce: nonce, Redirect: redirect, CreatedAt: time.Now().UTC()}
	return nil
}

func (s *memoryOIDCStateStore) Get(_ context.Context, state string) (oidcStateEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[state]
	if !ok {
		return oidcStateEntry{}, errOIDCStateNotFound
	}
	return entry, nil
}

func (s *memoryOIDCStateStore) Delete(_ context.Context, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, state)
	return nil
}

// OIDCFlowAdapter exposes browser-based OIDC sign-in while delegating regular
// request authentication back to the primary auth provider.
type OIDCFlowAdapter struct {
	enabled       bool
	provider      *OIDCProvider
	oauthConfig   *oauth2.Config
	defaultTenant string
	defaultRole   string
	autoProvision bool
	syncRoles     bool
	emailClaim    string
	nameClaim     string
	usernameClaim string
	stateTTL      time.Duration
	userStore     UserStore
	sessionStore  samlSessionStore
	stateStore    oidcStateStore
	redirectURL   string
	loginURL      string
	resolver      *licensing.EntitlementResolver
	now           func() time.Time
	newState      func() string
	newNonce      func() string
}

func NewOIDCFlowAdapter(provider *OIDCProvider, store UserStore, defaultTenant string, resolver *licensing.EntitlementResolver) (*OIDCFlowAdapter, error) {
	cfg := OIDCConfig{}
	if provider != nil {
		cfg = provider.Config()
	}
	if provider == nil || strings.TrimSpace(cfg.ClientID) == "" {
		return &OIDCFlowAdapter{
			defaultTenant: normalizeSAMLTenant(defaultTenant),
			defaultRole:   "viewer",
			resolver:      resolver,
			now:           func() time.Time { return time.Now().UTC() },
			newState:      newSAMLStateToken,
			newNonce:      newSAMLStateToken,
		}, nil
	}
	if store == nil {
		return nil, errors.New("oidc sso requires user store")
	}
	sessionStore, ok := store.(samlSessionStore)
	if !ok {
		return nil, errors.New("oidc sso requires session-capable user store")
	}
	callbackURL, err := validateOIDCURL(cfg.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("oidc redirect uri: %w", err)
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, errors.New("oidc: client secret required when client id is configured")
	}
	if strings.TrimSpace(cfg.AuthorizationURL) == "" || strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, errors.New("oidc: discovery document missing authorization or token endpoint")
	}
	stateStore := oidcStateStore(newMemoryOIDCStateStore())
	if redisStore, ok := store.(*RedisUserStore); ok && redisStore != nil && redisStore.client != nil {
		stateStore = &redisOIDCStateStore{client: redisStore.client}
	}
	stateTTL := 10 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("CORDUM_OIDC_STATE_TTL")); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr == nil && parsed > 0 {
			stateTTL = parsed
		}
	}
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  callbackURL.String(),
		Scopes:       append([]string(nil), cfg.Scopes...),
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthorizationURL,
			TokenURL: cfg.TokenURL,
		},
	}
	loginURL := callbackURL.ResolveReference(&url.URL{Path: OIDCLoginPath}).String()
	return &OIDCFlowAdapter{
		enabled:       true,
		provider:      provider,
		oauthConfig:   oauthCfg,
		defaultTenant: normalizeSAMLTenant(defaultTenant),
		defaultRole:   cfg.DefaultRole,
		autoProvision: cfg.AutoProvision,
		syncRoles:     cfg.SyncRoles,
		emailClaim:    cfg.EmailClaim,
		nameClaim:     cfg.NameClaim,
		usernameClaim: cfg.UsernameClaim,
		stateTTL:      stateTTL,
		userStore:     store,
		sessionStore:  sessionStore,
		stateStore:    stateStore,
		redirectURL:   resolveDefaultSAMLRedirectURL(callbackURL),
		loginURL:      loginURL,
		resolver:      resolver,
		now:           func() time.Time { return time.Now().UTC() },
		newState:      newSAMLStateToken,
		newNonce:      newSAMLStateToken,
	}, nil
}

func (a *OIDCFlowAdapter) Enabled() bool {
	return a != nil && a.enabled
}

func (a *OIDCFlowAdapter) entitlementEnabled() (bool, string) {
	entitlements := licensing.DefaultEntitlements(licensing.PlanCommunity)
	if a != nil && a.resolver != nil {
		entitlements = a.resolver.Entitlements()
	}
	if !entitlements.SSO {
		return false, "sso"
	}
	return true, ""
}

func (a *OIDCFlowAdapter) requireEntitlement(w http.ResponseWriter) bool {
	if a == nil || !a.enabled {
		writeSAMLForbidden(w, "sso", "OIDC SSO is not configured")
		return false
	}
	allowed, limit := a.entitlementEnabled()
	if allowed {
		return true
	}
	writeSAMLForbidden(w, limit, "OIDC SSO requires the SSO entitlement")
	return false
}

func (a *OIDCFlowAdapter) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	return nil, errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return nil, errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) RequireRole(*http.Request, ...string) error {
	return errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) ResolveTenant(*http.Request, string, string) (string, error) {
	return "", errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) RequireTenantAccess(*http.Request, string) error {
	return errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) ResolvePrincipal(*http.Request, string) (string, error) {
	return "", errors.New("oidc flow: delegate to primary")
}

func (a *OIDCFlowAdapter) IsPublicPath(path string) bool {
	if a == nil || !a.enabled {
		return false
	}
	switch strings.TrimSpace(path) {
	case OIDCLoginPath, OIDCCallbackPath:
		return true
	default:
		return false
	}
}

func (a *OIDCFlowAdapter) AuthConfig() AuthConfig {
	cfg := AuthConfig{
		OIDCEnabled: false,
		SessionTTL:  authSessionTTLString(),
	}
	if a == nil || a.provider == nil {
		return cfg
	}
	providerCfg := a.provider.Config()
	cfg.OIDCIssuer = providerCfg.IssuerURL
	cfg.OIDCLoginURL = a.loginURL
	cfg.OIDCClientID = providerCfg.ClientID
	cfg.OIDCRedirectURI = providerCfg.RedirectURI
	cfg.OIDCScopes = append([]string(nil), providerCfg.Scopes...)
	cfg.OIDCGroupsClaim = providerCfg.GroupsClaim
	cfg.OIDCGroupRoleMapping = cloneStringMap(providerCfg.GroupRoleMapping)
	cfg.OIDCClientSecretMasked = maskOIDCSecret(providerCfg.ClientSecret)
	if allowed, _ := a.entitlementEnabled(); allowed {
		cfg.OIDCEnabled = true
	}
	return cfg
}

func (a *OIDCFlowAdapter) UpdateOIDCGroupRoleMapping(groupsClaim string, mapping map[string]string) (AuthConfig, error) {
	if a == nil || a.provider == nil {
		return AuthConfig{}, errors.New("oidc flow: provider unavailable")
	}
	if _, err := a.provider.UpdateGroupRoleMapping(groupsClaim, mapping); err != nil {
		return AuthConfig{}, err
	}
	return a.AuthConfig(), nil
}

func (a *OIDCFlowAdapter) RegisterRoutes(mux *http.ServeMux, wrap func(string, http.HandlerFunc) http.HandlerFunc) {
	if a == nil || !a.enabled || mux == nil {
		return
	}
	apply := func(route string, fn http.HandlerFunc) http.HandlerFunc {
		if wrap == nil {
			return fn
		}
		return wrap(route, fn)
	}
	mux.HandleFunc("GET "+OIDCLoginPath, apply(OIDCLoginPath, a.handleLogin))
	mux.HandleFunc("GET "+OIDCCallbackPath, apply(OIDCCallbackPath, a.handleCallback))
}

func (a *OIDCFlowAdapter) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a == nil || !a.enabled {
		http.NotFound(w, r)
		return
	}
	if !a.requireEntitlement(w) {
		return
	}
	state := a.newState()
	nonce := a.newNonce()
	redirect := a.resolveRedirectTarget(strings.TrimSpace(r.URL.Query().Get("redirect")))
	if err := a.stateStore.Put(r.Context(), state, nonce, redirect, a.stateTTL); err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to persist OIDC state")
		return
	}
	authURL := a.oauthConfig.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (a *OIDCFlowAdapter) handleCallback(w http.ResponseWriter, r *http.Request) {
	if a == nil || !a.enabled {
		http.NotFound(w, r)
		return
	}
	if !a.requireEntitlement(w) {
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	entry, err := a.loadOIDCState(r.Context(), state)
	if err != nil {
		if errors.Is(err, errOIDCStateNotFound) {
			writeSAMLError(w, http.StatusUnauthorized, "invalid OIDC state")
			return
		}
		writeSAMLError(w, http.StatusInternalServerError, "failed to load OIDC state")
		return
	}
	if state != "" {
		defer func() {
			if err := a.stateStore.Delete(context.Background(), state); err != nil {
				slog.Warn("failed to delete OIDC state", "error", err)
			}
		}()
	}

	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		a.respondWithError(w, r, entry.Redirect, http.StatusUnauthorized, providerError, strings.TrimSpace(r.URL.Query().Get("error_description")))
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		a.respondWithError(w, r, entry.Redirect, http.StatusBadRequest, "invalid_request", "OIDC callback missing code")
		return
	}

	exchangeCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	token, err := a.oauthConfig.Exchange(exchangeCtx, code)
	if err != nil {
		a.respondWithError(w, r, entry.Redirect, http.StatusUnauthorized, "token_exchange_failed", "failed to exchange OIDC code")
		return
	}

	idToken, _ := token.Extra("id_token").(string)
	if strings.TrimSpace(idToken) == "" {
		a.respondWithError(w, r, entry.Redirect, http.StatusUnauthorized, "missing_id_token", "OIDC provider did not return an id_token")
		return
	}

	authCtx, claims, err := a.provider.validateJWT(idToken, a.oauthConfig.ClientID)
	if err != nil {
		a.respondWithError(w, r, entry.Redirect, http.StatusUnauthorized, "invalid_id_token", err.Error())
		return
	}
	if expectedNonce := strings.TrimSpace(entry.Nonce); expectedNonce != "" {
		if actualNonce := strings.TrimSpace(claimString(claims, "nonce")); actualNonce != expectedNonce {
			a.respondWithError(w, r, entry.Redirect, http.StatusUnauthorized, "invalid_nonce", "OIDC nonce validation failed")
			return
		}
	}

	if accessToken := strings.TrimSpace(token.AccessToken); accessToken != "" {
		if userInfo, err := a.fetchUserInfo(exchangeCtx, accessToken); err == nil {
			mergeOIDCClaims(claims, userInfo)
			authCtx = a.provider.authFromClaims(claims)
		} else if !errors.Is(err, errOIDCUserInfoUnavailable) {
			slog.Warn("oidc userinfo lookup failed", "error", err)
		}
	}

	user, err := a.resolveOIDCUser(r.Context(), authCtx, claims)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrUserNotFound) {
			status = http.StatusForbidden
		}
		a.respondWithError(w, r, entry.Redirect, status, "identity_resolution_failed", err.Error())
		return
	}

	now := a.now()
	if user.Role == "" {
		user.Role = a.defaultRole
	}
	user.UpdatedAt = now
	sessionToken, err := newSessionToken()
	if err != nil {
		a.respondWithError(w, r, entry.Redirect, http.StatusInternalServerError, "session_creation_failed", "failed to create session token")
		return
	}
	ttl := authSessionTTL()
	if err := a.sessionStore.StoreSession(r.Context(), sessionToken, user, ttl); err != nil {
		a.respondWithError(w, r, entry.Redirect, http.StatusInternalServerError, "session_creation_failed", "failed to persist session")
		return
	}
	expiresAt := now.Add(ttl)
	SetSessionCookie(w, r, sessionToken, expiresAt)

	redirect := strings.TrimSpace(entry.Redirect)
	if redirect == "" {
		redirect = a.redirectURL
	}
	if redirect == "" {
		writeJSON(w, map[string]any{
			"token":      sessionToken,
			"expires_at": expiresAt.Format(time.RFC3339),
			"user":       oidcUserPayload(user, now),
		})
		return
	}
	redirectURL, err := url.Parse(redirect)
	if err != nil {
		writeSAMLError(w, http.StatusBadRequest, "invalid redirect")
		return
	}
	fragment := url.Values{}
	fragment.Set("token", sessionToken)
	fragment.Set("expires_at", expiresAt.Format(time.RFC3339))
	fragment.Set("user_id", user.ID)
	fragment.Set("username", user.Username)
	fragment.Set("email", user.Email)
	fragment.Set("display_name", user.DisplayName)
	fragment.Set("role", user.Role)
	fragment.Set("tenant", user.Tenant)
	fragment.Set("source", "oidc")
	redirectURL.Fragment = fragment.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func (a *OIDCFlowAdapter) loadOIDCState(ctx context.Context, state string) (oidcStateEntry, error) {
	if strings.TrimSpace(state) == "" {
		return oidcStateEntry{}, errOIDCStateNotFound
	}
	return a.stateStore.Get(ctx, state)
}

var errOIDCUserInfoUnavailable = errors.New("oidc userinfo unavailable")

func (a *OIDCFlowAdapter) fetchUserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	providerCfg := a.provider.Config()
	if strings.TrimSpace(providerCfg.UserInfoURL) == "" {
		return nil, errOIDCUserInfoUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, providerCfg.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build oidc userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := a.provider.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch oidc userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc userinfo returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read oidc userinfo: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("parse oidc userinfo: %w", err)
	}
	return claims, nil
}

func mergeOIDCClaims(claims, incoming map[string]any) {
	if claims == nil || incoming == nil {
		return
	}
	for key, value := range incoming {
		if _, exists := claims[key]; exists {
			continue
		}
		claims[key] = value
	}
}

func (a *OIDCFlowAdapter) resolveOIDCUser(ctx context.Context, authCtx *AuthContext, claims map[string]any) (*User, error) {
	tenant := normalizeSAMLTenant("")
	if authCtx != nil {
		tenant = normalizeSAMLTenant(authCtx.Tenant)
	}
	if tenant == "" {
		tenant = normalizeSAMLTenant(a.defaultTenant)
	}
	if tenant == "" {
		tenant = "default"
	}

	email := firstOIDCClaim(claims, a.emailClaim, "email")
	name := firstOIDCClaim(claims, a.nameClaim, "name", "preferred_username")
	identifier := firstOIDCClaim(claims, a.usernameClaim, "preferred_username", "email", "sub")
	if identifier == "" {
		identifier = name
	}
	if identifier == "" {
		return nil, errors.New("sso identity missing")
	}
	username := normalizeSAMLUsername(identifier)
	if username == "" {
		return nil, errors.New("sso identity missing")
	}

	selectedRole := a.defaultRole
	if authCtx != nil && strings.TrimSpace(authCtx.Role) != "" {
		selectedRole = normalizeOIDCResolvedRole(authCtx.Role, true)
	}
	if selectedRole == "" {
		selectedRole = "viewer"
	}

	user, err := a.userStore.GetByUsername(ctx, username, tenant)
	if errors.Is(err, ErrUserNotFound) && email != "" {
		user, err = a.userStore.GetByEmail(ctx, email, tenant)
	}
	if err != nil {
		if !errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("load OIDC user: %w", err)
		}
		if !a.autoProvision {
			return nil, ErrUserNotFound
		}
		user = &User{
			Username:    username,
			Email:       strings.TrimSpace(email),
			DisplayName: strings.TrimSpace(name),
			Tenant:      tenant,
			Role:        selectedRole,
		}
		if err := a.userStore.Create(ctx, user, newProvisionedPassword()); err != nil {
			return nil, fmt.Errorf("provision OIDC user: %w", err)
		}
		return a.userStore.GetByUsername(ctx, username, tenant)
	}
	if user.Disabled {
		return nil, ErrUserDisabled
	}

	shouldUpdate := false
	if a.syncRoles && selectedRole != "" && selectedRole != user.Role {
		user.Role = selectedRole
		shouldUpdate = true
	}
	if trimmedName := strings.TrimSpace(name); trimmedName != "" && trimmedName != user.DisplayName {
		user.DisplayName = trimmedName
		shouldUpdate = true
	}
	if trimmedEmail := strings.TrimSpace(email); trimmedEmail != "" && trimmedEmail != user.Email {
		user.Email = trimmedEmail
		shouldUpdate = true
	}
	if shouldUpdate {
		if err := a.userStore.Update(ctx, user); err != nil {
			return nil, fmt.Errorf("update OIDC user: %w", err)
		}
	}
	if user.Role == "" {
		user.Role = selectedRole
	}
	return user, nil
}

func firstOIDCClaim(claims map[string]any, keys ...string) string {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value := strings.TrimSpace(claimString(claims, key)); value != "" {
			return value
		}
	}
	return ""
}

func (a *OIDCFlowAdapter) resolveRedirectTarget(raw string) string {
	fallback := strings.TrimSpace(a.redirectURL)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	base := fallback
	if base == "" {
		base = strings.TrimSpace(a.loginURL)
	}
	baseURL, baseErr := url.Parse(base)
	target, err := url.Parse(raw)
	if err != nil {
		return fallback
	}
	if target.IsAbs() {
		if baseErr == nil && baseURL != nil && baseURL.IsAbs() && !sameOrigin(baseURL, target) {
			return fallback
		}
		return target.String()
	}
	if strings.HasPrefix(raw, "//") {
		return fallback
	}
	if baseErr == nil && baseURL != nil && baseURL.IsAbs() {
		return baseURL.ResolveReference(target).String()
	}
	return fallback
}

func (a *OIDCFlowAdapter) respondWithError(w http.ResponseWriter, r *http.Request, redirect string, status int, code, description string) {
	redirect = strings.TrimSpace(redirect)
	if redirect != "" {
		if redirectURL, err := url.Parse(redirect); err == nil {
			fragment := url.Values{}
			fragment.Set("error", strings.TrimSpace(code))
			if strings.TrimSpace(description) != "" {
				fragment.Set("error_description", strings.TrimSpace(description))
			}
			redirectURL.Fragment = fragment.Encode()
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
			return
		}
	}
	message := strings.TrimSpace(description)
	if message == "" {
		message = strings.TrimSpace(code)
	}
	writeSAMLError(w, status, message)
}

func oidcUserPayload(user *User, now time.Time) map[string]any {
	roles := []string{}
	if user != nil && strings.TrimSpace(user.Role) != "" {
		roles = append(roles, strings.TrimSpace(user.Role))
	}
	payload := map[string]any{
		"id":            "",
		"username":      "",
		"email":         "",
		"display_name":  "",
		"tenant":        "default",
		"roles":         roles,
		"source":        "oidc",
		"last_login_at": now.UTC().Format(time.RFC3339),
	}
	if user == nil {
		return payload
	}
	payload["id"] = user.ID
	payload["username"] = user.Username
	payload["email"] = user.Email
	payload["display_name"] = user.DisplayName
	if strings.TrimSpace(user.Tenant) != "" {
		payload["tenant"] = user.Tenant
	}
	if !user.CreatedAt.IsZero() {
		payload["created_at"] = user.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !user.UpdatedAt.IsZero() {
		payload["updated_at"] = user.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return payload
}
