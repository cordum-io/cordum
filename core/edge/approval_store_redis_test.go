package edge

// Redis unavailable simulations in this file use miniredis.Close() for
// connection-loss paths and miniredis.SetError() for command/pipeline
// failures. miniredis cannot model every production Redis failure mode (for
// example timeout-after-WATCH but before EXEC, kernel-level half-open TCP, or
// cluster failover mid-pipeline), so those remain out of scope unless a fake
// store is introduced explicitly in a targeted test.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisStoreApprovalEnqueueReturnsErrorWhenRedisClosed(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	store, _, mr, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-appr-redis-closed", "exec-appr-redis-closed", "event-appr-redis-closed", base)
	req := validApprovalRequest("tenant-a", "sess-appr-redis-closed", "exec-appr-redis-closed", "event-appr-redis-closed", base)

	mr.Close()
	approval, err := store.EnqueueApproval(ctx, req)
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)
	if approval != nil {
		t.Fatalf("EnqueueApproval after Redis Close returned approval %#v, want nil", approval)
	}
}

func TestRedisStoreApprovalClaimReturnsErrorWhenRedisClosed(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 20, 30, 0, 0, time.UTC)
	store, _, mr, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-appr-claim-closed", "exec-appr-claim-closed", "event-appr-claim-closed", base)
	req := validApprovalRequest("tenant-a", "sess-appr-claim-closed", "exec-appr-claim-closed", "event-appr-claim-closed", base)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	approved, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approved before redis closes",
		ResolvedAt:  base.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}
	if approved.Status != ApprovalStatusApproved || approved.Decision != ApprovalDecisionApprove {
		t.Fatalf("approved status/decision = %q/%q, want approved/approve", approved.Status, approved.Decision)
	}

	mr.Close()
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
	})
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)
	if ok || claimed != nil {
		t.Fatalf("ClaimApproval after Redis Close = (%#v,%v), want nil,false", claimed, ok)
	}
}

func TestRedisStoreApprovalSetErrorLeavesNoPartialEnqueueIndexes(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 21, 0, 0, 0, time.UTC)
	store, client, mr, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-appr-seterror", "exec-appr-seterror", "event-appr-seterror", base)
	req := validApprovalRequest("tenant-a", "sess-appr-seterror", "exec-appr-seterror", "event-appr-seterror", base)

	mr.SetError("edge redis unavailable")
	approval, err := store.EnqueueApproval(ctx, req)
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)
	if approval != nil {
		t.Fatalf("EnqueueApproval during Redis SetError returned approval %#v, want nil", approval)
	}

	mr.SetError("")
	page, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: req.TenantID, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals after clearing SetError: %v", err)
	}
	assertApprovalRefs(t, page.Items, []string{})
	for _, key := range []string{
		edgeApprovalTenantIndexKey(req.TenantID),
		edgeApprovalStatusIndexKey(req.TenantID, ApprovalStatusPending),
		edgeApprovalPrincipalIndexKey(req.TenantID, req.PrincipalID),
		edgeApprovalPrincipalStatusIndexKey(req.TenantID, req.PrincipalID, ApprovalStatusPending),
		edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash),
	} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after failed EnqueueApproval, want no partial approval index writes", key)
		}
	}
}

func TestRedisStoreApprovalLifecycleEnqueueResolveListAndConsume(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-approval", "exec-approval", "event-approval", base)
	req := validApprovalRequest("tenant-a", "sess-approval", "exec-approval", "event-approval", base)

	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if !strings.HasPrefix(approval.ApprovalRef, "edge_appr_") {
		t.Fatalf("approval_ref = %q, want edge_appr_ prefix", approval.ApprovalRef)
	}
	if approval.Status != ApprovalStatusPending || approval.Decision != "" {
		t.Fatalf("new approval status/decision = %q/%q, want pending/empty", approval.Status, approval.Decision)
	}
	if approval.TenantID != req.TenantID || approval.SessionID != req.SessionID || approval.ExecutionID != req.ExecutionID || approval.EventID != req.EventID {
		t.Fatalf("approval tuple = tenant:%q session:%q execution:%q event:%q, want request tuple", approval.TenantID, approval.SessionID, approval.ExecutionID, approval.EventID)
	}
	if approval.ActionHash != req.ActionHash || approval.PolicySnapshot != req.PolicySnapshot || approval.InputHash != req.InputHash {
		t.Fatalf("approval binding = action:%q snapshot:%q input:%q, want action:%q snapshot:%q input:%q",
			approval.ActionHash, approval.PolicySnapshot, approval.InputHash, req.ActionHash, req.PolicySnapshot, req.InputHash)
	}
	if approval.PrincipalID != "principal-a" || approval.Requester != "principal-a" || approval.Reason != req.Reason || approval.RuleID != req.RuleID {
		t.Fatalf("approval requester fields = principal:%q requester:%q reason:%q rule:%q", approval.PrincipalID, approval.Requester, approval.Reason, approval.RuleID)
	}
	if approval.ResolvedAt != nil || approval.ResolverID != "" || approval.ResolvedBy != "" || approval.ConsumedAt != nil {
		t.Fatalf("pending approval carried resolution/consume data: %#v", approval)
	}
	if approval.ExpiresAt == nil || !approval.ExpiresAt.Equal(req.ExpiresAt) {
		t.Fatalf("expires_at = %v, want %s", approval.ExpiresAt, req.ExpiresAt)
	}

	got, ok, err := store.GetApproval(ctx, "tenant-a", approval.ApprovalRef)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if !ok || got.ApprovalRef != approval.ApprovalRef {
		t.Fatalf("GetApproval = (%#v,%v), want stored approval", got, ok)
	}
	if crossTenant, ok, err := store.GetApproval(ctx, "tenant-b", approval.ApprovalRef); err != nil || ok || crossTenant != nil {
		t.Fatalf("cross-tenant GetApproval = (%#v,%v,%v), want nil,false,nil", crossTenant, ok, err)
	}

	pending, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals pending: %v", err)
	}
	assertApprovalRefs(t, pending.Items, []string{approval.ApprovalRef})

	tuplePage, err := store.ListApprovals(ctx, ListApprovalsQuery{
		TenantID:    "tenant-a",
		SessionID:   "sess-approval",
		ExecutionID: "exec-approval",
		ActionHash:  req.ActionHash,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListApprovals tuple: %v", err)
	}
	assertApprovalRefs(t, tuplePage.Items, []string{approval.ApprovalRef})

	duplicate, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("duplicate EnqueueApproval: %v", err)
	}
	if duplicate.ApprovalRef != approval.ApprovalRef {
		t.Fatalf("duplicate enqueue ref = %q, want existing pending ref %q", duplicate.ApprovalRef, approval.ApprovalRef)
	}
	pending, err = store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals after duplicate: %v", err)
	}
	assertApprovalRefs(t, pending.Items, []string{approval.ApprovalRef})

	resolvedAt := base.Add(2 * time.Minute)
	approved, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approved for a one-shot retry",
		ResolvedAt:  resolvedAt,
	})
	if err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}
	if approved.Status != ApprovalStatusApproved || approved.Decision != ApprovalDecisionApprove {
		t.Fatalf("approved status/decision = %q/%q, want approved/approve", approved.Status, approved.Decision)
	}
	if approved.ResolverID != "principal-reviewer" || approved.ResolvedBy != "reviewer@example.invalid" || approved.ResolutionReason != "approved for a one-shot retry" {
		t.Fatalf("resolver fields = id:%q by:%q reason:%q", approved.ResolverID, approved.ResolvedBy, approved.ResolutionReason)
	}
	if approved.ResolvedAt == nil || !approved.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("resolved_at = %v, want %s", approved.ResolvedAt, resolvedAt)
	}
	if approved.ConsumedAt != nil {
		t.Fatalf("approved approval consumed_at = %v, want nil before claim", approved.ConsumedAt)
	}
	pending, err = store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals pending after approve: %v", err)
	}
	assertApprovalRefs(t, pending.Items, []string{})

	approvedPage, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusApproved, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals approved: %v", err)
	}
	assertApprovalRefs(t, approvedPage.Items, []string{approval.ApprovalRef})

	consumedAt := base.Add(3 * time.Minute)
	claimed, claimedOK, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       "tenant-a",
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     consumedAt,
	})
	if err != nil {
		t.Fatalf("ClaimApproval: %v", err)
	}
	if !claimedOK || claimed == nil {
		t.Fatalf("ClaimApproval ok=%v record=%#v, want one claimed record", claimedOK, claimed)
	}
	if claimed.ConsumedAt == nil || !claimed.ConsumedAt.Equal(consumedAt) {
		t.Fatalf("claimed consumed_at = %v, want %s", claimed.ConsumedAt, consumedAt)
	}
	if claimed.ActionHash != req.ActionHash || claimed.PolicySnapshot != req.PolicySnapshot {
		t.Fatalf("claimed binding = action:%q snapshot:%q", claimed.ActionHash, claimed.PolicySnapshot)
	}

	secondClaim, secondOK, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       "tenant-a",
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     consumedAt.Add(time.Second),
	})
	var consumedConflict *ApprovalConflictError
	if !errors.As(err, &consumedConflict) || consumedConflict.Kind != ApprovalConflictKindConsumed {
		t.Fatalf("second ClaimApproval err = %v, want consumed ApprovalConflictError", err)
	}
	if secondOK || secondClaim != nil {
		t.Fatalf("second ClaimApproval = (%#v,%v), want nil,false consume-once", secondClaim, secondOK)
	}

	members, err := client.SMembers(ctx, edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash)).Result()
	if err != nil {
		t.Fatalf("read tuple index: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("tuple index members after consume = %#v, want empty", members)
	}

	createApprovalParents(t, ctx, store, "tenant-a", "sess-reject", "exec-reject", "event-reject", base.Add(10*time.Minute))
	rejectReq := validApprovalRequest("tenant-a", "sess-reject", "exec-reject", "event-reject", base.Add(10*time.Minute))
	rejectedSeed, err := store.EnqueueApproval(ctx, rejectReq)
	if err != nil {
		t.Fatalf("EnqueueApproval reject seed: %v", err)
	}
	rejected, err := store.RejectApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: rejectedSeed.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "too risky",
		ResolvedAt:  base.Add(11 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	if rejected.Status != ApprovalStatusRejected || rejected.Decision != ApprovalDecisionReject || rejected.ResolutionReason != "too risky" {
		t.Fatalf("rejected status/decision/reason = %q/%q/%q", rejected.Status, rejected.Decision, rejected.ResolutionReason)
	}
	members, err = client.SMembers(ctx, edgeApprovalTupleIndexKey(rejectReq.TenantID, rejectReq.SessionID, rejectReq.ExecutionID, rejectReq.ActionHash)).Result()
	if err != nil {
		t.Fatalf("read rejected tuple index: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("tuple index members after reject = %#v, want empty", members)
	}
}

// EDGE-058 — EnqueueApproval refuses inline validation when the parent
// execution's event list exceeds maxEventsPerApprovalValidation. The pre-fix
// loadEventFromTx at approval_store_redis.go:692 ran an unbounded
// LRange(ctx, edgeEventsKey, 0, -1) inside the WATCH/MULTI/EXEC, which an
// attacker could weaponize by looping AppendEvent on a runaway execution to
// pin gateway memory and break the EXEC for healthy executions sharing the
// same Redis connection. This test seeds an execution with more events than
// the validation cap allows, calls EnqueueApproval, and asserts the typed
// error is returned + nothing was enqueued. RED on unfixed code (approval
// enqueued silently); GREEN once loadEventFromTx adds the LLEN guard.
func TestRedisStoreEnqueueApprovalRefusesWhenEventListExceedsValidationCap(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	sessionID := "sess-edge058"
	executionID := "exec-edge058"
	eventID := "event-edge058"
	createApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, base)

	// createApprovalParents already appended one event. Append (max - 0) more
	// events so the list size is strictly greater than the cap and the LLEN
	// guard is forced to fire (1 + maxEventsPerApprovalValidation = cap+1).
	extra := make([]AgentActionEvent, 0, maxEventsPerApprovalValidation)
	for i := 0; i < maxEventsPerApprovalValidation; i++ {
		evt := validStoreEvent(tenantID, sessionID, executionID, fmt.Sprintf("event-edge058-pad-%d", i), 0, base.Add(time.Duration(i+10)*time.Second), EventKindHookPreToolUse, DecisionAllow)
		extra = append(extra, evt)
	}
	if _, err := store.AppendEvents(ctx, extra); err != nil {
		t.Fatalf("AppendEvents pad: %v", err)
	}

	// Sanity: list should now be cap+1 entries (1 from createApprovalParents +
	// cap pads). LLEN equals exactly maxEventsPerApprovalValidation+1.
	listLen, err := client.LLen(ctx, edgeEventsKey(executionID)).Result()
	if err != nil {
		t.Fatalf("LLen events: %v", err)
	}
	if listLen != int64(maxEventsPerApprovalValidation+1) {
		t.Fatalf("event list length = %d, want %d (cap+1)", listLen, maxEventsPerApprovalValidation+1)
	}

	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, base)
	approval, err := store.EnqueueApproval(ctx, req)
	if !errors.Is(err, ErrEventListTooLarge) {
		t.Fatalf("EnqueueApproval err = %v, want ErrEventListTooLarge", err)
	}
	if approval != nil {
		t.Fatalf("EnqueueApproval returned approval=%#v, want nil on cap rejection", approval)
	}

	// No partial enqueue: the pending-status index for this tenant must stay
	// empty, the by-action tuple set must stay empty, and the per-principal
	// index must stay empty. (The TX is supposed to short-circuit before any
	// of the ZAdd / SAdd writes at L127-133.)
	pendingCount, err := client.ZCard(ctx, edgeApprovalStatusIndexKey(req.TenantID, ApprovalStatusPending)).Result()
	if err != nil {
		t.Fatalf("ZCard pending status index: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("pending-status index ZCard = %d after rejected enqueue, want 0", pendingCount)
	}
	tupleMembers, err := client.SMembers(ctx, edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash)).Result()
	if err != nil {
		t.Fatalf("SMembers tuple index: %v", err)
	}
	if len(tupleMembers) != 0 {
		t.Fatalf("tuple index members = %#v after rejected enqueue, want empty", tupleMembers)
	}
	principalCount, err := client.ZCard(ctx, edgeApprovalPrincipalStatusIndexKey(req.TenantID, req.PrincipalID, ApprovalStatusPending)).Result()
	if err != nil {
		t.Fatalf("ZCard principal-status index: %v", err)
	}
	if principalCount != 0 {
		t.Fatalf("principal-status index ZCard = %d after rejected enqueue, want 0", principalCount)
	}
}

