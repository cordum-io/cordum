package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// Regression coverage for task-66234a92: a legacy MCPApprovalStore outage
// must surface as JSON-RPC -32096 approval_store_unavailable with a fixed
// reason, never as -32603 echoing the raw Redis dial error (internal
// host:port) to an authenticated MCP client.

// outageDialErr is the address-bearing error a real refused Redis dial
// produces ("dial tcp 10.20.30.40:6379: connect: connection refused").
func outageDialErr() error {
	return &net.OpError{
		Op: "dial", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.IPv4(10, 20, 30, 40), Port: 6379},
		Err:  errors.New("connect: connection refused"),
	}
}

// outageRedisClient fails every command instantly with outageDialErr on any
// OS: no real network, no dial-retry sleeps. A closed client is not used on
// purpose — "redis: client is closed" carries no address, which would make
// the leak assertions vacuous.
func outageRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:               "10.20.30.40:6379",
		Password:           "outage-redis-secret",
		MaxRetries:         -1,
		DialerRetries:      1,
		DialerRetryTimeout: time.Millisecond,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, outageDialErr()
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// pipelineOutageHook passes single commands through (counting them) and
// fails every transactional (MULTI ... EXEC) pipeline, so the claim's
// SMEMBERS succeeds while EnqueueMCPApproval's TxPipeline Exec hits the
// outage. Non-transactional pipelines pass through: go-redis pipelines the
// connection handshake (HELLO, CLIENT SETINFO) through this same hook.
type pipelineOutageHook struct {
	mu        sync.Mutex
	processed int
}

func (h *pipelineOutageHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *pipelineOutageHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		h.processed++
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (h *pipelineOutageHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) == 0 || cmds[0].Name() != "multi" {
			return next(ctx, cmds)
		}
		err := outageDialErr()
		for _, cmd := range cmds {
			cmd.SetErr(err)
		}
		return err
	}
}

func (h *pipelineOutageHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.processed
}

func outageGateCtx(agentID, principal string) context.Context {
	return WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant: "default", AgentID: agentID, Principal: principal,
	})
}

var outageGateTool = mcp.Tool{Name: "fs.write", RequiresApproval: true}

const outageGateParams = `{"path":"/etc/hostname"}`

func TestGatewayApprovalGate_ClaimOutageIsStoreUnavailable(t *testing.T) {
	t.Parallel()
	gate := NewGatewayApprovalGate(NewMCPApprovalStore(outageRedisClient(t)))
	gated, err := gate.Check(outageGateCtx("agent-1", "alice"), outageGateTool, json.RawMessage(outageGateParams))
	if gated != nil {
		t.Fatalf("gated = %+v; want nil on store outage", gated)
	}
	if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
		t.Fatalf("err = %v; want errors.Is ErrApprovalStoreUnavailable", err)
	}
}

func TestGatewayApprovalGate_EnqueueOutageIsStoreUnavailable(t *testing.T) {
	t.Parallel()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })
	hook := &pipelineOutageHook{}
	client.AddHook(hook)

	gate := NewGatewayApprovalGate(NewMCPApprovalStore(client))
	gated, err := gate.Check(outageGateCtx("agent-1", "alice"), outageGateTool, json.RawMessage(outageGateParams))
	if gated != nil {
		t.Fatalf("gated = %+v; want nil on enqueue outage", gated)
	}
	if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
		t.Fatalf("err = %v; want errors.Is ErrApprovalStoreUnavailable", err)
	}
	if hook.count() == 0 {
		t.Fatal("no single command reached Redis; the claim stage did not run before enqueue")
	}
}

func TestGatewayApprovalGate_InvalidRequestIsNotStoreUnavailable(t *testing.T) {
	t.Parallel()
	gate := NewGatewayApprovalGate(newTestMCPStore(t))
	gated, err := gate.Check(outageGateCtx("", ""), outageGateTool, json.RawMessage(outageGateParams))
	if gated != nil {
		t.Fatalf("gated = %+v; want nil on invalid request", gated)
	}
	if err == nil {
		t.Fatal("err = nil; want a request validation error for empty agent_id")
	}
	if errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
		t.Fatalf("err = %v; a request-shape failure must not be reported as approval_store_unavailable", err)
	}
	if !strings.Contains(err.Error(), "agent_id") {
		t.Fatalf("err = %v; want it to name agent_id", err)
	}
}

