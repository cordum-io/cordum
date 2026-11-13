package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type driftSnapshot struct {
	reads       atomic.Int64
	stamp, mint string
}

func (s *driftSnapshot) read(context.Context) string {
	if s.reads.Add(1) == 1 {
		return s.stamp
	}
	return s.mint
}

type mintCountingStore struct {
	edge.Store
	mints atomic.Int64
}

func (s *mintCountingStore) EnqueueApproval(ctx context.Context, req edge.EdgeApprovalRequest) (*edge.EdgeApproval, error) {
	s.mints.Add(1)
	return s.Store.EnqueueApproval(ctx, req)
}

// MCP HTTP parents are unpinned; the policy evaluator supplies the event stamp.
func seedUnpinnedCall(t *testing.T, h *holdHarness, session, execution string) holdCall {
	t.Helper()
	if err := h.edgeStore.CreateSession(h.ctx, edge.EdgeSession{
		SessionID: session, TenantID: holdTenant, PrincipalID: holdMinter,
		PrincipalType: edge.PrincipalTypeHuman, AgentProduct: "Claude Code", AgentVersion: "2.1.0",
		Mode: edge.SessionModeLocalDev, EnforcementLayers: edge.EnforcementLayers{"mcp": true},
		PolicyMode: edge.PolicyModeEnforce, Status: edge.SessionStatusRunning,
		RiskSummary: edge.RiskSummary{MaxRisk: edge.RiskLevelLow}, StartedAt: h.base,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.edgeStore.CreateExecution(h.ctx, edge.AgentExecution{
		ExecutionID: execution, SessionID: session, TenantID: holdTenant,
		Adapter: edge.AdapterMCPGateway, Mode: edge.ExecutionModeLocalDev,
		Status: edge.ExecutionStatusRunning, StartedAt: h.base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	return holdCall{tenant: holdTenant, principal: holdMinter, agent: holdAgent, session: session, execution: execution}
}

func driftPolicyDeps(h *holdHarness, path string, snap *driftSnapshot) (mcp.ToolCallDeps, *mintCountingStore) {
	h.gate.policySnapshot = snap.read // pgPolicyDeps copies this function.
	counting := &mintCountingStore{Store: h.edgeStore}
	h.gate.edgeStore = counting
	deps := h.pgPolicyDeps(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, pgCountingUpstream{calls: &h.upstream})
	if path == "registry_dual_write" {
		deps = h.pgPolicyDeps(pb.DecisionType_DECISION_TYPE_ALLOW, pgGateUpstream{gate: h.gate, args: holdArgs})
	}
	deps.DedupeState = mcp.NewInProcessDedupeStore()
	return deps, counting
}

func driftMintRef(t *testing.T, path string, result *mcp.ToolCallResult, err error) string {
	t.Helper()
	if path == "registry_dual_write" {
		var required *mcp.ApprovalRequired
		if !errors.As(err, &required) || required == nil {
			t.Fatalf("registry mint: %v; want ApprovalRequired", err)
		}
		if !strings.HasPrefix(required.ApprovalRef, edge.ApprovalRefPrefix) || required.RetryHint != mcp.RetryHintApprovalRef {
			t.Fatalf("registry mint = %+v; want Edge ref and approval-ref retry hint", required)
		}
		return required.ApprovalRef
	}
	if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("action gate mint = %+v, %v; want approval pending", result, err)
	}
	ref, ok := strings.CutPrefix(result.Content[0].Text, "approval pending: ")
	if !ok || !strings.HasPrefix(ref, edge.ApprovalRefPrefix) {
		t.Fatalf("action gate text = %q; want Edge ref", result.Content[0].Text)
	}
	return ref
}

func TestEdgeMintPolicySnapshotDrift_IsRetryable(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"registry_dual_write", "action_gate"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			h := newHoldHarness(t, "unused", 0)
			snap := &driftSnapshot{stamp: "policy-v7", mint: "policy-v8"}
			deps, counting := driftPolicyDeps(h, path, snap)
			call := seedUnpinnedCall(t, h, "sess_drift", "exec_drift")
			params := mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}
			_, err := mcp.InvokeToolWithPolicy(call.ctx(h.ctx), deps, params, holdServer)
			if !errors.Is(err, mcp.ErrPolicySnapshotChanged) || errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
				t.Fatalf("drift error = %v; want policy_snapshot_changed, not approval_store_unavailable", err)
			}
			if n := snap.reads.Load(); n != 2 {
				t.Fatalf("snapshot reads = %d; want stamp then mint (2)", n)
			}
			pre := pgOnlyPreEvent(t, h.edgeStore, call)
			if pre.PolicySnapshot != "policy-v7" {
				t.Fatalf("pre snapshot = %q; want policy-v7", pre.PolicySnapshot)
			}
			if n := pgApprovalCount(t, h.edgeStore, holdTenant); counting.mints.Load() != 0 || n != 0 {
				t.Fatalf("drift mints = %d, approvals = %d; want 0, 0", counting.mints.Load(), n)
			}
			if path == "action_gate" && h.upstream.Load() != 0 {
				t.Fatal("upstream executed during a drifted action-gate mint")
			}
			result, err := mcp.InvokeToolWithPolicy(call.ctx(h.ctx), deps, params, holdServer)
			ref := driftMintRef(t, path, result, err)
			if n := pgApprovalCount(t, h.edgeStore, holdTenant); counting.mints.Load() != 1 || n != 1 || snap.reads.Load() != 4 {
				t.Fatalf("retry mints=%d approvals=%d reads=%d; want 1, 1, 4", counting.mints.Load(), n, snap.reads.Load())
			}
			assertDriftRetry(t, h, call, ref, pre.EventID)
		})
	}
}

