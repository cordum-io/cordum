package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const crossTenantIdentityPassword = "Sweep-Passw0rd!"

func init() {
	registerCrossTenantFamily("PR1-02-IDENTITY",
		crossTenantCase{"GET /api/v1/users", "users-list", crossTenantUserList},
		crossTenantUserWrite("users-update", "PUT", "/api/v1/users/{id}", `{"display_name":"intruder"}`),
		crossTenantUserWrite("users-delete", "DELETE", "/api/v1/users/{id}", ""),
		crossTenantUserWrite("users-password", "POST", "/api/v1/users/{id}/password", `{"password":"Intruder-Passw0rd!"}`),
		crossTenantCase{"GET /api/v1/auth/keys", "keys-list", crossTenantKeyList},
		crossTenantCase{"POST /api/v1/auth/keys", "keys-create", crossTenantKeyCreate},
		crossTenantCase{"DELETE /api/v1/auth/keys/{id}", "keys-revoke", crossTenantKeyRevoke},
		crossTenantCase{"GET /api/v1/auth/session", "session", crossTenantSessionRead},
		crossTenantCase{"POST /api/v1/auth/password", "self-password", crossTenantSelfPassword},
		crossTenantCase{"POST /api/v1/auth/logout", "logout", crossTenantLogout},
	)
	registerCrossTenantFamily("PR1-02-IDENTITY", crossTenantSCIMCases()...)
}

func TestCrossTenantSweepIdentity(t *testing.T) { runCrossTenantFamily(t, "PR1-02-IDENTITY") }

type crossTenantIdentity struct {
	users *auth.RedisUserStore
	keys  *auth.RedisKeyStore
	basic *auth.BasicAuthProvider
}

// newCrossTenantIdentity wires stores and providers as gateway.go does, with
// tenant B as the SCIM provisioning tenant, then rebuilds sw's routes.
func newCrossTenantIdentity(t *testing.T, sw *crossTenantSweep) *crossTenantIdentity {
	t.Helper()
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	setTestEntitlements(t, sw.server, licensing.PlanEnterprise, nil)
	users, oidc, scim := newCrossTenantProviders(t, sw)
	id := &crossTenantIdentity{users, auth.NewRedisKeyStoreFromClient(crossTenantRedis(t, sw)), auth.ExtractBasicAuth(sw.server.auth)}
	id.basic.SetUserStore(id.users)
	id.basic.SetKeyStore(id.keys)
	t.Cleanup(id.basic.DrainUsage)
	sw.server.userStore, sw.server.keyStore = id.users, id.keys
	composite, err := auth.NewCompositeAuthProvider(id.basic, oidc, scim)
	if err != nil {
		t.Fatal(err)
	}
	sw.server.auth = composite
	sw.mux = http.NewServeMux()
	if err := sw.server.registerRoutes(sw.mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	sw.handler = apiKeyMiddleware(composite, tenantMiddleware(composite, maxBodyMiddleware(sw.mux, sw.server.entitlements)))
	return id
}

func crossTenantRedis(t *testing.T, sw *crossTenantSweep) *redis.Client {
	t.Helper()
	client, ok := sw.server.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client type = %T", sw.server.jobStore.Client())
	}
	return client
}

// newCrossTenantProviders builds the user store and the enabled OIDC flow and
// SCIM providers; tenant B is the SCIM provisioning tenant.
func newCrossTenantProviders(t *testing.T, sw *crossTenantSweep) (*auth.RedisUserStore, *auth.OIDCFlowAdapter, *auth.SCIMService) {
	t.Helper()
	users := auth.NewRedisUserStoreFromClient(crossTenantRedis(t, sw))
	oidc, err := auth.NewOIDCFlowAdapter(newCrossTenantOIDCProvider(t), users, sw.server.tenant, sw.server.entitlements)
	if err != nil || !oidc.Enabled() {
		t.Fatalf("oidc flow adapter: enabled=%v, error=%v", err == nil && oidc.Enabled(), err)
	}
	scim, err := auth.NewSCIMService(users, sw.tenantB.tenantID, sw.server.entitlements)
	if err != nil {
		t.Fatal(err)
	}
	return users, oidc, scim
}

