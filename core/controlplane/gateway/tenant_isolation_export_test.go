package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Tenant isolation of the export routes and legal holds (PR1-03E). Each case
// seeds tenant B, acts as tenant A (a tenant-scoped admin that knows B's ids)
// and checks the returned bytes by ids and markers.
const (
	isoTenantA = "tenant-iso-a"
	isoTenantB = "tenant-iso-b"
	isoMarkerA = "MARKER-TENANT-ISO-A"
	isoMarkerB = "MARKER-TENANT-ISO-B"
)

var isoAdminA = &auth.AuthContext{Tenant: isoTenantA, Role: "admin", PrincipalID: "admin-iso-a"}

func assertNoLeak(t *testing.T, what, body string, leaks ...string) {
	t.Helper()
	for _, leak := range leaks {
		if leak != "" && strings.Contains(body, leak) {
			t.Fatalf("%s carries foreign tenant data %q: %s", what, leak, body)
		}
	}
}

func seedIsoAuditEvents(t *testing.T, s *server, tenant, marker string) []string {
	t.Helper()
	chainer := audit.NewChainer(s.redisClient(), "")
	var hashes []string
	for _, action := range []string{marker + "-deploy", marker + "-rollback"} {
		ev := audit.SIEMEvent{Timestamp: time.Now().UTC(), EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo,
			TenantID: tenant, Action: action, Decision: "allow", Reason: marker + " reason"}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("seed audit %s: %v", tenant, err)
		}
		hashes = append(hashes, ev.EventHash)
	}
	sort.Strings(hashes)
	return hashes
}

