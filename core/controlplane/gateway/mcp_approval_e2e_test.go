package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// mcpApprovalE2E is a production-mode gateway serving the real /mcp/message
// route with the policy gate and approval hold on, a real Edge store (built
// with opts), a RequiresApproval tool (e2eGatedTool) whose executions it
// counts, and an ungated e2eReadTool. Nothing is seeded in the Edge store:
// mcpAuth creates the session and execution, the mint records the approval's
// parent event.
type mcpApprovalE2E struct {
	s     *server
	url   string
	calls atomic.Int32
}

const (
	e2eGatedTool = "e2e_gated_write"
	e2eReadTool  = "e2e_read"
)

func newMCPApprovalE2E(t *testing.T, opts ...edge.StoreOption) *mcpApprovalE2E {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_ENV": "production",
		"CORDUM_API_KEYS": `[{"key":"key-alice","tenant":"default","principal_id":"alice","role":"viewer"},` +
			`{"key":"key-bob","tenant":"default","principal_id":"bob","role":"viewer"}]`,
	})
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	s.edgeStore = edge.NewRedisStoreFromClient(s.jobStore.Client(), opts...)
	s.wireActionGatePipeline()
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
	if err := runtime.toolRegistry.Register(mcp.Tool{Name: e2eReadTool, RiskTier: "low"},
		func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
			return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "read"}}}, nil
		}); err != nil {
		t.Fatalf("register read tool: %v", err)
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

// call POSTs one session-less tools/call of e2eGatedTool (VS Code's
// type:"http" shape) as key acting as agent.
func (h *mcpApprovalE2E) call(t *testing.T, key, agent string, args map[string]any) e2eRPC {
	t.Helper()
	return h.callTool(t, key, agent, "", e2eGatedTool, args)
}

// callTool POSTs one tools/call of tool as key acting as agent, naming MCP
// session sessionID when set.
func (h *mcpApprovalE2E) callTool(t *testing.T, key, agent, sessionID, tool string, args map[string]any) e2eRPC {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	req, _ := http.NewRequest(http.MethodPost, h.url+"/mcp/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set(mcpAgentIDHeader, agent)
	if sessionID != "" {
		req.Header.Set("X-MCP-Session-ID", sessionID)
	}
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

// TestMCPApprovalClaimRunsCallOnce: once a human approves, the caller's
// `_approval_ref` retry runs the RequiresApproval tool exactly once. The
// registry gate used to hold the retry again and mint a new approval. The
// claim is single-use: a second retry with the same ref is refused.
func TestMCPApprovalClaimRunsCallOnce(t *testing.T) {
	h := newMCPApprovalE2E(t)
	ref := h.call(t, "key-alice", "agent-a", map[string]any{"note": "hi"}).approvalRef(t)
	h.approve(t, ref)
	retry := map[string]any{"note": "hi", "_approval_ref": ref}
	got := h.call(t, "key-alice", "agent-a", retry)
	if got.Error != nil || got.Result == nil || len(got.Result.Content) == 0 || got.Result.Content[0].Text != "written" {
		t.Fatalf("approved retry = %+v (error %s), want the tool's result", got.Result, got.errorText())
	}
	again := h.call(t, "key-alice", "agent-a", retry)
	if again.Error == nil || again.Error.Code != -32096 {
		t.Fatalf("second retry with the same ref = %+v (error %s), want -32096", again.Result, again.errorText())
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("tool ran %d times, want exactly once", n)
	}
}

// approvalID returns the legacy approval_id of a -32099 reply.
func (r e2eRPC) approvalID(t *testing.T) string {
	t.Helper()
	var data struct {
		ApprovalID string `json:"approval_id"`
	}
	if r.Error == nil || json.Unmarshal(r.Error.Data, &data) != nil || data.ApprovalID == "" {
		t.Fatalf("reply without approval_id: error %s", r.errorText())
	}
	return data.ApprovalID
}

// TestMCPApprovalReaskReusesPendingApproval: a client that repeats a held
// call without `_approval_ref` (VS Code re-asks) used to mint a new approval
// pair every time. While the caller's approval for the call is pending, the
// re-ask gets that one back; another requester still gets their own.
func TestMCPApprovalReaskReusesPendingApproval(t *testing.T) {
	h := newMCPApprovalE2E(t)
	ctx := context.Background()
	args := map[string]any{"note": "hi"}
	first := h.call(t, "key-alice", "agent-a", args)
	again := h.call(t, "key-alice", "agent-a", args)
	if first.approvalRef(t) != again.approvalRef(t) || first.approvalID(t) != again.approvalID(t) {
		t.Fatalf("re-ask got %s/%s, want the pending %s/%s",
			again.approvalRef(t), again.approvalID(t), first.approvalRef(t), first.approvalID(t))
	}
	if bobs := h.call(t, "key-bob", "agent-b", args).approvalRef(t); bobs == first.approvalRef(t) {
		t.Fatalf("another requester got alice's approval %s", bobs)
	}
	for principal, want := range map[string]int{"alice": 1, "bob": 1} {
		page, err := h.s.edgeStore.ListApprovals(ctx, edge.ListApprovalsQuery{
			TenantID: "default", PrincipalID: principal, Status: edge.ApprovalStatusPending,
		})
		if err != nil || len(page.Items) != want {
			t.Fatalf("%s's pending Edge approvals = %d (err %v), want %d", principal, len(page.Items), err, want)
		}
	}
	legacyStore := h.s.getMCPRuntime().approvalStore
	legacy, _, err := legacyStore.ListByStatus(ctx, "pending", 0, 100)
	if err != nil || len(legacy) != 2 {
		t.Fatalf("pending legacy MCP approvals = %d (err %v), want one per requester", len(legacy), err)
	}
	// Once a reviewer rejects the approval, the next re-ask gets a fresh pair.
	if code := h.resolve(t, "mcp reject", first); code != http.StatusOK {
		t.Fatalf("reject on the MCP approvals page = %d, want 200", code)
	}
	if fresh := h.call(t, "key-alice", "agent-a", args); fresh.approvalRef(t) == first.approvalRef(t) || fresh.approvalID(t) == first.approvalID(t) {
		t.Fatalf("re-ask after the rejection reused %s/%s", fresh.approvalRef(t), fresh.approvalID(t))
	}
}

// resolve resolves held's approval as a reviewer does, with action "mcp
// approve" or "mcp reject" on the MCP approvals page (legacy approval id), or
// "edge approve" or "edge reject" on the Edge approvals page (approval ref).
// Returns the HTTP status.
func (h *mcpApprovalE2E) resolve(t *testing.T, action string, held e2eRPC) int {
	t.Helper()
	handler := map[string]http.HandlerFunc{
		"mcp approve": h.s.handleMCPApprovalApprove, "mcp reject": h.s.handleMCPApprovalReject,
		"edge approve": h.s.handleApproveEdgeApproval, "edge reject": h.s.handleRejectEdgeApproval,
	}[action]
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", held.approvalID(t))
	req.SetPathValue("approval_ref", held.approvalRef(t))
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: "reviewer", Role: "admin"})
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec.Code
}

// TestMCPApprovalRejectOnEitherPageRejectsBoth: an MCP approval is an Edge
// approval dual-written with a legacy record, each on its own approvals page.
// Rejecting one used to leave the other pending, so another reviewer could
// still approve it there and the call ran on that half's retry. Rejecting on
// either page now rejects both halves: the other page answers 409, and neither
// retry style runs the call.
func TestMCPApprovalRejectOnEitherPageRejectsBoth(t *testing.T) {
	for _, tc := range []struct{ reject, approve string }{
		{reject: "mcp reject", approve: "edge approve"},
		{reject: "edge reject", approve: "mcp approve"},
	} {
		t.Run(tc.reject, func(t *testing.T) {
			h := newMCPApprovalE2E(t)
			ctx := context.Background()
			args := map[string]any{"note": "hi"}
			held := h.call(t, "key-alice", "agent-a", args)
			ref, id := held.approvalRef(t), held.approvalID(t)
			if code := h.resolve(t, tc.reject, held); code != http.StatusOK {
				t.Fatalf("%s = %d, want 200", tc.reject, code)
			}
			approval, _, err := h.s.edgeStore.GetApproval(ctx, "default", ref)
			if err != nil || approval.Status != edge.ApprovalStatusRejected {
				t.Fatalf("Edge approval after %s = %+v (err %v), want rejected", tc.reject, approval, err)
			}
			rec, err := h.s.getMCPRuntime().approvalStore.Get(ctx, id)
			if err != nil || rec.Status != model.ApprovalStatusRejected {
				t.Fatalf("legacy record after %s = %+v (err %v), want rejected", tc.reject, rec, err)
			}
			if code := h.resolve(t, tc.approve, held); code != http.StatusConflict {
				t.Fatalf("%s after %s = %d, want 409", tc.approve, tc.reject, code)
			}
			if got := h.call(t, "key-alice", "agent-a", map[string]any{"note": "hi", "_approval_ref": ref}); got.Error == nil || got.Error.Code != -32096 {
				t.Fatalf("`_approval_ref` retry of a rejected approval = %+v (error %s), want -32096", got.Result, got.errorText())
			}
			if again := h.call(t, "key-alice", "agent-a", args); again.approvalRef(t) == ref {
				t.Fatalf("retry with the same args reused the rejected approval %s", ref)
			}
			if n := h.calls.Load(); n != 0 {
				t.Fatalf("tool ran %d times after a rejection, want never", n)
			}
		})
	}
}

// TestMCPApprovalApproveOnEitherPageRunsOnce: approving on either page
// approves both halves, and the call runs exactly once whichever retry style
// claims it first. Each half used to be consumable on its own, so an
// `_approval_ref` retry and a retry with the same args could each run it; both
// now claim the Edge approval, the pair's one consume token.
func TestMCPApprovalApproveOnEitherPageRunsOnce(t *testing.T) {
	for _, page := range []string{"mcp approve", "edge approve"} {
		for _, refFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/ref_first=%t", page, refFirst), func(t *testing.T) {
				h := newMCPApprovalE2E(t)
				ctx := context.Background()
				args := map[string]any{"note": "hi"}
				held := h.call(t, "key-alice", "agent-a", args)
				ref := held.approvalRef(t)
				if code := h.resolve(t, page, held); code != http.StatusOK {
					t.Fatalf("%s = %d, want 200", page, code)
				}
				approval, _, err := h.s.edgeStore.GetApproval(ctx, "default", ref)
				if err != nil || approval.Status != edge.ApprovalStatusApproved {
					t.Fatalf("Edge approval after %s = %+v (err %v), want approved", page, approval, err)
				}
				rec, err := h.s.getMCPRuntime().approvalStore.Get(ctx, held.approvalID(t))
				if err != nil || rec.Status != model.ApprovalStatusApproved {
					t.Fatalf("legacy record after %s = %+v (err %v), want approved", page, rec, err)
				}
				retries := []map[string]any{{"note": "hi", "_approval_ref": ref}, args}
				if !refFirst {
					retries[0], retries[1] = retries[1], retries[0]
				}
				if got := h.call(t, "key-alice", "agent-a", retries[0]); got.Error != nil || got.Result == nil {
					t.Fatalf("first retry %v = error %s, want the tool's result", retries[0], got.errorText())
				}
				if got := h.call(t, "key-alice", "agent-a", retries[1]); got.Error == nil || got.Result != nil {
					t.Fatalf("second retry %v = %+v, want it held or refused", retries[1], got.Result)
				}
				if n := h.calls.Load(); n != 1 {
					t.Fatalf("tool ran %d times on one approval, want once", n)
				}
			})
		}
	}
}

