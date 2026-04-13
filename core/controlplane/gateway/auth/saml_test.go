package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
	"github.com/crewjam/saml"
)

type fakeSAMLServiceProvider struct {
	metadata        *saml.EntityDescriptor
	bindingLocation string
	authReq         *saml.AuthnRequest
	assertion       *saml.Assertion
	makeErr         error
	parseErr        error
	lastRequestIDs  []string
}

func (f *fakeSAMLServiceProvider) Metadata() *saml.EntityDescriptor {
	if f.metadata != nil {
		return f.metadata
	}
	return &saml.EntityDescriptor{EntityID: "urn:cordum:test"}
}

func (f *fakeSAMLServiceProvider) GetSSOBindingLocation(string) string {
	if f.bindingLocation != "" {
		return f.bindingLocation
	}
	return "https://idp.example.com/sso"
}

func (f *fakeSAMLServiceProvider) MakeAuthenticationRequest(string, string, string) (*saml.AuthnRequest, error) {
	if f.makeErr != nil {
		return nil, f.makeErr
	}
	if f.authReq != nil {
		return f.authReq, nil
	}
	return &saml.AuthnRequest{ID: "req-1"}, nil
}

func (f *fakeSAMLServiceProvider) ParseResponse(_ *http.Request, possibleRequestIDs []string) (*saml.Assertion, error) {
	f.lastRequestIDs = append([]string(nil), possibleRequestIDs...)
	if f.parseErr != nil {
		return nil, f.parseErr
	}
	return f.assertion, nil
}

func licensedResolver(t *testing.T, plan licensing.Plan, mutate func(*licensing.Entitlements)) *licensing.EntitlementResolver {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}
	claims := licensing.Claims{
		OrgID:        "org-test",
		LicenseID:    "lic-test",
		Plan:         string(plan),
		Entitlements: &entitlements,
		IssuedAt:     time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		NotBefore:    time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal claims: %v", err)
	}
	license := licensing.License{
		Payload:   claims,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)),
	}
	rawLicense, err := json.Marshal(license)
	if err != nil {
		t.Fatalf("json.Marshal license: %v", err)
	}
	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(rawLicense))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func newTestSAMLAdapter(t *testing.T, resolver *licensing.EntitlementResolver) (*SAMLAuthAdapter, *RedisUserStore, *fakeSAMLServiceProvider, func()) {
	t.Helper()

	store, redisSrv := newTestUserStore(t)
	fakeSP := &fakeSAMLServiceProvider{}
	adapter := &SAMLAuthAdapter{
		enabled:         true,
		sp:              fakeSP,
		binding:         saml.HTTPPostBinding,
		responseBinding: saml.HTTPPostBinding,
		allowIDP:        false,
		defaultTenant:   "default",
		defaultRole:     "viewer",
		autoProvision:   true,
		syncRoles:       true,
		emailAttr:       "email",
		nameAttr:        "name",
		roleAttr:        "role",
		tenantAttr:      "tenant",
		stateTTL:        time.Minute,
		userStore:       store,
		sessionStore:    store,
		stateStore:      newMemorySAMLStateStore(),
		resolver:        resolver,
		now: func() time.Time {
			return time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
		},
		newState:    func() string { return "relay-1" },
		redirectURL: "",
		loginURL:    "http://localhost:8081" + SAMLLoginPath,
		metadataURL: "http://localhost:8081" + SAMLMetadataPath,
	}
	cleanup := func() {
		_ = store.Close()
		redisSrv.Close()
	}
	return adapter, store, fakeSP, cleanup
}

func buildAssertion(nameID string, attrs map[string]string) *saml.Assertion {
	attributes := make([]saml.Attribute, 0, len(attrs))
	for name, value := range attrs {
		attributes = append(attributes, saml.Attribute{
			Name: name,
			Values: []saml.AttributeValue{
				{Value: value},
			},
		})
	}
	return &saml.Assertion{
		Subject: &saml.Subject{
			NameID: &saml.NameID{Value: nameID},
		},
		AttributeStatements: []saml.AttributeStatement{
			{Attributes: attributes},
		},
	}
}

func TestSAMLHandlers_EntitlementDisabled(t *testing.T) {
	adapter, _, _, cleanup := newTestSAMLAdapter(t, nil)
	defer cleanup()

	cases := []struct {
		name    string
		method  string
		target  string
		handler http.HandlerFunc
		body    string
	}{
		{name: "metadata", method: http.MethodGet, target: SAMLMetadataPath, handler: adapter.handleMetadata},
		{name: "login", method: http.MethodGet, target: SAMLLoginPath, handler: adapter.handleLogin},
		{
			name:    "acs",
			method:  http.MethodPost,
			target:  SAMLACSPath,
			handler: adapter.handleACS,
			body:    url.Values{"SAMLResponse": {"ignored"}}.Encode(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			rr := httptest.NewRecorder()
			tc.handler(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") {
				t.Fatalf("expected tier_limit_exceeded response, got %s", rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), "upgrade_url") {
				t.Fatalf("expected upgrade_url in response, got %s", rr.Body.String())
			}
		})
	}
}

