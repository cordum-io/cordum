package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/tests/identity"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// Audience matrix harness (PR1-01). One production-profile gateway serves the
// real auth chain (newHTTPHandler: apiKeyMiddleware, tenantMiddleware, mux)
// in two deployments, because configuring a gateway JWT makes production
// JWT-only (auth/basic.go NewBasicAuthProvider):
//   - keys: static and managed API keys, sessions and the OIDC adapter;
//   - jwt:  the same plus the gateway JWT validator.
//
// Both share one Redis database and one bus, so a snapshot sees either.
// Every identity.Credential is minted by its real issuer with real keys.
const (
	matrixTenant        = "default"
	matrixStaticKey     = "audience-matrix-static-key-0000000000000000"
	matrixStaticOwner   = "principal-matrix-static"
	matrixJWTSecret     = "audience-matrix-hmac-secret-000000000000000"
	matrixJWTIssuer     = "cordum-matrix-issuer"
	matrixJWTAudience   = "cordum-gateway-matrix"
	matrixOIDCAudience  = "cordum-oidc-matrix"
	matrixWrongAudience = "another-service"
	matrixAgentID       = "agent-matrix-a"
	matrixWorkerID      = "worker-matrix"
)

type matrixDeployment struct {
	name    string
	auth    auth.AuthProvider
	handler http.Handler
}

type audienceMatrix struct {
	s      *server
	bus    *stubBus
	keys   matrixDeployment
	jwt    matrixDeployment
	tokens map[identity.Credential]string
}

// matrixCarrier sets one way of presenting a token on a request.
type matrixCarrier struct {
	name string
	set  func(r *http.Request, token string)
}

var matrixHTTPCarriers = []matrixCarrier{
	{"bearer", func(r *http.Request, tok string) { r.Header.Set("Authorization", "Bearer "+tok) }},
	{"x-api-key", func(r *http.Request, tok string) { r.Header.Set("X-API-Key", tok) }},
	{"cookie", func(r *http.Request, tok string) { r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tok}) }},
	{"ws-subprotocol", func(r *http.Request, tok string) {
		r.Header.Set("Sec-WebSocket-Protocol", auth.WSAuthSubprotocol+", "+base64.RawURLEncoding.EncodeToString([]byte(tok)))
	}},
}

func newAudienceMatrix(t *testing.T) *audienceMatrix {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	s.capProfile = capprofile.Production
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", s.jobStore.Client())
	}
	m := &audienceMatrix{s: s, bus: bus, tokens: map[identity.Credential]string{}}
	m.tokens[identity.SCIMBearer] = "scim-" + matrixRandom(t)
	oidcKey, oidcIssuer := startMatrixOIDC(t)
	m.mintBeforeAuth(t)
	users, keyStore := auth.NewRedisUserStoreFromClient(client), auth.NewRedisKeyStoreFromClient(client)
	env := map[string]string{
		"CORDUM_ENV":               "production",
		"CORDUM_API_KEYS":          fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":"admin","principal_id":%q}]`, matrixStaticKey, matrixTenant, matrixStaticOwner),
		"CORDUM_SCIM_BEARER_TOKEN": m.tokens[identity.SCIMBearer],
	}
	m.keys = m.deployment(t, "keys", env, users, keyStore)
	env["CORDUM_JWT_HMAC_SECRET"] = matrixJWTSecret
	env["CORDUM_JWT_ISSUER"] = matrixJWTIssuer
	env["CORDUM_JWT_AUDIENCE"] = matrixJWTAudience
	m.jwt = m.deployment(t, "jwt", env, users, keyStore)
	m.mintAfterAuth(t, users, keyStore, oidcKey, oidcIssuer)
	return m
}

// deployment builds Basic (with the shared session and managed-key stores)
// plus the OIDC adapter, composed as RunWithAuth composes them.
func (m *audienceMatrix) deployment(t *testing.T, name string, env map[string]string, users *auth.RedisUserStore, keys *auth.RedisKeyStore) matrixDeployment {
	t.Helper()
	basic := newBasicAuthForTest(t, env)
	basic.SetUserStore(users)
	basic.SetKeyStore(keys)
	oidc, err := auth.NewOIDCProviderFromEnv()
	if err != nil || oidc == nil {
		t.Fatalf("NewOIDCProviderFromEnv() = %v, %v", oidc, err)
	}
	t.Cleanup(oidc.Close)
	composite, err := auth.NewCompositeAuthProvider(basic, auth.NewOIDCAuthAdapter(oidc, matrixTenant))
	if err != nil {
		t.Fatalf("composite: %v", err)
	}
	m.s.auth = composite
	handler, err := newHTTPHandler(m.s)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	return matrixDeployment{name: name, auth: composite, handler: handler}
}

