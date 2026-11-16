package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/redis/go-redis/v9"
)

// These tests pin the scope resources of the /api/v1/edge routes in the
// production profile. Each managed key is minted with the scopes under test
// and sent through the real auth chain (newHTTPHandler). Effects are counted
// as Redis records, so a refusal is proved by a record that was not written.

const (
	edgeScopeStaticKey   = "edgescope-static-admin-key-000000000000000"
	edgeScopeSessionBody = `{"agent_product":"claude-code","mode":"local-dev"}`
	// The endpoint is a public IP literal, so validation resolves no DNS.
	edgeScopeUpstreamBody = `{"name":"edge-scope-tools","transport":"http","endpoint":"https://93.184.216.34/tools",` +
		`"auth_secret_ref":"secret://vault/mcp/edge-scope-tools","risk":"medium","enabled":true}`
)

type edgeScopeFixture struct {
	s        *server
	handler  http.Handler
	provider *auth.BasicAuthProvider
	keys     *auth.RedisKeyStore
}

// edgeScopeRoute is a protected Edge write outside the edge resource and the
// Redis records it creates.
type edgeScopeRoute struct {
	path, body, records string
}

func newEdgeScopeFixture(t *testing.T) edgeScopeFixture {
	t.Helper()
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	s, _, _ := newTestGateway(t)
	s.capProfile = capprofile.Production
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", s.jobStore.Client())
	}
	s.edgeStore = edgecore.NewRedisStoreFromClient(client)
	shadowStore, err := shadow.NewRedisStore(client)
	if err != nil {
		t.Fatalf("shadow.NewRedisStore: %v", err)
	}
	s.shadowFindingStore = shadowStore
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ENV":      "production",
		"CORDUM_API_KEYS": `[{"key":"` + edgeScopeStaticKey + `","tenant":"default","role":"admin","principal_id":"edge-scope-admin"}]`,
	})
	keys := auth.NewRedisKeyStoreFromClient(client)
	provider.SetKeyStore(keys)
	t.Cleanup(provider.DrainUsage)
	s.auth, s.keyStore = provider, keys
	handler, err := newHTTPHandler(s)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	return edgeScopeFixture{s: s, handler: handler, provider: provider, keys: keys}
}

// edgeScopeRoutes returns the MCP-upstream and shadow-exception creates.
func edgeScopeRoutes(t *testing.T) []edgeScopeRoute {
	t.Helper()
	exception, err := json.Marshal(validExceptionCreateBody())
	if err != nil {
		t.Fatalf("marshal exception: %v", err)
	}
	return []edgeScopeRoute{
		{"/api/v1/edge/mcp/upstreams", edgeScopeUpstreamBody, "edge:mcp:upstream:*"},
		{"/api/v1/edge/shadow/exception", string(exception), "edge:shadow:exception:*"},
	}
}

// mint stores a managed key in tenant default with scopes, the way
// POST /api/v1/auth/keys stores it, and returns it with its secret.
func (f edgeScopeFixture) mint(t *testing.T, scopes ...string) (*auth.ManagedKey, string) {
	t.Helper()
	secret, err := auth.GenerateRawKey()
	if err != nil {
		t.Fatalf("GenerateRawKey() error = %v, want nil", err)
	}
	key := &auth.ManagedKey{Name: "edge-scope", Tenant: "default", Scopes: scopes, CreatedAt: time.Now().UTC()}
	if err := f.keys.Create(context.Background(), key, secret); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	return key, secret
}

func (f edgeScopeFixture) post(t *testing.T, apiKey, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", apiKey)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	f.provider.DrainUsage()
	return rec
}

// records counts the Redis keys matching pattern. Session heartbeats are not
// session records, so they are left out.
func (f edgeScopeFixture) records(t *testing.T, pattern string) int {
	t.Helper()
	keys, err := f.s.jobStore.Client().Keys(context.Background(), pattern).Result()
	if err != nil {
		t.Fatalf("KEYS %s: %v", pattern, err)
	}
	n := 0
	for _, k := range keys {
		if !strings.HasPrefix(k, "edge:session:heartbeat:") {
			n++
		}
	}
	return n
}

