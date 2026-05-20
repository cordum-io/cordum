package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
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

	jobID := seedApprovalJob(t, s, "")

	// Attempt approval with the SAME identity → should be 403.
	httpReq := approvalDecisionRequest(t, jobID, "approve", `{"reason":"approving my own job"}`, "sk-test-self-approval", "alice")
	computedIdentity := submitterIdentity(httpReq)
	if computedIdentity == "" {
		t.Fatal("expected non-empty computed identity")
	}
	if err := s.jobStore.SetSubmittedBy(context.Background(), jobID, computedIdentity); err != nil {
		t.Fatalf("set submitted_by: %v", err)
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

	// Both submitter and approver use the same synthetic test API key.
	sharedKey := "sk-test-shared-approval"
	jobID := seedApprovalJob(t, s, "")

	// Compute the submitter identity using alice + shared key.
	submitReq := httptest.NewRequest(http.MethodPost, "/test", nil)
	submitReq = withAuth(submitReq, &auth.AuthContext{
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
	approveReq = withAuth(approveReq, &auth.AuthContext{
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

func TestSubmitterIdentity_NoCollisionAt100kKeys(t *testing.T) {
	seen := make(map[string]string, 100_000)
	for i := 0; i < 100_000; i++ {
		key := "sk-test-collision-" + strconv.Itoa(i)
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req = withAuth(req, &auth.AuthContext{
			APIKey:      key,
			PrincipalID: "same-principal",
			Tenant:      "default",
		})
		identity := submitterIdentity(req)
		if prior, ok := seen[identity]; ok {
			t.Fatalf("submitterIdentity collision for %q and %q: %s", prior, key, identity)
		}
		seen[identity] = key
	}
}

func TestIdentitiesOverlap_MatchesCompositePrincipal(t *testing.T) {
	requester := submitterIdentityForTest("sk-test-requester", "alice")
	approver := submitterIdentityForTest("sk-test-approver", "alice")
	if requester == approver {
		t.Fatal("fixture should use different API keys so overlap relies on principal")
	}
	if !identitiesOverlap(requester, approver) {
		t.Fatalf("expected composite identities to overlap on principal: %q vs %q", requester, approver)
	}
	if identitiesOverlap(requester, submitterIdentityForTest("sk-test-approver", "bob")) {
		t.Fatal("different API key and different principal should not overlap")
	}
}

const (
	approvalAuditBearerFixture = "Authorization: Bearer cordum_fake_token_approval_audit_0123456789"
	approvalAuditSKFixture     = "sk-test-approval-audit-note-0123456789"
	approvalLabelAPIKeyFixture = "APIKEY=cordum_fake_label_api_key_abc123"
	approvalLabelBearerFixture = "Bearer cordum_fake_label_token_0123456789"
)

func TestApprovalAuditIncludesReasonAndNoteRedacted(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})
	jobID := seedApprovalJob(t, s, submitterIdentityForTest("sk-test-submit-audit", "alice"))

	req := approvalDecisionRequest(t, jobID, "approve",
		`{"reason":"approved with `+approvalAuditBearerFixture+`","note":"note `+approvalAuditSKFixture+`"}`,
		"sk-test-review-audit", "bob")
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}

	entry := latestApprovalAuditEntry(t, s, "approve", jobID)
	if entry.Reason == "" || entry.Extra["note"] == "" {
		t.Fatalf("audit reason/note missing: %+v", entry)
	}
	combined := entry.Reason + " " + entry.Extra["note"]
	if strings.Contains(combined, approvalAuditBearerFixture) || strings.Contains(combined, approvalAuditSKFixture) {
		t.Fatalf("audit leaked raw approval reason/note: %+v", entry)
	}
}

func TestApprovalReasonNoteRedactedBeforePersistence(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})
	jobID := seedApprovalJob(t, s, submitterIdentityForTest("sk-test-submit-labels", "alice"))

	req := approvalDecisionRequest(t, jobID, "approve",
		`{"reason":"`+approvalLabelAPIKeyFixture+`","note":"`+approvalLabelBearerFixture+`"}`,
		"sk-test-review-labels", "bob")
	rr := httptest.NewRecorder()
	s.handleApproveJob(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}

	stored, err := s.jobStore.GetJobRequest(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJobRequest: %v", err)
	}
	labels := stored.GetLabels()
	if strings.Contains(labels["approval_reason"], approvalLabelAPIKeyFixture) || strings.Contains(labels["approval_note"], approvalLabelBearerFixture) {
		t.Fatalf("approval labels leaked raw secrets: %#v", labels)
	}
	if labels["approval_reason"] == "" || labels["approval_note"] == "" {
		t.Fatalf("approval labels missing redacted reason/note: %#v", labels)
	}
}

func submitterIdentityForTest(apiKey, principal string) string {
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req = withAuth(req, &auth.AuthContext{APIKey: apiKey, PrincipalID: principal, Tenant: "default"})
	return submitterIdentity(req)
}

func approvalDecisionRequest(t *testing.T, jobID, action, body, apiKey, principal string) *http.Request {
	t.Helper()
	path := "/api/v1/approvals/" + jobID + "/" + action
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("job_id", jobID)
	return withAuth(req, &auth.AuthContext{
		APIKey:      apiKey,
		PrincipalID: principal,
		Role:        "admin",
		Tenant:      "default",
	})
}

func latestApprovalAuditEntry(t *testing.T, s *server, action, jobID string) policybundles.PolicyAuditEntry {
	t.Helper()
	entries, err := s.loadPolicyAudit(context.Background())
	if err != nil {
		t.Fatalf("loadPolicyAudit: %v", err)
	}
	for _, entry := range entries {
		if entry.Action == action && entry.ResourceID == jobID {
			return entry
		}
	}
	t.Fatalf("missing %s audit entry for job %s in %#v", action, jobID, entries)
	return policybundles.PolicyAuditEntry{}
}

func TestCrossUserApprovalAllowed(t *testing.T) {
	s, _, safety := newTestGateway(t)
	safety.setSnapshots([]string{"snap-test"})

	// Submitter is alice.
	submitterID := submitterIdentityForTest("sk-test-submit-allowed", "alice")
	jobID := seedApprovalJob(t, s, submitterID)

	// Approver is bob with a different API key → should be allowed.
	body := `{"reason":"looks good"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", strings.NewReader(body))
	httpReq.Header.Set("X-Tenant-ID", "default")
	httpReq.SetPathValue("job_id", jobID)
	httpReq = withAuth(httpReq, &auth.AuthContext{
		APIKey:      "sk-test-approver-allowed",
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
	rejectReq = withAuth(rejectReq, &auth.AuthContext{
		APIKey:      "sk-test-self-reject",
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
	httpReq = withAuth(httpReq, &auth.AuthContext{
		APIKey:      "sk-test-legacy",
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