// EDGE-058 — verify the approval_enqueue_aborted_total{reason} metric fires
// with the bounded "event_list_too_large" reason on the abort path. Stub
// recorder embeds NoopRecorder so unrelated method calls (RecordSessionCreated
// etc. fired by other store paths) are no-ops; only RecordApprovalEnqueueAborted
// is captured under a mutex for safety.
type approvalAbortRecorder struct {
	NoopRecorder
	mu      sync.Mutex
	reasons []string
}

func (r *approvalAbortRecorder) RecordApprovalEnqueueAborted(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, reason)
}

func (r *approvalAbortRecorder) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...)
}

func TestRedisStoreEnqueueApprovalAbortedMetricFiresWithBoundedReason(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 4, 11, 0, 0, 0, time.UTC)
	rec := &approvalAbortRecorder{}
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }), WithRecorder(rec))
	defer cleanup()

	tenantID := "tenant-a"
	sessionID := "sess-edge058-metric"
	executionID := "exec-edge058-metric"
	eventID := "event-edge058-metric"
	createApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, base)

	// Pad past the cap so EnqueueApproval refuses.
	extra := make([]AgentActionEvent, 0, maxEventsPerApprovalValidation)
	for i := 0; i < maxEventsPerApprovalValidation; i++ {
		evt := validStoreEvent(tenantID, sessionID, executionID, fmt.Sprintf("event-edge058-metric-pad-%d", i), 0, base.Add(time.Duration(i+10)*time.Second), EventKindHookPreToolUse, DecisionAllow)
		extra = append(extra, evt)
	}
	if _, err := store.AppendEvents(ctx, extra); err != nil {
		t.Fatalf("AppendEvents pad: %v", err)
	}

	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, base)
	if _, err := store.EnqueueApproval(ctx, req); !errors.Is(err, ErrEventListTooLarge) {
		t.Fatalf("EnqueueApproval err = %v, want ErrEventListTooLarge", err)
	}
	got := rec.Snapshot()
	if len(got) != 1 || got[0] != "event_list_too_large" {
		t.Fatalf("recorder reasons = %#v, want [event_list_too_large]", got)
	}

	// Happy-path call (different execution, parent active, list under cap)
	// must NOT increment the abort counter.
	createApprovalParents(t, ctx, store, tenantID, "sess-happy", "exec-happy", "event-happy", base.Add(time.Hour))
	happyReq := validApprovalRequest(tenantID, "sess-happy", "exec-happy", "event-happy", base.Add(time.Hour))
	if _, err := store.EnqueueApproval(ctx, happyReq); err != nil {
		t.Fatalf("EnqueueApproval happy: %v", err)
	}
	got = rec.Snapshot()
	if len(got) != 1 {
		t.Fatalf("recorder reasons after happy enqueue = %#v, want still [event_list_too_large]", got)
	}
}

// EDGE-058 — adversarial-review case (c): zero-event execution must NOT
// surface as ErrEventListTooLarge from the new LLEN guard. The outside-TX
// validateApprovalRequestParents pre-check (L600 area) rejects with
// ErrNotFound before the WATCH/MULTI/EXEC closure runs, so loadEventFromTx
// is never reached. Confirms the bound is benign for empty lists and the
// pre-existing event-missing wire envelope is preserved (404, not 422).
func TestRedisStoreEnqueueApprovalZeroEventsReturnsNotFoundNotTooLarge(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 4, 11, 30, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	sessionID := "sess-edge058-zero"
	executionID := "exec-edge058-zero"
	eventID := "event-edge058-zero"
	// Create session + execution but DO NOT append the approval-parent event.
	createSessionAndExecution(t, ctx, store, tenantID, sessionID, executionID, base)

	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, base)
	approval, err := store.EnqueueApproval(ctx, req)
	if err == nil {
		t.Fatalf("EnqueueApproval err = nil, want ErrNotFound (event missing on zero-event execution)")
	}
	if errors.Is(err, ErrEventListTooLarge) {
		t.Fatalf("EnqueueApproval err = %v, must NOT be ErrEventListTooLarge for zero-event execution", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("EnqueueApproval err = %v, want ErrNotFound (event missing path)", err)
	}
	if approval != nil {
		t.Fatalf("EnqueueApproval returned approval=%#v, want nil", approval)
	}
}

