package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// newTestMCPStore wires a MCPApprovalStore against an in-process
// miniredis instance so tests don't depend on a live Redis.
func newTestMCPStore(t *testing.T) *MCPApprovalStore {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = client.Close() })
	return NewMCPApprovalStore(client)
}

func testReq() *MCPApprovalRequest {
	return &MCPApprovalRequest{
		Tenant:    "default",
		AgentID:   "agent-1",
		ToolName:  "files.delete",
		ArgsHash:  "abc123",
		Requester: "agent-1",
		TTL:       2 * time.Minute,
	}
}

// TestEnqueueMCPApprovalValidatesRequiredFields ensures a partial
// request is rejected before any Redis write — a half-populated record
// in Redis would leak through to the dashboard and confuse the
// approver.
func TestEnqueueMCPApprovalValidatesRequiredFields(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		mut  func(*MCPApprovalRequest)
		want string
	}{
		{"nil", func(r *MCPApprovalRequest) {}, ""},
		{"missing tenant", func(r *MCPApprovalRequest) { r.Tenant = "" }, "tenant"},
		{"missing agent", func(r *MCPApprovalRequest) { r.AgentID = "" }, "agent_id"},
		{"missing tool", func(r *MCPApprovalRequest) { r.ToolName = "" }, "tool_name"},
		{"missing hash", func(r *MCPApprovalRequest) { r.ArgsHash = "" }, "args_hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *MCPApprovalRequest
			if tc.name == "nil" {
				req = nil
			} else {
				req = testReq()
				tc.mut(req)
			}
			rec, err := s.EnqueueMCPApproval(ctx, req)
			if err == nil {
				t.Fatalf("expected error, got record %+v", rec)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q did not mention %q", err, tc.want)
			}
		})
	}
}

// TestEnqueueMCPApprovalPersistsAndIndexes asserts the happy path —
// the record lands in Redis in PENDING state, carries the expected
// fields, and appears in the per-tuple lookup index.
func TestEnqueueMCPApprovalPersistsAndIndexes(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	rec, err := s.EnqueueMCPApproval(ctx, testReq())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if rec.Status != model.ApprovalStatusPending {
		t.Errorf("status = %q, want pending", rec.Status)
	}
	if len(rec.ID) != 32 {
		t.Errorf("id length = %d, want 32 hex chars", len(rec.ID))
	}
	if rec.ExpiresAt <= rec.CreatedAt {
		t.Errorf("expires_at must be after created_at; got %d <= %d", rec.ExpiresAt, rec.CreatedAt)
	}

	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ToolName != "files.delete" || got.ArgsHash != "abc123" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}

	pre, err := s.FindPreApproved(ctx, "default", "agent-1", "files.delete", "abc123")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if pre != nil {
		t.Errorf("pending approval should NOT pre-approve; got %+v", pre)
	}
}

// TestResolveApprovesAndFindPreApprovedPicksIt verifies the approve
// path and that the index correctly surfaces a pre-approved record.
func TestResolveApprovesAndFindPreApprovedPicksIt(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	rec, err := s.EnqueueMCPApproval(ctx, testReq())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	approved, err := s.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin-1", "looks safe")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if approved.Status != model.ApprovalStatusApproved {
		t.Errorf("status = %q, want approved", approved.Status)
	}
	if approved.ResolvedBy != "admin-1" {
		t.Errorf("resolved_by = %q", approved.ResolvedBy)
	}

	pre, err := s.FindPreApproved(ctx, "default", "agent-1", "files.delete", "abc123")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if pre == nil {
		t.Fatal("expected pre-approval to resolve")
	}
	if pre.ID != rec.ID {
		t.Errorf("pre-approval id = %q, want %q", pre.ID, rec.ID)
	}
}

// TestConsumeBlocksSecondPreApproval is the consume-once contract from
// step 5 — an approved call used once must not serve a second identical
// call. The second call should re-enqueue a fresh approval.
func TestConsumeBlocksSecondPreApproval(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	rec, _ := s.EnqueueMCPApproval(ctx, testReq())
	_, _ = s.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin-1", "")
	if err := s.MarkConsumed(ctx, rec.ID); err != nil {
		t.Fatalf("consume: %v", err)
	}
	pre, err := s.FindPreApproved(ctx, "default", "agent-1", "files.delete", "abc123")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if pre != nil {
		t.Errorf("consumed approval should not satisfy pre-approval lookup; got %+v", pre)
	}
}

// TestSweepExpiredTransitionsTimedOut covers the reaper integration:
// a PENDING record past its ExpiresAt must be flipped to EXPIRED the
// next sweep.
func TestSweepExpiredTransitionsTimedOut(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	req := testReq()
	req.TTL = 10 * time.Millisecond
	rec, err := s.EnqueueMCPApproval(ctx, req)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Bump wall-clock past expiry and sweep.
	future := time.UnixMicro(rec.ExpiresAt + 1_000_000)
	n, err := s.SweepExpired(ctx, future)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("sweep transitioned %d records, want 1", n)
	}
	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.ApprovalStatusExpired {
		t.Errorf("post-sweep status = %q, want expired", got.Status)
	}
	if got.Decision != model.ApprovalDecisionExpire {
		t.Errorf("decision = %q, want expire", got.Decision)
	}
}