func TestManagedKeyEdgeScope_EdgeWriteKeyCreatesSession(t *testing.T) {
	f := newEdgeScopeFixture(t)
	key, secret := f.mint(t, "edge:write")
	before := f.records(t, "edge:session:*")
	rec := f.post(t, secret, "/api/v1/edge/sessions", edgeScopeSessionBody)
	var resp edgeSessionCreateResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.SessionID == "" {
		t.Fatalf("status = %d body=%s, want 201 with a session_id", rec.Code, rec.Body.String())
	}
	if got := f.records(t, "edge:session:*"); got != before+1 {
		t.Fatalf("edge:session records = %d, want %d", got, before+1)
	}
	session, ok, err := f.s.edgeStore.GetSession(context.Background(), "default", resp.SessionID)
	if err != nil || !ok || session.PrincipalID != key.ID {
		t.Fatalf("GetSession(%s) = (%+v, %v, %v), want a session owned by the key id %q", resp.SessionID, session, ok, err, key.ID)
	}
}

func TestManagedKeyEdgeScope_InsufficientKeysCreateNoSession(t *testing.T) {
	f := newEdgeScopeFixture(t)
	for _, tc := range []struct {
		name   string
		scopes []string
	}{
		{"unscoped key keeps the viewer default", nil},
		{"jobs:write", []string{"jobs:write"}},
		{"edge:read", []string{"edge:read"}},
		{"approvals:write", []string{"approvals:write"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, secret := f.mint(t, tc.scopes...)
			before := f.records(t, "edge:session:*")
			if rec := f.post(t, secret, "/api/v1/edge/sessions", edgeScopeSessionBody); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body=%s, want 403", rec.Code, rec.Body.String())
			}
			if got := f.records(t, "edge:session:*"); got != before {
				t.Fatalf("edge:session records = %d after the refusal, want %d", got, before)
			}
		})
	}
}

// An edge:write key, which an agent holds, never reaches the admin-gated
// MCP-upstream and shadow-exception writes.
func TestManagedKeyEdgeScope_EdgeWriteKeyWritesNoOtherEdgeDomain(t *testing.T) {
	f := newEdgeScopeFixture(t)
	_, secret := f.mint(t, "edge:write")
	for _, route := range edgeScopeRoutes(t) {
		t.Run(route.path, func(t *testing.T) {
			before := f.records(t, route.records)
			if rec := f.post(t, secret, route.path, route.body); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body=%s, want 403", rec.Code, rec.Body.String())
			}
			if got := f.records(t, route.records); got != before {
				t.Fatalf("%s records = %d after the refusal, want %d", route.records, got, before)
			}
		})
	}
}

// An admin-scoped key reaches the same writes, and the same record counts
// see them, so the refusals above are not vacuous.
func TestManagedKeyEdgeScope_AdminKeyWritesOtherEdgeDomains(t *testing.T) {
	f := newEdgeScopeFixture(t)
	_, secret := f.mint(t, "admin")
	for _, route := range edgeScopeRoutes(t) {
		t.Run(route.path, func(t *testing.T) {
			before := f.records(t, route.records)
			if rec := f.post(t, secret, route.path, route.body); rec.Code != http.StatusCreated {
				t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
			}
			if got := f.records(t, route.records); got <= before {
				t.Fatalf("%s records = %d after the create, want more than %d", route.records, got, before)
			}
		})
	}
}

func TestManagedKeyEdgeScope_EdgeScopesMintable(t *testing.T) {
	f := newEdgeScopeFixture(t)
	for _, tc := range []struct {
		scope string
		code  int
	}{
		{"edge:read", http.StatusCreated},
		{"edge:write", http.StatusCreated},
		{"edge:*", http.StatusCreated},
		{"edge:bogus", http.StatusBadRequest},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			before, err := f.keys.List(context.Background(), "default")
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			rec := f.post(t, edgeScopeStaticKey, "/api/v1/auth/keys", `{"name":"edge-scope","scopes":["`+tc.scope+`"]}`)
			if rec.Code != tc.code {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), tc.code)
			}
			after, err := f.keys.List(context.Background(), "default")
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			want := len(before)
			if tc.code == http.StatusCreated {
				want++
			}
			if len(after) != want {
				t.Fatalf("stored keys = %d, want %d", len(after), want)
			}
		})
	}
}