// EDGE-058 — boundary: list size exactly equal to the cap is allowed (the
// guard is `> cap`, not `>= cap`). Confirms the LLEN-vs-cap comparison is
// strict-greater-than so legitimate workloads at the ceiling proceed.
func TestRedisStoreEnqueueApprovalAllowsExactlyCapEvents(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 4, 10, 30, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	sessionID := "sess-edge058-boundary"
	executionID := "exec-edge058-boundary"
	eventID := "event-edge058-boundary"
	createApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, base)

	// createApprovalParents seeded 1 event; pad to exactly cap entries.
	extra := make([]AgentActionEvent, 0, maxEventsPerApprovalValidation-1)
	for i := 0; i < maxEventsPerApprovalValidation-1; i++ {
		evt := validStoreEvent(tenantID, sessionID, executionID, fmt.Sprintf("event-edge058-boundary-pad-%d", i), 0, base.Add(time.Duration(i+10)*time.Second), EventKindHookPreToolUse, DecisionAllow)
		extra = append(extra, evt)
	}
	if _, err := store.AppendEvents(ctx, extra); err != nil {
		t.Fatalf("AppendEvents pad: %v", err)
	}
	listLen, err := client.LLen(ctx, edgeEventsKey(executionID)).Result()
	if err != nil {
		t.Fatalf("LLen events: %v", err)
	}
	if listLen != int64(maxEventsPerApprovalValidation) {
		t.Fatalf("event list length = %d, want %d (exactly cap)", listLen, maxEventsPerApprovalValidation)
	}

	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, base)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval at boundary: %v, want success", err)
	}
	if approval == nil || approval.Status != ApprovalStatusPending {
		t.Fatalf("EnqueueApproval at boundary returned %#v, want pending approval", approval)
	}
}

func TestRedisStoreApprovalListPaginationIsBounded(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 15, 30, 0, 0, time.UTC)
	now := base
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	refs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		now = base.Add(time.Duration(i) * time.Minute)
		sessionID := fmt.Sprintf("sess-page-%d", i)
		executionID := fmt.Sprintf("exec-page-%d", i)
		eventID := fmt.Sprintf("event-page-%d", i)
		createApprovalParents(t, ctx, store, "tenant-a", sessionID, executionID, eventID, now)
		req := validApprovalRequest("tenant-a", sessionID, executionID, eventID, now)
		approval, err := store.EnqueueApproval(ctx, req)
		if err != nil {
			t.Fatalf("EnqueueApproval page %d: %v", i, err)
		}
		refs = append(refs, approval.ApprovalRef)
	}

	first, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Limit: 2})
	if err != nil {
		t.Fatalf("ListApprovals first page: %v", err)
	}
	assertApprovalRefs(t, first.Items, []string{refs[2], refs[1]})
	if first.NextCursor != "2" {
		t.Fatalf("first page next_cursor = %q, want 2", first.NextCursor)
	}

	second, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListApprovals second page: %v", err)
	}
	assertApprovalRefs(t, second.Items, []string{refs[0]})
	if second.NextCursor != "" {
		t.Fatalf("second page next_cursor = %q, want empty", second.NextCursor)
	}
}

func TestRedisStoreApprovalListFiltersByPrincipalAndStatusIndexes(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 15, 45, 0, 0, time.UTC)
	now := base
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	ownOld := seedRedisApprovalForPrincipal(t, ctx, store, &now, base, "tenant-a", "principal-a", "principal-old")
	other := seedRedisApprovalForPrincipal(t, ctx, store, &now, base.Add(time.Minute), "tenant-a", "principal-b", "principal-other")
	ownApproved := seedRedisApprovalForPrincipal(t, ctx, store, &now, base.Add(2*time.Minute), "tenant-a", "principal-a", "principal-approved")
	ownNew := seedRedisApprovalForPrincipal(t, ctx, store, &now, base.Add(3*time.Minute), "tenant-a", "principal-a", "principal-new")

	approved, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: ownApproved.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approved for scoped-list test",
		ResolvedAt:  base.Add(4 * time.Minute),
	})
	if err != nil || approved.Status != ApprovalStatusApproved {
		t.Fatalf("ApproveApproval = (%#v,%v), want approved record", approved, err)
	}

	ownPending, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", PrincipalID: "principal-a", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals own pending: %v", err)
	}
	assertApprovalRefs(t, ownPending.Items, []string{ownNew.ApprovalRef, ownOld.ApprovalRef})

	ownAllFirst, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", PrincipalID: "principal-a", Limit: 2})
	if err != nil {
		t.Fatalf("ListApprovals own all first: %v", err)
	}
	assertApprovalRefs(t, ownAllFirst.Items, []string{ownNew.ApprovalRef, ownApproved.ApprovalRef})
	if ownAllFirst.NextCursor != "2" {
		t.Fatalf("own all first cursor = %q, want 2", ownAllFirst.NextCursor)
	}
	ownAllSecond, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", PrincipalID: "principal-a", Cursor: ownAllFirst.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListApprovals own all second: %v", err)
	}
	assertApprovalRefs(t, ownAllSecond.Items, []string{ownOld.ApprovalRef})

	otherPending, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", PrincipalID: "principal-b", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals other pending: %v", err)
	}
	assertApprovalRefs(t, otherPending.Items, []string{other.ApprovalRef})

	tupleAsWrongPrincipal, err := store.ListApprovals(ctx, ListApprovalsQuery{
		TenantID:    "tenant-a",
		PrincipalID: "principal-a",
		SessionID:   other.SessionID,
		ExecutionID: other.ExecutionID,
		ActionHash:  other.ActionHash,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListApprovals tuple wrong principal: %v", err)
	}
	assertApprovalRefs(t, tupleAsWrongPrincipal.Items, []string{})
}

func TestRedisStoreApprovalExpireAndStaleParentsFailClosed(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 16, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-expire", "exec-expire", "event-expire", base)
	expireReq := validApprovalRequest("tenant-a", "sess-expire", "exec-expire", "event-expire", base)
	expireReq.ExpiresAt = base.Add(time.Minute)
	expiring, err := store.EnqueueApproval(ctx, expireReq)
	if err != nil {
		t.Fatalf("EnqueueApproval expiring: %v", err)
	}
	expiredCount, err := store.ExpireApprovals(ctx, "tenant-a", base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ExpireApprovals: %v", err)
	}
	if expiredCount != 1 {
		t.Fatalf("ExpireApprovals count = %d, want 1", expiredCount)
	}
	expired, ok, err := store.GetApproval(ctx, "tenant-a", expiring.ApprovalRef)
	if err != nil || !ok {
		t.Fatalf("GetApproval expired = (%#v,%v,%v), want hit", expired, ok, err)
	}
	if expired.Status != ApprovalStatusExpired || expired.Decision != ApprovalDecisionExpire || expired.ResolvedAt == nil || !expired.ResolvedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("expired state = status:%q decision:%q resolved_at:%v", expired.Status, expired.Decision, expired.ResolvedAt)
	}

	createApprovalParents(t, ctx, store, "tenant-a", "sess-expired-resolve", "exec-expired-resolve", "event-expired-resolve", base.Add(5*time.Minute))
	expiredResolveReq := validApprovalRequest("tenant-a", "sess-expired-resolve", "exec-expired-resolve", "event-expired-resolve", base.Add(5*time.Minute))
	expiredResolveReq.ExpiresAt = base.Add(6 * time.Minute)
	expiredResolveApproval, err := store.EnqueueApproval(ctx, expiredResolveReq)
	if err != nil {
		t.Fatalf("EnqueueApproval expired resolve: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: expiredResolveApproval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "too late",
		ResolvedAt:  base.Add(7 * time.Minute),
	}); !errors.Is(err, ErrApprovalConflict) {
		t.Fatalf("ApproveApproval expired pending error = %v, want ErrApprovalConflict", err)
	}

	createApprovalParents(t, ctx, store, "tenant-a", "sess-stale", "exec-stale", "event-stale", base.Add(10*time.Minute))
	staleReq := validApprovalRequest("tenant-a", "sess-stale", "exec-stale", "event-stale", base.Add(10*time.Minute))
	staleApproval, err := store.EnqueueApproval(ctx, staleReq)
	if err != nil {
		t.Fatalf("EnqueueApproval stale: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: staleApproval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "ok",
		ResolvedAt:  base.Add(11 * time.Minute),
	}); err != nil {
		t.Fatalf("ApproveApproval stale seed: %v", err)
	}
	claimWrongAction, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       "tenant-a",
		ApprovalRef:    staleApproval.ApprovalRef,
		SessionID:      staleReq.SessionID,
		ExecutionID:    staleReq.ExecutionID,
		EventID:        staleReq.EventID,
		ActionHash:     "different-action-hash",
		InputHash:      staleReq.InputHash,
		PolicySnapshot: staleReq.PolicySnapshot,
		ConsumedAt:     base.Add(12 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimWrongAction != nil {
		t.Fatalf("ClaimApproval wrong action hash = (%#v,%v,%v), want ErrApprovalConflict nil,false", claimWrongAction, ok, err)
	}
	claimWrongSnapshot, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       "tenant-a",
		ApprovalRef:    staleApproval.ApprovalRef,
		SessionID:      staleReq.SessionID,
		ExecutionID:    staleReq.ExecutionID,
		EventID:        staleReq.EventID,
		ActionHash:     staleReq.ActionHash,
		InputHash:      staleReq.InputHash,
		PolicySnapshot: "policy-v2",
		ConsumedAt:     base.Add(12 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimWrongSnapshot != nil {
		t.Fatalf("ClaimApproval wrong snapshot = (%#v,%v,%v), want ErrApprovalConflict nil,false", claimWrongSnapshot, ok, err)
	}

	endedAt := base.Add(13 * time.Minute)
	if _, err := store.EndExecution(ctx, "tenant-a", staleReq.ExecutionID, endedAt, ExecutionStatusCancelled); err != nil {
		t.Fatalf("EndExecution stale: %v", err)
	}
	claimEnded, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       "tenant-a",
		ApprovalRef:    staleApproval.ApprovalRef,
		SessionID:      staleReq.SessionID,
		ExecutionID:    staleReq.ExecutionID,
		EventID:        staleReq.EventID,
		ActionHash:     staleReq.ActionHash,
		InputHash:      staleReq.InputHash,
		PolicySnapshot: staleReq.PolicySnapshot,
		ConsumedAt:     base.Add(14 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimEnded != nil {
		t.Fatalf("ClaimApproval ended execution = (%#v,%v,%v), want ErrApprovalConflict nil,false", claimEnded, ok, err)
	}

	createApprovalParents(t, ctx, store, "tenant-a", "sess-missing-event", "exec-missing-event", "event-missing-event", base.Add(15*time.Minute))
	missingEventReq := validApprovalRequest("tenant-a", "sess-missing-event", "exec-missing-event", "event-missing-event", base.Add(15*time.Minute))
	missingEventApproval, err := store.EnqueueApproval(ctx, missingEventReq)
	if err != nil {
		t.Fatalf("EnqueueApproval missing event seed: %v", err)
	}
	if err := client.Del(ctx, edgeEventsKey(missingEventReq.ExecutionID)).Err(); err != nil {
		t.Fatalf("delete edge events for missing-event test: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: missingEventApproval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "event missing",
		ResolvedAt:  base.Add(16 * time.Minute),
	}); !errors.Is(err, ErrApprovalConflict) {
		t.Fatalf("ApproveApproval missing event error = %v, want ErrApprovalConflict", err)
	}

	createApprovalParents(t, ctx, store, "tenant-a", "sess-ended", "exec-ended", "event-ended", base.Add(20*time.Minute))
	endedReq := validApprovalRequest("tenant-a", "sess-ended", "exec-ended", "event-ended", base.Add(20*time.Minute))
	endedApproval, err := store.EnqueueApproval(ctx, endedReq)
	if err != nil {
		t.Fatalf("EnqueueApproval ended-session seed: %v", err)
	}
	if _, err := store.EndSession(ctx, "tenant-a", endedReq.SessionID, base.Add(21*time.Minute), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: endedApproval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "too late",
		ResolvedAt:  base.Add(22 * time.Minute),
	}); !errors.Is(err, ErrApprovalConflict) {
		t.Fatalf("ApproveApproval ended session error = %v, want ErrApprovalConflict", err)
	}
}

func TestFirstLiveTupleApproval_ExcludesExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-expired-tuple", "exec-expired-tuple", "event-expired-tuple", now)
	req := validApprovalRequest("tenant-a", "sess-expired-tuple", "exec-expired-tuple", "event-expired-tuple", now)
	req.ExpiresAt = now.Add(time.Minute)
	first, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval first: %v", err)
	}

	now = now.Add(2 * time.Minute)
	req.ExpiresAt = now.Add(5 * time.Minute)
	second, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval second: %v", err)
	}
	if second.ApprovalRef == first.ApprovalRef {
		t.Fatalf("EnqueueApproval reused expired approval %q; want a fresh live tuple approval", first.ApprovalRef)
	}
}