func assertDriftRetry(t *testing.T, h *holdHarness, call holdCall, ref, firstEventID string) {
	t.Helper()
	approval, found, err := h.edgeStore.GetApproval(h.ctx, holdTenant, ref)
	if err != nil || !found || approval == nil || approval.PolicySnapshot != "policy-v8" {
		t.Fatalf("retry approval = %+v, %t, %v; want policy-v8", approval, found, err)
	}
	events := pgPreEvents(t, h.edgeStore, call.tenant, call.session, call.execution)
	if len(events) != 2 || approval.EventID == firstEventID {
		t.Fatalf("retry events = %+v; approval must bind second pre-event, not %q", events, firstEventID)
	}
	bound := false
	for _, event := range events {
		if event.EventID == approval.EventID && event.PolicySnapshot == "policy-v8" {
			bound = true
		}
	}
	if !bound {
		t.Fatalf("retry approval %q has no policy-v8 pre-event", approval.EventID)
	}
	h.approve(t, holdTenant, ref)
	outcome, err := h.claim(h.edgeStore, call.consumer(), pgWithRef(t, holdArgs, ref), "policy-v8")
	if err != nil || !outcome.Consumed || outcome.ConflictErr != nil {
		t.Fatalf("current-snapshot claim = %+v, %v; want consumed without conflict", outcome, err)
	}
}

