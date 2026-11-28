package agentd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/policysign"
	"github.com/cordum/cordum/tests/identity"
)

// TestAudienceMatrixEdgeAgentdHook presents every credential to the local
// hook server. Only the server's own nonce may get a hook event recorded.
func TestAudienceMatrixEdgeAgentdHook(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	kit := mintMatrixCredentials(t)
	present := func(t *testing.T, c identity.Credential) identity.Result {
		var res identity.Result
		var evidence []string
		for i := range hookMatrixCarriers(c, "") {
			f := newHookMatrixFixture(t)
			tok, ok := kit[c]
			if c == identity.AgentdNonce {
				tok, ok = f.server.Nonce(), true
			}
			if !ok {
				t.Fatalf("no minted credential for %q", c)
			}
			h := hookMatrixCarriers(c, tok)[i]
			before := len(f.events.events)
			status := f.post(t, h)
			after := len(f.events.events)
			res.Accepted = res.Accepted || status != http.StatusUnauthorized
			res.Effect = res.Effect || after > before
			evidence = append(evidence, fmt.Sprintf("%s status %d, hook events written %d->%d", hookCarrierName(h), status, before, after))
		}
		res.Evidence = strings.Join(evidence, "; ")
		return res
	}
	identity.Run(t, identity.EdgeAgentdHook, present)
}

// hookMatrixCarriers is one header set per carrier. agentd_nonce goes in its
// own header; agent_id_header is X-Agent-Id alone; anonymous sends nothing;
// every other credential goes in the nonce header and each HTTP carrier.
func hookMatrixCarriers(c identity.Credential, tok string) []http.Header {
	switch c {
	case identity.Anonymous:
		return []http.Header{{}}
	case identity.AgentIDHeader:
		return []http.Header{{"X-Agent-Id": {tok}}}
	case identity.AgentdNonce:
		return []http.Header{{agentdNonceHeader: {tok}}}
	}
	return []http.Header{
		{agentdNonceHeader: {tok}},
		{"Authorization": {"Bearer " + tok}},
		{"X-Api-Key": {tok}},
		{"Cookie": {"cordum_session=" + tok}},
	}
}

func hookCarrierName(h http.Header) string {
	for k := range h {
		return k
	}
	return "none"
}

// hookMatrixFixture is a LocalServer with a generated nonce, bound to one
// agentd session, whose event writer stands in for the gateway client.
type hookMatrixFixture struct {
	server *LocalServer
	events *stubEventWriter
}