func TestRedisStoreApprovalRejectedAndExpiredCannotBeClaimed(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 16, 30, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-rejected-claim", "exec-rejected-claim", "event-rejected-claim", base)
	rejectReq := validApprovalRequest("tenant-a", "sess-rejected-claim", "exec-rejected-claim", "event-rejected-claim", base)
	rejectSeed, err := store.EnqueueApproval(ctx, rejectReq)
	if err != nil {
		t.Fatalf("EnqueueApproval rejected seed: %v", err)
	}
	rejected, err := store.RejectApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: rejectSeed.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "denied by reviewer",
		ResolvedAt:  base.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	assertRedisApprovalCannotBeClaimed(t, ctx, store, *rejected, rejectReq, base.Add(2*time.Minute))

	createApprovalParents(t, ctx, store, "tenant-a", "sess-expired-claim", "exec-expired-claim", "event-expired-claim", base.Add(10*time.Minute))
	expireReq := validApprovalRequest("tenant-a", "sess-expired-claim", "exec-expired-claim", "event-expired-claim", base.Add(10*time.Minute))
	expiring, err := store.EnqueueApproval(ctx, expireReq)
	if err != nil {
		t.Fatalf("EnqueueApproval expired seed: %v", err)
	}
	if n, err := store.ExpireApprovals(ctx, "tenant-a", base.Add(16*time.Minute)); err != nil || n != 1 {
		t.Fatalf("ExpireApprovals = %d,%v, want 1,nil", n, err)
	}
	expired, ok, err := store.GetApproval(ctx, "tenant-a", expiring.ApprovalRef)
	if err != nil || !ok || expired == nil {
		t.Fatalf("GetApproval expired = (%#v,%v,%v), want stored expired approval", expired, ok, err)
	}
	assertRedisApprovalCannotBeClaimed(t, ctx, store, *expired, expireReq, base.Add(17*time.Minute))

	createApprovalParents(t, ctx, store, "tenant-a", "sess-approved-expired-claim", "exec-approved-expired-claim", "event-approved-expired-claim", base.Add(20*time.Minute))
	approvedExpiredReq := validApprovalRequest("tenant-a", "sess-approved-expired-claim", "exec-approved-expired-claim", "event-approved-expired-claim", base.Add(20*time.Minute))
	approvedExpiredReq.ExpiresAt = base.Add(21 * time.Minute)
	approvedExpired, err := store.EnqueueApproval(ctx, approvedExpiredReq)
	if err != nil {
		t.Fatalf("EnqueueApproval approved-expired seed: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    approvedExpiredReq.TenantID,
		ApprovalRef: approvedExpired.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approved before expiry",
		ResolvedAt:  base.Add(20*time.Minute + 30*time.Second),
	}); err != nil {
		t.Fatalf("ApproveApproval approved-expired seed: %v", err)
	}
	claimed, claimedOK, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       approvedExpiredReq.TenantID,
		ApprovalRef:    approvedExpired.ApprovalRef,
		SessionID:      approvedExpiredReq.SessionID,
		ExecutionID:    approvedExpiredReq.ExecutionID,
		EventID:        approvedExpiredReq.EventID,
		ActionHash:     approvedExpiredReq.ActionHash,
		InputHash:      approvedExpiredReq.InputHash,
		PolicySnapshot: approvedExpiredReq.PolicySnapshot,
		ConsumedAt:     base.Add(22 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || claimedOK || claimed != nil {
		t.Fatalf("ClaimApproval approved-after-expiry = (%#v,%v,%v), want ErrApprovalConflict nil,false", claimed, claimedOK, err)
	}
}

func TestRedisStoreApprovalValidationAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 17, 0, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-validate", "exec-validate", "event-validate", base)
	req := validApprovalRequest("tenant-a", "sess-validate", "exec-validate", "event-validate", base)
	for _, tc := range []struct {
		name    string
		mutate  func(*EdgeApprovalRequest)
		wantErr string
	}{
		{name: "tenant", mutate: func(r *EdgeApprovalRequest) { r.TenantID = "" }, wantErr: "tenant_id"},
		{name: "session", mutate: func(r *EdgeApprovalRequest) { r.SessionID = "" }, wantErr: "session_id"},
		{name: "execution", mutate: func(r *EdgeApprovalRequest) { r.ExecutionID = "" }, wantErr: "execution_id"},
		{name: "event", mutate: func(r *EdgeApprovalRequest) { r.EventID = "" }, wantErr: "event_id"},
		{name: "principal", mutate: func(r *EdgeApprovalRequest) { r.PrincipalID = "" }, wantErr: "principal_id"},
		{name: "requester", mutate: func(r *EdgeApprovalRequest) { r.Requester = "" }, wantErr: "requester"},
		{name: "action", mutate: func(r *EdgeApprovalRequest) { r.ActionHash = "" }, wantErr: "action_hash"},
		{name: "policy", mutate: func(r *EdgeApprovalRequest) { r.PolicySnapshot = "" }, wantErr: "policy_snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := req
			tc.mutate(&next)
			_, err := store.EnqueueApproval(ctx, next)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("EnqueueApproval error = %v, want %q", err, tc.wantErr)
			}
		})
	}

	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval valid: %v", err)
	}
	if crossTenant, ok, err := store.GetApproval(ctx, "tenant-b", approval.ApprovalRef); err != nil || ok || crossTenant != nil {
		t.Fatalf("cross-tenant GetApproval = (%#v,%v,%v), want miss", crossTenant, ok, err)
	}
	otherTenantPage, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-b", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals other tenant: %v", err)
	}
	assertApprovalRefs(t, otherTenantPage.Items, []string{})
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-b",
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "cross tenant",
		ResolvedAt:  base.Add(time.Minute),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant ApproveApproval error = %v, want ErrNotFound", err)
	}
}

// EDGE-062 — terminal-state transitions on an approval must remove the
// ref from the tenant-wide ZSET index. Pre-fix, expireApproval and
// resolveApproval (approve/reject) added the ref to a per-status index
// but did NOT ZRem from the tenant index, so the tenant index grew
// unbounded over the system's lifetime and a list-without-filter call
// returned ghosts of terminal approvals.

