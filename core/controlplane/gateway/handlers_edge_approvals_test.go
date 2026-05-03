package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

// TestGatewayEdgeApprovalResolveEmitsAuditEvent pins EDGE-014 step-10
// audit instrumentation for the approve/reject handlers. Approve emits
// edge.approval_resolved (info severity); reject emits
// edge.approval_rejected (high severity).
func TestGatewayEdgeApprovalResolveEmitsAuditEvent(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		wantType string
		wantSev  string
	}{
		{"approve_emits_resolved_info", "/approve", audit.EventEdgeApprovalResolved, audit.SeverityInfo},
		{"reject_emits_rejected_high", "/reject", audit.EventEdgeApprovalRejected, audit.SeverityHigh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, handler := newEdgeRouteTestServer(t)
			sink := &testAuditSender{}
			s.auditExporter = sink
			approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "audit-"+c.name)

			before := sink.Len()
			rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey,
				"/api/v1/edge/approvals/"+approval.ApprovalRef+c.path,
				`{"reason":"audit-test reason with Authorization: Bearer secret"}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s status = %d body=%s", c.path, rr.Code, rr.Body.String())
			}
			after := sink.Len()
			if after-before != 1 {
				t.Fatalf("audit events emitted = %d, want 1", after-before)
			}
			ev := sink.Get(after - 1)
			if ev.EventType != c.wantType {
				t.Errorf("EventType = %q, want %q", ev.EventType, c.wantType)
			}
			if ev.Severity != c.wantSev {
				t.Errorf("Severity = %q, want %q", ev.Severity, c.wantSev)
			}
			if ev.TenantID != edgeRouteTenant {
				t.Errorf("TenantID = %q, want %q", ev.TenantID, edgeRouteTenant)
			}
			if ev.Extra["approval_ref"] != approval.ApprovalRef {
				t.Errorf("Extra[approval_ref] = %q, want %q", ev.Extra["approval_ref"], approval.ApprovalRef)
			}
			// Raw resolution Reason (with Bearer secret) must NEVER reach Extra.
			for k, v := range ev.Extra {
				if strings.Contains(v, "Authorization") || strings.Contains(v, "Bearer") {
					t.Errorf("Extra[%q] leaked secret: %q", k, v)
				}
			}
		})
	}
}

func TestGatewayEdgeApprovalRejectsSelfApproval(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)

	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "self-"+action)
			rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteTestAPIKey,
				"/api/v1/edge/approvals/"+approval.ApprovalRef+"/"+action,
				`{"reason":"resolve myself"}`)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("self %s status = %d, want 403 body=%s", action, rr.Code, rr.Body.String())
			}
			var body map[string]any
			decodeEdgeRouteJSON(t, rr, &body)
			if body["code"] != "self_approval_denied" {
				t.Fatalf("self %s code = %#v, want self_approval_denied body=%s", action, body["code"], rr.Body.String())
			}
			stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, approval.ApprovalRef)
			if err != nil || !ok {
				t.Fatalf("GetApproval after self-denied = (%#v,%v,%v)", stored, ok, err)
			}
			if stored.Status != edgecore.ApprovalStatusPending || stored.ResolvedAt != nil || stored.ConsumedAt != nil {
				t.Fatalf("self-denied approval = status:%q resolved:%v consumed:%v, want pending unresolved unconsumed",
					stored.Status, stored.ResolvedAt, stored.ConsumedAt)
			}
		})
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

func TestGatewayEdgeApprovalTerminalMutationsReturnConflict(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "terminal")

	approve := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"approved once"}`)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200 body=%s", approve.Code, approve.Body.String())
	}
	var approved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, approve, &approved)
	if approved.Status != edgecore.ApprovalStatusApproved ||
		approved.Decision != edgecore.ApprovalDecisionApprove ||
		approved.ResolverID != "principal-reviewer" ||
		!strings.Contains(approved.ResolvedBy, "principal:principal-reviewer") ||
		approved.ResolvedAt == nil {
		t.Fatalf("approved record = %#v, want approved/approve reviewer with resolved_at", approved)
	}

	secondApprove := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"approve twice"}`)
	assertEdgeErrorShape(t, secondApprove, http.StatusConflict, edgeErrCodeApprovalConflict)
	secondReject := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/reject", `{"reason":"reject after approve"}`)
	assertEdgeErrorShape(t, secondReject, http.StatusConflict, edgeErrCodeApprovalConflict)

	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, approval.ApprovalRef)
	if err != nil || !ok || stored == nil {
		t.Fatalf("GetApproval after terminal mutations = (%#v,%v,%v)", stored, ok, err)
	}
	if stored.Status != edgecore.ApprovalStatusApproved ||
		stored.Decision != edgecore.ApprovalDecisionApprove ||
		stored.ResolutionReason != "approved once" ||
		stored.ConsumedAt != nil {
		t.Fatalf("stored approval after terminal mutations = %#v, want original approved state without consume", stored)
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

func TestGatewayEdgeApprovalListPrincipalBinding(t *testing.T) {
	s, _ := newEdgeRouteTestServer(t)
	ownOld := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-user", "list-principal-own-old")
	otherPrincipal := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-other-member", "list-principal-other")
	ownNew := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-user", "list-principal-own-new")
	crossTenantOwn := seedGatewayEdgeApproval(t, s, edgeRouteOtherTenant, "principal-edge-user", "list-principal-cross")

	first := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteTenant, "user",
		"principal-edge-user", "?status=pending&limit=1", s.handleListEdgeApprovals))
	if len(first.Items) != 1 || first.NextCursor != "1" || first.Items[0].PrincipalID != "principal-edge-user" {
		t.Fatalf("first requester page = len:%d cursor:%q items:%#v, want one own item and cursor 1",
			len(first.Items), first.NextCursor, first.Items)
	}
	second := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteTenant, "user",
		"principal-edge-user", "?status=pending&limit=1&cursor="+first.NextCursor, s.handleListEdgeApprovals))
	if second.NextCursor != "" {
		t.Fatalf("second requester cursor = %q, want empty", second.NextCursor)
	}
	assertEdgeApprovalPageRefs(t, edgeApprovalPageResponse{Items: append(first.Items, second.Items...)}, ownOld.ApprovalRef, ownNew.ApprovalRef)

	other := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteTenant, "user",
		"principal-other-member", "?status=pending&limit=10", s.handleListEdgeApprovals))
	assertEdgeApprovalPageRefs(t, other, otherPrincipal.ApprovalRef)

	admin := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteTenant, "admin",
		"principal-admin", "?status=pending&limit=10", s.handleListEdgeApprovals))
	assertEdgeApprovalPageRefs(t, admin, ownOld.ApprovalRef, otherPrincipal.ApprovalRef, ownNew.ApprovalRef)

	operator := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteTenant, "operator",
		"principal-operator", "?status=pending&limit=10", s.handleListEdgeApprovals))
	assertEdgeApprovalPageRefs(t, operator, ownOld.ApprovalRef, otherPrincipal.ApprovalRef, ownNew.ApprovalRef)

	cross := decodeEdgeApprovalPage(t, edgeApprovalListDirectRequest(t, edgeRouteOtherTenant, "user",
		"principal-edge-user", "?status=pending&limit=10", s.handleListEdgeApprovals))
	assertEdgeApprovalPageRefs(t, cross, crossTenantOwn.ApprovalRef)
}

func TestGatewayEdgeApprovalGetPrincipalBinding(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-user", "get-principal")
	path := "/api/v1/edge/approvals/" + approval.ApprovalRef

	requester := edgeApprovalRouteGETAs(t, handler, edgeRouteUserAPIKey, edgeRouteTenant, path)
	assertEdgeApprovalResponseRef(t, requester, approval.ApprovalRef)

	sameTenant404 := edgeApprovalDirectRequest(t, http.MethodGet, edgeRouteTenant, "user",
		"principal-other-member", approval.ApprovalRef, "", "", "edge-approval-get-404", s.handleGetEdgeApproval)
	assertEdgeErrorShape(t, sameTenant404, http.StatusNotFound, edgeErrCodeNotFound)

	admin := edgeApprovalRouteGETAs(t, handler, edgeRouteReviewerAPIKey, edgeRouteTenant, path)
	assertEdgeApprovalResponseRef(t, admin, approval.ApprovalRef)

	operator := edgeApprovalDirectRequest(t, http.MethodGet, edgeRouteTenant, "operator",
		"principal-operator", approval.ApprovalRef, "", "", "edge-approval-get-operator", s.handleGetEdgeApproval)
	assertEdgeApprovalResponseRef(t, operator, approval.ApprovalRef)

	crossTenant404 := edgeApprovalDirectRequest(t, http.MethodGet, edgeRouteOtherTenant, "user",
		"principal-other-tenant", approval.ApprovalRef, "", "", "edge-approval-get-404", s.handleGetEdgeApproval)
	assertEdgeErrorShape(t, crossTenant404, http.StatusNotFound, edgeErrCodeNotFound)
	if sameTenant404.Body.String() != crossTenant404.Body.String() {
		t.Fatalf("same-tenant mismatch 404 body = %q, want byte-identical cross-tenant 404 body %q",
			sameTenant404.Body.String(), crossTenant404.Body.String())
	}
}

func TestGatewayEdgeApprovalWaitPrincipalBinding(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-user", "wait-principal")
	path := "/api/v1/edge/approvals/" + approval.ApprovalRef + "/wait"
	waitBody := `{"timeout_ms":1}`

	requester := edgeApprovalRoutePOSTAs(t, handler, edgeRouteUserAPIKey, path, waitBody)
	assertEdgeApprovalResponseRef(t, requester, approval.ApprovalRef)

	sameStarted := time.Now()
	sameTenant404 := edgeApprovalDirectRequest(t, http.MethodPost, edgeRouteTenant, "user",
		"principal-other-member", approval.ApprovalRef, "/wait", `{"timeout_ms":1000}`,
		"edge-approval-wait-404", s.handleWaitEdgeApproval)
	sameElapsed := time.Since(sameStarted)
	assertEdgeErrorShape(t, sameTenant404, http.StatusNotFound, edgeErrCodeNotFound)

	admin := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, path, waitBody)
	assertEdgeApprovalResponseRef(t, admin, approval.ApprovalRef)

	operator := edgeApprovalDirectRequest(t, http.MethodPost, edgeRouteTenant, "operator",
		"principal-operator", approval.ApprovalRef, "/wait", waitBody,
		"edge-approval-wait-operator", s.handleWaitEdgeApproval)
	assertEdgeApprovalResponseRef(t, operator, approval.ApprovalRef)

	crossStarted := time.Now()
	crossTenant404 := edgeApprovalDirectRequest(t, http.MethodPost, edgeRouteOtherTenant, "user",
		"principal-other-tenant", approval.ApprovalRef, "/wait", `{"timeout_ms":1000}`,
		"edge-approval-wait-404", s.handleWaitEdgeApproval)
	crossElapsed := time.Since(crossStarted)
	assertEdgeErrorShape(t, crossTenant404, http.StatusNotFound, edgeErrCodeNotFound)
	if sameTenant404.Body.String() != crossTenant404.Body.String() {
		t.Fatalf("same-tenant mismatch wait 404 body = %q, want byte-identical cross-tenant 404 body %q",
			sameTenant404.Body.String(), crossTenant404.Body.String())
	}
	if sameElapsed > 500*time.Millisecond || crossElapsed > 500*time.Millisecond {
		t.Fatalf("unauthorized wait 404s took same-tenant=%v cross-tenant=%v, want both below 500ms despite 1000ms body timeout",
			sameElapsed, crossElapsed)
	}
}

func TestGatewayEdgeApprovalRoutesRequireAuthAndTenant(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "auth-tenant")

	routes := []struct {
		name   string
		method string
		path   string
		body   string
		apiKey string
	}{
		{
			name:   "list",
			method: http.MethodGet,
			path:   "/api/v1/edge/approvals?status=pending",
			apiKey: edgeRouteTestAPIKey,
		},
		{
			name:   "detail",
			method: http.MethodGet,
			path:   "/api/v1/edge/approvals/" + approval.ApprovalRef,
			apiKey: edgeRouteTestAPIKey,
		},
		{
			name:   "approve",
			method: http.MethodPost,
			path:   "/api/v1/edge/approvals/" + approval.ApprovalRef + "/approve",
			body:   `{"reason":"auth gate"}`,
			apiKey: edgeRouteReviewerAPIKey,
		},
		{
			name:   "reject",
			method: http.MethodPost,
			path:   "/api/v1/edge/approvals/" + approval.ApprovalRef + "/reject",
			body:   `{"reason":"auth gate"}`,
			apiKey: edgeRouteReviewerAPIKey,
		},
		{
			name:   "wait",
			method: http.MethodPost,
			path:   "/api/v1/edge/approvals/" + approval.ApprovalRef + "/wait",
			body:   `{"timeout_ms":1}`,
			apiKey: edgeRouteReviewerAPIKey,
		},
	}

	for _, route := range routes {
		t.Run(route.name+"/missing_auth", func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			req.Header.Set("X-Tenant-ID", edgeRouteTenant)
			if route.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("%s missing auth status = %d, want 401 body=%s", route.name, rr.Code, rr.Body.String())
			}
		})

		t.Run(route.name+"/missing_tenant", func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			addEdgeRouteAuthFor(req, route.apiKey)
			if route.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s missing tenant status = %d, want 400 body=%s", route.name, rr.Code, rr.Body.String())
			}
		})
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

func edgeApprovalListDirectRequest(t *testing.T, tenantID, role, principalID, query string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/approvals"+query, nil)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("X-Request-Id", "edge-approval-list-principal")
	req = withAuth(req, &auth.AuthContext{Tenant: tenantID, PrincipalID: principalID, Role: role})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list approvals status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	return rr
}

func edgeApprovalDirectRequest(
	t *testing.T,
	method string,
	tenantID string,
	role string,
	principalID string,
	approvalRef string,
	pathSuffix string,
	body string,
	requestID string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/edge/approvals/"+approvalRef+pathSuffix, strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("X-Request-Id", requestID)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetPathValue("approval_ref", approvalRef)
	req = withAuth(req, &auth.AuthContext{Tenant: tenantID, PrincipalID: principalID, Role: role})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeEdgeApprovalPage(t *testing.T, rr *httptest.ResponseRecorder) edgeApprovalPageResponse {
	t.Helper()
	var page edgeApprovalPageResponse
	decodeEdgeRouteJSON(t, rr, &page)
	return page
}

func assertEdgeApprovalPageRefs(t *testing.T, page edgeApprovalPageResponse, want ...string) {
	t.Helper()
	if len(page.Items) != len(want) {
		t.Fatalf("approval page len = %d refs=%#v, want %d refs=%#v",
			len(page.Items), edgeApprovalPageRefs(page.Items), len(want), want)
	}
	got := map[string]int{}
	for _, item := range page.Items {
		got[item.ApprovalRef]++
	}
	for _, ref := range want {
		if got[ref] != 1 {
			t.Fatalf("approval page refs=%#v, want exactly %#v", got, want)
		}
		delete(got, ref)
	}
	if len(got) != 0 {
		t.Fatalf("approval page had unexpected refs=%#v, want exactly %#v", got, want)
	}
}

func edgeApprovalPageRefs(items []edgecore.EdgeApproval) []string {
	refs := make([]string, 0, len(items))
	for _, item := range items {
		refs = append(refs, item.ApprovalRef)
	}
	return refs
}

func assertEdgeApprovalResponseRef(t *testing.T, rr *httptest.ResponseRecorder, approvalRef string) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("approval read status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var approval edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rr, &approval)
	if approval.ApprovalRef != approvalRef {
		t.Fatalf("approval_ref = %q, want %q body=%s", approval.ApprovalRef, approvalRef, rr.Body.String())
	}
}

func TestGatewayEdgeApprovalWaitReturnsResolvedDuringWait(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "wait-resolve")

	resolveDone := make(chan struct{})
	go func() {
		defer close(resolveDone)
		time.Sleep(150 * time.Millisecond)
		_, _ = s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
			TenantID:    edgeRouteTenant,
			ApprovalRef: approval.ApprovalRef,
			ResolverID:  "principal-reviewer",
			ResolvedBy:  "principal:principal-reviewer|role:admin",
			Reason:      "approved during wait",
			ResolvedAt:  time.Now().UTC(),
		})
	}()

	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/wait", `{"timeout_ms":3000}`)
	<-resolveDone
	if rr.Code != http.StatusOK {
		t.Fatalf("wait status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resolved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rr, &resolved)
	if resolved.Status != edgecore.ApprovalStatusApproved {
		t.Fatalf("wait status after approve = %q, want approved", resolved.Status)
	}
	if resolved.ApprovalRef != approval.ApprovalRef {
		t.Fatalf("wait approval_ref = %q, want %q", resolved.ApprovalRef, approval.ApprovalRef)
	}
}

func TestGatewayEdgeApprovalWaitTimesOutKeepsPending(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "wait-timeout")

	start := time.Now()
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/wait", `{"timeout_ms":400}`)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("wait status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var pending edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rr, &pending)
	if pending.Status != edgecore.ApprovalStatusPending {
		t.Fatalf("wait status after timeout = %q, want pending", pending.Status)
	}
	if elapsed < 350*time.Millisecond {
		t.Fatalf("wait timeout elapsed %v, expected >= ~400ms", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("wait timeout elapsed %v, expected to honor 400ms cap", elapsed)
	}
}

func TestGatewayEdgeApprovalWaitDeniesCrossTenant(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "wait-cross-tenant")

	// A reviewer API key bound to edgeRouteTenant is rejected by the auth layer
	// when it asserts a different X-Tenant-ID, so we never reach the handler's
	// tenant-scoped GetApproval. Both 403 (tenant access denied) and 404 (handler
	// scoped) are valid tenant-isolation outcomes — what matters is the response
	// must not leak the approval_ref or the original tenant.
	rr := edgeApprovalRoutePOSTAsTenant(t, handler, edgeRouteReviewerAPIKey, edgeRouteOtherTenant, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/wait", `{}`)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant wait status = %d, want 403 or 404 body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), approval.ApprovalRef, edgeRouteTenant)
}

func TestGatewayEdgeApprovalWaitReturnsImmediatelyWhenAlreadyResolved(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "wait-already-resolved")

	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "principal:principal-reviewer|role:admin",
		Reason:      "approved before wait",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("pre-approve: %v", err)
	}

	start := time.Now()
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/wait", `{"timeout_ms":5000}`)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("wait status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resolved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rr, &resolved)
	if resolved.Status != edgecore.ApprovalStatusApproved {
		t.Fatalf("status = %q, want approved", resolved.Status)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("already-resolved wait took %v, expected immediate return", elapsed)
	}
}
