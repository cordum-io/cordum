package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

var approvalActions = []string{"approve", "reject", "repair"}

func approvalHandlers(s *server) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"approve": s.handleApproveJob, "reject": s.handleRejectJob, "repair": s.handleRepairApproval}
}

func approvalCaseName(action string, missing bool) string {
	if missing {
		return action + "/tenant=missing"
	}
	return action + "/tenant=empty"
}

// seedApprovalAction seeds a job awaiting approval. For repair, a stored
// reject decision makes the job repairable.
func seedApprovalAction(t *testing.T, s *server, action string) string {
	t.Helper()
	id := seedApprovalJob(t, s, "")
	if action == "repair" {
		rec := model.ApprovalRecord{Decision: model.ApprovalDecisionReject, ApprovedBy: "bob"}
		if err := s.jobStore.SetApprovalRecord(context.Background(), id, rec); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// approvalWithoutTenantJob seeds a job for action whose job:meta tenant reads
// back empty: the field holds "" or, when missing is true, is absent.
func approvalWithoutTenantJob(t *testing.T, s *server, action string, missing bool) string {
	t.Helper()
	ctx := context.Background()
	id := seedApprovalAction(t, s, action)
	c := s.jobStore.Client()
	err := c.HSet(ctx, "job:meta:"+id, "tenant", "").Err()
	if missing {
		err = c.HDel(ctx, "job:meta:"+id, "tenant").Err()
	}
	if err != nil {
		t.Fatal(err)
	}
	if tenant, err := s.jobStore.GetTenant(ctx, id); tenant != "" || (missing != (err != nil)) {
		t.Fatalf("tenant read back %q, %v", tenant, err)
	}
	return id
}

func approvalActionRequest(action, id, tenant string) *http.Request {
	r := tenantObjectRequest(http.MethodPost, "/api/v1/approvals/"+id+"/"+action, tenant, id)
	if action == "repair" {
		r.Body = io.NopCloser(strings.NewReader(`{"apply":true}`))
	}
	return r
}

func approvalAuditCount(t *testing.T, s *server, id string) int {
	t.Helper()
	entries, err := s.loadPolicyAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if entry.ResourceID == id {
			n++
		}
	}
	return n
}

// assertApprovalUntouched checks that the job still awaits approval, nothing
// was published or audited, and no stored record changed since before.
func assertApprovalUntouched(t *testing.T, s *server, bus *stubBus, id string, before map[string]string, published int) {
	t.Helper()
	if state, err := s.jobStore.GetState(context.Background(), id); err != nil || state != model.JobStateApproval {
		t.Errorf("effect: job state %q, %v", state, err)
	}
	if n := len(bus.published) - published; n != 0 {
		t.Errorf("effect: published %d messages", n)
	}
	if n := approvalAuditCount(t, s, id); n != 0 {
		t.Errorf("effect: %d audit entries", n)
	}
	if after := tenantObjectSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Error("effect: stored records changed")
	}
}

func TestTenantIsolationGatewayApprovalWithoutTenant(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, action := range approvalActions {
			t.Run(approvalCaseName(action, missing), func(t *testing.T) {
				s, bus, _ := newTestGateway(t)
				s.auth = newBasicAuthForTest(t, nil)
				id := approvalWithoutTenantJob(t, s, action, missing)
				before, published := tenantObjectSnapshot(t, s), len(bus.published)

				rr := httptest.NewRecorder()
				approvalHandlers(s)[action](rr, approvalActionRequest(action, id, "tenant-a"))

				if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "job not found") {
					t.Errorf("status: %d %s", rr.Code, rr.Body.String())
				}
				assertApprovalUntouched(t, s, bus, id, before, published)
			})
		}
	}
}

func TestTenantIsolationGatewayApprovalCrossTenant(t *testing.T) {
	wantState := map[string]model.JobState{
		"approve": model.JobStatePending, "reject": model.JobStateDenied, "repair": model.JobStateDenied}
	for _, caller := range []string{"cross-tenant", "no-auth"} {
		for _, missing := range []bool{false, true} {
			for _, action := range approvalActions {
				t.Run(caller+"/"+approvalCaseName(action, missing), func(t *testing.T) {
					s, bus, _ := newTestGateway(t)
					id := approvalWithoutTenantJob(t, s, action, missing)
					r := approvalActionRequest(action, id, "tenant-a")
					if caller == "cross-tenant" {
						s.auth = newBasicAuthForTest(t, nil)
						auth.FromRequest(r).AllowCrossTenant = true
					}

					rr := httptest.NewRecorder()
					approvalHandlers(s)[action](rr, r)

					if rr.Code != http.StatusOK {
						t.Errorf("status: %d %s", rr.Code, rr.Body.String())
					}
					if state, err := s.jobStore.GetState(context.Background(), id); err != nil || state != wantState[action] {
						t.Errorf("effect: job state %q, %v", state, err)
					}
					if n := len(bus.published); n != 1 {
						t.Errorf("effect: published %d messages, want 1", n)
					}
				})
			}
		}
	}
}

func TestTenantIsolationGatewayApprovalLookupError(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	id := seedApprovalAction(t, s, "repair")
	fault := &approvalReadFault{}
	s.jobStore.Client().AddHook(fault)
	before, published := tenantObjectSnapshot(t, s), len(bus.published)

	fault.arm(tenantCmd(id, ""))
	rr := httptest.NewRecorder()
	s.handleRepairApproval(rr, approvalActionRequest("repair", id, "default"))
	if fired := fault.disarm(); fired != 1 {
		t.Errorf("fault fired %d times, want 1", fired)
	}

	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "tenant lookup unavailable") {
		t.Errorf("status: %d %s", rr.Code, rr.Body.String())
	}
	assertApprovalUntouched(t, s, bus, id, before, published)
}
