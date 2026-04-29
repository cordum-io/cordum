package auth

import (
	"context"
	"crypto"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/licensing"
	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/redis/go-redis/v9"
)

const (
	SAMLMetadataPath = "/api/v1/auth/sso/saml/metadata"
	SAMLLoginPath    = "/api/v1/auth/sso/saml/login"
	SAMLACSPath      = "/api/v1/auth/sso/saml/acs"

	SessionCookieName = "cordum_session"

	defaultSAMLBaseURL = "http://localhost:8081"
)

var errSAMLStateNotFound = errors.New("saml state not found")

type samlServiceProvider interface {
	Metadata() *saml.EntityDescriptor
	GetSSOBindingLocation(binding string) string
	MakeAuthenticationRequest(idpURL string, binding string, resultBinding string) (*saml.AuthnRequest, error)
	ParseResponse(req *http.Request, possibleRequestIDs []string) (*saml.Assertion, error)
}

type samlSessionStore interface {
	StoreSession(ctx context.Context, token string, user *User, ttl time.Duration) error
}

type samlStateEntry struct {
	RequestID string    `json:"request_id,omitempty"`
	Redirect  string    `json:"redirect,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type samlStateStore interface {
	Put(ctx context.Context, state, requestID, redirect string, ttl time.Duration) error
	Get(ctx context.Context, state string) (samlStateEntry, error)
	Delete(ctx context.Context, state string) error
}

type redisSAMLStateStore struct {
	client *redis.Client
}

func (s *redisSAMLStateStore) Put(ctx context.Context, state, requestID, redirect string, ttl time.Duration) error {
	entry := samlStateEntry{
		RequestID: requestID,
		Redirect:  redirect,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal saml state: %w", err)
	}
	return s.client.Set(ctx, samlStateKey(state), data, ttl).Err()
}

func (s *redisSAMLStateStore) Get(ctx context.Context, state string) (samlStateEntry, error) {
	data, err := s.client.Get(ctx, samlStateKey(state)).Bytes()
	if err == redis.Nil {
		return samlStateEntry{}, errSAMLStateNotFound
	}
	if err != nil {
		return samlStateEntry{}, fmt.Errorf("get saml state: %w", err)
	}
	var entry samlStateEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return samlStateEntry{}, fmt.Errorf("unmarshal saml state: %w", err)
	}
	return entry, nil
}

func (s *redisSAMLStateStore) Delete(ctx context.Context, state string) error {
	return s.client.Del(ctx, samlStateKey(state)).Err()
}

type memorySAMLStateStore struct {
	mu      sync.RWMutex
	entries map[string]samlStateEntry
}

func newMemorySAMLStateStore() *memorySAMLStateStore {
	return &memorySAMLStateStore{entries: make(map[string]samlStateEntry)}
}

func (s *memorySAMLStateStore) Put(_ context.Context, state, requestID, redirect string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[state] = samlStateEntry{
		RequestID: requestID,
		Redirect:  redirect,
		CreatedAt: time.Now().UTC(),
	}
	return nil
}

func (s *memorySAMLStateStore) Get(_ context.Context, state string) (samlStateEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[state]
	if !ok {
		return samlStateEntry{}, errSAMLStateNotFound
	}
	return entry, nil
}

func (s *memorySAMLStateStore) Delete(_ context.Context, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, state)
	return nil
}

// SAMLAuthAdapter exposes SAML login endpoints and session creation while
// delegating normal request authentication to the primary auth provider.
type SAMLAuthAdapter struct {
	enabled         bool
	sp              samlServiceProvider
	binding         string
	responseBinding string
	allowIDP        bool
	defaultTenant   string
	defaultRole     string
	autoProvision   bool
	syncRoles       bool
	emailAttr       string
	nameAttr        string
	roleAttr        string
	tenantAttr      string
	stateTTL        time.Duration
	userStore       UserStore
	sessionStore    samlSessionStore
	stateStore      samlStateStore
	redirectURL     string
	loginURL        string
	metadataURL     string
	resolver        *licensing.EntitlementResolver
	now             func() time.Time
	newState        func() string
}

// SAMLService preserves the historical gateway-facing SAML type name.
type SAMLService = SAMLAuthAdapter

// NewSAMLAuthAdapter creates a SAML auth adapter from environment variables.
// When SAML is not configured, it returns a disabled adapter and no error.
func NewSAMLAuthAdapter(store UserStore, defaultTenant string, resolver *licensing.EntitlementResolver) (*SAMLAuthAdapter, error) {
	enabled := envBool("CORDUM_SAML_ENABLED")
	metadataURL := strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA_URL"))
	metadataRaw := strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA"))
	if !enabled && metadataURL == "" && metadataRaw == "" {
		return &SAMLAuthAdapter{
			defaultTenant: defaultTenant,
			defaultRole:   "viewer",
			resolver:      resolver,
			now: func() time.Time {
				return time.Now().UTC()
			},
			newState: newSAMLStateToken,
		}, nil
	}
	if store == nil {
		return nil, errors.New("saml requires user store")
	}
	sessionStore, ok := store.(samlSessionStore)
	if !ok {
		return nil, errors.New("saml requires session-capable user store")
	}
	stateStore := samlStateStore(newMemorySAMLStateStore())
	if redisStore, ok := store.(*RedisUserStore); ok && redisStore != nil && redisStore.client != nil {
		return newConfiguredSAMLAuthAdapter(store, sessionStore, &redisSAMLStateStore{client: redisStore.client}, defaultTenant, resolver)
	}
	return newConfiguredSAMLAuthAdapter(store, sessionStore, stateStore, defaultTenant, resolver)
}

// NewSAMLService preserves the historical gateway-facing constructor name.
func NewSAMLService(store UserStore, defaultTenant string, resolver *licensing.EntitlementResolver) (*SAMLService, error) {
	return NewSAMLAuthAdapter(store, defaultTenant, resolver)
}

func newConfiguredSAMLAuthAdapter(
	store UserStore,
	sessionStore samlSessionStore,
	stateStore samlStateStore,
	defaultTenant string,
	resolver *licensing.EntitlementResolver,
) (*SAMLAuthAdapter, error) {
	metadataURL := strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA_URL"))
	metadataRaw := strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA"))

	idpMetadata, err := loadSAMLIDPMetadata(metadataURL, metadataRaw)
	if err != nil {
		return nil, err
	}
	baseURL, err := parseSAMLBaseURL("CORDUM_SAML_BASE_URL", "CORDUM_API_BASE_URL", "CORDUM_API_BASE")
	if err != nil {
		return nil, err
	}
	cert, key, err := loadSAMLKeyPair()
	if err != nil {
		return nil, err
	}
	entityID := strings.TrimSpace(os.Getenv("CORDUM_SAML_ENTITY_ID"))
	if entityID == "" {
		entityID = baseURL.ResolveReference(&url.URL{Path: SAMLMetadataPath}).String()
	}

	opts := samlsp.Options{
		EntityID:    entityID,
		URL:         *baseURL,
		Key:         key,
		Certificate: cert,
		IDPMetadata: idpMetadata,
	}
	serviceProvider := samlsp.DefaultServiceProvider(opts)
	serviceProvider.MetadataURL = *baseURL.ResolveReference(&url.URL{Path: SAMLMetadataPath})
	serviceProvider.AcsURL = *baseURL.ResolveReference(&url.URL{Path: SAMLACSPath})
	serviceProvider.AllowIDPInitiated = envBool("CORDUM_SAML_ALLOW_IDP_INITIATED")

	binding := samlBindingFromEnv("CORDUM_SAML_BINDING", saml.HTTPRedirectBinding)
	responseBinding := samlBindingFromEnv("CORDUM_SAML_RESPONSE_BINDING", saml.HTTPPostBinding)
	redirectURL := resolveDefaultSAMLRedirectURL(baseURL)
	stateTTL := 10 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("CORDUM_SAML_STATE_TTL")); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr == nil && parsed > 0 {
			stateTTL = parsed
		}
	}
	defaultRole := strings.TrimSpace(os.Getenv("CORDUM_SAML_DEFAULT_ROLE"))
	if defaultRole == "" {
		defaultRole = "viewer"
	}
	return &SAMLAuthAdapter{
		enabled:         true,
		sp:              &serviceProvider,
		binding:         binding,
		responseBinding: responseBinding,
		allowIDP:        serviceProvider.AllowIDPInitiated,
		defaultTenant:   normalizeSAMLTenant(defaultTenant),
		defaultRole:     NormalizeRole(defaultRole),
		autoProvision:   !envBool("CORDUM_SAML_DISABLE_AUTO_PROVISION"),
		syncRoles:       !envBool("CORDUM_SAML_DISABLE_ROLE_SYNC"),
		emailAttr:       envOrDefault("CORDUM_SAML_EMAIL_ATTR", "email"),
		nameAttr:        envOrDefault("CORDUM_SAML_NAME_ATTR", "name"),
		roleAttr:        envOrDefault("CORDUM_SAML_ROLE_ATTR", "role"),
		tenantAttr:      envOrDefault("CORDUM_SAML_TENANT_ATTR", "tenant"),
		stateTTL:        stateTTL,
		userStore:       store,
		sessionStore:    sessionStore,
		stateStore:      stateStore,
		redirectURL:     redirectURL,
		loginURL:        baseURL.ResolveReference(&url.URL{Path: SAMLLoginPath}).String(),
		metadataURL:     serviceProvider.MetadataURL.String(),
		resolver:        resolver,
		now: func() time.Time {
			return time.Now().UTC()
		},
		newState: newSAMLStateToken,
	}, nil
}

// Enabled reports whether SAML is configured.
func (a *SAMLAuthAdapter) Enabled() bool {
	return a != nil && a.enabled
}

func (a *SAMLAuthAdapter) entitlementEnabled() (bool, string) {
	entitlements := licensing.DefaultEntitlements(licensing.PlanCommunity)
	if a != nil && a.resolver != nil {
		entitlements = a.resolver.Entitlements()
	}
	if !entitlements.SSO {
		return false, "sso"
	}
	if !entitlements.SAML {
		return false, "saml"
	}
	return true, ""
}

func (a *SAMLAuthAdapter) requireEntitlement(w http.ResponseWriter) bool {
	if a == nil {
		writeSAMLForbidden(w, "saml", "SAML SSO is not configured")
		return false
	}
	allowed, limit := a.entitlementEnabled()
	if allowed {
		return true
	}
	writeSAMLForbidden(w, limit, fmt.Sprintf("SAML SSO requires the %s entitlement", strings.ToUpper(limit)))
	return false
}

func (a *SAMLAuthAdapter) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	return nil, errors.New("saml: delegate to primary")
}

func (a *SAMLAuthAdapter) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return nil, errors.New("saml: delegate to primary")
}

func (a *SAMLAuthAdapter) RequireRole(*http.Request, ...string) error {
	return errors.New("saml: delegate to primary")
}

func (a *SAMLAuthAdapter) ResolveTenant(*http.Request, string, string) (string, error) {
	return "", errors.New("saml: delegate to primary")
}

func (a *SAMLAuthAdapter) RequireTenantAccess(*http.Request, string) error {
	return errors.New("saml: delegate to primary")
}

func (a *SAMLAuthAdapter) ResolvePrincipal(*http.Request, string) (string, error) {
	return "", errors.New("saml: delegate to primary")
}

// IsPublicPath marks the SAML endpoints as public so the gateway auth
// middleware can permit the initial login flow.
func (a *SAMLAuthAdapter) IsPublicPath(path string) bool {
	if a == nil || !a.enabled {
		return false
	}
	switch strings.TrimSpace(path) {
	case SAMLMetadataPath, SAMLLoginPath, SAMLACSPath:
		return true
	default:
		return false
	}
}

// AuthConfig exposes SAML auth configuration for dashboard clients.
func (a *SAMLAuthAdapter) AuthConfig() AuthConfig {
	cfg := AuthConfig{
		SAMLEnabled:     false,
		SAMLEnterprise:  true,
		SAMLLoginURL:    "",
		SAMLMetadataURL: "",
		SessionTTL:      authSessionTTLString(),
	}
	if a == nil || !a.enabled {
		return cfg
	}
	cfg.SAMLLoginURL = a.loginURL
	cfg.SAMLMetadataURL = a.metadataURL
	if allowed, _ := a.entitlementEnabled(); allowed {
		cfg.SAMLEnabled = true
	}
	return cfg
}

// RegisterRoutes wires the SAML metadata, login, and ACS handlers.
func (a *SAMLAuthAdapter) RegisterRoutes(mux *http.ServeMux, wrap func(route string, fn http.HandlerFunc) http.HandlerFunc) {
	if a == nil || !a.enabled || mux == nil {
		return
	}
	apply := func(route string, fn http.HandlerFunc) http.HandlerFunc {
		if wrap == nil {
			return fn
		}
		return wrap(route, fn)
	}
	mux.HandleFunc("GET "+SAMLMetadataPath, apply(SAMLMetadataPath, a.handleMetadata))
	mux.HandleFunc("GET "+SAMLLoginPath, apply(SAMLLoginPath, a.handleLogin))
	mux.HandleFunc("POST "+SAMLACSPath, apply(SAMLACSPath, a.handleACS))
}

func (a *SAMLAuthAdapter) handleMetadata(w http.ResponseWriter, r *http.Request) {
	if a == nil || !a.enabled {
		http.NotFound(w, r)
		return
	}
	if !a.requireEntitlement(w) {
		return
	}
	buf, err := xml.MarshalIndent(a.sp.Metadata(), "", "  ")
	if err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to render SAML metadata")
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	if _, err := w.Write(buf); err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to write SAML metadata")
	}
}

func (a *SAMLAuthAdapter) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a == nil || !a.enabled {
		http.NotFound(w, r)
		return
	}
	if !a.requireEntitlement(w) {
		return
	}
	slog.Info("SAML login initiated", "remote", r.RemoteAddr)
	bindingLocation := a.sp.GetSSOBindingLocation(a.binding)
	if bindingLocation == "" {
		writeSAMLError(w, http.StatusBadRequest, "sso binding not available")
		return
	}
	authReq, err := a.sp.MakeAuthenticationRequest(bindingLocation, a.binding, a.responseBinding)
	if err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to create SAML auth request")
		return
	}
	state := a.newState()
	redirect := a.resolveRedirectTarget(strings.TrimSpace(r.URL.Query().Get("redirect")))
	if err := a.stateStore.Put(r.Context(), state, authReq.ID, redirect, a.stateTTL); err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to persist SAML state")
		return
	}
	switch a.binding {
	case saml.HTTPRedirectBinding:
		redirectURL, err := authReq.Redirect(state, serviceProviderFromAdapter(a))
		if err != nil {
			writeSAMLError(w, http.StatusInternalServerError, "failed to build SAML redirect")
			return
		}
		http.Redirect(w, r, redirectURL.String(), http.StatusFound)
	case saml.HTTPPostBinding:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>"))
		_, _ = w.Write(authReq.Post(state))
		_, _ = w.Write([]byte("</body></html>"))
	default:
		writeSAMLError(w, http.StatusBadRequest, "unsupported SAML binding")
	}
}

func (a *SAMLAuthAdapter) handleACS(w http.ResponseWriter, r *http.Request) {
	if a == nil || !a.enabled {
		http.NotFound(w, r)
		return
	}
	if !a.requireEntitlement(w) {
		return
	}
	if err := r.ParseForm(); err != nil {
		writeSAMLError(w, http.StatusBadRequest, "invalid SAML callback payload")
		return
	}
	state := strings.TrimSpace(r.Form.Get("RelayState"))
	entry, err := a.loadSAMLState(r.Context(), state)
	if err != nil {
		if errors.Is(err, errSAMLStateNotFound) {
			writeSAMLError(w, http.StatusUnauthorized, "invalid SAML state")
			return
		}
		writeSAMLError(w, http.StatusInternalServerError, "failed to load SAML state")
		return
	}
	possibleIDs := make([]string, 0, 2)
	if a.allowIDP {
		possibleIDs = append(possibleIDs, "")
	}
	if entry.RequestID != "" {
		possibleIDs = append(possibleIDs, entry.RequestID)
	}
	assertion, err := a.sp.ParseResponse(r, possibleIDs)
	if err != nil {
		writeSAMLError(w, http.StatusUnauthorized, "invalid SAML response")
		return
	}
	if state != "" {
		if delErr := a.stateStore.Delete(r.Context(), state); delErr != nil {
			slog.Warn("SAML state cleanup failed", "err", delErr)
		}
	}
	slog.Info("SAML ACS callback processed", "subject", assertion.Subject.NameID.Value)
	user, err := a.resolveSAMLUser(r.Context(), assertion)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrUserNotFound) {
			status = http.StatusForbidden
		}
		writeSAMLError(w, status, err.Error())
		return
	}
	now := a.now()
	if user.Role == "" {
		user.Role = a.defaultRole
	}
	user.UpdatedAt = now
	token, err := newSessionToken()
	if err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to create session token")
		return
	}
	ttl := authSessionTTL()
	if err := a.sessionStore.StoreSession(r.Context(), token, user, ttl); err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	expiresAt := now.Add(ttl)
	SetSessionCookie(w, r, token, expiresAt)

	redirect := strings.TrimSpace(entry.Redirect)
	if redirect == "" {
		redirect = a.redirectURL
	}
	if redirect == "" {
		writeJSON(w, map[string]any{
			"token":      token,
			"expires_at": expiresAt.Format(time.RFC3339),
			"user":       samlUserPayload(user, now),
		})
		return
	}
	redirectURL, err := url.Parse(redirect)
	if err != nil {
		writeSAMLError(w, http.StatusBadRequest, "invalid redirect")
		return
	}
	fragment := url.Values{}
	fragment.Set("token", token)
	fragment.Set("expires_at", expiresAt.Format(time.RFC3339))
	fragment.Set("user_id", user.ID)
	fragment.Set("username", user.Username)
	fragment.Set("email", user.Email)
	fragment.Set("display_name", user.DisplayName)
	fragment.Set("role", user.Role)
	fragment.Set("tenant", user.Tenant)
	redirectURL.Fragment = fragment.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func (a *SAMLAuthAdapter) loadSAMLState(ctx context.Context, state string) (samlStateEntry, error) {
	if strings.TrimSpace(state) == "" {
		if a.allowIDP {
			return samlStateEntry{}, nil
		}
		return samlStateEntry{}, errSAMLStateNotFound
	}
	entry, err := a.stateStore.Get(ctx, state)
	if err == nil {
		return entry, nil
	}
	if errors.Is(err, errSAMLStateNotFound) && a.allowIDP {
		return samlStateEntry{}, nil
	}
	return samlStateEntry{}, err
}

func (a *SAMLAuthAdapter) resolveSAMLUser(ctx context.Context, assertion *saml.Assertion) (*User, error) {
	attrs := collectSAMLAttributes(assertion)
	email := firstSAMLAttribute(attrs, a.emailAttr)
	name := firstSAMLAttribute(attrs, a.nameAttr)
	role := firstSAMLAttribute(attrs, a.roleAttr)
	tenant := normalizeSAMLTenant(firstSAMLAttribute(attrs, a.tenantAttr))
	if tenant == "" {
		tenant = normalizeSAMLTenant(a.defaultTenant)
	}
	if tenant == "" {
		tenant = "default"
	}
	identifier := strings.TrimSpace(email)
	if identifier == "" && assertion != nil && assertion.Subject != nil && assertion.Subject.NameID != nil {
		identifier = strings.TrimSpace(assertion.Subject.NameID.Value)
	}
	if identifier == "" {
		identifier = strings.TrimSpace(name)
	}
	if identifier == "" {
		return nil, errors.New("sso identity missing")
	}
	username := normalizeSAMLUsername(identifier)
	if username == "" {
		return nil, errors.New("sso identity missing")
	}
	selectedRole := selectSAMLRole(role, a.defaultRole)

	user, err := a.userStore.GetByUsername(ctx, username, tenant)
	if errors.Is(err, ErrUserNotFound) && email != "" {
		user, err = a.userStore.GetByEmail(ctx, email, tenant)
	}
	if err != nil {
		if !errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("load SAML user: %w", err)
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
			return nil, fmt.Errorf("provision SAML user: %w", err)
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
			return nil, fmt.Errorf("update SAML user: %w", err)
		}
	}
	if user.Role == "" {
		user.Role = selectedRole
	}
	return user, nil
}

func (a *SAMLAuthAdapter) resolveRedirectTarget(raw string) string {
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

func serviceProviderFromAdapter(a *SAMLAuthAdapter) *saml.ServiceProvider {
	if a == nil {
		return nil
	}
	if sp, ok := a.sp.(*saml.ServiceProvider); ok {
		return sp
	}
	return nil
}

func sameOrigin(baseURL, targetURL *url.URL) bool {
	if baseURL == nil || targetURL == nil {
		return false
	}
	return strings.EqualFold(baseURL.Scheme, targetURL.Scheme) &&
		strings.EqualFold(baseURL.Host, targetURL.Host)
}

func loadSAMLIDPMetadata(metadataURL, metadataRaw string) (*saml.EntityDescriptor, error) {
	if metadataURL != "" {
		parsed, err := url.Parse(metadataURL)
		if err != nil {
			return nil, fmt.Errorf("parse metadata url: %w", err)
		}
		client := &http.Client{Timeout: 10 * time.Second}
		return samlsp.FetchMetadata(context.Background(), client, *parsed)
	}
	if metadataRaw == "" {
		return nil, errors.New("idp metadata required")
	}
	if data, err := readMaybeFile(metadataRaw); err == nil {
		return samlsp.ParseMetadata(data)
	}
	return samlsp.ParseMetadata([]byte(metadataRaw))
}

func parseSAMLBaseURL(envs ...string) (*url.URL, error) {
	for _, key := range envs {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			parsed, err := url.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", key, err)
			}
			if parsed.Scheme == "" || parsed.Host == "" {
				return nil, fmt.Errorf("%s must be an absolute URL", key)
			}
			return parsed, nil
		}
	}
	return url.Parse(defaultSAMLBaseURL)
}

func loadSAMLKeyPair() (*x509.Certificate, crypto.Signer, error) {
	certPath := strings.TrimSpace(os.Getenv("CORDUM_SAML_CERT_PATH"))
	keyPath := strings.TrimSpace(os.Getenv("CORDUM_SAML_KEY_PATH"))
	if certPath == "" || keyPath == "" {
		return nil, nil, errors.New("SAML cert/key required")
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load SAML key pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, nil, errors.New("SAML certificate missing")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("parse SAML certificate: %w", err)
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("SAML private key is not a signer")
	}
	return cert, key, nil
}

func resolveDefaultSAMLRedirectURL(baseURL *url.URL) string {
	if raw := strings.TrimSpace(os.Getenv("CORDUM_AUTH_REDIRECT_URL")); raw != "" {
		return raw
	}
	if origin := strings.TrimSpace(os.Getenv("CORDUM_AUTH_UI_ORIGIN")); origin != "" {
		return strings.TrimRight(origin, "/") + "/login"
	}
	if baseURL == nil {
		return ""
	}
	return baseURL.ResolveReference(&url.URL{Path: "/login"}).String()
}

func readMaybeFile(raw string) ([]byte, error) {
	if strings.Contains(raw, "<") {
		return []byte(raw), nil
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path) // #nosec G304 -- path is explicit operator SAML configuration; Abs normalizes it before read.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func collectSAMLAttributes(assertion *saml.Assertion) map[string][]string {
	attrs := make(map[string][]string)
	if assertion == nil {
		return attrs
	}
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			names := []string{strings.TrimSpace(attr.Name), strings.TrimSpace(attr.FriendlyName)}
			for _, value := range attr.Values {
				trimmed := strings.TrimSpace(value.Value)
				if trimmed == "" {
					continue
				}
				for _, name := range names {
					if name == "" {
						continue
					}
					key := strings.ToLower(name)
					attrs[key] = append(attrs[key], trimmed)
				}
			}
		}
	}
	return attrs
}

func firstSAMLAttribute(attrs map[string][]string, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	values := attrs[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func normalizeSAMLTenant(tenant string) string {
	tenant = strings.TrimSpace(strings.ToLower(tenant))
	if tenant == "" {
		return ""
	}
	return tenant
}

func normalizeSAMLUsername(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(value, " ", "_")
}

func selectSAMLRole(raw, fallback string) string {
	roles := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '|':
			return true
		default:
			return false
		}
	})
	selected := NormalizeRole(fallback)
	if selected == "" {
		selected = "viewer"
	}
	for _, role := range roles {
		normalized := NormalizeRole(role)
		switch normalized {
		case "admin":
			return "admin"
		case "viewer":
			if selected != "admin" {
				selected = "viewer"
			}
		default:
			if normalized != "" && selected == "viewer" {
				selected = normalized
			}
		}
	}
	return selected
}

func envOrDefault(key, def string) string {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		return raw
	}
	return def
}

func newSAMLStateToken() string {
	b := make([]byte, 24)
	if _, err := io.ReadFull(crand.Reader, b); err != nil {
		// crypto/rand failure indicates a catastrophic system problem.
		// Do not fall back to a predictable value — it defeats CSRF protection.
		panic("crypto/rand failure: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func newSessionToken() (string, error) {
	var tokenBytes [32]byte
	if _, err := io.ReadFull(crand.Reader, tokenBytes[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return "session-" + base64.RawURLEncoding.EncodeToString(tokenBytes[:]), nil
}

func newProvisionedPassword() string {
	buf := make([]byte, 18)
	if _, err := io.ReadFull(crand.Reader, buf); err != nil {
		return "Aa1!CordumSAMLProvisioned"
	}
	return "Aa1!" + base64.RawURLEncoding.EncodeToString(buf)
}

func authSessionTTL() time.Duration {
	const fallback = 24 * time.Hour
	for _, key := range []string{"CORDUM_AUTH_SESSION_TTL", "CORDUM_SESSION_TTL"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				return d
			}
		}
	}
	return fallback
}

func authSessionTTLString() string {
	for _, key := range []string{"CORDUM_AUTH_SESSION_TTL", "CORDUM_SESSION_TTL"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				return raw
			}
		}
	}
	return "24h"
}

func samlStateKey(state string) string {
	return samlStateKeyPrefix + state
}

// SetSessionCookie stores the session token in an HttpOnly cookie for browser flows.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	if w == nil || strings.TrimSpace(token) == "" {
		return
	}
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		Secure:   env.IsProduction() || (r != nil && r.TLS != nil),
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie invalidates the browser session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		Secure:   env.IsProduction() || (r != nil && r.TLS != nil),
	})
}

func sessionCookieToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return NormalizeAPIKey(cookie.Value)
}

func sessionTokenFromCookie(r *http.Request) string {
	return sessionCookieToken(r)
}

// SessionTokenFromCookie exposes browser session cookie extraction to the
// gateway compatibility layer.
func SessionTokenFromCookie(r *http.Request) string {
	return sessionTokenFromCookie(r)
}

func samlBindingFromEnv(key string, def string) string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return def
	case "redirect":
		return saml.HTTPRedirectBinding
	case "post":
		return saml.HTTPPostBinding
	default:
		return raw
	}
}

func samlUserPayload(user *User, now time.Time) map[string]any {
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
		"source":        "saml",
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

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		writeSAMLError(w, http.StatusInternalServerError, "failed to encode response")
	}
}

func writeSAMLError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  strings.TrimSpace(message),
		"status": status,
	})
}

func writeSAMLForbidden(w http.ResponseWriter, limit, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":       "tier_limit_exceeded",
		"code":        "tier_limit_exceeded",
		"status":      http.StatusForbidden,
		"message":     strings.TrimSpace(message),
		"limit":       strings.TrimSpace(limit),
		"current":     1,
		"allowed":     0,
		"upgrade_url": licensing.DefaultUpgradeURL,
	})
}