// startMatrixOIDC serves OIDC discovery and a one-key RSA JWKS on loopback and
// points the CORDUM_OIDC_* environment at it.
func startMatrixOIDC(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": issuer + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "matrix-kid", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	issuer = srv.URL
	for name, value := range map[string]string{
		"CORDUM_OIDC_ENABLED": "true", "CORDUM_OIDC_ISSUER": issuer, "CORDUM_OIDC_AUDIENCE": matrixOIDCAudience,
		"CORDUM_OIDC_ALLOW_HTTP": "true", "CORDUM_OIDC_ALLOW_PRIVATE": "true",
	} {
		t.Setenv(name, value)
	}
	return key, issuer
}

// mintBeforeAuth mints the credentials whose issuers need no auth provider.
func (m *audienceMatrix) mintBeforeAuth(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	setDelegationKeys(t)
	for _, id := range []string{matrixAgentID, "agent-matrix-b"} {
		createDelegationAgent(t, m.s, matrixTenant, id, []string{"read"}, []string{"job.test"})
	}
	service, err := m.s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegation service: %v", err)
	}
	m.tokens[identity.DelegationToken], _, err = service.IssueDelegationToken(ctx, delegation.IssueRequest{
		Tenant: matrixTenant, DelegatingAgentID: matrixAgentID, TargetAgentID: "agent-matrix-b",
		AllowedActions: []string{"read"}, AllowedTopics: []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("delegation token: %v", err)
	}
	issuer := wireSessionIssuer(t, m.s)
	if m.tokens[identity.ServiceToken], err = issuer.MintServiceToken("api-gateway"); err != nil {
		t.Fatalf("service token: %v", err)
	}
	cred, err := m.s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{TenantID: matrixTenant, WorkerID: matrixWorkerID})
	if err != nil {
		t.Fatalf("worker credential: %v", err)
	}
	m.tokens[identity.WorkerCredential] = cred.Token
	for c, aud := range map[identity.Credential]string{
		identity.WorkerSession: scheduler.WorkerHandshakeAudience, identity.WorkerSessionWrongAudience: matrixWrongAudience,
	} {
		m.tokens[c], _, err = issuer.IssueBound(ctx, scheduler.SessionBinding{
			WorkerID: matrixWorkerID, AgentID: matrixAgentID, Tenant: matrixTenant,
			Audience: aud, ProofKeyID: "matrix-proof-key", SDKVersion: "matrix-sdk",
		})
		if err != nil {
			t.Fatalf("worker session %s: %v", c, err)
		}
	}
	local, err := agentd.NewLocalServer(agentd.LocalServerConfig{})
	if err != nil {
		t.Fatalf("agentd: %v", err)
	}
	m.tokens[identity.AgentdNonce] = local.Nonce()
	m.tokens[identity.AgentIDHeader] = matrixAgentID
}

// mintAfterAuth mints the credentials of the stores and validators the
// deployments read: sessions, keys, gateway JWTs and OIDC bearers.
func (m *audienceMatrix) mintAfterAuth(t *testing.T, users *auth.RedisUserStore, keys *auth.RedisKeyStore, oidcKey *rsa.PrivateKey, oidcIssuer string) {
	t.Helper()
	ctx := context.Background()
	session := "session-" + matrixRandom(t)
	if err := users.StoreSession(ctx, session, &auth.User{ID: "user-matrix", Username: "matrix-admin", Tenant: matrixTenant, Role: "admin"}, time.Hour); err != nil {
		t.Fatalf("store session: %v", err)
	}
	m.tokens[identity.UserSession] = session
	m.tokens[identity.APIKeyStatic] = matrixStaticKey
	raw, err := auth.GenerateRawKey()
	if err != nil {
		t.Fatalf("raw key: %v", err)
	}
	if err := keys.Create(ctx, &auth.ManagedKey{Name: "audience-matrix", Tenant: matrixTenant, Scopes: []string{"admin"}}, raw); err != nil {
		t.Fatalf("managed key: %v", err)
	}
	m.tokens[identity.APIKeyManaged] = raw
	hmacKey := []byte(matrixJWTSecret)
	m.tokens[identity.GatewayJWT] = matrixSign(t, jwt.SigningMethodHS256, hmacKey, "", matrixJWTIssuer, matrixJWTAudience, "role", "tenant")
	m.tokens[identity.GatewayJWTWrongAudience] = matrixSign(t, jwt.SigningMethodHS256, hmacKey, "", matrixJWTIssuer, matrixWrongAudience, "role", "tenant")
	m.tokens[identity.OIDCBearer] = matrixSign(t, jwt.SigningMethodRS256, oidcKey, "matrix-kid", oidcIssuer, matrixOIDCAudience, "cordum_role", "org_id")
	m.tokens[identity.OIDCBearerWrongAudience] = matrixSign(t, jwt.SigningMethodRS256, oidcKey, "matrix-kid", oidcIssuer, matrixWrongAudience, "cordum_role", "org_id")
}