func TestTenantIsolationAuditExportHandler(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	grantExportEntitlement(t, s)
	hashesA := seedIsoAuditEvents(t, s, isoTenantA, isoMarkerA)
	hashesB := seedIsoAuditEvents(t, s, isoTenantB, isoMarkerB)
	leaks := append([]string{isoMarkerB, isoTenantB}, hashesB...)

	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=json"+rangeQS(), nil), isoAdminA))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Cordum-Tenant") != isoTenantA {
		t.Fatalf("A export = %d tenant %q: %s", rec.Code, rec.Header().Get("X-Cordum-Tenant"), rec.Body.String())
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		var ev struct {
			Type      string `json:"type"`
			TenantID  string `json:"tenant_id"`
			EventHash string `json:"event_hash"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("export line %q: %v", line, err)
		}
		if ev.Type == "event" {
			if ev.TenantID != isoTenantA {
				t.Fatalf("A export carries an event of tenant %q", ev.TenantID)
			}
			got = append(got, ev.EventHash)
		}
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, hashesA) || !strings.Contains(rec.Body.String(), isoMarkerA) {
		t.Fatalf("A export event hashes = %v, want exactly A's %v with A's marker", got, hashesA)
	}
	assertNoLeak(t, "A export", rec.Body.String(), leaks...)

	// A naming B's tenant in the header is refused, and no B bytes leave.
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=json"+rangeQS(), nil), isoAdminA)
	req.Header.Set("X-Tenant-ID", isoTenantB)
	rec = httptest.NewRecorder()
	s.handleAuditExport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("A export with X-Tenant-ID B = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, "A export as B", rec.Body.String(), leaks...)
}

func postIsoEdgeEvent(t *testing.T, handler http.Handler, apiKey, tenant string, sess edgeSessionCreateResponseJSON, marker string) string {
	t.Helper()
	eventID := "evt-" + strings.ToLower(marker)
	body := idempotentEdgeEventMap(sess.SessionID, sess.ExecutionID, tenant, eventID, "npm test "+marker)
	body["tool_use_id"] = "toolu-" + marker
	rr := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, apiKey, tenant, "/api/v1/edge/events", mustJSON(body), "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("post %s event = %d: %s", tenant, rr.Code, rr.Body.String())
	}
	return eventID
}

func TestTenantIsolationEdgeSessionExportHandler(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	sessA := createEdgeRouteSessionForTenant(t, handler, edgeRouteTestAPIKey, edgeRouteTenant)
	sessB := createEdgeRouteSessionForTenant(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant)
	eventA := postIsoEdgeEvent(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, sessA, isoMarkerA)
	eventB := postIsoEdgeEvent(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant, sessB, isoMarkerB)
	ctx := context.Background()
	beforeB, ok, err := s.edgeStore.GetSession(ctx, edgeRouteOtherTenant, sessB.SessionID)
	if err != nil || !ok {
		t.Fatalf("read B session: ok=%v err=%v", ok, err)
	}
	leaks := []string{edgeRouteOtherTenant, sessB.ExecutionID, eventB, isoMarkerB}
	exportAs := func(tenant, sessionID string) *httptest.ResponseRecorder {
		return edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteTestAPIKey, tenant, "/api/v1/edge/sessions/"+sessionID+"/export", "", "")
	}

	own := exportAs(edgeRouteTenant, sessA.SessionID)
	if own.Code != http.StatusOK {
		t.Fatalf("A export of own session = %d: %s", own.Code, own.Body.String())
	}
	var bundle edgecore.SessionExportBundle
	decodeEdgeRouteJSON(t, own, &bundle)
	sawA := false
	for _, ev := range bundle.Events {
		if ev.TenantID != edgeRouteTenant || ev.SessionID != sessA.SessionID {
			t.Fatalf("A bundle carries event %s of tenant %q session %q", ev.EventID, ev.TenantID, ev.SessionID)
		}
		sawA = sawA || ev.EventID == eventA
	}
	if bundle.TenantID != edgeRouteTenant || bundle.Session.SessionID != sessA.SessionID || !sawA ||
		!strings.Contains(own.Body.String(), "toolu-"+isoMarkerA) {
		t.Fatalf("A bundle = tenant %q session %q events %+v, want A's session with A's marked event", bundle.TenantID, bundle.Session.SessionID, bundle.Events)
	}
	assertNoLeak(t, "A export", own.Body.String(), append(leaks, sessB.SessionID)...)

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"A export of B's session id":  exportAs(edgeRouteTenant, sessB.SessionID),
		"A export with X-Tenant-ID B": exportAs(edgeRouteOtherTenant, sessB.SessionID),
	} {
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusForbidden {
			t.Fatalf("%s = %d, want 404 or 403: %s", name, rr.Code, rr.Body.String())
		}
		assertNoLeak(t, name, rr.Body.String(), leaks...)
	}
	afterB, ok, err := s.edgeStore.GetSession(ctx, edgeRouteOtherTenant, sessB.SessionID)
	if err != nil || !ok || !reflect.DeepEqual(afterB, beforeB) {
		t.Fatalf("B session after A's exports = %+v (ok=%v err=%v), want unchanged %+v", afterB, ok, err, beforeB)
	}
}

func legalHoldRequestAs(s *server, method, target, body string) *httptest.ResponseRecorder {
	req := withAuth(httptest.NewRequest(method, target, strings.NewReader(body)), isoAdminA)
	rec := httptest.NewRecorder()
	switch method {
	case http.MethodPost:
		s.handleCreateLegalHold(rec, req)
	case http.MethodDelete:
		req.SetPathValue("id", strings.TrimPrefix(target, "/api/v1/audit/legal-hold/"))
		s.handleReleaseLegalHold(rec, req)
	default:
		s.handleListLegalHolds(rec, req)
	}
	return rec
}

func TestTenantIsolationLegalHoldHandlers(t *testing.T) {
	s := newLegalHoldTenantGateway(t)
	ctx := context.Background()
	holdA, err := s.legalHoldStore.CreateHold(ctx, isoTenantA, isoMarkerA+" litigation", "admin-iso-a")
	if err != nil {
		t.Fatalf("seed A hold: %v", err)
	}
	holdB, err := s.legalHoldStore.CreateHold(ctx, isoTenantB, isoMarkerB+" litigation", "admin-iso-b")
	if err != nil {
		t.Fatalf("seed B hold: %v", err)
	}
	beforeB, err := s.legalHoldStore.GetHold(ctx, holdB.ID)
	if err != nil {
		t.Fatalf("read B hold: %v", err)
	}
	leaks := []string{holdB.ID, isoMarkerB, isoTenantB}

	// Create naming B: refused before B's existing hold is consulted (no 409).
	created := legalHoldRequestAs(s, http.MethodPost, "/api/v1/audit/legal-hold", `{"tenant_id":"`+isoTenantB+`","reason":"probe"}`)
	if created.Code != http.StatusForbidden {
		t.Fatalf("A create on B = %d, want 403: %s", created.Code, created.Body.String())
	}
	assertNoLeak(t, "A create on B", created.Body.String(), leaks...)
	// Release of B's id answers like an unknown id.
	unknown := legalHoldRequestAs(s, http.MethodDelete, "/api/v1/audit/legal-hold/no-such-hold", "")
	denied := legalHoldRequestAs(s, http.MethodDelete, "/api/v1/audit/legal-hold/"+holdB.ID, "")
	if denied.Code != http.StatusNotFound || denied.Body.String() != unknown.Body.String() {
		t.Fatalf("A release of B's hold = %d %q, want the unknown-id answer %d %q", denied.Code, denied.Body.String(), unknown.Code, unknown.Body.String())
	}
	assertNoLeak(t, "A release of B's hold", denied.Body.String(), isoMarkerB, isoTenantB)

	listedB := legalHoldRequestAs(s, http.MethodGet, "/api/v1/audit/legal-holds?tenant="+isoTenantB, "")
	if listedB.Code != http.StatusForbidden {
		t.Fatalf("A list ?tenant=B = %d, want 403: %s", listedB.Code, listedB.Body.String())
	}
	assertNoLeak(t, "A list ?tenant=B", listedB.Body.String(), leaks...)
	for _, query := range []string{"", "?tenant=" + isoTenantA} {
		rec := legalHoldRequestAs(s, http.MethodGet, "/api/v1/audit/legal-holds"+query, "")
		var body struct {
			Holds []*audit.LegalHold `json:"holds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("A list %q = %d (decode %v): %s", query, rec.Code, err, rec.Body.String())
		}
		if len(body.Holds) != 1 || body.Holds[0].ID != holdA.ID || body.Holds[0].Reason != isoMarkerA+" litigation" {
			t.Fatalf("A list %q = %+v, want only A's hold %s", query, body.Holds, holdA.ID)
		}
		assertNoLeak(t, "A list "+query, rec.Body.String(), leaks...)
	}

	afterB, err := s.legalHoldStore.GetHold(ctx, holdB.ID)
	if err != nil || !reflect.DeepEqual(afterB, beforeB) || !afterB.IsActive() {
		t.Fatalf("B hold after A's calls = %+v (err %v), want unchanged and active %+v", afterB, err, beforeB)
	}
	if ids, err := s.legalHoldStore.ListHolds(ctx, isoTenantB); err != nil || len(ids) != 1 || ids[0].ID != holdB.ID {
		t.Fatalf("B holds after A's calls = %+v (err %v), want only %s", ids, err, holdB.ID)
	}
}

