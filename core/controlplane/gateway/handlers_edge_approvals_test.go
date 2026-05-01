package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestGatewayEdgeApprovalRejectsSelfApproval(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "self")

	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteTestAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"approve myself"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("self approve status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	decodeEdgeRouteJSON(t, rr, &body)
	if body["code"] != "self_approval_denied" {
		t.Fatalf("self approve code = %#v, want self_approval_denied body=%s", body["code"], rr.Body.String())
	}
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, approval.ApprovalRef)
	if err != nil || !ok {
		t.Fatalf("GetApproval after self-denied = (%#v,%v,%v)", stored, ok, err)
	}
	if stored.Status != edgecore.ApprovalStatusPending {
		t.Fatalf("self-denied status = %q, want pending", stored.Status)
	}
}

func TestGatewayEdgeApprovalStoresResolverOnApproval(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "approve")

	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"reviewed and approved"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var approved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rr, &approved)
	if approved.Status != edgecore.ApprovalStatusApproved || approved.Decision != edgecore.ApprovalDecisionApprove {
		t.Fatalf("approved status/decision = %q/%q", approved.Status, approved.Decision)
	}
	if approved.ResolverID != "principal-reviewer" || !strings.Contains(approved.ResolvedBy, "principal:principal-reviewer") {
		t.Fatalf("resolver fields = id:%q by:%q", approved.ResolverID, approved.ResolvedBy)
	}
	if approved.ResolutionReason != "reviewed and approved" || approved.ResolvedAt == nil {
		t.Fatalf("resolution reason/at = %q/%v", approved.ResolutionReason, approved.ResolvedAt)
	}
}

func TestGatewayEdgeApprovalListDetailRejectAndTenantIsolation(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approvalA := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "list-a")
	approvalB := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "list-b")
	approvalOther := seedGatewayEdgeApproval(t, s, edgeRouteOtherTenant, "principal-edge-b", "list-other")

	list := edgeApprovalRouteGETAs(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, "/api/v1/edge/approvals?status=pending&limit=10")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 body=%s", list.Code, list.Body.String())
	}
	var page edgeApprovalPageResponse
	decodeEdgeRouteJSON(t, list, &page)
	gotRefs := map[string]bool{}
	for _, item := range page.Items {
		if item.TenantID != edgeRouteTenant {
			t.Fatalf("list leaked tenant %q item %#v", item.TenantID, item)
		}
		gotRefs[item.ApprovalRef] = true
	}
	if !gotRefs[approvalA.ApprovalRef] || !gotRefs[approvalB.ApprovalRef] || gotRefs[approvalOther.ApprovalRef] {
		t.Fatalf("list refs = %#v, want tenant-a approvals only", gotRefs)
	}

	detail := edgeApprovalRouteGETAs(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, "/api/v1/edge/approvals/"+approvalA.ApprovalRef)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200 body=%s", detail.Code, detail.Body.String())
	}
	var detailApproval edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, detail, &detailApproval)
	if detailApproval.ApprovalRef != approvalA.ApprovalRef || detailApproval.ActionHash != approvalA.ActionHash || detailApproval.PolicySnapshot != "policy-v1" {
		t.Fatalf("detail approval = ref:%q action:%q snapshot:%q", detailApproval.ApprovalRef, detailApproval.ActionHash, detailApproval.PolicySnapshot)
	}

	cross := edgeApprovalRouteGETAs(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant, "/api/v1/edge/approvals/"+approvalA.ApprovalRef)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant detail status = %d, want 404 body=%s", cross.Code, cross.Body.String())
	}

	reject := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approvalB.ApprovalRef+"/reject", `{"reason":"not safe"}`)
	if reject.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200 body=%s", reject.Code, reject.Body.String())
	}
	var rejected edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, reject, &rejected)
	if rejected.Status != edgecore.ApprovalStatusRejected || rejected.Decision != edgecore.ApprovalDecisionReject || rejected.ResolutionReason != "not safe" {
		t.Fatalf("reject body status/decision/reason = %q/%q/%q", rejected.Status, rejected.Decision, rejected.ResolutionReason)
	}
}