func newCrossTenantOIDCProvider(t *testing.T) *auth.OIDCProvider {
	t.Helper()
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	var issuer string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/jwks" {
			_, _ = w.Write([]byte(`{"keys":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": issuer + "/jwks",
			"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token"})
	}))
	t.Cleanup(idp.Close)
	issuer = idp.URL
	provider, err := auth.NewOIDCProvider(auth.OIDCConfig{Enabled: true, IssuerURL: issuer, Audience: "cordum",
		ClientID: "cordum-sweep", ClientSecret: "sweep-client-secret", RedirectURI: "http://127.0.0.1" + auth.OIDCCallbackPath})
	if err != nil {
		t.Fatalf("oidc provider: %v", err)
	}
	t.Cleanup(provider.Close)
	return provider
}

func crossTenantIdentityDo(t *testing.T, sw *crossTenantSweep, key, tenant, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", tenant)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	sw.handler.ServeHTTP(rr, req)
	return rr
}

func seedCrossTenantUser(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) AuthUser {
	t.Helper()
	name := "private-user-" + uuid.NewString()
	body := fmt.Sprintf(`{"username":%q,"email":%q,"password":%q}`, name, name+"@example.test", crossTenantIdentityPassword)
	rr := sw.as(t, who, http.MethodPost, "/api/v1/users", body)
	assertCrossTenantAbsent(t, rr, "seed user")
	var user AuthUser
	if err := json.Unmarshal(rr.Body.Bytes(), &user); err != nil || user.ID == "" || user.Tenant != who.tenantID {
		t.Fatalf("decode seeded user: %v; %s", err, rr.Body.String())
	}
	return user
}

// seedCrossTenantLogin seeds a user for who and returns it with a session token.
func seedCrossTenantLogin(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) (AuthUser, string) {
	t.Helper()
	user := seedCrossTenantUser(t, sw, who)
	return user, crossTenantLogin(t, sw, who, user.Username, crossTenantIdentityPassword)
}

func crossTenantLogin(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q,"tenant":%q}`, username, password, who.tenantID)
	rr := sw.as(t, who, http.MethodPost, "/api/v1/auth/login", body)
	var resp AuthLoginResponse
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || !strings.HasPrefix(resp.Token, "session-") {
		t.Fatalf("login %s: status=%d body=%s", username, rr.Code, rr.Body.String())
	}
	return resp.Token
}

func seedCrossTenantKey(t *testing.T, sw *crossTenantSweep) (keyID, name, secret string) {
	t.Helper()
	name = "private-key-" + uuid.NewString()
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/auth/keys", fmt.Sprintf(`{"name":%q,"scopes":[]}`, name))
	assertCrossTenantAbsent(t, rr, "owner seed key")
	var created struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.Key.ID == "" || created.Secret == "" {
		t.Fatalf("decode owner key: %v; %s", err, rr.Body.String())
	}
	return created.Key.ID, name, created.Secret
}

func (id *crossTenantIdentity) userState(t *testing.T, tenant string) map[string]auth.User {
	t.Helper()
	users, err := id.users.List(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]auth.User{}
	for _, u := range users {
		state[u.ID] = *u
	}
	return state
}

func (id *crossTenantIdentity) keyState(t *testing.T, tenant string) map[string]auth.ManagedKey {
	t.Helper()
	keys, err := id.keys.List(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]auth.ManagedKey{}
	for _, k := range keys {
		state[k.ID] = *k
	}
	return state
}

func assertCrossTenantQuiet(t *testing.T, sw *crossTenantSweep, mark int, label string, unchanged bool) {
	t.Helper()
	if !unchanged {
		t.Fatalf("%s changed tenant B state", label)
	}
	if n := len(sw.publishedSince(mark)); n != 0 {
		t.Fatalf("%s published %d packets", label, n)
	}
}

func assertCrossTenantPacketsClean(t *testing.T, sw *crossTenantSweep, mark int, label string, leaks ...string) {
	t.Helper()
	for _, msg := range sw.publishedSince(mark) {
		text := msg.subject + " " + msg.packet.String()
		for _, leak := range append(leaks, sw.tenantB.tenantID) {
			if strings.Contains(text, leak) {
				t.Fatalf("%s published tenant-B data %q on %s", label, leak, msg.subject)
			}
		}
	}
}

func crossTenantUserList(t *testing.T, sw *crossTenantSweep) {
	newCrossTenantIdentity(t, sw)
	owner := seedCrossTenantUser(t, sw, sw.tenantB)
	rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/users", "")
	assertCrossTenantAbsent(t, rr, "A list users", owner.ID, owner.Username)
	rr = sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/users", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), owner.ID) {
		t.Fatalf("owner list users: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func crossTenantUserWrite(name, method, route, body string) crossTenantCase {
	return crossTenantCase{method + " " + route, name, func(t *testing.T, sw *crossTenantSweep) {
		id := newCrossTenantIdentity(t, sw)
		owner := seedCrossTenantUser(t, sw, sw.tenantB)
		path := strings.Replace(route, "{id}", owner.ID, 1)
		before, mark := id.userState(t, sw.tenantB.tenantID), sw.publishMark()
		rr := sw.as(t, sw.tenantA, method, path, body)
		assertCrossTenantRefused(t, rr, "A "+name, owner.Username, owner.Email)
		assertCrossTenantQuiet(t, sw, mark, "A "+name, reflect.DeepEqual(before, id.userState(t, sw.tenantB.tenantID)))
		rr = sw.as(t, sw.tenantB, method, path, body)
		if rr.Code < 200 || rr.Code >= 300 || reflect.DeepEqual(before, id.userState(t, sw.tenantB.tenantID)) {
			t.Fatalf("owner %s: status=%d, state changed=false; body=%s", name, rr.Code, rr.Body.String())
		}
	}}
}

func crossTenantKeyList(t *testing.T, sw *crossTenantSweep) {
	newCrossTenantIdentity(t, sw)
	keyID, name, _ := seedCrossTenantKey(t, sw)
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/auth/keys", ""), "A list keys", keyID, name)
	rr := sw.as(t, sw.tenantB, http.MethodGet, "/api/v1/auth/keys", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), keyID) {
		t.Fatalf("owner list keys: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func crossTenantKeyCreate(t *testing.T, sw *crossTenantSweep) {
	id := newCrossTenantIdentity(t, sw)
	keyID, name, _ := seedCrossTenantKey(t, sw)
	before, mark := id.keyState(t, sw.tenantB.tenantID), sw.publishMark()
	body := fmt.Sprintf(`{"name":%q,"scopes":[]}`, "a-key-"+uuid.NewString())
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/auth/keys", body)
	assertCrossTenantAbsent(t, rr, "A create key", keyID, name)
	if !reflect.DeepEqual(before, id.keyState(t, sw.tenantB.tenantID)) {
		t.Fatal("A create key changed tenant B keys")
	}
	assertCrossTenantPacketsClean(t, sw, mark, "A create key", keyID, name)
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.Secret == "" {
		t.Fatalf("decode A key: %v", err)
	}
	if rr := crossTenantIdentityDo(t, sw, created.Secret, sw.tenantB.tenantID, http.MethodGet, "/api/v1/auth/session", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("A key under tenant B: status=%d, want 403", rr.Code)
	}
	if rr := crossTenantIdentityDo(t, sw, created.Secret, sw.tenantA.tenantID, http.MethodGet, "/api/v1/auth/session", ""); rr.Code != http.StatusOK {
		t.Fatalf("A key under tenant A: status=%d, want 200", rr.Code)
	}
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/auth/keys", body), "owner create key")
	if len(id.keyState(t, sw.tenantB.tenantID)) != len(before)+1 {
		t.Fatal("owner create key did not add a B key")
	}
}

func crossTenantKeyRevoke(t *testing.T, sw *crossTenantSweep) {
	id := newCrossTenantIdentity(t, sw)
	keyID, name, secret := seedCrossTenantKey(t, sw)
	path := "/api/v1/auth/keys/" + keyID
	before, mark := id.keyState(t, sw.tenantB.tenantID), sw.publishMark()
	assertCrossTenantRefused(t, sw.as(t, sw.tenantA, http.MethodDelete, path, ""), "A revoke key", name, secret)
	assertCrossTenantQuiet(t, sw, mark, "A revoke key", reflect.DeepEqual(before, id.keyState(t, sw.tenantB.tenantID)))
	if rr := crossTenantIdentityDo(t, sw, secret, sw.tenantB.tenantID, http.MethodGet, "/api/v1/auth/session", ""); rr.Code != http.StatusOK {
		t.Fatalf("B key after A revoke: status=%d, want 200", rr.Code)
	}
	if rr := sw.as(t, sw.tenantB, http.MethodDelete, path, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("owner revoke: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := crossTenantIdentityDo(t, sw, secret, sw.tenantB.tenantID, http.MethodGet, "/api/v1/auth/session", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("B key after owner revoke: status=%d, want 401", rr.Code)
	}
}

func crossTenantSessionRead(t *testing.T, sw *crossTenantSweep) {
	newCrossTenantIdentity(t, sw)
	owner, ownerToken := seedCrossTenantLogin(t, sw, sw.tenantB)
	intruder, intruderToken := seedCrossTenantLogin(t, sw, sw.tenantA)
	for _, tc := range []struct {
		token, tenant string
		want          AuthUser
		leaks         []string
	}{
		{intruderToken, sw.tenantA.tenantID, intruder, []string{owner.Username, owner.Email, ownerToken}},
		{ownerToken, sw.tenantB.tenantID, owner, nil},
	} {
		rr := crossTenantIdentityDo(t, sw, tc.token, tc.tenant, http.MethodGet, "/api/v1/auth/session", "", "X-Principal-Id", owner.ID)
		assertCrossTenantAbsent(t, rr, "session "+tc.tenant, tc.leaks...)
		var resp AuthLoginResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.User.ID != tc.want.ID || resp.User.Tenant != tc.tenant {
			t.Fatalf("session %s: user=%+v, want id %s; err=%v", tc.tenant, resp.User, tc.want.ID, err)
		}
	}
}

func crossTenantSelfPassword(t *testing.T, sw *crossTenantSweep) {
	id := newCrossTenantIdentity(t, sw)
	owner, ownerToken := seedCrossTenantLogin(t, sw, sw.tenantB)
	intruder, intruderToken := seedCrossTenantLogin(t, sw, sw.tenantA)
	const changed = "Changed-Passw0rd!"
	body := fmt.Sprintf(`{"current_password":%q,"new_password":%q}`, crossTenantIdentityPassword, changed)
	before, mark := id.userState(t, sw.tenantB.tenantID), sw.publishMark()
	rr := crossTenantIdentityDo(t, sw, intruderToken, sw.tenantA.tenantID, http.MethodPost, "/api/v1/auth/password", body, "X-Principal-Id", owner.ID)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("A self password: status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertCrossTenantNoLeaks(t, rr, "A self password", owner.Username, owner.Email)
	if !reflect.DeepEqual(before, id.userState(t, sw.tenantB.tenantID)) {
		t.Fatal("A self password changed tenant B users")
	}
	assertCrossTenantPacketsClean(t, sw, mark, "A self password", owner.Username)
	crossTenantLogin(t, sw, sw.tenantA, intruder.Username, changed)
	crossTenantLogin(t, sw, sw.tenantB, owner.Username, crossTenantIdentityPassword)
	rr = crossTenantIdentityDo(t, sw, ownerToken, sw.tenantB.tenantID, http.MethodPost, "/api/v1/auth/password", body)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owner self password: status=%d body=%s", rr.Code, rr.Body.String())
	}
	crossTenantLogin(t, sw, sw.tenantB, owner.Username, changed)
}

func crossTenantLogout(t *testing.T, sw *crossTenantSweep) {
	id := newCrossTenantIdentity(t, sw)
	_, ownerToken := seedCrossTenantLogin(t, sw, sw.tenantB)
	_, intruderToken := seedCrossTenantLogin(t, sw, sw.tenantA)
	ctx := context.Background()
	before, err := id.users.ValidateSession(ctx, ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	mark := sw.publishMark()
	rr := crossTenantIdentityDo(t, sw, intruderToken, sw.tenantA.tenantID, http.MethodPost, "/api/v1/auth/logout", "", "Cookie", auth.SessionCookieName+"="+ownerToken)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("A logout: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if after, err := id.users.ValidateSession(ctx, ownerToken); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("A logout changed B session: %+v, %v", after, err)
	}
	assertCrossTenantPacketsClean(t, sw, mark, "A logout")
	rr = crossTenantIdentityDo(t, sw, ownerToken, sw.tenantB.tenantID, http.MethodPost, "/api/v1/auth/logout", "")
	if _, err := id.users.ValidateSession(ctx, ownerToken); rr.Code != http.StatusNoContent || err == nil {
		t.Fatalf("owner logout: status=%d, session error=%v", rr.Code, err)
	}
}

const crossTenantSAMLMetadata = `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.test/metadata">` +
	`<IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
	`<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.test/sso"/>` +
	`</IDPSSODescriptor></EntityDescriptor>`

func newCrossTenantSAML(t *testing.T, users auth.UserStore, sw *crossTenantSweep) *auth.SAMLService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cordum-sweep"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "sp.crt"), filepath.Join(dir, "sp.key")
	for path, block := range map[string]*pem.Block{certPath: {Type: "CERTIFICATE", Bytes: der},
		keyPath: {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CORDUM_SAML_ENABLED", "true")
	t.Setenv("CORDUM_SAML_IDP_METADATA", crossTenantSAMLMetadata)
	t.Setenv("CORDUM_SAML_CERT_PATH", certPath)
	t.Setenv("CORDUM_SAML_KEY_PATH", keyPath)
	saml, err := auth.NewSAMLService(users, sw.server.tenant, sw.server.entitlements)
	if err != nil || !saml.Enabled() {
		t.Fatalf("saml service: enabled=%v, error=%v", err == nil && saml.Enabled(), err)
	}
	return saml
}

// crossTenantProviderRoutes returns the routes the OIDC flow, SAML and SCIM
// providers register through RegisterRoutes, outside the gateway route table.
func crossTenantProviderRoutes(t *testing.T, sw *crossTenantSweep) map[string]bool {
	t.Helper()
	users, oidc, scim := newCrossTenantProviders(t, sw)
	routes := map[string]bool{}
	providers := map[string]auth.RouteRegistrar{"oidc": oidc, "saml": newCrossTenantSAML(t, users, sw), "scim": scim}
	for name, provider := range providers {
		mux, paths := http.NewServeMux(), map[string]bool{}
		provider.RegisterRoutes(mux, func(route string, fn http.HandlerFunc) http.HandlerFunc {
			paths[route] = true
			return fn
		})
		if len(paths) == 0 {
			t.Errorf("provider %s registered no routes", name)
		}
		for path := range paths {
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				req := httptest.NewRequest(method, strings.ReplaceAll(path, "{id}", "sweep-id"), nil)
				if _, pattern := mux.Handler(req); pattern != "" {
					routes[pattern] = true
				}
			}
		}
	}
	return routes
}

const (
	crossTenantSCIMUser  = `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":%q,"active":true}`
	crossTenantSCIMGroup = `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":%q}`
	crossTenantSCIMPatch = `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"externalId","value":"sweep-external"}]}`
)

func crossTenantSCIMCases() []crossTenantCase {
	users, groups := auth.SCIMUsersPath, auth.SCIMGroupsPath
	return []crossTenantCase{
		crossTenantSCIMCase("scim-users-list", "GET", users, ""),
		crossTenantSCIMCase("scim-users-create", "POST", users, fmt.Sprintf(crossTenantSCIMUser, "sweep-scim-user")),
		crossTenantSCIMCase("scim-user-get", "GET", users+"/{id}", ""),
		crossTenantSCIMCase("scim-user-replace", "PUT", users+"/{id}", strings.Replace(crossTenantSCIMUser, `"active":true`, `"active":false`, 1)),
		crossTenantSCIMCase("scim-user-patch", "PATCH", users+"/{id}", crossTenantSCIMPatch),
		crossTenantSCIMCase("scim-user-delete", "DELETE", users+"/{id}", ""),
		crossTenantSCIMCase("scim-groups-list", "GET", groups, ""),
		crossTenantSCIMCase("scim-groups-create", "POST", groups, fmt.Sprintf(crossTenantSCIMGroup, "sweep-scim-group")),
		crossTenantSCIMCase("scim-group-get", "GET", groups+"/{id}", ""),
		crossTenantSCIMCase("scim-group-replace", "PUT", groups+"/{id}", fmt.Sprintf(crossTenantSCIMGroup, "sweep-renamed")),
		crossTenantSCIMCase("scim-group-patch", "PATCH", groups+"/{id}", crossTenantSCIMPatch),
		crossTenantSCIMCase("scim-group-delete", "DELETE", groups+"/{id}", ""),
	}
}

type crossTenantSCIMSeed struct{ token, userID, userName, groupID, groupName string }

// seedCrossTenantSCIM has B's admin mint the SCIM token and B's IdP provision
// one user and one group with it.
func seedCrossTenantSCIM(t *testing.T, sw *crossTenantSweep) crossTenantSCIMSeed {
	t.Helper()
	seed := crossTenantSCIMSeed{token: crossTenantSCIMToken(t, sw, sw.tenantB),
		userName: "private-scim-" + uuid.NewString(), groupName: "private-group-" + uuid.NewString()}
	for _, r := range []struct {
		path, body string
		id         *string
	}{
		{auth.SCIMUsersPath, fmt.Sprintf(crossTenantSCIMUser, seed.userName), &seed.userID},
		{auth.SCIMGroupsPath, fmt.Sprintf(crossTenantSCIMGroup, seed.groupName), &seed.groupID},
	} {
		rr := crossTenantSCIMDo(t, sw, seed.token, http.MethodPost, r.path, r.body)
		var created struct {
			ID string `json:"id"`
		}
		if rr.Code != http.StatusCreated || json.Unmarshal(rr.Body.Bytes(), &created) != nil || created.ID == "" {
			t.Fatalf("IdP seed %s: status=%d body=%s", r.path, rr.Code, rr.Body.String())
		}
		*r.id = created.ID
	}
	return seed
}

func crossTenantSCIMToken(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) string {
	t.Helper()
	rr := sw.as(t, who, http.MethodPost, "/api/v1/scim/settings/token", "")
	var rotated struct {
		BearerToken string `json:"bearerToken"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &rotated) != nil || rotated.BearerToken == "" {
		t.Fatalf("rotate SCIM token: status=%d body=%s", rr.Code, rr.Body.String())
	}
	return rotated.BearerToken
}

func crossTenantSCIMDo(t *testing.T, sw *crossTenantSweep, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return crossTenantIdentityDo(t, sw, "", "", method, path, body, "Authorization", "Bearer "+token)
}

// state reads B's SCIM users and groups back with B's token.
func (seed crossTenantSCIMSeed) state(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	var state string
	for _, path := range []string{auth.SCIMUsersPath, auth.SCIMGroupsPath} {
		rr := crossTenantSCIMDo(t, sw, seed.token, http.MethodGet, path, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("read B SCIM %s: status=%d", path, rr.Code)
		}
		state += rr.Body.String() + "\n"
	}
	return state
}

// The v2 routes are public paths authenticated by the SCIM bearer token.
func crossTenantSCIMCase(name, method, route, body string) crossTenantCase {
	return crossTenantCase{method + " " + route, name, func(t *testing.T, sw *crossTenantSweep) {
		newCrossTenantIdentity(t, sw)
		seed := seedCrossTenantSCIM(t, sw)
		id, resource := seed.userID, seed.userName
		if strings.HasPrefix(route, auth.SCIMGroupsPath) {
			id, resource = seed.groupID, seed.groupName
		}
		path := strings.Replace(route, "{id}", id, 1)
		payload := body
		if strings.Contains(body, "%q") {
			payload = fmt.Sprintf(body, resource)
		}
		before, mark := seed.state(t, sw), sw.publishMark()
		rr := sw.as(t, sw.tenantA, method, path, payload)
		missing := sw.as(t, sw.tenantA, method, strings.Replace(route, "{id}", "missing-"+uuid.NewString(), 1), payload)
		if rr.Code != http.StatusUnauthorized || rr.Body.String() != missing.Body.String() {
			t.Fatalf("A %s: status=%d body=%s; never-existing id gets %d %s", name, rr.Code, rr.Body.String(), missing.Code, missing.Body.String())
		}
		assertCrossTenantNoLeaks(t, rr, "A "+name, resource, seed.token)
		assertCrossTenantQuiet(t, sw, mark, "A "+name, before == seed.state(t, sw))
		rr = crossTenantSCIMDo(t, sw, seed.token, method, path, payload)
		read := method == http.MethodGet
		if rr.Code < 200 || rr.Code >= 300 || read != (before == seed.state(t, sw)) || (read && !strings.Contains(rr.Body.String(), resource)) {
			t.Fatalf("owner %s: status=%d body=%s", name, rr.Code, rr.Body.String())
		}
	}}
}
