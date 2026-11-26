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

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Tenant isolation of the policy audit list, on the helpers of the PR1-03E
// export cases. Each case seeds tenant B, acts as tenant A (a tenant-scoped
// admin that knows B's ids) and checks the returned bytes by ids and markers.
const (
	isoTenantA = "tenant-iso-a"
	isoTenantB = "tenant-iso-b"
	isoMarkerA = "MARKER-TENANT-ISO-A"
	isoMarkerB = "MARKER-TENANT-ISO-B"
)

var isoAdminA = &auth.AuthContext{Tenant: isoTenantA, Role: "admin", PrincipalID: "admin-iso-a"}

func assertNoLeak(t *testing.T, what, body string, leaks ...string) {
	t.Helper()
	for _, foreign := range leaks {
		if foreign != "" && strings.Contains(body, foreign) {
			t.Fatalf("%s carries foreign tenant data %q: %s", what, foreign, body)
		}
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

// submitIsoJob submits a job for marker through POST /api/v1/jobs as caller
// and returns the job id.
func submitIsoJob(t *testing.T, s *server, caller *auth.AuthContext, marker string) string {
	t.Helper()
	body := mustJSON(map[string]string{"prompt": marker, "topic": "job." + marker, "tenant_id": caller.Tenant})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body)), caller)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	var resp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); rec.Code != http.StatusOK || err != nil || resp.JobID == "" {
		t.Fatalf("submit as %s = %d (decode %v): %s", caller.Tenant, rec.Code, err, rec.Body.String())
	}
	return resp.JobID
}

// listIsoPolicyAudit serves GET /api/v1/policy/audit for r and returns the
// listed resource ids sorted, the reported total and the body.
func listIsoPolicyAudit(t *testing.T, s *server, r *http.Request) ([]string, int64, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleListPolicyAudit(rec, r)
	var resp struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
		Total int64                            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("policy audit list = %d (decode %v): %s", rec.Code, err, rec.Body.String())
	}
	got := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		got = append(got, item.ResourceID)
	}
	sort.Strings(got)
	return got, resp.Total, rec.Body.String()
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
// listed only to an admin whose access spans all tenants, and a request with
// no auth context lists nothing.
func TestTenantIsolationPolicyAuditListScope(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	ctx := context.Background()
	adminB := &auth.AuthContext{Tenant: isoTenantB, Role: "admin", PrincipalID: "admin-iso-b"}
	crossAdmin := &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "root-iso", AllowCrossTenant: true}
	asA, asCross := context.WithValue(ctx, auth.ContextKey{}, isoAdminA), context.WithValue(ctx, auth.ContextKey{}, crossAdmin)
	jobA, jobB := seedIsoJob(t, s, isoTenantA, isoMarkerA), seedIsoJob(t, s, isoTenantB, isoMarkerB)
	bundleA := "bundle-" + isoMarkerA
	s.appendApprovalDecisionAudit(ctx, "approve", jobA, "job."+isoMarkerA, "admin-iso-a", "admin", "", "", isoMarkerA+" reason", "", "", "", "")
	s.appendApprovalDecisionAudit(asCross, "approve", jobB, "job."+isoMarkerB, "root-iso", "admin", "", "", isoMarkerB+" reason", "", "", "", "")
	submitB := submitIsoJob(t, s, adminB, isoMarkerB)
	s.appendAuditEntryNamed(asA, "publish", "bundle", bundleA, "", "admin-iso-a", "admin", isoMarkerA+" publish")
	s.appendAuditEntryNamed(asCross, "publish", "bundle", "bundle-cross-iso", "", "root-iso", "admin", "cross publish")
	// An entry with no tenant, as stored before entries recorded one: no auth
	// context and a job id the store lacks.
	if err := s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{Action: "approve", ResourceType: "job", ResourceID: "job-legacy-iso"}); err != nil {
		t.Fatalf("seed tenantless entry: %v", err)
	}

	hidden := []string{"bundle-cross-iso", "job-legacy-iso"}
	all := append([]string{jobA, jobB, submitB, bundleA}, hidden...)
	cases := []struct {
		name   string
		caller *auth.AuthContext
		want   []string
		leaks  []string
	}{
		{"A", isoAdminA, []string{jobA, bundleA}, append([]string{jobB, submitB, isoMarkerB, isoTenantB, "admin-iso-b"}, hidden...)},
		{"B", adminB, []string{jobB, submitB}, append([]string{jobA, bundleA, isoMarkerA, isoTenantA, "admin-iso-a"}, hidden...)},
		{"all-tenants", crossAdmin, all, nil},
		// Auth stays on, and publicPathAuth admits the role check, so the list
		// alone decides what a request with no auth context reads.
		{"no-auth", nil, []string{}, all},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil)
			if tc.caller == nil {
				s.auth = &publicPathAuth{}
			} else {
				req = withAuth(req, tc.caller)
			}
			got, total, body := listIsoPolicyAudit(t, s, req)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) || total != int64(len(tc.want)) {
				t.Fatalf("policy audit list = %v (total %d), want %v", got, total, tc.want)
			}
			assertNoLeak(t, tc.name+" policy audit list", body, tc.leaks...)
		})
	}
}
