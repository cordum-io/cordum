package gateway

// TestActionGatePreapproval (task-06784e6d R1) drives the production HTTP MCP
// route with the policy gate on, through the agr harness of
// mcp_action_gate_resume_test.go, for an identity whose scope preapproval
// covers the destructive tool. Preapproval still waives the registry gate's
// per-tool approval (control), but never the MCPGate's taint-unavailable
// REQUIRE_HUMAN: the call is held for a resumable Edge approval, an identical
// ref-less retry replays the pending result, and the approved resume executes
// exactly once.
//
// Not parallel: agrNewServer calls t.Setenv. Subtests share one session, run
// in order, and stop at the first failure.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// paWidgetTool needs the registry gate's per-tool approval and is not
// destructive, so the MCPGate never holds it: preapproval alone lets it run.
const paWidgetTool = "agr_install_widget"

func TestActionGatePreapproval(t *testing.T) {
	h := agrNewServer(t)
	var widgetCalls atomic.Int64
	h.paRegisterWidget(t, &widgetCalls)
	h.paPreapprove(t)
	const argsB = `{"record":"rec-pa"}`
	var (
		first agrReply
		ref   string
	)
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"a_control_preapproved_registry_tool_executes", func(t *testing.T) {
			r := h.paCall(t, paWidgetTool, `{"widget":"w-1"}`)
			if !r.succeeded("installed w-1") || widgetCalls.Load() != 1 {
				t.Fatalf("widget call = %s (executions %d); want its result and 1 execution", r, widgetCalls.Load())
			}
		}},
		{"b_hold_returns_resumable_edge_ref", func(t *testing.T) {
			first = h.call(t, argsB)
			ref = h.requirePending(t, first)
			if n := h.calls.Load(); n != 0 {
				t.Fatalf("tool executions = %d, want 0", n)
			}
			if a := h.approval(t, ref); a.Status != edge.ApprovalStatusPending {
				t.Fatalf("approval %s status = %q, want %q", ref, a.Status, edge.ApprovalStatusPending)
			}
		}},
		{"c_refless_retry_replays_pending_digest", func(t *testing.T) {
			approvals := pgApprovalCount(t, h.store, agrTenant)
			h.replaysExecuted(t, argsB, first.Result, 0)
			if n := pgApprovalCount(t, h.store, agrTenant); n != approvals {
				t.Fatalf("Edge approvals = %d after the ref-less retry, want %d (no re-mint)", n, approvals)
			}
		}},
		{"d_approved_resume_executes_once", func(t *testing.T) {
			h.approve(t, ref)
			h.resumeExecutesOnce(t, argsB, ref)
		}},
		{"e_replay_refused", func(t *testing.T) {
			r := h.resume(t, argsB, ref)
			var data struct {
				Kind string `json:"kind"`
			}
			if r.Error == nil || r.Error.Code != -32096 || json.Unmarshal(r.Error.Data, &data) != nil || data.Kind != "not_found" {
				t.Fatalf("replay = %s; want JSON-RPC -32096 kind not_found", r)
			}
			if n := h.calls.Load(); n != 1 {
				t.Fatalf("tool executions = %d after the replay, want 1", n)
			}
		}},
	}
	for _, step := range steps {
		if !t.Run(step.name, step.run) {
			return
		}
	}
}

// paRegisterWidget registers paWidgetTool with RequiresApproval and a counter.
func (h *agrHarness) paRegisterWidget(t *testing.T, calls *atomic.Int64) {
	t.Helper()
	tool := mcp.Tool{Name: paWidgetTool, InputSchema: map[string]any{"type": "object"}, RequiresApproval: true}
	if err := h.s.getMCPRuntime().toolRegistry.Register(tool, func(_ context.Context, args json.RawMessage) (*mcp.ToolCallResult, error) {
		calls.Add(1)
		var in struct {
			Widget string `json:"widget"`
		}
		_ = json.Unmarshal(args, &in)
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "installed " + in.Widget}}}, nil
	}); err != nil {
		t.Fatalf("register %s: %v", paWidgetTool, err)
	}
}

// paPreapprove gives agrAgentID scope preapproval for the destructive tool and
// the widget, and checks the production preapproval lookup sees it.
func (h *agrHarness) paPreapprove(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.s.agentIdentityStore.Update(ctx, agrTenant, agrAgentID, store.AgentIdentity{
		PreapprovedMutatingTools: []string{agrToolName, paWidgetTool},
	}); err != nil {
		t.Fatalf("preapprove %s: %v", agrAgentID, err)
	}
	if !newAgentIdentityPreapprovalLookup(h.s.agentIdentityStore).IsPreapproved(ctx, agrTenant, agrAgentID, agrToolName) {
		t.Fatalf("%s is not preapproved for %s after the update", agrAgentID, agrToolName)
	}
}

// paCall issues tools/call for tool as agr-alice on agrSessionID.
func (h *agrHarness) paCall(t *testing.T, tool, args string) agrReply {
	t.Helper()
	id := strconv.FormatInt(h.rpcID.Add(1), 10)
	body := `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	resp, err := h.post("key-agr-alice", "/mcp/message", body, true)
	if err != nil {
		t.Fatalf("tools/call %s: %v", tool, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call %s HTTP status = %d, want 200", tool, resp.StatusCode)
	}
	var reply agrReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("decode tools/call %s reply: %v", tool, err)
	}
	return reply
}
