// EDGE-143.6 — handler-layer tests for the operator-defined Shadow
// Exception API (§10.3). Tests written before implementation per TDD;
// they fail until handlers_edge_shadow_exception_handlers.go +
// gateway.go route registration land in step-5.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge/shadow"
)

func shadowCtxWithRole(req *http.Request, role string) *http.Request {
	authCtx := &auth.AuthContext{
		Role:             role,
		Tenant:           "",
		PrincipalID:      "alice",
		AllowCrossTenant: true,
	}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
}

func validExceptionCreateBody() shadowExceptionCreateRequest {
	return shadowExceptionCreateRequest{
		ExpiresAt:       time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		Reason:          "approved by SRE for known kube-system DaemonSet pattern",
		ScopeSourceType: "kubernetes",
		ScopeSourceID:   "k8s-detector-1",
		ScopeRiskLevel:  shadow.FindingRiskHigh,
		ScopeSignalSet:  []string{"k8s_unmanaged_process"},
	}
}

func postShadowAs(t *testing.T, s *server, role, tenant, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := shadowCtxWithRole(httptest.NewRequest(http.MethodPost, path, reader), role)
	req.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func deleteShadowAs(t *testing.T, s *server, role, tenant, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := shadowCtxWithRole(httptest.NewRequest(http.MethodDelete, path, nil), role)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func getShadowAs(t *testing.T, s *server, role, tenant, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := shadowCtxWithRole(httptest.NewRequest(http.MethodGet, path, nil), role)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestParseShadowExceptionListQuery_Limit(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantOK    bool
		wantLimit int
	}{
		{name: "missing limit uses store default", query: "", wantOK: true},
		{name: "empty limit uses store default", query: "?limit=", wantOK: true},
		{name: "valid limit", query: "?limit=10", wantOK: true, wantLimit: 10},
		{name: "max boundary", query: "?limit=1000", wantOK: true, wantLimit: 1000},
		{name: "zero invalid", query: "?limit=0"},
		{name: "negative invalid", query: "?limit=-1"},
		{name: "over max invalid", query: "?limit=1001"},
		{name: "non-numeric invalid", query: "?limit=abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/shadow/exceptions"+tc.query, nil)
			rec := httptest.NewRecorder()

			got, ok := parseShadowExceptionListQuery(rec, req, "tenant-a")

			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v; query=%q body=%s", ok, tc.wantOK, tc.query, rec.Body.String())
			}
			if tc.wantOK {
				if got.TenantID != "tenant-a" {
					t.Fatalf("TenantID = %q, want tenant-a", got.TenantID)
				}
				if got.Limit != tc.wantLimit {
					t.Fatalf("Limit = %d, want %d", got.Limit, tc.wantLimit)
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			var env edgeErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
			}
			if env.Code != edgeErrCodeInvalidRequest {
				t.Fatalf("error code = %q, want %q", env.Code, edgeErrCodeInvalidRequest)
			}
		})
	}
}

func TestWriteShadowExceptionStoreError_LimitExceededUsesDedicatedCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/shadow/exception", nil)
	rec := httptest.NewRecorder()

	writeShadowExceptionStoreError(rec, req, shadow.ErrExceptionLimitExceeded, "create shadow exception")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	if env.Code != edgeErrCodeLimitExceeded {
		t.Fatalf("error code = %q, want %q", env.Code, edgeErrCodeLimitExceeded)
	}
}

func TestEdgeErrorEnum_IncludesStepUpAndLimitExceeded(t *testing.T) {
	assertOpenAPIEdgeErrorEnumContains(t, edgeErrCodeStepUpRequired, edgeErrCodeLimitExceeded)
}

func TestShadowException_Create_HighRisk_RequiresStepUp(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", validExceptionCreateBody())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("high-risk POST as user: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != edgeErrCodeStepUpRequired {
		t.Fatalf("error code = %q, want %q", env.Code, edgeErrCodeStepUpRequired)
	}
}

func TestShadowException_Create_CriticalRisk_RequiresStepUp(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskCritical
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("critical-risk POST as user: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != edgeErrCodeStepUpRequired {
		t.Fatalf("error code = %q, want %q", env.Code, edgeErrCodeStepUpRequired)
	}
}