func seedIsoJob(t *testing.T, s *server, tenant, marker string) string {
	t.Helper()
	ctx := context.Background()
	req := &pb.JobRequest{JobId: "job-" + marker, Topic: "job." + marker, TenantId: tenant, Labels: map[string]string{"marker": marker}}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("seed job meta %s: %v", tenant, err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("seed job request %s: %v", tenant, err)
	}
	if err := s.jobStore.SetState(ctx, req.JobId, model.JobStateRunning); err != nil {
		t.Fatalf("seed job state %s: %v", tenant, err)
	}
	return req.JobId
}

// TestTenantIsolationTelemetryExport: the telemetry export is install-level.
// It counts the seeded jobs of both tenants but carries no tenant id, job id
// or payload marker of either.
func TestTenantIsolationTelemetryExport(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	jobA, jobB := seedIsoJob(t, s, isoTenantA, isoMarkerA), seedIsoJob(t, s, isoTenantB, isoMarkerB)
	hashesA := seedIsoAuditEvents(t, s, isoTenantA, isoMarkerA)
	hashesB := seedIsoAuditEvents(t, s, isoTenantB, isoMarkerB)
	seedTelemetryCollector(t, s)

	rec := httptest.NewRecorder()
	s.handleGetTelemetryExport(rec, withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/export", nil), isoAdminA))
	var payload struct {
		Usage struct {
			JobsLast24h int64 `json:"jobs_last_24h"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("telemetry export = %d (decode %v): %s", rec.Code, err, rec.Body.String())
	}
	if payload.Usage.JobsLast24h < 2 {
		t.Fatalf("jobs_last_24h = %d, want the seeded jobs counted (>= 2)", payload.Usage.JobsLast24h)
	}
	leaks := append([]string{isoTenantA, isoTenantB, isoMarkerA, isoMarkerB, jobA, jobB}, append(hashesA, hashesB...)...)
	assertNoLeak(t, "telemetry export", rec.Body.String(), leaks...)
}

// TestTenantIsolationPolicyAuditList: GET /api/v1/policy/audit reads the
// install-wide cfg:system:policy_audit log, which also records per-job
// approval decisions. Tenant A may see its own job entries or be refused,
// but never reads tenant B's.
func TestTenantIsolationPolicyAuditList(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	ctx := context.Background()
	jobA, jobB := seedIsoJob(t, s, isoTenantA, isoMarkerA), seedIsoJob(t, s, isoTenantB, isoMarkerB)
	s.appendApprovalDecisionAudit(ctx, "approve", jobA, "job."+isoMarkerA, "admin-iso-a", "admin", "", "", isoMarkerA+" reason", "", "", "", "")
	s.appendApprovalDecisionAudit(ctx, "approve", jobB, "job."+isoMarkerB, "admin-iso-b", "admin", "", "", isoMarkerB+" reason", "", "", "", "")

	rec := httptest.NewRecorder()
	s.handleListPolicyAudit(rec, withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil), isoAdminA))
	switch rec.Code {
	case http.StatusOK:
		if !strings.Contains(rec.Body.String(), jobA) {
			t.Fatalf("A policy audit list omits A's own job entry: %s", rec.Body.String())
		}
	case http.StatusForbidden, http.StatusNotFound:
	default:
		t.Fatalf("A policy audit list = %d: %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, "A policy audit list", rec.Body.String(), jobB, isoMarkerB, "admin-iso-b")
}

// TestTenantIsolationPolicyAuditListScope: a job entry records the job's
// tenant, a submit entry the submit's tenant, and another entry its
// tenant-scoped writer's tenant. A tenant-scoped admin lists exactly its own
// tenant's entries and total counts only those; an entry with no tenant is
// listed only to a cross-tenant admin.
func TestTenantIsolationPolicyAuditListScope(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	ctx := context.Background()
	adminB := &auth.AuthContext{Tenant: isoTenantB, Role: "admin", PrincipalID: "admin-iso-b"}
	crossAdmin := &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "root-iso", AllowCrossTenant: true}
	asA, asCross := context.WithValue(ctx, auth.ContextKey{}, isoAdminA), context.WithValue(ctx, auth.ContextKey{}, crossAdmin)
	jobA, jobB := seedIsoJob(t, s, isoTenantA, isoMarkerA), seedIsoJob(t, s, isoTenantB, isoMarkerB)
	deniedB, bundleA := "job-"+isoMarkerB+"-denied", "bundle-"+isoMarkerA
	s.appendApprovalDecisionAudit(ctx, "approve", jobA, "job."+isoMarkerA, "admin-iso-a", "admin", "", "", isoMarkerA+" reason", "", "", "", "")
	s.appendApprovalDecisionAudit(asCross, "approve", jobB, "job."+isoMarkerB, "root-iso", "admin", "", "", isoMarkerB+" reason", "", "", "", "")
	s.appendSubmitSafetyDecisionAudit(ctx, "submit_denied", deniedB, isoTenantB, "job."+isoMarkerB, "admin-iso-b", "admin", "", "",
		isoMarkerB+" denied", submitPolicyDecision{Denied: true}, nil, "", "", "")
	s.appendAuditEntryNamed(asA, "publish", "bundle", bundleA, "", "admin-iso-a", "admin", isoMarkerA+" publish")
	s.appendAuditEntryNamed(asCross, "publish", "bundle", "bundle-cross-iso", "", "root-iso", "admin", "cross publish")
	legacy := normalizePolicyAuditEntry(policybundles.PolicyAuditEntry{Action: "approve", ResourceType: "job", ResourceID: "job-legacy-iso"})
	if err := s.configSvc.SetWithRetry(ctx, configsvc.Scope(policybundles.PolicyAuditScope), policybundles.PolicyAuditID, 1,
		func(doc *configsvc.Document) error { return prependPolicyAuditEntry(doc, legacy) }); err != nil {
		t.Fatalf("seed tenantless entry: %v", err)
	}

	hidden := []string{"bundle-cross-iso", "job-legacy-iso"}
	cases := []struct {
		name   string
		caller *auth.AuthContext
		want   []string
		leaks  []string
	}{
		{"A", isoAdminA, []string{jobA, bundleA}, append([]string{jobB, deniedB, isoMarkerB, isoTenantB, "admin-iso-b"}, hidden...)},
		{"B", adminB, []string{jobB, deniedB}, append([]string{jobA, bundleA, isoMarkerA, isoTenantA, "admin-iso-a"}, hidden...)},
		{"cross-tenant", crossAdmin, append([]string{jobA, jobB, deniedB, bundleA}, hidden...), nil},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.handleListPolicyAudit(rec, withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil), tc.caller))
		var resp struct {
			Items []policybundles.PolicyAuditEntry `json:"items"`
			Total int64                            `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); rec.Code != http.StatusOK || err != nil {
			t.Fatalf("%s policy audit list = %d (decode %v): %s", tc.name, rec.Code, err, rec.Body.String())
		}
		got := make([]string, 0, len(resp.Items))
		for _, item := range resp.Items {
			got = append(got, item.ResourceID)
		}
		sort.Strings(got)
		sort.Strings(tc.want)
		if !reflect.DeepEqual(got, tc.want) || resp.Total != int64(len(tc.want)) {
			t.Fatalf("%s policy audit list = %v (total %d), want %v", tc.name, got, resp.Total, tc.want)
		}
		assertNoLeak(t, tc.name+" policy audit list", rec.Body.String(), tc.leaks...)
	}
}