// TestMCPApprovalWorksWithoutPolicyConfig: a fresh install has no policy
// config document, so the policy snapshot was empty and every MCP Edge
// approval mint failed with approval_store_unavailable. Approvals now bind to
// the empty-policy snapshot and run once approved, and creating the first
// policy document still invalidates an approval minted before it.
func TestMCPApprovalWorksWithoutPolicyConfig(t *testing.T) {
	h := newMCPApprovalE2E(t)
	ctx := context.Background()
	key := fmt.Sprintf("cfg:%s:%s", packs.PolicyConfigScope, packs.PolicyConfigID)
	if err := h.s.jobStore.Client().Del(ctx, key).Err(); err != nil {
		t.Fatalf("delete policy config: %v", err)
	}
	if _, err := h.s.configSvc.Get(ctx, configsvc.Scope(packs.PolicyConfigScope), packs.PolicyConfigID); !errors.Is(err, redis.Nil) {
		t.Fatalf("policy config still present after deleting %s: %v", key, err)
	}
	held := h.call(t, "key-alice", "agent-a", map[string]any{"note": "first"})
	var data struct {
		PolicySnapshot string `json:"policy_snapshot"`
	}
	if ref := held.approvalRef(t); json.Unmarshal(held.Error.Data, &data) != nil || data.PolicySnapshot != mcpEmptyPolicySnapshot {
		t.Fatalf("approval %s policy_snapshot = %q, want %q", ref, data.PolicySnapshot, mcpEmptyPolicySnapshot)
	}
	h.approve(t, held.approvalRef(t))
	if got := h.call(t, "key-alice", "agent-a", map[string]any{"note": "first", "_approval_ref": held.approvalRef(t)}); got.Error != nil || got.Result == nil {
		t.Fatalf("approved retry without a policy = error %s, want the tool's result", got.errorText())
	}

	stale := h.call(t, "key-alice", "agent-a", map[string]any{"note": "second"})
	h.approve(t, stale.approvalRef(t))
	if err := h.s.configSvc.Set(ctx, &configsvc.Document{
		Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID,
		Data: map[string]any{packs.PolicyConfigKey: map[string]any{}},
	}); err != nil {
		t.Fatalf("set policy config: %v", err)
	}
	got := h.call(t, "key-alice", "agent-a", map[string]any{"note": "second", "_approval_ref": stale.approvalRef(t)})
	var conflict struct {
		Kind string `json:"kind"`
	}
	if got.Error == nil || got.Error.Code != -32096 || json.Unmarshal(got.Error.Data, &conflict) != nil || conflict.Kind != string(edge.ApprovalConflictKindPolicyMismatch) {
		t.Fatalf("retry after the first policy = %+v (error %s), want -32096 policy_mismatch", got.Result, got.errorText())
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("tool ran %d times, want once", n)
	}
}