// matrixSign signs an admin token for matrixTenant; only aud differs between
// a credential and its wrong-audience variant.
func matrixSign(t *testing.T, method jwt.SigningMethod, key any, kid, iss, aud, roleClaim, tenantClaim string) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": "principal-matrix-" + roleClaim, "exp": time.Now().Add(time.Hour).Unix(),
		roleClaim: "admin", tenantClaim: matrixTenant,
	})
	if kid != "" {
		tok.Header["kid"] = kid
	}
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign %s: %v", method.Alg(), err)
	}
	return signed
}

func matrixRandom(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// carriers lists every way the gateway's HTTP entries read credential c.
func matrixCarriersFor(c identity.Credential) []matrixCarrier {
	switch c {
	case identity.Anonymous:
		return []matrixCarrier{{"none", func(*http.Request, string) {}}}
	case identity.AgentIDHeader:
		return []matrixCarrier{{"x-agent-id", func(r *http.Request, v string) { r.Header.Set("X-Agent-Id", v) }}}
	case identity.AgentdNonce:
		nonce := matrixCarrier{"agentd-nonce", func(r *http.Request, v string) { r.Header.Set("X-Cordum-Agentd-Nonce", v) }}
		return append(append([]matrixCarrier{}, matrixHTTPCarriers...), nonce)
	}
	return matrixHTTPCarriers
}

// matrixNaturalCarrier is where an allowed credential is presented, and in
// which deployment it is meant to work.
func (m *audienceMatrix) matrixNatural(c identity.Credential) (matrixDeployment, matrixCarrier) {
	switch c {
	case identity.APIKeyStatic, identity.APIKeyManaged:
		return m.keys, matrixHTTPCarriers[1]
	case identity.GatewayJWT:
		return m.jwt, matrixHTTPCarriers[0]
	}
	return m.keys, matrixHTTPCarriers[0]
}

// presentHTTP presents credential c at entry e. An allowed credential is sent
// once through its natural carrier; any other is sent in every carrier in
// both deployments. Accepted is any 2xx; Effect is a change in effect()
// between before the first request and after the last.
func (m *audienceMatrix) presentHTTP(t *testing.T, e identity.Entry, c identity.Credential, newReq func() *http.Request, effect func(t *testing.T) int, what string) identity.Result {
	t.Helper()
	type send struct {
		d       matrixDeployment
		carrier matrixCarrier
	}
	var sends []send
	if identity.Allowed(c, e) {
		d, carrier := m.matrixNatural(c)
		sends = append(sends, send{d, carrier})
	} else {
		for _, d := range []matrixDeployment{m.keys, m.jwt} {
			for _, carrier := range matrixCarriersFor(c) {
				sends = append(sends, send{d, carrier})
			}
		}
	}
	before := effect(t)
	res := identity.Result{}
	var trail []string
	for _, sd := range sends {
		req := newReq()
		sd.carrier.set(req, m.tokens[c])
		rec := httptest.NewRecorder()
		m.s.auth = sd.d.auth
		sd.d.handler.ServeHTTP(rec, req)
		res.Accepted = res.Accepted || (rec.Code >= 200 && rec.Code < 300)
		trail = append(trail, fmt.Sprintf("%s/%s=%d", sd.d.name, sd.carrier.name, rec.Code))
	}
	after := effect(t)
	res.Effect = after != before
	res.Evidence = fmt.Sprintf("%s %d->%d; %v", what, before, after, trail)
	return res
}