func TestEdgeMintPolicySnapshotDrift_BlankSnapshotsKeepTodaysOutcome(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"registry_dual_write", "action_gate"} {
		for _, tc := range []struct{ name, stamp, mint string }{
			{"unreadable_current", "policy-v7", ""},
			{"unstamped_pre_event", "", "policy-v8"},
			{"whitespace_current", "policy-v7", " \t"},
			{"whitespace_pre_event", " \t", "policy-v8"},
			{"trimmed_equal", " policy-v8 ", "policy-v8"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				h := newHoldHarness(t, "unused", 0)
				deps, counting := driftPolicyDeps(h, path, &driftSnapshot{stamp: tc.stamp, mint: tc.mint})
				call := seedUnpinnedCall(t, h, "sess_blank", "exec_blank")
				result, err := mcp.InvokeToolWithPolicy(call.ctx(h.ctx), deps, mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}, holdServer)
				if counting.mints.Load() != 1 {
					t.Fatalf("mint attempts = %d; want 1", counting.mints.Load())
				}
				if strings.TrimSpace(tc.mint) == "" {
					if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) || errors.Is(err, mcp.ErrPolicySnapshotChanged) {
						t.Fatalf("blank current error = %v; want store unavailable, not drift", err)
					}
					if n := pgApprovalCount(t, h.edgeStore, holdTenant); n != 0 {
						t.Fatalf("blank current minted %d approvals; want 0", n)
					}
					return
				}
				ref := driftMintRef(t, path, result, err)
				approval, found, err := h.edgeStore.GetApproval(h.ctx, holdTenant, ref)
				if err != nil || !found || approval == nil || approval.PolicySnapshot != "policy-v8" {
					t.Fatalf("approval = %+v, %t, %v; want policy-v8", approval, found, err)
				}
			})
		}
	}
}

// task-fbe79532: when the registry gate's Edge mint fails after the
// correlation record was enqueued, the record must not stay PENDING and the
// invocation event must not report approval_status=required for it.

// mgAudit is the invocation auditor the MCP server installs per tools/call,
// wired to a recording sink.
type mgAudit struct {
	sink    *rpaAuditSink
	auditor mcp.ToolInvocationAuditor
}

func newMGAudit() *mgAudit {
	sink := &rpaAuditSink{}
	return &mgAudit{sink: sink, auditor: mcp.NewToolInvocationAuditor(sink, nil)}
}

func (a *mgAudit) start(ctx context.Context) (context.Context, *mcp.InvocationHandle) {
	return a.auditor.StartInbound(ctx, holdAgent, holdTenant, holdTool, holdArgs)
}

// finish emits the call's mcp.tool_invocation event and returns it.
func (a *mgAudit) finish(t *testing.T, handle *mcp.InvocationHandle, result *mcp.ToolCallResult, err error) audit.SIEMEvent {
	t.Helper()
	a.auditor.FinishInbound(handle, result, err)
	events := a.sink.rpaInvocations(holdTool)
	if len(events) != 1 {
		t.Fatalf("invocation events = %d; want 1", len(events))
	}
	return events[0]
}

func mgRecords(t *testing.T, h *holdHarness, status model.ApprovalStatus) []*MCPApprovalRecord {
	t.Helper()
	recs, _, err := h.gate.store.ListByStatus(h.ctx, string(status), 0, 50)
	if err != nil {
		t.Fatalf("ListByStatus(%s): %v", status, err)
	}
	return recs
}

func mgRegistryDeps(h *holdHarness) mcp.ToolCallDeps {
	return h.pgPolicyDeps(pb.DecisionType_DECISION_TYPE_ALLOW, pgGateUpstream{gate: h.gate, args: holdArgs})
}