// TestResolveRefusesNonPendingRecords prevents double-resolution races —
// once a record is APPROVED or REJECTED, further Resolve calls must
// return an error rather than silently overwrite.
func TestResolveRefusesNonPendingRecords(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	ctx := context.Background()

	rec, _ := s.EnqueueMCPApproval(ctx, testReq())
	_, err := s.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin-1", "")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	_, err = s.Resolve(ctx, rec.ID, model.ApprovalDecisionReject, "admin-2", "second thought")
	if err == nil {
		t.Fatal("expected error on second resolve, got nil")
	}
}

// TestGetReturnsRedisNilForMissingID keeps the "no such approval"
// contract — callers depend on errors.Is(err, redis.Nil) to
// distinguish missing from wire errors.
func TestGetReturnsRedisNilForMissingID(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	_, err := s.Get(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, redis.Nil) {
		t.Errorf("want redis.Nil, got %v", err)
	}
}

// TestMCPAuditHookCapturesLifecycle asserts that every canonical
// lifecycle event (enqueue → approve → consume; and separately expire
// and reject) emits a SIEMEvent with the expected Extra fields. This
// is the step-9 contract — SIEM correlation rules depend on the
// outcome/tool_name/args_hash triple appearing in every event.
func TestMCPAuditHookCapturesLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	var captured []audit.SIEMEvent
	s = s.WithAuditHook(func(ev audit.SIEMEvent) { captured = append(captured, ev) })
	ctx := context.Background()

	rec, err := s.EnqueueMCPApproval(ctx, testReq())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := s.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin-1", "reviewed"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := s.MarkConsumed(ctx, rec.ID); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if len(captured) != 3 {
		t.Fatalf("expected 3 audit events (enqueued, approved, consumed), got %d", len(captured))
	}
	outcomes := []string{
		captured[0].Extra["outcome"],
		captured[1].Extra["outcome"],
		captured[2].Extra["outcome"],
	}
	want := []string{"enqueued", "approved", "consumed"}
	for i, w := range want {
		if outcomes[i] != w {
			t.Errorf("event[%d].outcome = %q, want %q", i, outcomes[i], w)
		}
	}
	// Every event must carry EventType, tool_name, args_hash, approval_id, requester.
	for i, ev := range captured {
		if ev.EventType != audit.EventMCPToolApproval {
			t.Errorf("event[%d].EventType = %q, want %q", i, ev.EventType, audit.EventMCPToolApproval)
		}
		for _, key := range []string{"tool_name", "args_hash", "approval_id", "requester", "outcome"} {
			if _, ok := ev.Extra[key]; !ok {
				t.Errorf("event[%d].Extra missing %q: %v", i, key, ev.Extra)
			}
		}
		if ev.TenantID != "default" {
			t.Errorf("event[%d].TenantID = %q, want default", i, ev.TenantID)
		}
		if ev.AgentID != "agent-1" {
			t.Errorf("event[%d].AgentID = %q, want agent-1", i, ev.AgentID)
		}
	}
	// Approve event must carry the resolver identity.
	if captured[1].Identity != "admin-1" || captured[1].Extra["resolver"] != "admin-1" {
		t.Errorf("approve event missing resolver: %+v", captured[1])
	}
}

// TestMCPAuditHookCapturesRejectAndExpire covers the remaining two
// outcomes on separate records so the audit-hook test coverage is
// exhaustive per outcome.
func TestMCPAuditHookCapturesRejectAndExpire(t *testing.T) {
	t.Parallel()
	s := newTestMCPStore(t)
	var captured []audit.SIEMEvent
	s = s.WithAuditHook(func(ev audit.SIEMEvent) { captured = append(captured, ev) })
	ctx := context.Background()

	// Reject path.
	rec, _ := s.EnqueueMCPApproval(ctx, testReq())
	if _, err := s.Resolve(ctx, rec.ID, model.ApprovalDecisionReject, "admin-2", "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	// Expire path — separate record, short TTL.
	req2 := testReq()
	req2.AgentID = "agent-2"
	req2.ArgsHash = "distinct"
	req2.TTL = 10 * time.Millisecond
	rec2, _ := s.EnqueueMCPApproval(ctx, req2)
	future := time.UnixMicro(rec2.ExpiresAt + 1_000_000)
	if _, err := s.SweepExpired(ctx, future); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	outcomes := make(map[string]int)
	for _, ev := range captured {
		outcomes[ev.Extra["outcome"]]++
	}
	if outcomes["enqueued"] != 2 {
		t.Errorf("enqueued events = %d, want 2", outcomes["enqueued"])
	}
	if outcomes["rejected"] != 1 {
		t.Errorf("rejected events = %d, want 1", outcomes["rejected"])
	}
	if outcomes["expired"] != 1 {
		t.Errorf("expired events = %d, want 1", outcomes["expired"])
	}
}
