package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
)

// seedApprovalJob creates a job in APPROVAL state with a submitted_by identity.
func seedApprovalJob(t *testing.T, s *server, submittedBy string) string {
	t.Helper()
	jobID := uuid.NewString()
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
	}
	ctx := context.Background()
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if submittedBy != "" {
		if err := s.jobStore.SetSubmittedBy(ctx, jobID, submittedBy); err != nil {
			t.Fatalf("set submitted_by: %v", err)
		}
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash job: %v", err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-test",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	return jobID
}

func TestSelfApprovalBlocked(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Submitter identity: apikey:abcd1234|principal:alice
	submitterID := "apikey:abcd1234|principal:alice"
	jobID := seedApprovalJob(t, s, submitterID)

	// Attempt approval with the SAME identity → should be 403.
	body := `{"reason":"approving my own job"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	// Auth context that produces the same submitterIdentity hash.
	httpReq = withAuth(httpReq, &AuthContext{
		APIKey:      "\xab\xcd\x12\x34", // produces apikey:abcd1234 after sha256[:4]
		PrincipalID: "alice",
		Role:        "admin",
		Tenant:      "default",
	})

	// Since submitterIdentity hashes the API key, we need to match the stored value.
	// The stored value is "apikey:abcd1234|principal:alice".
	// The computed value from the auth context above will use sha256 of the API key bytes.
	// They won't match exactly since stored value was set manually.
	// Instead, let's test by setting the exact computed identity.
	computedIdentity := submitterIdentity(httpReq)
	if computedIdentity == "" {
		t.Fatal("expected non-empty computed identity")
	}

	// Re-seed with the actual computed identity.
	if err := s.jobStore.SetSubmittedBy(context.Background(), jobID, computedIdentity); err != nil {
		t.Fatalf("re-set submitted_by: %v", err)
	}

	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for self-approval, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if code, ok := resp["code"].(string); !ok || code != "self_approval_denied" {
		t.Fatalf("expected code self_approval_denied, got %v", resp["code"])
	}
}

func TestSameAPIKeyDifferentPrincipalBlocked(t *testing.T) {
	// Same API key, different principal → should still be blocked.
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Both submitter and approver use the same API key "shared-team-key".
	sharedKey := "shared-team-key"
	jobID := seedApprovalJob(t, s, "")

	// Compute the submitter identity using alice + shared key.
	submitReq := httptest.NewRequest(http.MethodPost, "/test", nil)
	submitReq = withAuth(submitReq, &AuthContext{
		APIKey:      sharedKey,
		PrincipalID: "alice",
		Role:        "admin",
		Tenant:      "default",
	})
	submittedBy := submitterIdentity(submitReq)
	if err := s.jobStore.SetSubmittedBy(context.Background(), jobID, submittedBy); err != nil {
		t.Fatalf("set submitted_by: %v", err)
	}

	// Approver: same API key, different principal "bob".
	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(`{"reason":"different principal"}`))
	approveReq.Header.Set("X-Tenant-ID", "default")
	approveReq.SetPathValue("job_id", jobID)
	approveReq = withAuth(approveReq, &AuthContext{
		APIKey:      sharedKey,
		PrincipalID: "bob",
		Role:        "admin",
		Tenant:      "default",
	})

	// Verify the identities differ (different principals) but share API key.
	approverID := submitterIdentity(approveReq)
	if submittedBy == approverID {
		t.Fatal("identities should differ when principals differ")
	}
	if !identitiesOverlap(submittedBy, approverID) {
		t.Fatal("expected overlap — same API key should be detected")
	}

	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, approveReq)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for same-key/different-principal, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCrossUserApprovalAllowed(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Submitter is alice.
	submitterID := "apikey:aaaa1111|principal:alice"
	jobID := seedApprovalJob(t, s, submitterID)

	// Approver is bob with a different API key → should be allowed.
	body := `{"reason":"looks good"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &AuthContext{
		APIKey:      "different-key",
		PrincipalID: "bob",
		Role:        "admin",
		Tenant:      "default",
	})
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for cross-user approval, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSelfRejectionBlocked(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Seed job, then set submitted_by to the computed identity.
	jobID := seedApprovalJob(t, s, "")
	rejectReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(`{"reason":"rejecting my own job"}`))
	rejectReq.Header.Set("X-Tenant-ID", "default")
	rejectReq.SetPathValue("job_id", jobID)
	rejectReq = withAuth(rejectReq, &AuthContext{
		APIKey:      "self-reject-key",
		PrincipalID: "alice",
		Role:        "admin",
		Tenant:      "default",
	})

	// Set submitted_by to match the rejecter identity.
	computedID := submitterIdentity(rejectReq)
	if err := s.jobStore.SetSubmittedBy(context.Background(), jobID, computedID); err != nil {
		t.Fatalf("set submitted_by: %v", err)
	}

	rr := httptest.NewRecorder()
	s.handleRejectJob(rr, rejectReq)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for self-rejection, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if code, ok := resp["code"].(string); !ok || code != "self_approval_denied" {
		t.Fatalf("expected code self_approval_denied, got %v", resp["code"])
	}
}

func TestApprovalBackwardCompatibility(t *testing.T) {
	// Jobs submitted before this change have no submitted_by field.
	// Approval should still work (graceful degradation).
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Seed job WITHOUT submitted_by.
	jobID := seedApprovalJob(t, s, "")

	body := `{"reason":"legacy job"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &AuthContext{
		APIKey:      "any-key",
		PrincipalID: "admin",
		Role:        "admin",
		Tenant:      "default",
	})
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, httpReq)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for legacy job without submitted_by, got %d: %s", rr.Code, rr.Body.String())
	}
}
