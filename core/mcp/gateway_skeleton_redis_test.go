package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/edge"
	"github.com/redis/go-redis/v9"
)

func TestMCPGatewayClientConnectPersistsConnectedEventRedisStore(t *testing.T) {
	store, _ := newGatewayRedisStore(t)
	mux := newGatewayRedisMux(t, store, true)
	req := newGatewayRedisRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("connect: got status %d want 200/202; body=%q", rr.Code, rr.Body.String())
	}
	session, execution, event := requireGatewayRedisRecord(t, store, "tenant-a", edge.EventKindMCPServerConnected)
	assertGatewayRedisLinkage(t, session, execution, event)
	if event.Decision != edge.DecisionRecorded || event.Status != edge.ActionStatusOK {
		t.Fatalf("connected event decision/status = %q/%q, want recorded/ok", event.Decision, event.Status)
	}
}

func TestMCPGatewayEnabledNoUpstreamPersistsFailedEventRedisStore(t *testing.T) {
	store, _ := newGatewayRedisStore(t)
	mux := newGatewayRedisMux(t, store, true)
	req := newGatewayRedisRequest(http.MethodPost, "/api/v1/mcp/gateway/upstream/connect", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("enabled no-upstream: got status %d want 503; body=%q", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "code", "no_upstream_configured")
	session, execution, event := requireGatewayRedisRecord(t, store, "tenant-a", edge.EventKindMCPServerFailed)
	assertGatewayRedisLinkage(t, session, execution, event)
	if event.Decision != edge.DecisionDeny || event.Status != edge.ActionStatusFailed {
		t.Fatalf("failed event decision/status = %q/%q, want deny/failed", event.Decision, event.Status)
	}
	assertJSONField(t, rr.Body.Bytes(), "session_id", session.SessionID)
	assertJSONField(t, rr.Body.Bytes(), "execution_id", execution.ExecutionID)
}

func TestMCPGatewayDisabledUpstreamWritesNoRedisRecords(t *testing.T) {
	store, mr := newGatewayRedisStore(t)
	mux := newGatewayRedisMux(t, store, false)
	req := newGatewayRedisRequest(http.MethodPost, "/api/v1/mcp/gateway/upstream/connect", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled upstream: got status %d want 503; body=%q", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "code", "gateway_disabled")
	assertNoGatewayRedisRecords(t, store, mr, "tenant-a")
}

func newGatewayRedisStore(t *testing.T) (*edge.RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	return edge.NewRedisStoreFromClient(client), mr
}

func newGatewayRedisMux(t *testing.T, store edge.Store, enabled bool) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	err := RegisterGatewayRoutes(mux, GatewayDeps{
		Store: store,
		GatewayEnabled: func(context.Context, string) (bool, error) {
			return enabled, nil
		},
		ResolveTenant: func(r *http.Request) (string, string, error) {
			tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			if tenantID == "" {
				return "", "", errors.New("tenant_missing")
			}
			return tenantID, "principal-test", nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterGatewayRoutes: %v", err)
	}
	return mux
}

func newGatewayRedisRequest(method, path, tenantID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Tenant-ID", tenantID)
	return req
}

func requireGatewayRedisRecord(t *testing.T, store *edge.RedisStore, tenantID string, kind edge.EventKind) (edge.EdgeSession, edge.AgentExecution, edge.AgentActionEvent) {
	t.Helper()
	ctx := context.Background()
	sessions, err := store.ListSessions(ctx, edge.ListSessionsQuery{TenantID: tenantID, Limit: 10})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions.Items) != 1 {
		t.Fatalf("ListSessions count = %d, want 1: %#v", len(sessions.Items), sessions.Items)
	}
	session := sessions.Items[0]
	executions, err := store.ListExecutions(ctx, edge.ListExecutionsQuery{TenantID: tenantID, SessionID: session.SessionID, Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(executions.Items) != 1 {
		t.Fatalf("ListExecutions count = %d, want 1: %#v", len(executions.Items), executions.Items)
	}
	execution := executions.Items[0]
	events, err := store.ListEvents(ctx, edge.ListEventsQuery{TenantID: tenantID, ExecutionID: execution.ExecutionID, Kind: kind, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("ListEvents kind %q count = %d, want 1: %#v", kind, len(events.Items), events.Items)
	}
	return session, execution, events.Items[0]
}

func assertGatewayRedisLinkage(t *testing.T, session edge.EdgeSession, execution edge.AgentExecution, event edge.AgentActionEvent) {
	t.Helper()
	if session.TenantID != "tenant-a" || session.PrincipalID != "principal-test" {
		t.Fatalf("session tenant/principal = %q/%q, want tenant-a/principal-test", session.TenantID, session.PrincipalID)
	}
	if execution.TenantID != session.TenantID || execution.SessionID != session.SessionID {
		t.Fatalf("execution linkage = tenant:%q session:%q, want %q/%q", execution.TenantID, execution.SessionID, session.TenantID, session.SessionID)
	}
	if event.TenantID != session.TenantID || event.SessionID != session.SessionID || event.ExecutionID != execution.ExecutionID {
		t.Fatalf("event linkage = tenant:%q session:%q execution:%q, want %q/%q/%q",
			event.TenantID, event.SessionID, event.ExecutionID, session.TenantID, session.SessionID, execution.ExecutionID)
	}
	if event.PrincipalID != session.PrincipalID || event.Layer != edge.LayerMCP {
		t.Fatalf("event principal/layer = %q/%q, want %q/%q", event.PrincipalID, event.Layer, session.PrincipalID, edge.LayerMCP)
	}
}

func assertNoGatewayRedisRecords(t *testing.T, store *edge.RedisStore, mr *miniredis.Miniredis, tenantID string) {
	t.Helper()
	sessions, err := store.ListSessions(context.Background(), edge.ListSessionsQuery{TenantID: tenantID, Limit: 10})
	if err != nil {
		t.Fatalf("ListSessions disabled: %v", err)
	}
	if len(sessions.Items) != 0 || len(mr.Keys()) != 0 {
		t.Fatalf("disabled upstream wrote records: sessions=%d redis_keys=%v", len(sessions.Items), mr.Keys())
	}
}

func assertJSONField(t *testing.T, raw []byte, field, want string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not string JSON object: %v body=%q", err, string(raw))
	}
	if got := body[field]; got != want {
		t.Fatalf("response %s = %q, want %q; body=%v", field, got, want, body)
	}
}
