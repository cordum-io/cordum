// EDGE-143.6 — tests for the operator-defined exception API (§10.3) and
// emit-time suppression contract. Tests written before implementation
// per TDD; they intentionally fail until exception.go +
// finding_store_redis.go exception extension land.
package shadow

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2/server"
)

func minimalCreateExceptionReq(tenant string) CreateExceptionRequest {
	return CreateExceptionRequest{
		TenantID:        tenant,
		CreatedBy:       "alice@example.com",
		ExpiresAt:       time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		Reason:          "approved by SRE for known kube-system DaemonSet pattern",
		ScopeSourceType: "kubernetes",
		ScopeSourceID:   "k8s-detector-1",
		ScopeRiskLevel:  FindingRiskHigh,
		ScopeSignalSet:  []string{"k8s_unmanaged_process"},
		StepUpFactor:    StepUpFactorMFARecent,
	}
}

func TestException_Create_BasicFields(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	got, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	if got.ExceptionID == "" || !strings.HasPrefix(got.ExceptionID, exceptionIDPrefix) {
		t.Errorf("ExceptionID %q lacks prefix %q", got.ExceptionID, exceptionIDPrefix)
	}
	if got.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q", got.TenantID)
	}
	if got.CreatedBy != "alice@example.com" {
		t.Errorf("CreatedBy = %q", got.CreatedBy)
	}
	if got.Status != ExceptionStatusActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.ScopeRiskLevel != FindingRiskHigh {
		t.Errorf("ScopeRiskLevel = %q", got.ScopeRiskLevel)
	}
	if got.StepUpFactor != StepUpFactorMFARecent {
		t.Errorf("StepUpFactor = %q, want mfa_recent", got.StepUpFactor)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt unset")
	}
}

func TestException_Create_RejectsExpiresAtBeyond90Days(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateExceptionReq("tenant-a")
	// 100 days from the store's pinned clock (2026-05-17T13:00Z) is past
	// the §10.3 90-day max.
	req.ExpiresAt = time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	_, err := s.CreateException(ctx, req)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateException expires_at > 90d: err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "expires_at") {
		t.Errorf("err = %q, want mention of expires_at", err)
	}
}

func TestException_Create_RejectsReasonOver512Bytes(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateExceptionReq("tenant-a")
	req.Reason = strings.Repeat("x", 513)
	_, err := s.CreateException(ctx, req)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateException reason >512: err = %v, want ErrValidation", err)
	}
}

func TestException_Create_RejectsTooManySignals(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateExceptionReq("tenant-a")
	req.ScopeSignalSet = make([]string, 17)
	for i := range req.ScopeSignalSet {
		req.ScopeSignalSet[i] = "sig_" + string(rune('a'+i))
	}
	_, err := s.CreateException(ctx, req)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateException signal_set >16: err = %v, want ErrValidation", err)
	}
}

func TestException_Get_TenantIsolation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	// Same id, foreign tenant → ErrNotFound (never tenant-mismatch).
	_, err = s.GetException(ctx, "tenant-b", created.ExceptionID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetException err = %v, want ErrNotFound", err)
	}
	// Same tenant succeeds.
	got, err := s.GetException(ctx, "tenant-a", created.ExceptionID)
	if err != nil {
		t.Fatalf("GetException same tenant: %v", err)
	}
	if got.ExceptionID != created.ExceptionID {
		t.Errorf("ExceptionID round-trip = %q, want %q", got.ExceptionID, created.ExceptionID)
	}
}