func TestGatewayEdgeApprovalErrors(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "errors")

	viewerApprove := edgeApprovalRoutePOSTAs(t, handler, edgeRouteViewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"viewer"}`)
	if viewerApprove.Code != http.StatusForbidden {
		t.Fatalf("viewer approve status = %d, want 403 body=%s", viewerApprove.Code, viewerApprove.Body.String())
	}

	malformed := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{`)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed approve status = %d, want 400 body=%s", malformed.Code, malformed.Body.String())
	}

	notFound := edgeApprovalRouteGETAs(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, "/api/v1/edge/approvals/edge_appr_missing")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("missing detail status = %d, want 404 body=%s", notFound.Code, notFound.Body.String())
	}

	if _, err := s.edgeStore.EndSession(context.Background(), edgeRouteTenant, approval.SessionID, approval.CreatedAt.Add(time.Minute), edgecore.SessionStatusEnded); err != nil {
		t.Fatalf("EndSession for stale approval: %v", err)
	}
	stale := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"late"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale approve status = %d, want 409 body=%s", stale.Code, stale.Body.String())
	}

	expiring := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "expired")
	if expiring.ExpiresAt == nil {
		t.Fatalf("expiring approval has nil expires_at")
	}
	if n, err := s.edgeStore.ExpireApprovals(context.Background(), edgeRouteTenant, expiring.ExpiresAt.Add(time.Second)); err != nil || n == 0 {
		t.Fatalf("ExpireApprovals = %d,%v want at least one expired", n, err)
	}
	expired := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+expiring.ApprovalRef+"/approve", `{"reason":"too late"}`)
	if expired.Code != http.StatusConflict {
		t.Fatalf("expired approve status = %d, want 409 body=%s", expired.Code, expired.Body.String())
	}
}

func seedGatewayEdgeApproval(t *testing.T, s *server, tenantID, requester, suffix string) edgecore.EdgeApproval {
	t.Helper()
	ctx := context.Background()
	started := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Microsecond)
	slug := strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name() + "-" + suffix))
	sessionID := "sess-" + slug
	executionID := "exec-" + slug
	eventID := "event-" + slug
	session := edgecore.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       requester,
		PrincipalType:     edgecore.PrincipalTypeHuman,
		AgentProduct:      "Claude Code",
		AgentVersion:      "2.1.123",
		Mode:              edgecore.SessionModeLocalDev,
		Repo:              "cordum",
		PolicySnapshot:    "policy-v1",
		EnforcementLayers: edgecore.EnforcementLayers{"hook": true},
		PolicyMode:        edgecore.PolicyModeEnforce,
		Status:            edgecore.SessionStatusRunning,
		RiskSummary:       edgecore.RiskSummary{ApprovalCount: 1, MaxRisk: edgecore.RiskLevelHigh},
		StartedAt:         started,
		Labels:            edgecore.Labels{"test": suffix},
	}
	if err := s.edgeStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution := edgecore.AgentExecution{
		ExecutionID:    executionID,
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        edgecore.AdapterClaudeCodeHook,
		Mode:           edgecore.ExecutionModeLocalDev,
		PolicySnapshot: "policy-v1",
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      started.Add(time.Second),
		Labels:         edgecore.Labels{"test": suffix},
	}
	if err := s.edgeStore.CreateExecution(ctx, execution); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	event := edgecore.AgentActionEvent{
		EventID:        eventID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		TenantID:       tenantID,
		PrincipalID:    requester,
		Timestamp:      started.Add(2 * time.Second),
		Layer:          edgecore.LayerHook,
		Kind:           edgecore.EventKindApprovalRequested,
		AgentProduct:   "Claude Code",
		ToolName:       "Bash",
		ActionName:     "bash",
		Capability:     "filesystem.write",
		InputRedacted:  map[string]any{"summary": "redacted"},
		InputHash:      "sha256:" + eventID,
		Decision:       edgecore.DecisionRequireApproval,
		DecisionReason: "approval required",
		RuleID:         "claude-code.require-approval-for-edits",
		PolicySnapshot: "policy-v1",
		Status:         edgecore.ActionStatusBlocked,
	}
	if _, err := s.edgeStore.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	approval, err := s.edgeStore.EnqueueApproval(ctx, edgecore.EdgeApprovalRequest{
		TenantID:       tenantID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		EventID:        eventID,
		PrincipalID:    requester,
		Requester:      requester,
		Reason:         "gateway approval test",
		RuleID:         "claude-code.require-approval-for-edits",
		PolicySnapshot: "policy-v1",
		ActionHash:     "actionhash-" + eventID,
		InputHash:      "sha256:" + eventID,
		ExpiresAt:      expires,
		Labels:         edgecore.Labels{"test": suffix},
		Metadata:       edgecore.Metadata{"source": "gateway-test"},
	})
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	return *approval
}

func edgeApprovalRoutePOSTAs(t *testing.T, handler http.Handler, apiKey, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return edgeApprovalRoutePOSTAsTenant(t, handler, apiKey, edgeRouteTenant, path, body)
}

func edgeApprovalRoutePOSTAsTenant(t *testing.T, handler http.Handler, apiKey, tenantID, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuthFor(req, apiKey)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func edgeApprovalRouteGETAs(t *testing.T, handler http.Handler, apiKey, tenantID, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	addEdgeRouteAuthFor(req, apiKey)
	req.Header.Set("X-Tenant-ID", tenantID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}
