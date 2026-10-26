package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// mcpApprovalE2E is a production-mode gateway serving the real /mcp/message
// route with the policy gate and approval hold on, a real Edge store, and a
// RequiresApproval tool (e2eGatedTool) whose executions it counts. Nothing is
// seeded in the Edge store: mcpAuth creates the session and execution, the
// mint records the approval's parent event.
type mcpApprovalE2E struct {
	s     *server
	url   string
	calls atomic.Int32
}

const e2eGatedTool = "e2e_gated_write"

func newMCPApprovalE2E(t *testing.T) *mcpApprovalE2E {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_ENV": "production",
		"CORDUM_API_KEYS": `[{"key":"key-alice","tenant":"default","principal_id":"alice","role":"viewer"},` +
			`{"key":"key-bob","tenant":"default","principal_id":"bob","role":"viewer"}]`,
	})
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, true)
	ctx := context.Background()
	// A policy config document gives the approvals a policy snapshot.
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID,
		Data: map[string]any{packs.PolicyConfigKey: map[string]any{}},
	}); err != nil {
		t.Fatalf("set policy config: %v", err)
	}
	for agent, principal := range map[string]string{"agent-a": "alice", "agent-b": "bob"} {
		if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
			ID: agent, TenantID: "default", Name: agent, Owner: "ops", RiskTier: "high",
			AllowedTools: []string{"*"}, AllowedServers: []string{"*"}, Status: "active",
		}); err != nil {
			t.Fatalf("create %s: %v", agent, err)
		}
		if err := s.agentIdentityStore.LinkWorker(ctx, agent, principal); err != nil {
			t.Fatalf("link %s: %v", agent, err)
		}
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || !runtime.server.HasApprovalHold() {
		t.Fatal("mcp runtime without approval hold; the policy gate must be wired")
	}
	h := &mcpApprovalE2E{s: s}
	if err := runtime.toolRegistry.Register(mcp.Tool{Name: e2eGatedTool, RequiresApproval: true, RiskTier: "low"},
		func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
			h.calls.Add(1)
			return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "written"}}}, nil
		}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

// e2eRPC is a JSON-RPC response from /mcp/message.
type e2eRPC struct {
	Result *mcp.ToolCallResult `json:"result"`
	Error  *struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} `json:"error"`
}

// call POSTs one session-less tools/call (VS Code's type:"http" shape) as key
// acting as agent.
func (h *mcpApprovalE2E) call(t *testing.T, key, agent string, args map[string]any) e2eRPC {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": e2eGatedTool, "arguments": args},
	})
	req, _ := http.NewRequest(http.MethodPost, h.url+"/mcp/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set(mcpAgentIDHeader, agent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/message: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out e2eRPC
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response (status %d): %v", resp.StatusCode, err)
	}
	return out
}

// approvalRef returns the Edge approval_ref of a -32099 approval-required reply.
func (r e2eRPC) approvalRef(t *testing.T) string {
	t.Helper()
	if r.Error == nil || r.Error.Code != -32099 {
		t.Fatalf("reply = %+v (error %s), want -32099 approval required", r.Result, r.errorText())
	}
	var data struct {
		ApprovalRef string `json:"approval_ref"`
	}
	_ = json.Unmarshal(r.Error.Data, &data)
	if !strings.HasPrefix(data.ApprovalRef, edge.ApprovalRefPrefix) {
		t.Fatalf("approval_ref = %q, want an Edge approval ref (data %s)", data.ApprovalRef, r.Error.Data)
	}
	return data.ApprovalRef
}

func (r e2eRPC) errorText() string {
	if r.Error == nil {
		return "<nil>"
	}
	return string(r.Error.Data)
}

func (h *mcpApprovalE2E) approve(t *testing.T, ref string) {
	t.Helper()
	if _, err := h.s.edgeStore.ApproveApproval(context.Background(), edge.ApprovalResolution{
		TenantID: "default", ApprovalRef: ref, ResolverID: "reviewer",
		ResolvedBy: "reviewer@example.invalid", Reason: "approve", ResolvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("approve %s: %v", ref, err)
	}
}

// TestMCPApprovalMintRecordsParentEvent: in production mode, a RequiresApproval
// tool over the real /mcp/message route used to fail every mint with "edge
// approval event <agent> not found" (the approval named the agent id as its
// parent event, which nothing records). The mint now records the
// approval.requested event itself, so the call returns -32099 with an Edge
// approval_ref, and the same caller's `_approval_ref` retry claims it.
func TestMCPApprovalMintRecordsParentEvent(t *testing.T) {
	h := newMCPApprovalE2E(t)
	args := map[string]any{"note": "hi"}
	ref := h.call(t, "key-alice", "agent-a", args).approvalRef(t)

	approval, ok, err := h.s.edgeStore.GetApproval(context.Background(), "default", ref)
	if err != nil || !ok {
		t.Fatalf("GetApproval %s: ok=%t err=%v", ref, ok, err)
	}
	events, err := h.s.edgeStore.ListEvents(context.Background(), edge.ListEventsQuery{
		TenantID: "default", SessionID: approval.SessionID, ExecutionID: approval.ExecutionID,
		Kind: edge.EventKindApprovalRequested,
	})
	if err != nil || len(events.Items) != 1 || events.Items[0].EventID != approval.EventID {
		t.Fatalf("approval.requested events in %s = %+v (err %v), want the approval's parent %s",
			approval.ExecutionID, events.Items, err, approval.EventID)
	}

	h.approve(t, ref)
	h.call(t, "key-alice", "agent-a", map[string]any{"note": "hi", "_approval_ref": ref})
	if approval, _, _ = h.s.edgeStore.GetApproval(context.Background(), "default", ref); approval.ConsumedAt == nil {
		t.Fatalf("approval %s not consumed by the same caller's retry", ref)
	}
}
