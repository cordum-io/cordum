package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/mcp"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
	"github.com/cordum/cordum/core/policysign"
	"github.com/cordum/cordum/tests/identity"
)

// TestAudienceMatrixMCPStandalone presents every credential to the HTTP
// transport's /sse and /message routes in place of the inbound token.
func TestAudienceMatrixMCPStandalone(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	kit := mintMatrixCredentials(t)
	present := func(t *testing.T, c identity.Credential) identity.Result {
		tok, ok := kit[c]
		if !ok {
			t.Fatalf("no minted credential for %q", c)
		}
		var res identity.Result
		var evidence []string
		for _, h := range mcpMatrixCarriers(c, tok) {
			for _, o := range newMCPMatrixFixture(t).send(t, h) {
				res.Accepted = res.Accepted || o.status != http.StatusUnauthorized
				res.Effect = res.Effect || o.effect
				evidence = append(evidence, o.evidence)
			}
		}
		res.Evidence = strings.Join(evidence, "; ")
		return res
	}
	probe := func(t *testing.T) identity.Result {
		f := newMCPMatrixFixture(t)
		res := identity.Result{Accepted: true, Effect: true}
		var evidence []string
		for _, o := range f.send(t, http.Header{"Authorization": {"Bearer " + f.token}}) {
			res.Accepted = res.Accepted && o.status == http.StatusOK
			res.Effect = res.Effect && o.effect
			evidence = append(evidence, o.evidence)
		}
		res.Evidence = strings.Join(evidence, "; ")
		return res
	}
	identity.Run(t, identity.MCPStandalone, present, identity.Baseline(probe))
}

// mcpMatrixCarriers is one header set per carrier: every HTTP credential
// carrier, or X-Agent-Id alone for agent_id_header, or none for anonymous.
func mcpMatrixCarriers(c identity.Credential, tok string) []http.Header {
	switch c {
	case identity.Anonymous:
		return []http.Header{{}}
	case identity.AgentIDHeader:
		return []http.Header{{"X-Agent-Id": {tok}}}
	}
	return []http.Header{
		{"Authorization": {"Bearer " + tok}},
		{"X-Api-Key": {tok}},
		{"Cookie": {"cordum_session=" + tok}},
	}
}

// mcpMatrixFixture is the HTTP-mode server main() runs, with inbound token
// token, serving httpMux in front of a gateway stub that counts job submits.
type mcpMatrixFixture struct {
	url, token string
	transport  *mcp.HTTPTransport
	jobs       atomic.Int64
}

func newMCPMatrixFixture(t *testing.T) *mcpMatrixFixture {
	t.Helper()
	f := &mcpMatrixFixture{token: rand.Text(), transport: mcp.NewHTTPTransport(0, 5*time.Second)}
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs" {
			f.jobs.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"job-matrix"}`))
	}))
	t.Cleanup(gw.Close)
	client, err := gatewayHTTPClient("")
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}
	tools := mcp.NewToolRegistry()
	if err := mcptools.Register(tools, mcptools.NewGatewayClient(gw.URL, "gateway-key", client).WithAllowPrivateHosts(true)); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	server := mcp.NewServer(f.transport, tools, mcp.NewResourceRegistry(), mcp.ServerConfig{Name: "cordum", RequestTimeout: 5 * time.Second})
	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	t.Cleanup(func() { _ = f.transport.Close(); <-served })
	srv := httptest.NewServer(httpMux(f.token, f.transport))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

type mcpObservation struct {
	status   int
	effect   bool
	evidence string
}

// send makes one tools/call POST to /message and opens /sse once, both with
// headers h, and snapshots the effect of each.
func (f *mcpMatrixFixture) send(t *testing.T, h http.Header) []mcpObservation {
	t.Helper()
	carrier := "none"
	for k := range h {
		carrier = k
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + mcp.ToolSubmitJob + `","arguments":{"prompt":"audience matrix"}}}`
	jobsBefore := f.jobs.Load()
	status := f.do(t, http.MethodPost, "/message", h, body, func() {})
	jobsAfter := f.jobs.Load()
	message := mcpObservation{status, jobsAfter > jobsBefore,
		fmt.Sprintf("%s /message status %d, gateway POST /api/v1/jobs %d->%d", carrier, status, jobsBefore, jobsAfter)}

	sessionsBefore := f.transport.ActiveSessionCount()
	var sessionsOpen int
	status = f.do(t, http.MethodGet, "/sse", h, "", func() { sessionsOpen = f.transport.ActiveSessionCount() })
	sse := mcpObservation{status, sessionsOpen > sessionsBefore,
		fmt.Sprintf("%s /sse status %d, open SSE sessions %d->%d", carrier, status, sessionsBefore, sessionsOpen)}
	return []mcpObservation{message, sse}
}

// do sends one request and calls during while the response is still open,
// so an SSE stream is counted before it is closed.
func (f *mcpMatrixFixture) do(t *testing.T, method, path string, h http.Header, body string, during func()) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, f.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header = h.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	during()
	return resp.StatusCode
}

// mintMatrixCredentials mints every identity.Credential with its real issuer
// and keys. A wrong-audience variant is the same token with another aud.
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

	hook, err := agentd.NewLocalServer(agentd.LocalServerConfig{BindURL: "http://127.0.0.1:8765/v1/edge/hooks/claude"})
	check(err)
	kit[identity.AgentdNonce] = hook.Nonce()
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