// outageHTTPHarness is lexpHarness's setup (mcp_gate_legacy_expiry_test.go)
// that also returns the *server, so the test can reach the live runtime.
func outageHTTPHarness(t *testing.T) (*server, *httptest.Server) {
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
	return s, srv
}

// recordCreateWorkflow re-registers cordum_create_workflow with its tool
// definition unchanged (approval, scope and schema semantics stay) and a
// handler that counts executions.
func recordCreateWorkflow(t *testing.T, registry *mcp.ToolRegistry) *atomic.Int32 {
	t.Helper()
	var executed atomic.Int32
	for _, tool := range registry.ListToolsUnfiltered() {
		if tool.Name != "cordum_create_workflow" {
			continue
		}
		if err := registry.Register(tool, func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
			executed.Add(1)
			return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "ok"}}}, nil
		}); err != nil {
			t.Fatalf("re-register cordum_create_workflow: %v", err)
		}
		return &executed
	}
	t.Fatal("cordum_create_workflow is not registered")
	return nil
}

type outageRPCReply struct {
	Error *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

func outageToolsCall(t *testing.T, srv *httptest.Server) ([]byte, outageRPCReply) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cordum_create_workflow","arguments":{"name":"mcp-demo","steps":{"s1":{"type":"approval"}}}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/message", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-lexp")
	req.Header.Set("X-Tenant-ID", "default")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", resp.StatusCode, raw)
	}
	var reply outageRPCReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply %q: %v", raw, err)
	}
	return raw, reply
}

var outageLeakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d{1,5}\b`),
	regexp.MustCompile(`(?i)localhost:\d+`),
}

// assertNoOutageLeak forbids the port token ":6379", never the bare digits:
// those can appear inside an RFC3339Nano timestamp (task-3c75c7b8 flake).
func assertNoOutageLeak(t *testing.T, raw []byte) {
	t.Helper()
	for _, marker := range []string{"10.20.30.40", ":6379", "dial tcp", "connection refused", "mcp:approvals:", "key-lexp", "outage-redis-secret"} {
		if strings.Contains(string(raw), marker) {
			t.Errorf("JSON-RPC body leaks %q: %s", marker, raw)
		}
	}
	for _, re := range outageLeakPatterns {
		if loc := re.Find(raw); loc != nil {
			t.Errorf("JSON-RPC body leaks %q (pattern %s): %s", loc, re, raw)
		}
	}
}

type admitAllGate struct{}

func (admitAllGate) Check(context.Context, mcp.Tool, json.RawMessage) (*mcp.ApprovalRequired, error) {
	return nil, nil
}

func TestMCPHTTP_ApprovalStoreOutageReturnsSanitized32096(t *testing.T) {
	t.Parallel()
	s, srv := outageHTTPHarness(t)
	rt := s.getMCPRuntime()
	if rt == nil || rt.toolRegistry == nil {
		t.Fatal("mcp runtime not started")
	}
	executed := recordCreateWorkflow(t, rt.toolRegistry)
	gate, _ := s.wireMCPApprovalGate(NewMCPApprovalStore(outageRedisClient(t)), nil)
	rt.toolRegistry.SetApprovalGate(gate)

	raw, reply := outageToolsCall(t, srv)
	assertNoOutageLeak(t, raw)
	if n := executed.Load(); n != 0 {
		t.Fatalf("tool handler executed %d time(s) during a store outage; want 0 (fail closed)", n)
	}
	if reply.Error == nil || reply.Error.Code != -32096 || reply.Error.Message != "approval lifecycle error" {
		t.Fatalf("want JSON-RPC -32096 \"approval lifecycle error\", got %s", raw)
	}
	var data map[string]any
	if err := json.Unmarshal(reply.Error.Data, &data); err != nil {
		t.Fatalf("error.data %s is not an object: %v", reply.Error.Data, err)
	}
	want := map[string]any{"kind": "approval_store_unavailable", "reason": "approval store unavailable"}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("error.data = %#v; want %#v", data, want)
	}

	// Control: an admit-all gate runs the handler, so the zero above is an
	// observation, not an unreachable probe (a nil gate now fails closed).
	rt.toolRegistry.SetApprovalGate(admitAllGate{})
	raw, reply = outageToolsCall(t, srv)
	if reply.Error != nil {
		t.Fatalf("control call (admit-all gate) returned JSON-RPC error: %s", raw)
	}
	if n := executed.Load(); n != 1 {
		t.Fatalf("control call executed %d time(s); want 1", n)
	}
}