func TestException_List_FilteredByScope(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a := minimalCreateExceptionReq("tenant-a")
	a.ScopeSourceType = "kubernetes"
	a.ScopeRiskLevel = FindingRiskHigh
	if _, err := s.CreateException(ctx, a); err != nil {
		t.Fatalf("create A: %v", err)
	}
	b := minimalCreateExceptionReq("tenant-a")
	b.ScopeSourceType = "ci"
	b.ScopeRiskLevel = FindingRiskMedium
	b.StepUpFactor = StepUpFactorNone
	if _, err := s.CreateException(ctx, b); err != nil {
		t.Fatalf("create B: %v", err)
	}
	// Wrong-tenant insert is invisible to tenant-a listing.
	c := minimalCreateExceptionReq("tenant-b")
	if _, err := s.CreateException(ctx, c); err != nil {
		t.Fatalf("create C: %v", err)
	}

	got, err := s.ListExceptions(ctx, ListExceptionsQuery{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListExceptions: %v", err)
	}
	if len(got.Exceptions) != 2 {
		t.Fatalf("ListExceptions tenant-a returned %d, want 2", len(got.Exceptions))
	}
	for _, ex := range got.Exceptions {
		if ex.TenantID != "tenant-a" {
			t.Errorf("cross-tenant leak: %+v", ex)
		}
	}

	filtered, err := s.ListExceptions(ctx, ListExceptionsQuery{TenantID: "tenant-a", ScopeSourceType: "kubernetes"})
	if err != nil {
		t.Fatalf("ListExceptions filtered: %v", err)
	}
	if len(filtered.Exceptions) != 1 || filtered.Exceptions[0].ScopeSourceType != "kubernetes" {
		t.Fatalf("kubernetes filter returned %+v", filtered.Exceptions)
	}
}

func TestException_Revoke_TerminalState(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	revoked, err := s.RevokeException(ctx, "tenant-a", created.ExceptionID, RevokeExceptionRequest{RevokedBy: "bob@example.com", Reason: "scope-too-broad"})
	if err != nil {
		t.Fatalf("RevokeException: %v", err)
	}
	if revoked.Status != ExceptionStatusRevoked {
		t.Errorf("Status = %q, want revoked", revoked.Status)
	}
	if revoked.RevokedBy != "bob@example.com" {
		t.Errorf("RevokedBy = %q", revoked.RevokedBy)
	}
	// Re-revoke → terminal conflict (or idempotent same-state).
	_, err = s.RevokeException(ctx, "tenant-a", created.ExceptionID, RevokeExceptionRequest{RevokedBy: "bob@example.com"})
	if err != nil && !errors.Is(err, ErrTerminalConflict) {
		t.Errorf("second RevokeException err = %v, want nil or ErrTerminalConflict", err)
	}
}

func TestException_AppliedAtEmit_SuppressMatching(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	exc, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	// Matching finding: same tenant + scope predicate (source_type +
	// risk + signal). Emit-time hook must stamp exception_id +
	// false_positive_reason and flip status to managed_skip.
	req := fullCreateReq("tenant-a")
	req.Risk = FindingRiskHigh
	req.SourceType = "kubernetes"
	req.SourceID = "k8s-detector-1"
	req.SignalSet = []string{"k8s_unmanaged_process"}
	finding, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding (matching): %v", err)
	}
	if finding.ExceptionID != exc.ExceptionID {
		t.Errorf("suppressed finding ExceptionID = %q, want %q", finding.ExceptionID, exc.ExceptionID)
	}
	if finding.FalsePositiveReason != FalsePositiveReasonOperatorException {
		t.Errorf("FalsePositiveReason = %q, want operator_exception", finding.FalsePositiveReason)
	}
	if finding.Status != FindingStatusManagedSkip {
		t.Errorf("Status = %q, want managed_skip", finding.Status)
	}

	// Default list query excludes managed_skip; explicit
	// include_managed_skip returns the suppressed finding.
	page, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListFindings default: %v", err)
	}
	for _, f := range page.Findings {
		if f.FindingID == finding.FindingID {
			t.Errorf("default ListFindings included managed_skip finding %s", f.FindingID)
		}
	}
	all, err := s.ListFindings(ctx, ListFindingsQuery{TenantID: "tenant-a", IncludeManagedSkip: true})
	if err != nil {
		t.Fatalf("ListFindings include: %v", err)
	}
	var found bool
	for _, f := range all.Findings {
		if f.FindingID == finding.FindingID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("include_managed_skip ListFindings missing suppressed finding %s", finding.FindingID)
	}
}

