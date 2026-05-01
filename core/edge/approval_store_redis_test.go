package edge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

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
		PolicySnapshot: req.PolicySnapshot,
		ConsumedAt:     consumedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("second ClaimApproval: %v", err)
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
				PolicySnapshot: req.PolicySnapshot,
				ConsumedAt:     base.Add(2*time.Minute + time.Duration(i)*time.Microsecond),
			})
			if err != nil {
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