func TestRegistryGateMintError_WithdrawsCorrelationRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		wantErr error
		arm     func(h *holdHarness) mcp.ToolCallDeps
	}{
		{"policy_snapshot_changed", mcp.ErrPolicySnapshotChanged, func(h *holdHarness) mcp.ToolCallDeps {
			deps, _ := driftPolicyDeps(h, "registry_dual_write", &driftSnapshot{stamp: "policy-v7", mint: "policy-v8"})
			return deps
		}},
		{"edge_store_error", mcp.ErrApprovalStoreUnavailable, func(h *holdHarness) mcp.ToolCallDeps {
			down := &pgEdgeStore{Store: h.edgeStore}
			down.failMint.Store(true)
			h.gate.edgeStore = down
			return mgRegistryDeps(h)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHoldHarness(t, "policy-v7", 0)
			deps := tc.arm(h)
			call := seedUnpinnedCall(t, h, "sess_mint_err", "exec_mint_err")
			a := newMGAudit()
			ctx, handle := a.start(call.ctx(h.ctx))
			result, err := mcp.InvokeToolWithPolicy(ctx, deps, mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}, holdServer)
			event := a.finish(t, handle, result, err)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v; want %v", err, tc.wantErr)
			}
			if result != nil {
				t.Fatalf("result = %+v; the upstream must not execute", result)
			}
			if n := pgApprovalCount(t, h.edgeStore, holdTenant); n != 0 {
				t.Fatalf("Edge approvals minted = %d; want 0", n)
			}
			pending, expired := mgRecords(t, h, model.ApprovalStatusPending), mgRecords(t, h, model.ApprovalStatusExpired)
			if len(pending) != 0 || len(expired) != 1 {
				t.Errorf("legacy records: %d pending, %d expired; want 0 pending, 1 withdrawn (expired)", len(pending), len(expired))
			}
			if got := event.Extra["approval_status"]; got == "required" {
				t.Errorf("approval_status = %q; the failed mint must not report required", got)
			}
			for _, rec := range append(pending, expired...) {
				if got := event.Extra["approval_id"]; got == rec.ID {
					t.Errorf("approval_id = %q; it names the orphaned correlation record", got)
				}
			}
		})
	}
	t.Run("gate_off_unchanged", func(t *testing.T) {
		t.Parallel()
		h := newHoldHarness(t, "policy-v7", 0)
		h.gate.edgeStore = nil // gate off: the legacy record is the resume authority
		a := newMGAudit()
		ctx, handle := a.start(holdCall{tenant: holdTenant, principal: holdMinter, agent: holdAgent}.ctx(h.ctx))
		required, err := h.gate.Check(ctx, mcp.Tool{Name: holdTool, RequiresApproval: true}, holdArgs)
		pending := mgRecords(t, h, model.ApprovalStatusPending)
		if err != nil || required == nil || len(pending) != 1 {
			t.Fatalf("Check = %+v, %v with %d pending records; want ApprovalRequired and 1 pending", required, err, len(pending))
		}
		if required.ApprovalID != pending[0].ID || required.ApprovalRef != pending[0].ID || required.RetryHint != mcp.RetryHintSameArgs {
			t.Errorf("ApprovalRequired = %+v; want the pending record %q and retry_with_same_args", required, pending[0].ID)
		}
		// A nil error: the ApprovalRequired branch of the auditor stamps
		// "required" itself, so only the gate's own mark can show here.
		event := a.finish(t, handle, nil, nil)
		if event.Extra["approval_status"] != "required" || event.Extra["approval_id"] != pending[0].ID {
			t.Errorf("event approval_status=%q approval_id=%q; want required and %q",
				event.Extra["approval_status"], event.Extra["approval_id"], pending[0].ID)
		}
	})
	t.Run("edge_mint_success_keeps_mark", func(t *testing.T) {
		t.Parallel()
		h := newHoldHarness(t, "policy-v7", 0)
		deps := mgRegistryDeps(h)
		call := seedUnpinnedCall(t, h, "sess_mint_ok", "exec_mint_ok")
		a := newMGAudit()
		ctx, handle := a.start(call.ctx(h.ctx))
		_, err := mcp.InvokeToolWithPolicy(ctx, deps, mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}, holdServer)
		var required *mcp.ApprovalRequired
		pending := mgRecords(t, h, model.ApprovalStatusPending)
		if !errors.As(err, &required) || required.RetryHint != mcp.RetryHintApprovalRef || len(pending) != 1 {
			t.Fatalf("mint = %v with %d pending records; want an Edge-ref ApprovalRequired and 1 correlation record", err, len(pending))
		}
		if n := pgApprovalCount(t, h.edgeStore, holdTenant); n != 1 {
			t.Fatalf("Edge approvals = %d; want 1", n)
		}
		event := a.finish(t, handle, nil, nil) // nil error: only the gate's own mark can show
		if event.Extra["approval_status"] != "required" || event.Extra["approval_id"] != pending[0].ID {
			t.Errorf("event approval_status=%q approval_id=%q; want required and %q",
				event.Extra["approval_status"], event.Extra["approval_id"], pending[0].ID)
		}
	})
}