// TestMCPApprovalClaimWorksAcrossEdgeRotation: with lowered Edge caps (12
// events per execution, 2 executions per session) a long-lived SSE session
// used to fail its calls once its one execution filled. The session now moves
// to new executions and then to a new Edge session, every call succeeds, and
// an approval minted before the rotation is still claimed by the caller's
// `_approval_ref` retry afterwards.
func TestMCPApprovalClaimWorksAcrossEdgeRotation(t *testing.T) {
	h := newMCPApprovalE2E(t, edge.WithMaxEventsPerExecution(12), edge.WithMaxExecutionsPerSession(2))
	ctx := context.Background()
	sse := openMCPSSE(t, h.url, "key-alice", "")
	ref := h.callTool(t, "key-alice", "agent-a", sse, e2eGatedTool, map[string]any{"note": "hi"}).approvalRef(t)
	approval, _, err := h.s.edgeStore.GetApproval(ctx, "default", ref)
	if err != nil || approval.SessionID != sse {
		t.Fatalf("approval %s minted in %+v (err %v), want the MCP session's first Edge session %s", ref, approval, err, sse)
	}
	for i := 0; i < 12; i++ {
		got := h.callTool(t, "key-alice", "agent-a", sse, e2eReadTool, map[string]any{"i": i})
		if got.Error != nil || got.Result == nil {
			t.Fatalf("read %d in a long-lived SSE session failed: %s", i, got.errorText())
		}
	}
	rotated, ok, err := h.s.edgeStore.GetSession(ctx, "default", sse+".s1")
	if err != nil || !ok || rotated.Labels[mcpSessionLabel] != sse {
		t.Fatalf("rotated Edge session = %+v ok=%v err=%v, want %s.s1 linked to the MCP session", rotated, ok, err, sse)
	}

	h.approve(t, ref)
	got := h.callTool(t, "key-alice", "agent-a", sse, e2eGatedTool, map[string]any{"note": "hi", "_approval_ref": ref})
	if got.Error != nil || got.Result == nil || len(got.Result.Content) == 0 || got.Result.Content[0].Text != "written" {
		t.Fatalf("claim after rotation = %+v (error %s), want the tool's result", got.Result, got.errorText())
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("gated tool ran %d times, want once", n)
	}
}
