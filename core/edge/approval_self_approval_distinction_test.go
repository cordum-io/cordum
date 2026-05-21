package edge

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestApprovalSelfApprovalDistinction_RetrySucceedsWhenCallerEqualsRequesterButNotApprover
// pins the production MCP retry semantic: the SAME agent principal acts as both
// the original Requester (issued the mutating tool call) AND the consume-time
// Caller (re-issued the tool call with _approval_ref). A SEPARATE human
// principal approved via /approve. ClaimApproval MUST succeed — this is not a
// self-approval attempt; the human approver is the third party.
//
// Before the task-3924519d fix, classifyApprovalClaimMismatch fired
// ApprovalConflictKindSelfApproval at "caller is requester" (approval_store_redis.go:1011)
// breaking every legitimate MCP retry. The fix removes that branch and keeps the
// "caller is approver" branch (line 1014) which is the only valid self-approval
// guard at the store layer (approve-time requesterMatchesApprover already blocks
// Requester==ResolverID at the API surface).
//
// Cross-references: worker-f3843b02 adversarial finding msg-6dcfd7a1; carrier
// commits fcac36ec (task-241c35b5) + f6c9ac58 (task-968d6646); architect
// amendment comment-56322331 on task-3924519d.
func TestApprovalSelfApprovalDistinction_RetrySucceedsWhenCallerEqualsRequesterButNotApprover(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	const (
		agentAlice = "agent-alice"
		humanBob   = "human-bob"
	)

	createApprovalParents(t, ctx, store, "tenant-a", "sess-mcp-retry", "exec-mcp-retry", "event-mcp-retry", base)
	req := validApprovalRequest("tenant-a", "sess-mcp-retry", "exec-mcp-retry", "event-mcp-retry", base)
	req.PrincipalID = agentAlice
	req.Requester = agentAlice
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}

	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  humanBob,
		ResolvedBy:  "bob@example.invalid",
		Reason:      "human approver bob signs off",
		ResolvedAt:  base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}

	claimed, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       req.TenantID,
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     base.Add(2 * time.Minute),
		CallerAgentID:  agentAlice,
	})
	if err != nil {
		var conflict *ApprovalConflictError
		if errors.As(err, &conflict) && conflict.Kind == ApprovalConflictKindSelfApproval {
			t.Fatalf("ClaimApproval returned SelfApproval false-positive (kind=%q reason=%q): MCP retry where caller==Requester but != Approver MUST dispatch — see task-3924519d", conflict.Kind, conflict.Reason)
		}
		t.Fatalf("ClaimApproval = %v; want nil error (legitimate MCP retry)", err)
	}
	if !ok || claimed == nil {
		t.Fatalf("ClaimApproval = (ok=%v, claimed=%v); want ok=true and non-nil approval", ok, claimed)
	}
	if claimed.ConsumedAt == nil {
		t.Fatalf("claimed.ConsumedAt = nil; want set (claim path must mark approval consumed)")
	}
}

// TestApprovalSelfApprovalDistinction_PositiveCaseStillFiresWhenCallerEqualsApprover
// pins the legitimate self-approval guard: a Caller who matches the
// ResolverID (the principal who issued /approve) is rejected with
// ApprovalConflictKindSelfApproval. This is the only valid self-approval
// scenario at the store layer — the approve-time requesterMatchesApprover
// guard separately blocks Requester==ResolverID at the API surface, so this
// store-level check is defense-in-depth backing the approve-time check.
//
// Setup mirrors the existing TestApprovalConsumeRejectsSelfApprovalAtStore
// (Requester==ResolverID==Caller) which the task-3924519d fix MUST NOT
// regress. After the fix the "caller is approver" branch must still return
// ApprovalConflictKindSelfApproval.
func TestApprovalSelfApprovalDistinction_PositiveCaseStillFiresWhenCallerEqualsApprover(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 17, 6, 5, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	const agentAlice = "agent-alice"

	createApprovalParents(t, ctx, store, "tenant-a", "sess-self-pos", "exec-self-pos", "event-self-pos", base)
	req := validApprovalRequest("tenant-a", "sess-self-pos", "exec-self-pos", "event-self-pos", base)
	req.PrincipalID = agentAlice
	req.Requester = agentAlice
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}

	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  agentAlice,
		ResolvedBy:  "alice@example.invalid",
		Reason:      "agent attempts to self-approve (defense-in-depth check)",
		ResolvedAt:  base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}

	claimed, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       req.TenantID,
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     base.Add(2 * time.Minute),
		CallerAgentID:  agentAlice,
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimed != nil {
		t.Fatalf("ClaimApproval = (%#v, %v, %v); want (nil, false, ErrApprovalConflict) — legitimate self-approval guard MUST fire", claimed, ok, err)
	}
	var conflict *ApprovalConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("ClaimApproval err = %v; want *ApprovalConflictError typed wrapper", err)
	}
	if conflict.Kind != ApprovalConflictKindSelfApproval {
		t.Fatalf("conflict.Kind = %q; want %q (caller==ResolverID is the legitimate self-approval scenario)", conflict.Kind, ApprovalConflictKindSelfApproval)
	}
}