func TestCreateFinding_ExceptionRevokeBetweenMatchAndPersist(t *testing.T) {
	s, mr := newExceptionCapTestStore(t, 4)
	ctx := context.Background()
	created, err := s.CreateException(ctx, minimalCreateExceptionReq("tenant-a"))
	if err != nil {
		t.Fatalf("CreateException: %v", err)
	}

	const findingID = "edge_shadow_exception-revoke-race-finding"
	var triggered atomic.Bool
	mr.Server().SetPreHook(func(_ *server.Peer, cmd string, args ...string) bool {
		if !strings.EqualFold(cmd, "watch") || !hasArg(args, findingKey(findingID)) {
			return false
		}
		if !triggered.CompareAndSwap(false, true) {
			return false
		}
		done := make(chan error, 1)
		go func() {
			_, err := s.RevokeException(context.Background(), created.TenantID, created.ExceptionID, RevokeExceptionRequest{
				RevokedBy: "bob@example.com",
				Reason:    "revoke before create commit",
			})
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RevokeException during CreateFinding: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("timed out revoking exception during CreateFinding")
		}
		return false
	})
	t.Cleanup(func() { mr.Server().SetPreHook(nil) })

	req := fullCreateReq("tenant-a")
	req.FindingID = findingID
	req.Risk = FindingRiskHigh
	req.SourceType = "kubernetes"
	req.SourceID = "k8s-detector-1"
	req.SignalSet = []string{"k8s_unmanaged_process"}
	finding, err := s.CreateFinding(ctx, req)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if !triggered.Load() {
		t.Fatal("test hook did not revoke between exception match and finding persistence")
	}
	if finding.ExceptionID != "" {
		t.Fatalf("CreateFinding returned stale exception_id %q after concurrent revoke", finding.ExceptionID)
	}
	got, err := s.GetFinding(ctx, "tenant-a", findingID)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}
	if got.ExceptionID != "" || got.Status == FindingStatusManagedSkip {
		t.Fatalf("persisted finding kept stale exception state: %+v", got)
	}
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func TestException_AppliedAtEmit_NoMatch(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateExceptionReq("tenant-a")
	req.ScopeSourceType = "ci"
	req.ScopeRiskLevel = FindingRiskHigh
	req.ScopeSignalSet = []string{"ci_unmanaged_action"}
	if _, err := s.CreateException(ctx, req); err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	// Different source_type → no scope match.
	f := fullCreateReq("tenant-a")
	f.SourceType = "kubernetes"
	f.SignalSet = []string{"k8s_unmanaged_process"}
	finding, err := s.CreateFinding(ctx, f)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if finding.ExceptionID != "" {
		t.Errorf("non-matching finding got ExceptionID = %q, want empty", finding.ExceptionID)
	}
	if finding.FalsePositiveReason != "" {
		t.Errorf("non-matching finding got FalsePositiveReason = %q, want empty", finding.FalsePositiveReason)
	}
	if finding.Status != FindingStatusDetected {
		t.Errorf("non-matching finding Status = %q, want detected", finding.Status)
	}
}

func TestException_ExpiredExceptionDoesNotSuppress(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req := minimalCreateExceptionReq("tenant-a")
	// One hour in the future from the pinned store clock — past after
	// CreateFinding's clock counter advances; we instead directly
	// expire by setting ExpiresAt just past the store clock.
	req.ExpiresAt = time.Date(2026, 5, 17, 13, 0, 1, 0, time.UTC)
	if _, err := s.CreateException(ctx, req); err != nil {
		t.Fatalf("CreateException: %v", err)
	}
	// Advance clock past expiry by burning counter ticks, then create.
	for i := 0; i < 10; i++ {
		_ = s.now()
	}
	f := fullCreateReq("tenant-a")
	f.Risk = FindingRiskHigh
	f.SourceType = "kubernetes"
	f.SourceID = "k8s-detector-1"
	f.SignalSet = []string{"k8s_unmanaged_process"}
	finding, err := s.CreateFinding(ctx, f)
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if finding.ExceptionID != "" {
		t.Errorf("expired-exception scope-match still suppressed: ExceptionID = %q", finding.ExceptionID)
	}
}