func TestRedisStoreApprovalExpireRemovesFromTenantIndex(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 19, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	expireAt := base.Add(2 * time.Hour) // past the 5m default TTL in validApprovalRequest
	for i := 0; i < 5; i++ {
		suffix := fmt.Sprintf("expire-%d", i)
		createApprovalParents(t, ctx, store, tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		req := validApprovalRequest(tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		if _, err := store.EnqueueApproval(ctx, req); err != nil {
			t.Fatalf("EnqueueApproval %s: %v", suffix, err)
		}
	}

	preExpire, err := client.ZCard(ctx, edgeApprovalTenantIndexKey(tenantID)).Result()
	if err != nil || preExpire != 5 {
		t.Fatalf("ZCard tenant index pre-expire = %d (err=%v), want 5", preExpire, err)
	}

	expired, err := store.ExpireApprovals(ctx, tenantID, expireAt)
	if err != nil {
		t.Fatalf("ExpireApprovals: %v", err)
	}
	if expired != 5 {
		t.Fatalf("ExpireApprovals expired = %d, want 5", expired)
	}

	postExpire, err := client.ZCard(ctx, edgeApprovalTenantIndexKey(tenantID)).Result()
	if err != nil {
		t.Fatalf("ZCard tenant index post-expire: %v", err)
	}
	if postExpire != 0 {
		t.Fatalf("tenant index ZCard post-expire = %d, want 0 (EDGE-062: expire path leaks tenant-index membership)", postExpire)
	}

	// Status indexes should reflect the transition.
	expiredCount, err := client.ZCard(ctx, edgeApprovalStatusIndexKey(tenantID, ApprovalStatusExpired)).Result()
	if err != nil || expiredCount != 5 {
		t.Fatalf("expired status index ZCard = %d (err=%v), want 5", expiredCount, err)
	}
}

func TestRedisStoreApprovalRejectRemovesFromTenantIndex(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 19, 30, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	refs := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		suffix := fmt.Sprintf("reject-%d", i)
		createApprovalParents(t, ctx, store, tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		req := validApprovalRequest(tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		approval, err := store.EnqueueApproval(ctx, req)
		if err != nil {
			t.Fatalf("EnqueueApproval %s: %v", suffix, err)
		}
		refs = append(refs, approval.ApprovalRef)
	}

	for i, ref := range refs {
		if _, err := store.RejectApproval(ctx, ApprovalResolution{
			TenantID:    tenantID,
			ApprovalRef: ref,
			ResolverID:  "principal-reviewer",
			ResolvedBy:  "reviewer@example.invalid",
			Reason:      fmt.Sprintf("rejected %d", i),
			ResolvedAt:  base.Add(time.Duration(i+1) * time.Minute),
		}); err != nil {
			t.Fatalf("RejectApproval %s: %v", ref, err)
		}
	}

	postReject, err := client.ZCard(ctx, edgeApprovalTenantIndexKey(tenantID)).Result()
	if err != nil {
		t.Fatalf("ZCard tenant index post-reject: %v", err)
	}
	if postReject != 0 {
		t.Fatalf("tenant index ZCard post-reject = %d, want 0 (EDGE-062: reject path leaks tenant-index membership)", postReject)
	}
}

func TestRedisStoreApprovalApproveAndClaimRemovesFromTenantIndex(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	type seeded struct {
		ref string
		req EdgeApprovalRequest
	}
	approvals := make([]seeded, 0, 5)
	for i := 0; i < 5; i++ {
		suffix := fmt.Sprintf("approve-%d", i)
		createApprovalParents(t, ctx, store, tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		req := validApprovalRequest(tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		approval, err := store.EnqueueApproval(ctx, req)
		if err != nil {
			t.Fatalf("EnqueueApproval %s: %v", suffix, err)
		}
		approvals = append(approvals, seeded{ref: approval.ApprovalRef, req: req})
	}

	// Approvals have a 5-minute TTL by default; keep all resolutions and
	// consumptions inside the first minute so the cap doesn't fire.
	for i, s := range approvals {
		offset := time.Duration(i) * time.Second
		if _, err := store.ApproveApproval(ctx, ApprovalResolution{
			TenantID:    tenantID,
			ApprovalRef: s.ref,
			ResolverID:  "principal-reviewer",
			ResolvedBy:  "reviewer@example.invalid",
			Reason:      fmt.Sprintf("approved %d", i),
			ResolvedAt:  base.Add(offset),
		}); err != nil {
			t.Fatalf("ApproveApproval %s: %v", s.ref, err)
		}
		consumed, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
			TenantID:       tenantID,
			ApprovalRef:    s.ref,
			SessionID:      s.req.SessionID,
			ExecutionID:    s.req.ExecutionID,
			EventID:        s.req.EventID,
			ActionHash:     s.req.ActionHash,
			PolicySnapshot: s.req.PolicySnapshot,
			InputHash:      s.req.InputHash,
			ConsumedAt:     base.Add(offset + 100*time.Millisecond),
		})
		if err != nil || !ok || consumed == nil {
			t.Fatalf("ClaimApproval %s = (%v,%v,%v), want consumed", s.ref, consumed, ok, err)
		}
	}

	postConsume, err := client.ZCard(ctx, edgeApprovalTenantIndexKey(tenantID)).Result()
	if err != nil {
		t.Fatalf("ZCard tenant index post-consume: %v", err)
	}
	if postConsume != 0 {
		t.Fatalf("tenant index ZCard post-consume = %d, want 0 (EDGE-062: consumed approvals leak tenant-index membership)", postConsume)
	}
}

func TestRedisStoreApprovalListWithoutFilterReturnsOnlyActive(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 21, 0, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	tenantID := "tenant-a"
	for i := 0; i < 5; i++ {
		suffix := fmt.Sprintf("list-%d", i)
		createApprovalParents(t, ctx, store, tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		req := validApprovalRequest(tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, base)
		if _, err := store.EnqueueApproval(ctx, req); err != nil {
			t.Fatalf("EnqueueApproval %s: %v", suffix, err)
		}
	}

	// Expire the original approvals by advancing past their 5m TTL.
	expireAt := base.Add(10 * time.Minute)
	expired, err := store.ExpireApprovals(ctx, tenantID, expireAt)
	if err != nil {
		t.Fatalf("ExpireApprovals: %v", err)
	}
	if expired != 5 {
		t.Fatalf("ExpireApprovals expired %d approvals, want 5", expired)
	}

	// Re-seed 2 active approvals AFTER the expire pass so they survive.
	expectedActive := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		suffix := fmt.Sprintf("active-%d", i)
		createApprovalParents(t, ctx, store, tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, expireAt)
		req := validApprovalRequest(tenantID, "sess-"+suffix, "exec-"+suffix, "event-"+suffix, expireAt)
		req.ExpiresAt = expireAt.Add(1 * time.Hour) // active beyond expireAt
		approval, err := store.EnqueueApproval(ctx, req)
		if err != nil {
			t.Fatalf("EnqueueApproval active %s: %v", suffix, err)
		}
		expectedActive = append(expectedActive, approval.ApprovalRef)
	}

	page, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: tenantID, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals no-filter: %v", err)
	}
	gotRefs := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		gotRefs = append(gotRefs, item.ApprovalRef)
	}

	// Post-fix: only active approvals appear in tenant index. Pre-fix: all 7
	// would appear (5 expired + 2 active) because no ZRem on expire.
	if len(gotRefs) != 2 {
		t.Fatalf("ListApprovals no-filter returned %d items, want 2 (only active approvals — EDGE-062 expire leaks tenant-index): %#v", len(gotRefs), gotRefs)
	}
	for _, ref := range gotRefs {
		found := false
		for _, expected := range expectedActive {
			if ref == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ListApprovals no-filter returned unexpected ref %q (not in expected active set %#v)", ref, expectedActive)
		}
	}
}

func TestRedisStoreApprovalConcurrentClaimConsumesOnce(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-concurrent", "exec-concurrent", "event-concurrent", base)
	req := validApprovalRequest("tenant-a", "sess-concurrent", "exec-concurrent", "event-concurrent", base)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    "tenant-a",
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approve once",
		ResolvedAt:  base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}

	prewarmRedisClientPool(t, ctx, client)

	const goroutines = 32
	start := make(chan struct{})
	results := make(chan bool, goroutines)
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			claimed, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
				TenantID:       "tenant-a",
				ApprovalRef:    approval.ApprovalRef,
				SessionID:      req.SessionID,
				ExecutionID:    req.ExecutionID,
				EventID:        req.EventID,
				ActionHash:     req.ActionHash,
				InputHash:      req.InputHash,
				PolicySnapshot: req.PolicySnapshot,
				ConsumedAt:     base.Add(2*time.Minute + time.Duration(i)*time.Microsecond),
			})
			if err != nil {
				var conflict *ApprovalConflictError
				if errors.As(err, &conflict) && conflict.Kind == ApprovalConflictKindConsumed {
					results <- false
					return
				}
				errs <- err
				return
			}
			results <- ok && claimed != nil
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("ClaimApproval concurrent error: %v", err)
	}
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent ClaimApproval winners = %d, want exactly 1", winners)
	}

	got, ok, err := store.GetApproval(ctx, "tenant-a", approval.ApprovalRef)
	if err != nil || !ok {
		t.Fatalf("GetApproval after concurrent claim = (%#v,%v,%v)", got, ok, err)
	}
	if got.ConsumedAt == nil {
		t.Fatalf("approval consumed_at is nil after one winning claim")
	}
	members, err := client.SMembers(ctx, edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash)).Result()
	if err != nil {
		t.Fatalf("read tuple index after concurrent claim: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("tuple index after concurrent claim = %#v, want empty", members)
	}
}

func TestApprovalCASRejectsSameActionHashDifferentInputHash(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 18, 30, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-input-cas", "exec-input-cas", "event-input-cas", base)
	req := validApprovalRequest("tenant-a", "sess-input-cas", "exec-input-cas", "event-input-cas", base)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approve for input CAS",
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
		InputHash:      "sha256:different-input",
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     base.Add(2 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimed != nil {
		t.Fatalf("ClaimApproval same action/different input = (%#v,%v,%v), want ErrApprovalConflict nil,false", claimed, ok, err)
	}

	claimed, ok, err = store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       req.TenantID,
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     base.Add(3 * time.Minute),
	})
	if err != nil || !ok || claimed == nil {
		t.Fatalf("ClaimApproval correct input after rejected mismatch = (%#v,%v,%v), want consumed approval", claimed, ok, err)
	}
}