func TestNewSAMLAuthAdapter_DisabledWithoutConfig(t *testing.T) {
	for _, key := range []string{
		"CORDUM_SAML_ENABLED",
		"CORDUM_SAML_IDP_METADATA_URL",
		"CORDUM_SAML_IDP_METADATA",
		"CORDUM_SAML_BASE_URL",
		"CORDUM_SAML_CERT_PATH",
		"CORDUM_SAML_KEY_PATH",
	} {
		t.Setenv(key, "")
	}

	adapter, err := NewSAMLAuthAdapter(nil, "default", nil)
	if err != nil {
		t.Fatalf("NewSAMLAuthAdapter() error = %v", err)
	}
	if adapter == nil {
		t.Fatal("expected adapter")
	}
	if adapter.Enabled() {
		t.Fatal("expected disabled SAML adapter when env is not configured")
	}
}

func TestSAMLMetadataEndpoint_ReturnsXML(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, nil)
	adapter, _, fakeSP, cleanup := newTestSAMLAdapter(t, resolver)
	defer cleanup()

	fakeSP.metadata = &saml.EntityDescriptor{EntityID: "urn:cordum:metadata"}
	req := httptest.NewRequest(http.MethodGet, SAMLMetadataPath, nil)
	rr := httptest.NewRecorder()

	adapter.handleMetadata(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var metadata saml.EntityDescriptor
	if err := xml.Unmarshal(rr.Body.Bytes(), &metadata); err != nil {
		t.Fatalf("xml.Unmarshal: %v body=%s", err, rr.Body.String())
	}
	if metadata.EntityID != "urn:cordum:metadata" {
		t.Fatalf("EntityID = %q, want %q", metadata.EntityID, "urn:cordum:metadata")
	}
}

func TestSAMLACS_CreatesSessionWithTenantAndRole(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, nil)
	adapter, store, fakeSP, cleanup := newTestSAMLAdapter(t, resolver)
	defer cleanup()

	fakeSP.assertion = buildAssertion("alice@example.com", map[string]string{
		"email":  "alice@example.com",
		"name":   "Alice Example",
		"role":   "admin",
		"tenant": "tenant-a",
	})
	if err := adapter.stateStore.Put(context.Background(), "relay-1", "req-1", "", time.Minute); err != nil {
		t.Fatalf("Put state: %v", err)
	}

	form := url.Values{
		"RelayState":   {"relay-1"},
		"SAMLResponse": {"ignored"},
	}
	req := httptest.NewRequest(http.MethodPost, SAMLACSPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	adapter.handleACS(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID       string   `json:"id"`
			Username string   `json:"username"`
			Tenant   string   `json:"tenant"`
			Roles    []string `json:"roles"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, rr.Body.String())
	}
	if resp.Token == "" {
		t.Fatal("expected session token in ACS response")
	}
	authCtx, err := store.ValidateSession(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if authCtx.Tenant != "tenant-a" {
		t.Fatalf("Tenant = %q, want %q", authCtx.Tenant, "tenant-a")
	}
	if authCtx.Role != "admin" {
		t.Fatalf("Role = %q, want %q", authCtx.Role, "admin")
	}
	user, err := store.GetByUsername(context.Background(), "alice@example.com", "tenant-a")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("stored user role = %q, want %q", user.Role, "admin")
	}
	if got, want := resp.User.Tenant, "tenant-a"; got != want {
		t.Fatalf("response tenant = %q, want %q", got, want)
	}
}

func TestSAMLACS_SessionTTLRespectsEnv(t *testing.T) {
	t.Setenv("CORDUM_AUTH_SESSION_TTL", "90m")
	resolver := licensedResolver(t, licensing.PlanEnterprise, nil)
	adapter, _, fakeSP, cleanup := newTestSAMLAdapter(t, resolver)
	defer cleanup()

	adapter.redirectURL = ""
	fakeSP.assertion = buildAssertion("bob@example.com", map[string]string{
		"email": "bob@example.com",
		"role":  "viewer",
	})
	if err := adapter.stateStore.Put(context.Background(), "relay-1", "req-1", "", time.Minute); err != nil {
		t.Fatalf("Put state: %v", err)
	}

	form := url.Values{
		"RelayState":   {"relay-1"},
		"SAMLResponse": {"ignored"},
	}
	req := httptest.NewRequest(http.MethodPost, SAMLACSPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	adapter.handleACS(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	cookie := rr.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("expected session cookie to be set")
	}
	expiresAt := cookie[0].Expires.Sub(adapter.now())
	if expiresAt < 89*time.Minute || expiresAt > 90*time.Minute+5*time.Second {
		t.Fatalf("cookie TTL = %s, want approximately 90m", expiresAt)
	}
}

func TestSAMLACS_BlocksIDPInitiatedWhenDisabled(t *testing.T) {
	resolver := licensedResolver(t, licensing.PlanEnterprise, nil)
	adapter, _, fakeSP, cleanup := newTestSAMLAdapter(t, resolver)
	defer cleanup()

	fakeSP.parseErr = errors.New("missing request id")

	form := url.Values{
		"SAMLResponse": {"ignored"},
	}
	req := httptest.NewRequest(http.MethodPost, SAMLACSPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	adapter.handleACS(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if len(fakeSP.lastRequestIDs) != 0 {
		t.Fatalf("request IDs = %v, want empty when IDP initiated is disabled", fakeSP.lastRequestIDs)
	}
}