func TestShadowException_Create_HighRisk_AdminStepUp(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", validExceptionCreateBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin high-risk POST: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.Exception
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ExceptionID == "" || created.TenantID != "tenant-a" {
		t.Fatalf("created shape: %+v", created)
	}
	if created.StepUpFactor != shadow.StepUpFactorSignedAdminToken {
		t.Errorf("StepUpFactor = %q, want signed_admin_token", created.StepUpFactor)
	}

	// Audit event recorded with actor + step_up_factor + exception_id.
	exp := s.auditExporter.(*shadowFindingAuditExporter)
	var createdEvent *audit.SIEMEvent
	for i := range exp.events {
		if exp.events[i].EventType == audit.EventShadowAgentExceptionCreated {
			createdEvent = &exp.events[i]
			break
		}
	}
	if createdEvent == nil {
		t.Fatalf("no shadow_agent.exception_created audit event; got %+v", exp.events)
	}
	if createdEvent.Identity != "alice" {
		t.Errorf("audit Identity = %q, want alice", createdEvent.Identity)
	}
	if createdEvent.Extra["step_up_factor"] != string(shadow.StepUpFactorSignedAdminToken) {
		t.Errorf("audit step_up_factor = %q, want signed_admin_token", createdEvent.Extra["step_up_factor"])
	}
	if createdEvent.Extra["exception_id"] != created.ExceptionID {
		t.Errorf("audit exception_id = %q, want %q", createdEvent.Extra["exception_id"], created.ExceptionID)
	}
}

func TestShadowException_Create_CriticalRisk_AdminStepUp(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskCritical
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin critical-risk POST: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.Exception
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ScopeRiskLevel != shadow.FindingRiskCritical {
		t.Fatalf("ScopeRiskLevel = %q, want critical", created.ScopeRiskLevel)
	}
	if created.StepUpFactor != shadow.StepUpFactorSignedAdminToken {
		t.Errorf("StepUpFactor = %q, want signed_admin_token", created.StepUpFactor)
	}
}

func TestShadowException_Create_MediumRisk_RegularAuth(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskMedium
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("medium-risk POST as user: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.Exception
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.StepUpFactor != shadow.StepUpFactorNone {
		t.Errorf("StepUpFactor = %q, want none", created.StepUpFactor)
	}
}

func TestShadowException_Create_LowRisk_RegularAuth(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskLow
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("low-risk POST as user: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowException_Get_TenantGated(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", validExceptionCreateBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rec.Code)
	}
	var created shadow.Exception
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Same id, wrong tenant header → 404.
	getRec := getShadowAs(t, s, "admin", "tenant-b", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant GET status = %d, want 404; body=%s", getRec.Code, getRec.Body.String())
	}
	// Same tenant succeeds.
	okRec := getShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if okRec.Code != http.StatusOK {
		t.Fatalf("same-tenant GET status = %d, want 200; body=%s", okRec.Code, okRec.Body.String())
	}
}

func TestShadowException_List_FiltersHonored(t *testing.T) {
	s := newShadowGateway(t)
	// Create two exceptions with different source_type scopes.
	for _, src := range []string{"kubernetes", "ci"} {
		body := validExceptionCreateBody()
		body.ScopeSourceType = src
		body.ScopeRiskLevel = shadow.FindingRiskMedium
		rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", src, rec.Code)
		}
	}
	listRec := getShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exceptions?source_type=kubernetes")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	var page shadow.ExceptionPage
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Exceptions) != 1 || page.Exceptions[0].ScopeSourceType != "kubernetes" {
		t.Fatalf("filter returned %+v", page.Exceptions)
	}
}

func TestShadowException_Delete_HighRiskRequiresStepUp(t *testing.T) {
	s := newShadowGateway(t)
	// Create a HIGH-risk exception (admin path).
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", validExceptionCreateBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created shadow.Exception
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// User attempts revoke → must be denied with step_up_required because
	// the original create was step-up gated.
	delRec := deleteShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if delRec.Code != http.StatusForbidden {
		t.Fatalf("user revoke of high-risk exception status = %d, want 403; body=%s", delRec.Code, delRec.Body.String())
	}
	var env edgeErrorEnvelope
	_ = json.Unmarshal(delRec.Body.Bytes(), &env)
	if env.Code != edgeErrCodeStepUpRequired {
		t.Errorf("revoke error code = %q, want %q", env.Code, edgeErrCodeStepUpRequired)
	}

	// Admin can revoke successfully.
	okRec := deleteShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("admin revoke status = %d, want 204; body=%s", okRec.Code, okRec.Body.String())
	}
}

func TestShadowException_Delete_CriticalRiskRequiresStepUp(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskCritical
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("critical create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.Exception
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.StepUpFactor != shadow.StepUpFactorSignedAdminToken {
		t.Fatalf("critical create StepUpFactor = %q, want signed_admin_token", created.StepUpFactor)
	}

	delRec := deleteShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if delRec.Code != http.StatusForbidden {
		t.Fatalf("user revoke of critical-risk exception status = %d, want 403; body=%s", delRec.Code, delRec.Body.String())
	}
	var env edgeErrorEnvelope
	_ = json.Unmarshal(delRec.Body.Bytes(), &env)
	if env.Code != edgeErrCodeStepUpRequired {
		t.Errorf("critical revoke error code = %q, want %q", env.Code, edgeErrCodeStepUpRequired)
	}

	okRec := deleteShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("admin revoke critical-risk status = %d, want 204; body=%s", okRec.Code, okRec.Body.String())
	}
	exp := s.auditExporter.(*shadowFindingAuditExporter)
	var revoked *audit.SIEMEvent
	for i := range exp.events {
		if exp.events[i].EventType == audit.EventShadowAgentExceptionRevoked {
			revoked = &exp.events[i]
		}
	}
	if revoked == nil {
		t.Fatalf("no shadow_agent.exception_revoked audit event; got %+v", exp.events)
	}
	if revoked.Extra["step_up_factor"] != string(shadow.StepUpFactorSignedAdminToken) {
		t.Errorf("revoked step_up_factor = %q, want signed_admin_token", revoked.Extra["step_up_factor"])
	}
	if revoked.Extra["scope_risk_level"] != string(shadow.FindingRiskCritical) {
		t.Errorf("revoked scope_risk_level = %q, want critical", revoked.Extra["scope_risk_level"])
	}
}

