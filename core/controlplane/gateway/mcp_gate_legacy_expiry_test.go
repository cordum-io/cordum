package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// Regression coverage for the legacy -32099 path: MCPApprovalRecord.ExpiresAt
// is UnixMicro, and decoding it as seconds produced a year far past 9999 that
// json.Marshal rejects, so HTTP MCP answered 500 "encode response".

// lexpAssertWithinTTL fails unless got lies within the default approval TTL
// window measured from [before, after], with one second of slack.
func lexpAssertWithinTTL(t *testing.T, got, before, after time.Time) {
	t.Helper()
	lo := before.Add(mcpApprovalDefaultTTL).Add(-time.Second)
	hi := after.Add(mcpApprovalDefaultTTL).Add(time.Second)
	if got.Before(lo) || got.After(hi) {
		t.Fatalf("expires_at = %s, want within [%s, %s]", got, lo, hi)
	}
}

func TestGate_LegacyApprovalExpiresAtDecodesUnixMicro(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "tnt_a",
		AgentID:   "agent_alpha",
		Principal: "alice@corp",
	})
	before := time.Now()
	got, err := gate.Check(ctx,
		mcp.Tool{Name: "fs.write", RequiresApproval: true},
		json.RawMessage(`{"path":"/etc/hostname"}`))
	after := time.Now()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired")
	}
	rec, err := store.Get(ctx, got.ApprovalID)
	if err != nil || rec == nil {
		t.Fatalf("store.Get(%q) = %v, %v", got.ApprovalID, rec, err)
	}
	if want := time.UnixMicro(rec.ExpiresAt).UTC(); !got.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %s, want %s (record micros %d)", got.ExpiresAt, want, rec.ExpiresAt)
	}
	if got.ExpiresAt.Year() >= 10000 {
		t.Errorf("ExpiresAt year = %d; not JSON-encodable", got.ExpiresAt.Year())
	}
	lexpAssertWithinTTL(t, got.ExpiresAt, before, after)
}

type lexpAuth struct {
	testAuthProvider
}

func (lexpAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	if strings.TrimSpace(r.Header.Get("X-API-Key")) != "key-lexp" {
		return nil, errors.New("unauthorized")
	}
	return &auth.AuthContext{
		Tenant: "default", PrincipalID: "lexp", Role: "admin",
		APIKey: "key-lexp", AuthSource: auth.AuthSourceAPIKey,
	}, nil
}

// lexpHarness serves the HTTP MCP routes WITHOUT an Edge store, so the gate
// answers -32099 from the legacy MCPApprovalStore path alone.
func lexpHarness(t *testing.T) *httptest.Server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = lexpAuth{}
	ctx := context.Background()
	for _, doc := range []*configsvc.Document{
		{Scope: configsvc.ScopeSystem, ScopeID: "default",
			Data: map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}}},
		{Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID,
			Data: map[string]any{packs.PolicyConfigKey: map[string]any{}}},
	} {
		if err := s.configSvc.Set(ctx, doc); err != nil {
			t.Fatalf("set config %s/%s: %v", doc.Scope, doc.ScopeID, err)
		}
	}
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: "agent-lexp", TenantID: "default", Name: "agent-lexp", Owner: "ops@example.com",
		RiskTier: "high", AllowedTools: []string{"cordum_*"},
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, "agent-lexp", "lexp"); err != nil {
		t.Fatalf("link worker: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	srv := httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPHTTP_ApprovalRequiredWithoutEdgeStoreReturns32099(t *testing.T) {
	t.Parallel()
	srv := lexpHarness(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cordum_create_workflow","arguments":{"name":"mcp-demo","steps":{"s1":{"type":"approval"}}}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/message", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-lexp")
	req.Header.Set("X-Tenant-ID", "default")
	before := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	after := time.Now()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", resp.StatusCode, raw)
	}
	var reply struct {
		Error *struct {
			Code int `json:"code"`
			Data struct {
				ApprovalID string `json:"approval_id"`
				ExpiresAt  string `json:"expires_at"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply %q: %v", raw, err)
	}
	if reply.Error == nil || reply.Error.Code != -32099 {
		t.Fatalf("want JSON-RPC -32099, got %s", raw)
	}
	if reply.Error.Data.ApprovalID == "" {
		t.Errorf("-32099 without approval_id: %s", raw)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, reply.Error.Data.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q not RFC3339: %v", reply.Error.Data.ExpiresAt, err)
	}
	lexpAssertWithinTTL(t, expiresAt, before, after)
}