func TestRedisStoreApprovalConcurrentResolveExpireHasSingleOutcome(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 19, 0, 0, 0, time.UTC)
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-race", "exec-race", "event-race", base)
	req := validApprovalRequest("tenant-a", "sess-race", "exec-race", "event-race", base)
	req.ExpiresAt = base.Add(time.Minute)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}

	prewarmRedisClientPool(t, ctx, client)

	const goroutines = 24
	start := make(chan struct{})
	outcomes := make(chan string, goroutines)
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			switch i % 3 {
			case 0:
				_, err := store.ApproveApproval(ctx, ApprovalResolution{
					TenantID:    "tenant-a",
					ApprovalRef: approval.ApprovalRef,
					ResolverID:  "reviewer-approve",
					ResolvedBy:  "approve@example.invalid",
					Reason:      "approve race",
					ResolvedAt:  base.Add(30 * time.Second),
				})
				if err == nil {
					outcomes <- "approved"
					return
				}
				if !errors.Is(err, ErrApprovalConflict) {
					errs <- err
				}
			case 1:
				_, err := store.RejectApproval(ctx, ApprovalResolution{
					TenantID:    "tenant-a",
					ApprovalRef: approval.ApprovalRef,
					ResolverID:  "reviewer-reject",
					ResolvedBy:  "reject@example.invalid",
					Reason:      "reject race",
					ResolvedAt:  base.Add(30 * time.Second),
				})
				if err == nil {
					outcomes <- "rejected"
					return
				}
				if !errors.Is(err, ErrApprovalConflict) {
					errs <- err
				}
			default:
				n, err := store.ExpireApprovals(ctx, "tenant-a", base.Add(2*time.Minute))
				if err != nil {
					errs <- err
					return
				}
				if n == 1 {
					outcomes <- "expired"
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	close(errs)
	for err := range errs {
		t.Fatalf("resolve/expire race unexpected error: %v", err)
	}
	winners := make([]string, 0, 1)
	for outcome := range outcomes {
		winners = append(winners, outcome)
	}
	if len(winners) != 1 {
		t.Fatalf("resolve/expire race winners = %#v, want exactly one terminal transition", winners)
	}

	got, ok, err := store.GetApproval(ctx, "tenant-a", approval.ApprovalRef)
	if err != nil || !ok {
		t.Fatalf("GetApproval after resolve/expire race = (%#v,%v,%v)", got, ok, err)
	}
	switch got.Status {
	case ApprovalStatusApproved:
		if got.Decision != ApprovalDecisionApprove || got.ResolverID != "reviewer-approve" {
			t.Fatalf("approved race record decision/resolver = %q/%q", got.Decision, got.ResolverID)
		}
	case ApprovalStatusRejected:
		if got.Decision != ApprovalDecisionReject || got.ResolverID != "reviewer-reject" {
			t.Fatalf("rejected race record decision/resolver = %q/%q", got.Decision, got.ResolverID)
		}
	case ApprovalStatusExpired:
		if got.Decision != ApprovalDecisionExpire || got.ResolvedAt == nil || !got.ResolvedAt.Equal(base.Add(2*time.Minute)) {
			t.Fatalf("expired race record decision/resolved_at = %q/%v", got.Decision, got.ResolvedAt)
		}
	default:
		t.Fatalf("resolve/expire race left status %q, want approved/rejected/expired", got.Status)
	}
	pending, err := store.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending, Limit: 10})
	if err != nil {
		t.Fatalf("ListApprovals pending after race: %v", err)
	}
	assertApprovalRefs(t, pending.Items, []string{})
	if got.Status == ApprovalStatusRejected || got.Status == ApprovalStatusExpired {
		members, err := client.SMembers(ctx, edgeApprovalTupleIndexKey(req.TenantID, req.SessionID, req.ExecutionID, req.ActionHash)).Result()
		if err != nil {
			t.Fatalf("read tuple index after terminal race: %v", err)
		}
		if len(members) != 0 {
			t.Fatalf("tuple index after %s race = %#v, want empty", got.Status, members)
		}
	}
}

func createApprovalParents(t *testing.T, ctx context.Context, store *RedisStore, tenantID, sessionID, executionID, eventID string, started time.Time) {
	t.Helper()
	createSessionAndExecution(t, ctx, store, tenantID, sessionID, executionID, started)
	event := validStoreEvent(tenantID, sessionID, executionID, eventID, 0, started.Add(2*time.Second), EventKindApprovalRequested, DecisionRequireApproval)
	event.InputHash = "sha256:" + eventID
	event.PolicySnapshot = "policy-v1"
	event.Status = ActionStatusBlocked
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent approval parent: %v", err)
	}
}

func validApprovalRequest(tenantID, sessionID, executionID, eventID string, createdAt time.Time) EdgeApprovalRequest {
	expiresAt := createdAt.Add(5 * time.Minute)
	return EdgeApprovalRequest{
		TenantID:       tenantID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		EventID:        eventID,
		PrincipalID:    "principal-a",
		Requester:      "principal-a",
		Reason:         "approval required for " + eventID,
		RuleID:         "claude-code.require-approval-for-edits",
		PolicySnapshot: "policy-v1",
		ActionHash:     "actionhash-" + eventID,
		InputHash:      "sha256:" + eventID,
		ExpiresAt:      expiresAt,
		Labels:         Labels{"env": "test"},
		Metadata:       Metadata{"source": "redis-test"},
	}
}

func seedRedisApprovalForPrincipal(
	t *testing.T,
	ctx context.Context,
	store *RedisStore,
	now *time.Time,
	createdAt time.Time,
	tenantID string,
	principalID string,
	suffix string,
) EdgeApproval {
	t.Helper()
	*now = createdAt
	sessionID := "sess-" + suffix
	executionID := "exec-" + suffix
	eventID := "event-" + suffix
	createApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, createdAt)
	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, createdAt)
	req.PrincipalID = principalID
	req.Requester = principalID
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval %s/%s: %v", principalID, suffix, err)
	}
	return *approval
}

func assertApprovalRefs(t *testing.T, got []EdgeApproval, want []string) {
	t.Helper()
	refs := make([]string, 0, len(got))
	for _, item := range got {
		refs = append(refs, item.ApprovalRef)
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("approval refs = %#v, want %#v", refs, want)
	}
}

func assertRedisApprovalCannotBeClaimed(t *testing.T, ctx context.Context, store *RedisStore, approval EdgeApproval, req EdgeApprovalRequest, consumedAt time.Time) {
	t.Helper()
	claimed, ok, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       req.TenantID,
		ApprovalRef:    approval.ApprovalRef,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     consumedAt,
	})
	if err != nil {
		var conflict *ApprovalConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("ClaimApproval terminal approval %q err=%v, want typed ApprovalConflictError", approval.Status, err)
		}
		switch approval.Status {
		case ApprovalStatusRejected:
			if conflict.Kind != ApprovalConflictKindRejected {
				t.Fatalf("ClaimApproval conflict kind = %q, want %q", conflict.Kind, ApprovalConflictKindRejected)
			}
		case ApprovalStatusExpired, ApprovalStatusInvalidated:
			if conflict.Kind != ApprovalConflictKindExpired {
				t.Fatalf("ClaimApproval conflict kind = %q, want %q", conflict.Kind, ApprovalConflictKindExpired)
			}
		}
	} else {
		t.Fatalf("ClaimApproval terminal approval %q returned nil err, want typed conflict", approval.Status)
	}
	if ok || claimed != nil {
		t.Fatalf("ClaimApproval terminal approval %q = (%#v,%v), want nil,false", approval.Status, claimed, ok)
	}
	stored, found, err := store.GetApproval(ctx, req.TenantID, approval.ApprovalRef)
	if err != nil || !found || stored == nil {
		t.Fatalf("GetApproval after terminal claim attempt = (%#v,%v,%v), want stored approval", stored, found, err)
	}
	if stored.Status != approval.Status || stored.ConsumedAt != nil {
		t.Fatalf("terminal approval after claim = status:%q consumed:%v, want %q nil", stored.Status, stored.ConsumedAt, approval.Status)
	}
}