func TestShadowException_Delete_LowRiskByUser(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskLow
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created shadow.Exception
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	delRec := deleteShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception/"+created.ExceptionID)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("user revoke of low-risk status = %d, want 204; body=%s", delRec.Code, delRec.Body.String())
	}
}

func TestShadowException_AuditEvent_StepUpFactorRecorded(t *testing.T) {
	s := newShadowGateway(t)
	// Medium-risk create → step_up_factor=none recorded.
	body := validExceptionCreateBody()
	body.ScopeRiskLevel = shadow.FindingRiskMedium
	rec := postShadowAs(t, s, "user", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	exp := s.auditExporter.(*shadowFindingAuditExporter)
	var ev *audit.SIEMEvent
	for i := range exp.events {
		if exp.events[i].EventType == audit.EventShadowAgentExceptionCreated {
			ev = &exp.events[i]
			break
		}
	}
	if ev == nil {
		t.Fatalf("no exception_created audit event")
	}
	if got := ev.Extra["step_up_factor"]; got != string(shadow.StepUpFactorNone) {
		t.Errorf("medium-risk step_up_factor = %q, want none", got)
	}
}

func TestShadowException_AppliedAtEmit_AuditEventEmitted(t *testing.T) {
	s := newShadowGateway(t)
	// 1. Create an exception scoped to kubernetes + high risk + a signal.
	body := validExceptionCreateBody()
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("exception create status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var exc shadow.Exception
	_ = json.Unmarshal(rec.Body.Bytes(), &exc)

	// 2. Ingest a finding that matches the exception scope.
	findingBody := validShadowCreateBody("tenant-a")
	findingBody.SourceType = "kubernetes"
	findingBody.SourceID = "k8s-detector-1"
	findingBody.Risk = shadow.FindingRiskHigh
	findingBody.SignalSet = []string{"k8s_unmanaged_process"}
	fRec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", findingBody)
	if fRec.Code != http.StatusCreated {
		t.Fatalf("finding create status = %d; body=%s", fRec.Code, fRec.Body.String())
	}
	var f shadow.ShadowAgentFinding
	_ = json.Unmarshal(fRec.Body.Bytes(), &f)
	if f.ExceptionID != exc.ExceptionID {
		t.Errorf("finding ExceptionID = %q, want %q", f.ExceptionID, exc.ExceptionID)
	}

	// 3. Audit event shadow_agent.exception_applied emitted with actor +
	// step_up_factor + exception_id.
	exp := s.auditExporter.(*shadowFindingAuditExporter)
	var applied *audit.SIEMEvent
	for i := range exp.events {
		if exp.events[i].EventType == audit.EventShadowAgentExceptionApplied {
			applied = &exp.events[i]
			break
		}
	}
	if applied == nil {
		t.Fatalf("no shadow_agent.exception_applied audit event; got %d events", len(exp.events))
	}
	if applied.Extra["exception_id"] != exc.ExceptionID {
		t.Errorf("applied exception_id = %q, want %q", applied.Extra["exception_id"], exc.ExceptionID)
	}
	if applied.Extra["finding_id"] != f.FindingID {
		t.Errorf("applied finding_id = %q, want %q", applied.Extra["finding_id"], f.FindingID)
	}
	if applied.Extra["step_up_factor"] != string(shadow.StepUpFactorSignedAdminToken) {
		t.Errorf("applied step_up_factor = %q, want signed_admin_token", applied.Extra["step_up_factor"])
	}
}

func TestShadowException_Create_RejectsExpiresAtBeyond90Days(t *testing.T) {
	s := newShadowGateway(t)
	body := validExceptionCreateBody()
	// 100 days in the future from any plausible runtime → reject.
	body.ExpiresAt = time.Now().Add(100 * 24 * time.Hour)
	rec := postShadowAs(t, s, "admin", "tenant-a", "/api/v1/edge/shadow/exception", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expires_at >90d status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expires_at") {
		t.Errorf("body should mention expires_at; got %s", rec.Body.String())
	}
}
