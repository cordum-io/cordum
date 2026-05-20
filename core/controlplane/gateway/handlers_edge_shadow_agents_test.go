package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge/shadow"
)

func shadowAdminCtx(req *http.Request) *http.Request {
	authCtx := &auth.AuthContext{
		Role:             "admin",
		Tenant:           "",
		PrincipalID:      "alice",
		AllowCrossTenant: true,
	}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
}

// newShadowGateway returns a gateway pre-wired with a shadow.RedisStore
// backed by the shared miniredis client. Most assertions exercise the
// full server.registerRoutes mux indirectly via direct handler calls;
// route-table coverage is asserted separately by
// TestShadowAgentRoutesRegistered.
func newShadowGateway(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	store, err := shadow.NewRedisStore(s.jobStore.Client())
	if err != nil {
		t.Fatalf("shadow.NewRedisStore: %v", err)
	}
	s.shadowFindingStore = store
	// Install an in-memory audit exporter so the lifecycle audit calls
	// can be asserted.
	exp := &shadowFindingAuditExporter{}
	s.auditExporter = exp
	return s
}

type shadowFindingAuditExporter struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (e *shadowFindingAuditExporter) Send(ev audit.SIEMEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *shadowFindingAuditExporter) Events() []audit.SIEMEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]audit.SIEMEvent, len(e.events))
	copy(out, e.events)
	return out
}

func (e *shadowFindingAuditExporter) Close() error { return nil }

func validShadowCreateBody(_ string) shadowAgentCreateRequest {
	return shadowAgentCreateRequest{
		OwnerPrincipalID: "owner-alice",
		AgentProduct:     "claude-code",
		AgentID:          "agent-xyz",
		Hostname:         "dev-mac-01",
		Risk:             shadow.FindingRiskHigh,
		EvidenceType:     "config_file",
		EvidenceSummary:  "2 mcp servers configured (transports: stdio)",
		RedactedPath:     "/home/dev/.config/cursor-mcp.json",
		DetectedAt:       time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
	}
}

func postShadow(t *testing.T, s *server, tenant, path string, body any) *httptest.ResponseRecorder {
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
	req := shadowAdminCtx(httptest.NewRequest(http.MethodPost, path, reader))
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

func getShadow(t *testing.T, s *server, tenant, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := shadowAdminCtx(httptest.NewRequest(http.MethodGet, path, nil))
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

func TestShadowAgents_CreateGetList_HappyPath(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", validShadowCreateBody("tenant-a"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created shadow.ShadowAgentFinding
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.FindingID == "" || created.TenantID != "tenant-a" || created.Status != shadow.FindingStatusDetected {
		t.Fatalf("created shape unexpected: %+v", created)
	}
	if strings.Contains(created.RedactedPath, "dev") && strings.Contains(created.RedactedPath, "/home/") {
		t.Fatalf("RedactedPath not home-stripped: %q", created.RedactedPath)
	}

	getRec := getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents/"+created.FindingID)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", getRec.Code, getRec.Body.String())
	}
	var got shadow.ShadowAgentFinding
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.FindingID != created.FindingID {
		t.Fatalf("get returned wrong finding: %+v", got)
	}

	listRec := getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents?risk=high")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}
	var page shadow.FindingPage
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Findings) != 1 || page.Findings[0].FindingID != created.FindingID {
		t.Fatalf("list page = %+v, want one finding %q", page.Findings, created.FindingID)
	}

	// Audit assertion: detected event recorded; severity HIGH for high-risk;
	// no raw secret-shaped strings or full local paths.
	events := s.auditExporter.(*shadowFindingAuditExporter).Events()
	if len(events) != 1 || events[0].EventType != audit.EventShadowAgentDetected {
		t.Fatalf("audit events = %+v, want one EventShadowAgentDetected", events)
	}
	if events[0].TenantID != "tenant-a" || events[0].Severity != audit.SeverityHigh {
		t.Fatalf("audit event identity wrong: %+v", events[0])
	}
	if rp := events[0].Extra["redacted_path"]; strings.Contains(rp, "/home/dev/") {
		t.Fatalf("audit redacted_path leak: %q", rp)
	}
}

