package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

var resolvedApprovalStates = map[string]model.JobState{
	"approved": model.JobStatePending, "rejected": model.JobStateDenied, "expired": model.JobStateTimeout}

// resolvedApprovalJob seeds a tenant-b job that bob-b resolved (approved or
// rejected) or that expired, and returns its id and trace id.
func resolvedApprovalJob(t *testing.T, s *server, resolution string) (string, string) {
	t.Helper()
	ctx := context.Background()
	id := seedApprovalJob(t, s, "")
	traceID := "trace-b-" + id
	if err := s.jobStore.Client().HSet(ctx, "job:meta:"+id, "tenant", "tenant-b").Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.AddJobToTrace(ctx, traceID, id); err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.SetState(ctx, id, resolvedApprovalStates[resolution]); err != nil {
		t.Fatal(err)
	}
	decision := map[string]model.ApprovalDecision{
		"approved": model.ApprovalDecisionApprove, "rejected": model.ApprovalDecisionReject}[resolution]
	if decision != "" {
		rec := model.ApprovalRecord{Decision: decision, ApprovedBy: "bob-b", ApprovedAt: 1}
		if err := s.jobStore.SetApprovalRecord(ctx, id, rec); err != nil {
			t.Fatal(err)
		}
	}
	return id, traceID
}

func TestTenantIsolationGatewayApprovalResolvedJob(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		for _, resolution := range []string{"approved", "rejected", "expired"} {
			t.Run(action+"/"+resolution, func(t *testing.T) {
				s, bus, _ := newTestGateway(t)
				s.auth = newBasicAuthForTest(t, nil)
				id, traceID := resolvedApprovalJob(t, s, resolution)
				before, published, audits := tenantObjectSnapshot(t, s), len(bus.published), approvalAuditCount(t, s, id)

				rr := httptest.NewRecorder()
				approvalHandlers(s)[action](rr, approvalActionRequest(action, id, "tenant-a"))

				body := rr.Body.String()
				if rr.Code != http.StatusForbidden || !strings.Contains(body, "tenant access denied") {
					t.Errorf("status: %d %s", rr.Code, body)
				}
				for _, leak := range []string{"already_", "bob-b", traceID} {
					if strings.Contains(body, leak) {
						t.Errorf("status: body carries %q: %s", leak, body)
					}
				}
				if n := len(bus.published) - published; n != 0 {
					t.Errorf("effect: published %d messages", n)
				}
				if n := approvalAuditCount(t, s, id) - audits; n != 0 {
					t.Errorf("effect: %d audit entries", n)
				}
				if after := tenantObjectSnapshot(t, s); !reflect.DeepEqual(before, after) {
					t.Error("effect: stored records changed")
				}
			})
		}
	}

	controls := []struct{ caller, action, resolution, want string }{
		{"owner", "approve", "approved", "already_approved"},
		{"owner", "reject", "rejected", "already_rejected"},
		{"cross-tenant", "approve", "approved", "already_approved"},
		{"cross-tenant", "reject", "rejected", "already_rejected"},
	}
	for _, c := range controls {
		t.Run(c.caller+"/"+c.action+"/"+c.resolution, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			id, traceID := resolvedApprovalJob(t, s, c.resolution)
			r := approvalActionRequest(c.action, id, "tenant-b")
			if c.caller == "cross-tenant" {
				r = approvalActionRequest(c.action, id, "tenant-a")
				auth.FromRequest(r).AllowCrossTenant = true
			}

			rr := httptest.NewRecorder()
			approvalHandlers(s)[c.action](rr, r)

			body := rr.Body.String()
			if rr.Code != http.StatusOK || !strings.Contains(body, c.want) || !strings.Contains(body, "bob-b") {
				t.Errorf("status: %d %s", rr.Code, body)
			}
			if c.action == "approve" && !strings.Contains(body, traceID) {
				t.Errorf("status: body lacks trace id %q: %s", traceID, body)
			}
		})
	}
}