// TestRedisStoreLookupByActionHashReturnsMostRecentApproved seeds two
// approvals against the same (tenant, action_hash), approves the second,
// and asserts LookupByActionHash returns the approved one. Verifies the
// action-hash index that backs actiongates.ApprovalLookup wiring.
func TestRedisStoreLookupByActionHashReturnsMostRecentApproved(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	now := base
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	sharedHash := "sha256:shared-action-payload"
	tenant := "tenant-hash"

	createApprovalParents(t, ctx, store, tenant, "sess-h1", "exec-h1", "event-h1", base)
	req1 := validApprovalRequest(tenant, "sess-h1", "exec-h1", "event-h1", base)
	req1.ActionHash = sharedHash
	now = base
	first, err := store.EnqueueApproval(ctx, req1)
	if err != nil {
		t.Fatalf("EnqueueApproval first: %v", err)
	}

	createApprovalParents(t, ctx, store, tenant, "sess-h2", "exec-h2", "event-h2", base.Add(1*time.Minute))
	req2 := validApprovalRequest(tenant, "sess-h2", "exec-h2", "event-h2", base.Add(1*time.Minute))
	req2.ActionHash = sharedHash
	now = base.Add(1 * time.Minute)
	second, err := store.EnqueueApproval(ctx, req2)
	if err != nil {
		t.Fatalf("EnqueueApproval second: %v", err)
	}

	// Both refs visible in GetApprovalsByActionHash, most-recent first.
	all, err := store.GetApprovalsByActionHash(ctx, tenant, sharedHash)
	if err != nil {
		t.Fatalf("GetApprovalsByActionHash: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d approvals, want 2", len(all))
	}
	if all[0].ApprovalRef != second.ApprovalRef || all[1].ApprovalRef != first.ApprovalRef {
		t.Fatalf("ordering = [%s, %s], want most-recent first [%s, %s]",
			all[0].ApprovalRef, all[1].ApprovalRef, second.ApprovalRef, first.ApprovalRef)
	}

	// Cross-tenant returns empty (action-hash index is tenant-scoped).
	if other, err := store.GetApprovalsByActionHash(ctx, "tenant-other", sharedHash); err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant GetApprovalsByActionHash = (%d, %v), want 0/nil", len(other), err)
	}

	// LookupByActionHash before any approval: no actionable approval.
	got, ok, err := store.LookupByActionHash(ctx, tenant, sharedHash)
	if err != nil {
		t.Fatalf("LookupByActionHash pre-approval err: %v", err)
	}
	if ok || got != nil {
		t.Fatalf("pre-approval lookup = (%#v, %v), want nil/false", got, ok)
	}

	// Approve the most recent (second) approval; lookup must return it.
	now = base.Add(2 * time.Minute)
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    tenant,
		ApprovalRef: second.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approved",
		ResolvedAt:  now,
	}); err != nil {
		t.Fatalf("ApproveApproval second: %v", err)
	}

	got, ok, err = store.LookupByActionHash(ctx, tenant, sharedHash)
	if err != nil {
		t.Fatalf("LookupByActionHash post-approval err: %v", err)
	}
	if !ok || got == nil || got.ApprovalRef != second.ApprovalRef {
		t.Fatalf("post-approval lookup = (%#v, %v), want second approval ref %q", got, ok, second.ApprovalRef)
	}

	// Consume the approval; lookup must return nil (no actionable approval left).
	now = base.Add(3 * time.Minute)
	if _, _, err := store.ClaimApproval(ctx, ApprovalClaimRequest{
		TenantID:       tenant,
		ApprovalRef:    second.ApprovalRef,
		SessionID:      req2.SessionID,
		ExecutionID:    req2.ExecutionID,
		EventID:        req2.EventID,
		ActionHash:     sharedHash,
		InputHash:      req2.InputHash,
		PolicySnapshot: req2.PolicySnapshot,
		ConsumedAt:     now,
	}); err != nil {
		t.Fatalf("ClaimApproval: %v", err)
	}
	got, ok, err = store.LookupByActionHash(ctx, tenant, sharedHash)
	if err != nil {
		t.Fatalf("LookupByActionHash post-claim err: %v", err)
	}
	if ok || got != nil {
		t.Fatalf("post-claim lookup = (%#v, %v), want nil/false (consumed)", got, ok)
	}

	// Empty action hash short-circuits to nil without an index call.
	if empty, err := store.GetApprovalsByActionHash(ctx, tenant, ""); err != nil || len(empty) != 0 {
		t.Fatalf("empty action_hash lookup = (%d, %v), want 0/nil", len(empty), err)
	}
}

// TestApprovalConsumePolicySnapshotMismatch is a regression guard for the
// existing approvalClaimMatches contract: when an approval was minted
// against policy_snapshot "policy-v1" and the consume call presents
// "policy-v2", ClaimApproval MUST refuse with ErrApprovalConflict. The
// behaviour is already implemented (approvalClaimMatches checks PolicySnapshot)
// but no test pinned it explicitly — this case is asserted via the args/
// input pair tests but never policy-snapshot in isolation. EDGE-103 surfaces
// the same mismatch as -32096 error.data.kind=policy_mismatch at the MCP
// entry-path layer; this test prevents a future refactor from silently
// dropping the store-level guard that backs that wire-level error.
func TestApprovalConsumePolicySnapshotMismatch(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 19, 0, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-pol", "exec-pol", "event-pol", base)
	req := validApprovalRequest("tenant-a", "sess-pol", "exec-pol", "event-pol", base)
	req.PolicySnapshot = "policy-v1"
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "approve for policy drift test",
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
		PolicySnapshot: "policy-v2",
		ConsumedAt:     base.Add(2 * time.Minute),
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimed != nil {
		t.Fatalf("ClaimApproval with drifted policy_snapshot = (%#v,%v,%v); want ErrApprovalConflict nil,false", claimed, ok, err)
	}
}

// TestApprovalConsumeRejectsSelfApprovalAtStore pins the store-level
// self-approval guard architect-cd323a16 mandated in EDGE-103
// (comment-f1d377b1 section D — defense in depth at BOTH store and entry).
// The gateway approve path enforces requester != approver; this store check
// backs that up if a future refactor bypasses the entry-path guard. The store
// must reject CallerAgentID == ResolverID. CallerAgentID == Requester is valid
// for ordinary MCP retry semantics unless that same principal is also the
// resolver/approver.
func TestApprovalConsumeRejectsSelfApprovalAtStore(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 19, 5, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-self", "exec-self", "event-self", base)
	req := validApprovalRequest("tenant-a", "sess-self", "exec-self", "event-self", base)
	req.Requester = "principal-a"
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	// Approve by the SAME principal who requested — this is the self-approval
	// scenario the store-level guard MUST reject when Consume happens.
	if _, err := store.ApproveApproval(ctx, ApprovalResolution{
		TenantID:    req.TenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "principal-a",
		ResolvedBy:  "principal-a@example.invalid",
		Reason:      "self-approve attempt",
		ResolvedAt:  base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}

	// Caller is the same principal as the resolver/approver — store must
	// recognize this as self-approval and refuse to consume. The store owns
	// the typed-error surface; entry paths compose that into protocol/UI copy.
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
		CallerAgentID:  "principal-a",
	})
	if !errors.Is(err, ErrApprovalConflict) || ok || claimed != nil {
		t.Fatalf("ClaimApproval with self-approval caller = (%#v,%v,%v); want ErrApprovalConflict nil,false (store-level defense-in-depth per EDGE-103 amendment D)",
			claimed, ok, err)
	}
}

func TestApprovalCreateClipsExpiresAtToDefaultMaxTTL(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 19, 5, 0, 0, time.UTC)
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return base }))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-ttl-default", "exec-ttl-default", "event-ttl-default", base)
	req := validApprovalRequest("tenant-a", "sess-ttl-default", "exec-ttl-default", "event-ttl-default", base)
	req.ExpiresAt = base.Add(24 * time.Hour)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if approval.ExpiresAt == nil {
		t.Fatalf("approval.ExpiresAt = nil; want clipped to default max %v after base", DefaultApprovalMaxTTL)
	}
	want := base.Add(DefaultApprovalMaxTTL)
	if !approval.ExpiresAt.Equal(want) {
		t.Fatalf("approval.ExpiresAt = %v; want %v (clipped to DefaultApprovalMaxTTL=%v)",
			approval.ExpiresAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano), DefaultApprovalMaxTTL)
	}
}

func TestApprovalCreateClipsExpiresAtToConfiguredMaxTTL(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 19, 10, 0, 0, time.UTC)
	const maxTTL = 10 * time.Minute
	store, _, _, cleanup := newRedisEdgeStore(t,
		WithClock(func() time.Time { return base }),
		WithApprovalMaxTTL(maxTTL),
	)
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-ttl", "exec-ttl", "event-ttl", base)
	req := validApprovalRequest("tenant-a", "sess-ttl", "exec-ttl", "event-ttl", base)
	req.ExpiresAt = base.Add(24 * time.Hour)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	if approval.ExpiresAt == nil {
		t.Fatalf("approval.ExpiresAt = nil; want clipped to %v after base", maxTTL)
	}
	want := base.Add(maxTTL)
	if !approval.ExpiresAt.Equal(want) {
		t.Fatalf("approval.ExpiresAt = %v; want %v (clipped to ApprovalMaxTTL=%v)",
			approval.ExpiresAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano), maxTTL)
	}
}

// TestGetApprovalsByActionHash_AppliesAuditLimit asserts the bounded-
// fetch contract added by task-69d1f82b (CodeRabbit PR #274 finding #2).
// Before this change, GetApprovalsByActionHash used ZRevRange(0,-1)
// and could fetch thousands of refs for a hot (tenant, action_hash)
// tuple — unbounded memory + latency. After this change, the cap is
// maxApprovalsByActionHashAudit = 256, most-recent first. 300 refs in,
// 256 out (cap), with the most-recent 256 preserved.
func TestGetApprovalsByActionHash_AppliesAuditLimit(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 21, 0, 0, 0, time.UTC)
	now := base
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	tenant := "tenant-limit"
	sharedHash := "sha256:hot-action"

	const created = 300
	for i := 0; i < created; i++ {
		now = base.Add(time.Duration(i) * time.Second)
		sessID := fmt.Sprintf("sess-%03d", i)
		execID := fmt.Sprintf("exec-%03d", i)
		eventID := fmt.Sprintf("event-%03d", i)
		createApprovalParents(t, ctx, store, tenant, sessID, execID, eventID, now)
		req := validApprovalRequest(tenant, sessID, execID, eventID, now)
		req.ActionHash = sharedHash
		if _, err := store.EnqueueApproval(ctx, req); err != nil {
			t.Fatalf("EnqueueApproval %d: %v", i, err)
		}
	}

	// DoD #3 bounded-scan proof: with 300 approvals indexed the call must
	// return exactly maxApprovalsByActionHashAudit rows, most-recent first.
	// Result-count is the deterministic contract verifier — a regression to
	// unbounded ZRevRange(0,-1) would return all 300 rows and fail the
	// len() check below immediately. Wall-clock elapsed is logged for
	// telemetry only; hard latency thresholds in unit tests are flake-prone
	// on slow CI runners (PR #276 CodeRabbit #19) and perf bounds belong
	// in benchmarks, not unit tests.
	start := time.Now()
	all, err := store.GetApprovalsByActionHash(ctx, tenant, sharedHash)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("GetApprovalsByActionHash: %v", err)
	}
	t.Logf("GetApprovalsByActionHash latency over %d-approval index: %v", created, elapsed)
	if len(all) != maxApprovalsByActionHashAudit {
		t.Fatalf("GetApprovalsByActionHash len = %d; want %d (audit cap)", len(all), maxApprovalsByActionHashAudit)
	}
	// Most-recent first: index 0 corresponds to event-299 (highest score).
	if !strings.HasSuffix(all[0].EventID, "-299") {
		t.Fatalf("most-recent ordering broken: all[0].EventID = %q; want suffix '-299'", all[0].EventID)
	}
}