func TestShadowAgents_CreateRejectsMissingEvidence(t *testing.T) {
	s := newShadowGateway(t)
	body := validShadowCreateBody("tenant-a")
	body.EvidenceSummary = ""
	body.EvidenceArtifact = nil

	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create without evidence status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var env edgeErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	if env.Code != edgeErrCodeInvalidRequest {
		t.Fatalf("error code = %q, want %q", env.Code, edgeErrCodeInvalidRequest)
	}
	if !strings.Contains(env.Message, "evidence_summary") || !strings.Contains(env.Message, "evidence_artifact_ptr") {
		t.Fatalf("message = %q, want both evidence field names", env.Message)
	}
	if strings.Contains(rec.Body.String(), body.RedactedPath) {
		t.Fatalf("error body leaked request path: %s", rec.Body.String())
	}
}

func TestShadowAgents_TenantIsolation_GetReturns404(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", validShadowCreateBody("tenant-a"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rec.Code)
	}
	var created shadow.ShadowAgentFinding
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Same finding id, different tenant header → 404 (not 403, to avoid
	// leaking tuple existence).
	getRec := getShadow(t, s, "tenant-b", "/api/v1/edge/shadow-agents/"+created.FindingID)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d, want 404; body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestShadowAgents_TenantIsolation_ListExcludesOther(t *testing.T) {
	s := newShadowGateway(t)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		body := validShadowCreateBody(tenant)
		rec := postShadow(t, s, tenant, "/api/v1/edge/shadow-agents", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d", tenant, rec.Code)
		}
	}
	listRec := getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents")
	var page shadow.FindingPage
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Findings) != 1 || page.Findings[0].TenantID != "tenant-a" {
		t.Fatalf("tenant isolation broken: %+v", page.Findings)
	}
}

func TestShadowAgents_RejectsMissingTenantHeader(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadow(t, s, "", "/api/v1/edge/shadow-agents", validShadowCreateBody("tenant-a"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no-tenant status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowAgents_RejectsBadFilters(t *testing.T) {
	s := newShadowGateway(t)
	rec := getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents?risk=nope")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-risk status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = getShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents?status=invalid")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-status status = %d, want 400", rec.Code)
	}
}

func TestShadowAgents_StoreUnavailable_503(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	// Deliberately leave s.shadowFindingStore nil.
	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", validShadowCreateBody("tenant-a"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil-store status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowAgents_ResolveAndSuppress_EmitAudit(t *testing.T) {
	s := newShadowGateway(t)
	tenant := "tenant-a"
	createRec := postShadow(t, s, tenant, "/api/v1/edge/shadow-agents", validShadowCreateBody(tenant))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", createRec.Code)
	}
	var created shadow.ShadowAgentFinding
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	resolveRec := postShadow(t, s, tenant,
		"/api/v1/edge/shadow-agents/"+created.FindingID+"/resolve",
		shadowAgentResolveRequest{Reason: "operator uninstalled the shadow agent"})
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200; body=%s", resolveRec.Code, resolveRec.Body.String())
	}
	var resolved shadow.ShadowAgentFinding
	_ = json.Unmarshal(resolveRec.Body.Bytes(), &resolved)
	if resolved.Status != shadow.FindingStatusResolved {
		t.Fatalf("Status = %q, want resolved", resolved.Status)
	}

	// Suppress on a resolved finding → 409 (terminal conflict).
	supRec := postShadow(t, s, tenant,
		"/api/v1/edge/shadow-agents/"+created.FindingID+"/suppress",
		shadowAgentSuppressRequest{Reason: "should fail"})
	if supRec.Code != http.StatusConflict {
		t.Fatalf("post-resolve suppress status = %d, want 409; body=%s", supRec.Code, supRec.Body.String())
	}

	// Audit assertions: detected + resolved emitted (no suppressed, because
	// the suppress call hit the 409 path before audit).
	events := s.auditExporter.(*shadowFindingAuditExporter).Events()
	if len(events) != 2 ||
		events[0].EventType != audit.EventShadowAgentDetected ||
		events[1].EventType != audit.EventShadowAgentResolved {
		gotTypes := make([]string, len(events))
		for i, e := range events {
			gotTypes[i] = e.EventType
		}
		t.Fatalf("audit event types = %v, want [detected resolved]", gotTypes)
	}
}