func newHookMatrixFixture(t *testing.T) *hookMatrixFixture {
	t.Helper()
	f := &hookMatrixFixture{events: &stubEventWriter{}}
	var err error
	f.server, err = NewLocalServer(LocalServerConfig{
		State:       SessionState{SessionID: "sess-matrix", ExecutionID: "exec-matrix", TenantID: "default", PrincipalID: "principal-matrix"},
		EventWriter: f.events,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	return f
}

// post sends one PreToolUse hook for the fixture's session with headers h.
func (f *hookMatrixFixture) post(t *testing.T, h http.Header) int {
	t.Helper()
	body, err := json.Marshal(claude.AgentdRequest{EventName: "PreToolUse", SessionID: "sess-matrix", ExecutionID: "exec-matrix", ToolName: "Bash"})
	if err != nil {
		t.Fatalf("marshal hook: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, defaultAgentdHookPath, bytes.NewReader(body))
	req.Header = h.Clone()
	rr := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rr, req)
	return rr.Code
}

// mintMatrixCredentials mints every identity.Credential except agentd_nonce,
// which is each fixture's own LocalServer.Nonce(), with its real issuer and
// keys. A wrong-audience variant is the same token with another aud.
func mintMatrixCredentials(t *testing.T) map[identity.Credential]string {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("mint credential: %v", err)
		}
	}
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	kit := map[identity.Credential]string{
		identity.Anonymous:     "",
		identity.APIKeyStatic:  rand.Text(),           // a CORDUM_API_KEYS entry
		identity.SCIMBearer:    "scim-" + rand.Text(), // a CORDUM_SCIM_BEARER_TOKEN value
		identity.AgentIDHeader: "agent-matrix",
	}

	kit[identity.UserSession] = "session-" + rand.Text()
	check(auth.NewRedisUserStoreFromClient(rdb).StoreSession(ctx, kit[identity.UserSession],
		&auth.User{ID: "user-matrix", Username: "matrix", Tenant: "default", Role: "admin"}, time.Hour))
	managed, err := auth.GenerateRawKey()
	check(err)
	check(auth.NewRedisKeyStoreFromClient(rdb).Create(ctx, &auth.ManagedKey{Name: "matrix", Tenant: "default"}, managed))
	kit[identity.APIKeyManaged] = managed

	hmacSecret := []byte(rand.Text()) // CORDUM_JWT_HMAC_SECRET
	oidcKey, err := rsa.GenerateKey(rand.Reader, 2048)
	check(err)
	kit[identity.GatewayJWT] = matrixJWT(t, jwt.SigningMethodHS256, hmacSecret, "cordum-matrix", "cordum-api")
	kit[identity.GatewayJWTWrongAudience] = matrixJWT(t, jwt.SigningMethodHS256, hmacSecret, "cordum-matrix", "other-audience")
	kit[identity.OIDCBearer] = matrixJWT(t, jwt.SigningMethodRS256, oidcKey, "https://idp.matrix.test", "cordum-oidc")
	kit[identity.OIDCBearerWrongAudience] = matrixJWT(t, jwt.SigningMethodRS256, oidcKey, "https://idp.matrix.test", "other-audience")

	_, delegationKey, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	delegations, err := delegation.NewTokenService(delegation.SigningKey{KID: "matrix-delegation", PrivateKey: delegationKey}, nil, nil, nil)
	check(err)
	kit[identity.DelegationToken], _, err = delegations.IssueDelegationToken(ctx,
		delegation.IssueRequest{Tenant: "default", DelegatingAgentID: "agent-a", TargetAgentID: "agent-b"})
	check(err)
	_, serviceKey, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	kit[identity.ServiceToken], err = servicetoken.MintService(serviceKey, "matrix-service", servicetoken.IdentityGateway, time.Now())
	check(err)
	issued, err := workercredentials.NewService(configsvc.NewFromClient(rdb)).Create(ctx,
		workercredentials.IssueInput{TenantID: "default", WorkerID: "worker-matrix"})
	check(err)
	kit[identity.WorkerCredential] = issued.Token

	sessionPub, sessionKey, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	trust := policysign.NewTrustStore()
	check(trust.Add("matrix-session", sessionPub))
	// IssueBound revokes the worker's previous token, so each audience is
	// issued into its own Redis DB.
	mintSession := func(db int, aud string) string {
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), DB: db})
		t.Cleanup(func() { _ = client.Close() })
		issuer, err := scheduler.NewSessionTokenIssuer(sessionKey, "matrix-session", trust, client, scheduler.SessionTokenIssuerOptions{})
		check(err)
		tok, _, err := issuer.IssueBound(ctx, scheduler.SessionBinding{WorkerID: "worker-matrix", AgentID: "agent-matrix",
			Tenant: "default", Audience: aud, ProofKeyID: "proof-matrix", SDKVersion: "matrix"})
		check(err)
		return tok
	}
	kit[identity.WorkerSession] = mintSession(0, scheduler.WorkerHandshakeAudience)
	kit[identity.WorkerSessionWrongAudience] = mintSession(1, "other-audience")
	return kit
}

// matrixJWT signs a one-hour bearer JWT for issuer iss and audience aud.
func matrixJWT(t *testing.T, method jwt.SigningMethod, key any, iss, aud string) string {
	t.Helper()
	now := time.Now()
	tok, err := jwt.NewWithClaims(method, jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": "user-matrix", "tenant": "default", "role": "admin",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatalf("sign JWT for %s: %v", aud, err)
	}
	return tok
}