func TestApprovalActionHash_ZSetGrowthIsBounded(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	now := base
	store, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	tenant := "tenant-action-hash-bound"
	otherTenant := "tenant-action-hash-other"
	sharedHash := "sha256:hot-action-bounded"
	const overflow = 25
	total := maxApprovalActionHashMembers + overflow
	refs := make([]string, 0, total)
	for i := 0; i < total; i++ {
		refs = append(refs, enqueueRejectedApprovalForHash(t, ctx, store, &now, base, tenant, sharedHash, i))
	}

	key := edgeApprovalActionHashIndexKey(tenant, sharedHash)
	memberCount, err := client.ZCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("ZCard action-hash index: %v", err)
	}
	if memberCount > int64(maxApprovalActionHashMembers) {
		t.Fatalf("action-hash index has %d members, want <= %d", memberCount, maxApprovalActionHashMembers)
	}
	members, err := client.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		t.Fatalf("ZRange action-hash index: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("action-hash member len = %d, want 0 terminal approvals retained in hot-path index", len(members))
	}
	old, ok, err := store.GetApproval(ctx, tenant, refs[0])
	if err != nil || !ok || old == nil || old.Status != ApprovalStatusRejected {
		t.Fatalf("trimmed index member primary record = (%#v,%v,%v), want rejected approval retained", old, ok, err)
	}
	otherRef := enqueueRejectedApprovalForHash(t, ctx, store, &now, base, otherTenant, sharedHash, total+1)
	otherMembers, err := client.ZRange(ctx, edgeApprovalActionHashIndexKey(otherTenant, sharedHash), 0, -1).Result()
	if err != nil || !reflect.DeepEqual(otherMembers, []string{}) {
		t.Fatalf("other-tenant action-hash members = %#v, %v; want no rejected approvals retained for %q", otherMembers, err, otherRef)
	}
}

func enqueueRejectedApprovalForHash(t *testing.T, ctx context.Context, store *RedisStore, now *time.Time, base time.Time, tenantID, actionHash string, index int) string {
	t.Helper()
	*now = base.Add(time.Duration(index) * time.Second)
	suffix := fmt.Sprintf("%04d", index)
	sessionID := "sess-bound-" + suffix
	executionID := "exec-bound-" + suffix
	eventID := "event-bound-" + suffix
	createApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, *now)
	req := validApprovalRequest(tenantID, sessionID, executionID, eventID, *now)
	req.ActionHash = actionHash
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval %d: %v", index, err)
	}
	if _, err := store.RejectApproval(ctx, ApprovalResolution{
		TenantID:    tenantID,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "reviewer-action-hash",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "deny after indexing for retention test",
		ResolvedAt:  now.Add(time.Millisecond),
	}); err != nil {
		t.Fatalf("RejectApproval %d: %v", index, err)
	}
	return approval.ApprovalRef
}

// TestLookupByActionHash_FastPathSemanticsDocumented asserts the
// fast-path semantics added by task-69d1f82b: LookupByActionHash now
// scans at most maxApprovalsByActionHashLookup = 64 most-recent entries.
// If no actionable approval surfaces in that window, the caller sees
// a clean miss — fail-closed by design. An attacker buring an old
// APPROVED approval under >64 fresh PENDING records causes the gate
// to re-fire REQUIRE_HUMAN; the call cannot silently land using the
// stale grant. (1) PENDING fill up to 100 + APPROVED at index 30 →
// found. (2) Same fill + APPROVED at index 80 → miss.
func TestLookupByActionHash_FastPathSemanticsDocumented(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 15, 21, 30, 0, 0, time.UTC)
	now := base
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()

	tenant := "tenant-fastpath"

	t.Run("approved_within_fast_path_returns_match", func(t *testing.T) {
		sharedHash := "sha256:fast-path-within"
		const total = 100
		// Build oldest→newest so score(i)=base+i. ZRevRange returns
		// newest first (index 0 = i=99). Place APPROVED such that its
		// reverse-index < 64 (well within the maxApprovalsByActionHashLookup cap).
		const approvedReverseIndex = 30 // approved is the 31st-newest
		approvedIdx := total - 1 - approvedReverseIndex
		var approvedRef string
		for i := 0; i < total; i++ {
			now = base.Add(time.Duration(i) * time.Second)
			sessID := fmt.Sprintf("sess-w-%03d", i)
			execID := fmt.Sprintf("exec-w-%03d", i)
			eventID := fmt.Sprintf("event-w-%03d", i)
			createApprovalParents(t, ctx, store, tenant, sessID, execID, eventID, now)
			req := validApprovalRequest(tenant, sessID, execID, eventID, now)
			req.ActionHash = sharedHash
			approval, err := store.EnqueueApproval(ctx, req)
			if err != nil {
				t.Fatalf("EnqueueApproval %d: %v", i, err)
			}
			if i == approvedIdx {
				approvedRef = approval.ApprovalRef
				if _, err := store.ApproveApproval(ctx, ApprovalResolution{
					TenantID:    tenant,
					ApprovalRef: approval.ApprovalRef,
					ResolverID:  "reviewer",
					ResolvedBy:  "reviewer@example.invalid",
					Reason:      "approve for fast-path test",
					ResolvedAt:  now.Add(time.Millisecond),
				}); err != nil {
					t.Fatalf("ApproveApproval: %v", err)
				}
			}
		}
		got, ok, err := store.LookupByActionHash(ctx, tenant, sharedHash)
		if err != nil || !ok || got == nil {
			t.Fatalf("LookupByActionHash within-fast-path = (%#v,%v,%v); want match", got, ok, err)
		}
		if got.ApprovalRef != approvedRef {
			t.Fatalf("LookupByActionHash ref = %q; want %q", got.ApprovalRef, approvedRef)
		}
	})

	t.Run("approved_outside_fast_path_returns_miss", func(t *testing.T) {
		sharedHash := "sha256:fast-path-outside"
		const total = 100
		// Place APPROVED at reverse-index 80 (well beyond the 64-cap).
		const approvedReverseIndex = 80
		approvedIdx := total - 1 - approvedReverseIndex
		for i := 0; i < total; i++ {
			now = base.Add(time.Duration(i) * time.Second)
			sessID := fmt.Sprintf("sess-x-%03d", i)
			execID := fmt.Sprintf("exec-x-%03d", i)
			eventID := fmt.Sprintf("event-x-%03d", i)
			createApprovalParents(t, ctx, store, tenant, sessID, execID, eventID, now)
			req := validApprovalRequest(tenant, sessID, execID, eventID, now)
			req.ActionHash = sharedHash
			approval, err := store.EnqueueApproval(ctx, req)
			if err != nil {
				t.Fatalf("EnqueueApproval %d: %v", i, err)
			}
			if i == approvedIdx {
				if _, err := store.ApproveApproval(ctx, ApprovalResolution{
					TenantID:    tenant,
					ApprovalRef: approval.ApprovalRef,
					ResolverID:  "reviewer",
					ResolvedBy:  "reviewer@example.invalid",
					Reason:      "approve outside fast path",
					ResolvedAt:  now.Add(time.Millisecond),
				}); err != nil {
					t.Fatalf("ApproveApproval: %v", err)
				}
			}
		}
		got, ok, err := store.LookupByActionHash(ctx, tenant, sharedHash)
		if err != nil {
			t.Fatalf("LookupByActionHash beyond-fast-path err: %v", err)
		}
		if ok || got != nil {
			t.Fatalf("LookupByActionHash beyond-fast-path = (%#v,%v); want miss (fail-closed semantics)", got, ok)
		}
	})
}

// prewarmRedisClientPool serially pings the redis client enough times to
// fully populate the connection pool before the test fans out to many
// concurrent goroutines. It exists to defuse the go-redis v9 lazy-init
// race in (*baseClient).initConn vs (*Options).clone — when many
// goroutines hit the pool for the first time simultaneously, -race trips
// on the shared Options struct because every connection's first use clones
// the pool-level Options. Sequential warm-up forces every per-connection
// init to run on the test goroutine, so the parallel fan-out below pulls
// already-initialized connections out of the pool with no further Options
// reads/writes.
//
// Default go-redis PoolSize in v9 is 10 connections per CPU; we ping 32
// times to cover modern multi-core CI runners where the effective pool is
// larger. The Pings are sequential so initConn runs on this goroutine only.
func prewarmRedisClientPool(t *testing.T, ctx context.Context, client *redis.Client) {
	t.Helper()
	for i := 0; i < 32; i++ {
		if err := client.Ping(ctx).Err(); err != nil {
			t.Fatalf("redis prewarm ping %d: %v", i, err)
		}
	}
}