func TestShadowAgents_SuppressAndIgnoreAliasShareHandler(t *testing.T) {
	s := newShadowGateway(t)
	tenant := "tenant-a"
	createRec := postShadow(t, s, tenant, "/api/v1/edge/shadow-agents", validShadowCreateBody(tenant))
	var created shadow.ShadowAgentFinding
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	// Use the /ignore alias to suppress.
	rec := postShadow(t, s, tenant,
		"/api/v1/edge/shadow-agents/"+created.FindingID+"/ignore",
		shadowAgentSuppressRequest{Reason: "false positive"})
	if rec.Code != http.StatusOK {
		t.Fatalf("ignore-alias status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got shadow.ShadowAgentFinding
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != shadow.FindingStatusSuppressed {
		t.Fatalf("Status = %q, want suppressed", got.Status)
	}
}

func TestShadowAgents_RejectsRawEvidence_StripsSecrets(t *testing.T) {
	s := newShadowGateway(t)
	body := validShadowCreateBody("tenant-a")
	body.EvidenceSummary = "cordum_fake_sk-ant-abcdef1234567890ABCDEFGH found in mcp config"
	rec := postShadow(t, s, "tenant-a", "/api/v1/edge/shadow-agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rec.Code)
	}
	var created shadow.ShadowAgentFinding
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if strings.Contains(created.EvidenceSummary, "sk-ant-") {
		t.Fatalf("EvidenceSummary contains raw secret: %q", created.EvidenceSummary)
	}
}

// TestShadowAgents_ResolveImmutabilityInvariant pins the contract that
// resolve/suppress only flip lifecycle fields (status, resolved_*) — they
// must not let a caller smuggle a different tenant_id, finding_id,
// evidence_summary, or owner_principal_id through the update path.
func TestShadowAgents_ResolveImmutabilityInvariant(t *testing.T) {
	s := newShadowGateway(t)
	tenant := "tenant-a"
	createBody := validShadowCreateBody(tenant)
	createBody.EvidenceSummary = "original evidence summary content"
	createBody.OwnerPrincipalID = "owner-original"
	rec := postShadow(t, s, tenant, "/api/v1/edge/shadow-agents", createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created shadow.ShadowAgentFinding
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	resolveRec := postShadow(t, s, tenant,
		"/api/v1/edge/shadow-agents/"+created.FindingID+"/resolve",
		shadowAgentResolveRequest{Reason: "fixed"})
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d", resolveRec.Code)
	}
	var resolved shadow.ShadowAgentFinding
	_ = json.Unmarshal(resolveRec.Body.Bytes(), &resolved)

	if resolved.TenantID != created.TenantID {
		t.Errorf("TenantID mutated: %q → %q", created.TenantID, resolved.TenantID)
	}
	if resolved.FindingID != created.FindingID {
		t.Errorf("FindingID mutated: %q → %q", created.FindingID, resolved.FindingID)
	}
	if resolved.EvidenceSummary != created.EvidenceSummary {
		t.Errorf("EvidenceSummary mutated: %q → %q", created.EvidenceSummary, resolved.EvidenceSummary)
	}
	if resolved.OwnerPrincipalID != created.OwnerPrincipalID {
		t.Errorf("OwnerPrincipalID mutated: %q → %q", created.OwnerPrincipalID, resolved.OwnerPrincipalID)
	}
}

func TestShadowAgentRoutesRegistered(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	wantRoutes := []string{
		"POST /api/v1/edge/shadow-agents",
		"GET /api/v1/edge/shadow-agents",
		"GET /api/v1/edge/shadow-agents/{finding_id}",
		"POST /api/v1/edge/shadow-agents/{finding_id}/resolve",
		"POST /api/v1/edge/shadow-agents/{finding_id}/suppress",
		"POST /api/v1/edge/shadow-agents/{finding_id}/ignore",
	}
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	seen := make(map[string]bool)
	for _, r := range s.routeTable {
		seen[r.Method+" "+r.Path] = true
	}
	for _, w := range wantRoutes {
		if !seen[w] {
			t.Errorf("missing route registration: %q", w)
		}
	}
}
