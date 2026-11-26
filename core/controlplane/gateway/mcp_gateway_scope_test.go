package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
	"github.com/redis/go-redis/v9"
)

func TestMCPGatewayManagedKeyScope(t *testing.T) {
	m := newAudienceMatrix(t)
	m.matrixStartMCP(t)
	m.s.auth = m.keys.auth
	client, ok := m.s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", m.s.jobStore.Client())
	}
	m.s.keyStore = auth.NewRedisKeyStoreFromClient(client)
	srv := httptest.NewServer(m.keys.handler)
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name, scope   string
		status, delta int
	}{
		{"mcpgateway_scope", "mcpgateway:*", http.StatusOK, 2},
		{"unrelated_scopes", "jobs:write", http.StatusUnauthorized, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := mintMCPGatewayKey(t, m, tc.scope)
			before := m.jobEffects(t)
			checkMCPGatewayMessage(t, m, key, tc.status)
			if after := m.jobEffects(t); after != before+tc.delta {
				t.Fatalf("message jobEffects %d->%d, want delta %d", before, after, tc.delta)
			}
			before = m.jobEffects(t)
			checkMCPGatewaySSE(t, srv.URL, key, tc.status)
			if after := m.jobEffects(t); after != before {
				t.Fatalf("SSE jobEffects %d->%d, want unchanged", before, after)
			}
		})
	}
}

func mintMCPGatewayKey(t *testing.T, m *audienceMatrix, scope string) string {
	t.Helper()
	req := matrixRequest("/api/v1/auth/keys", fmt.Sprintf(`{"name":"mcp-scope","scopes":[%q]}`, scope))
	req.Header.Set("X-API-Key", matrixStaticKey)
	rec := httptest.NewRecorder()
	m.keys.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key: status %d, want 201", rec.Code)
	}
	var created struct {
		Key    apiKeyResponse `json:"key"`
		Secret string         `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if created.Key.ID == "" || created.Secret == "" {
		t.Fatal("create key returned no id or secret")
	}
	if err := m.s.agentIdentityStore.LinkWorker(context.Background(), matrixMCPAgent, created.Key.ID); err != nil {
		t.Fatalf("link key: %v", err)
	}
	return created.Secret
}

func checkMCPGatewayMessage(t *testing.T, m *audienceMatrix, key string, status int) {
	t.Helper()
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + mcp.ToolSubmitJob +
		`","arguments":{"prompt":"audience matrix","topic":"job.test"}}}`
	req := matrixRequest("/mcp/message", call)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	m.keys.handler.ServeHTTP(rec, req)
	if rec.Code != status {
		t.Fatalf("message status %d, want %d", rec.Code, status)
	}
}

func checkMCPGatewaySSE(t *testing.T, url, key string, status int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", matrixTenant)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("SSE: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close SSE response: %v", err)
		}
	}()
	if resp.StatusCode != status {
		t.Fatalf("SSE status %d, want %d", resp.StatusCode, status)
	}
	session := resp.Header.Get("X-MCP-Session-ID")
	events := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	if status == http.StatusOK {
		if session == "" || !events {
			t.Fatal("SSE missing session or event-stream content type")
		}
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("read denied SSE: %v", err)
	}
	if session != "" || events || strings.Contains(string(body), "event:") || strings.Contains(string(body), "data:") {
		t.Fatal("denied SSE returned a session or event stream")
	}
}
