package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/redis/go-redis/v9"
)

// permBindingState is a forced license state; gated ones refuse at break-glass.
type permBindingState struct {
	name, status string
	plan         licensing.Plan
	rbac, gated  bool
}

var (
	permBindingValid   = permBindingState{"valid", "active", licensing.PlanEnterprise, true, false}
	permBindingMissing = permBindingState{"missing", "active", licensing.PlanCommunity, false, false}
	permBindingAll     = []permBindingState{
		permBindingValid,
		{"degraded", "fallback", licensing.PlanCommunity, false, true},
		{"invalid", "fallback", licensing.PlanCommunity, false, true},
		permBindingMissing,
	}
	permBindingUngated = []permBindingState{permBindingValid, permBindingMissing}
)

// TestPermissionChecksBindWithoutRBAC pins the built-in role map as the answer to every
// permission check without the RBAC entitlement. Refusals check effect, then status.
func TestPermissionChecksBindWithoutRBAC(t *testing.T) {
	t.Run("administration", permBindingConfigWrite)
	t.Run("jobs_list", permBindingJobsList)
	t.Run("protected_operations", permBindingSubmit)
	t.Run("approvals", permBindingApprove)
	t.Run("policy_evaluation", permBindingPolicyEvaluate)
	t.Run("audit", permBindingAuditRead)
	t.Run("runner", permBindingDLQRetry)
	t.Run("edge", permBindingEdgeWrite)
	t.Run("silent_check", permBindingSilentCheck)
	t.Run("positive_controls", permBindingControls)
}

func permBindingEach(t *testing.T, states []permBindingState, fn func(*testing.T, permBindingState)) {
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) { fn(t, st) })
	}
}

func permBindingGateway(t *testing.T, st permBindingState) (*server, *stubBus, *stubSafetyClient) {
	t.Helper()
	s, bus, safety := newTestGateway(t)
	s.tenant = "default"
	enableTestAuth(s)
	s.entitlements.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
	if got := auth.RBACEntitled(s.currentEntitlements()); got != st.rbac {
		t.Fatalf("rbac entitled = %t, want %t", got, st.rbac)
	}
	return s, bus, safety
}

// permBindingRBACUserRole turns the RBAC entitlement on, with newTestGateway's Community
// limits, and stores the user role holding permissions.
func permBindingRBACUserRole(t *testing.T, s *server, permissions ...string) {
	t.Helper()
	setTestEntitlements(t, s, licensing.PlanCommunity, func(e *licensing.Entitlements) {
		e.MaxPolicyBundles = licensing.Unlimited
		e.RBAC = true
	})
	putTestRole(t, s, "user", permissions...)
}

func permBindingRequest(method, target, body, role string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	return withAuth(req, &auth.AuthContext{Tenant: "default", Role: role, PrincipalID: role + "-1"})
}

func permBindingServe(h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func permBindingStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func permBindingNoEffect(t *testing.T, bus *stubBus, safety *stubSafetyClient) {
	t.Helper()
	safety.mu.Lock()
	defer safety.mu.Unlock()
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if safety.lastReq != nil || len(bus.published) != 0 {
		t.Fatalf("refused request reached the kernel: %t, published %d messages", safety.lastReq != nil, len(bus.published))
	}
}

func permBindingConfigWrite(t *testing.T) {
	permBindingEach(t, permBindingAll, func(t *testing.T, st permBindingState) {
		s, bus, safety := permBindingGateway(t, st)
		rec := permBindingSetConfig(s, "operator")
		if permBindingConfigWritten(t, s) {
			t.Fatalf("refused config write stored the document: %d %s", rec.Code, rec.Body.String())
		}
		permBindingNoEffect(t, bus, safety)
		if !st.gated {
			permBindingStatus(t, rec, http.StatusForbidden)
		} else if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"license.degraded"`) {
			t.Fatalf("status = %d, want the 503 license.degraded refusal: %s", rec.Code, rec.Body.String())
		}
	})
}

func permBindingSetConfig(s *server, role string) *httptest.ResponseRecorder {
	return permBindingServe(s.handleSetConfig, permBindingRequest(http.MethodPost, "/api/v1/config", `{"permission_binding":"on"}`, role))
}

func permBindingConfigWritten(t *testing.T, s *server) bool {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("read system config: %v", err)
	}
	return doc != nil && doc.Data["permission_binding"] == "on"
}

func permBindingJobsList(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, _, _ := permBindingGateway(t, st)
		jobID := seedApprovalJob(t, s, "")
		putTestRole(t, s, "permbinding-writer", auth.PermJobsWrite)
		putTestRole(t, s, "permbinding-reader", auth.PermJobsRead)
		reader := http.StatusForbidden
		if st.rbac {
			reader = http.StatusOK
		}
		list := func(role string, want int) {
			t.Run(role, func(t *testing.T) {
				rec := permBindingServe(s.handleListJobs, permBindingRequest(http.MethodGet, "/api/v1/jobs", "", role))
				if listed := strings.Contains(rec.Body.String(), jobID); listed != (want == http.StatusOK) {
					t.Fatalf("job listed = %t, want %t: %d %s", listed, want == http.StatusOK, rec.Code, rec.Body.String())
				}
				permBindingStatus(t, rec, want)
			})
		}
		list("permbinding-writer", http.StatusForbidden)
		list("permbinding-reader", reader)
		list("viewer", http.StatusOK)
	})
}

func permBindingSubmit(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, bus, safety := permBindingGateway(t, st)
		rec := permBindingSubmitJob(s, "user")
		permBindingNoEffect(t, bus, safety)
		if keys, err := s.jobStore.Client().Keys(context.Background(), "job:*").Result(); err != nil || len(keys) != 0 {
			t.Fatalf("refused submit stored %v (err %v)", keys, err)
		}
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingSubmitJob(s *server, role string) *httptest.ResponseRecorder {
	return permBindingServe(s.handleSubmitJobHTTP, permBindingRequest(http.MethodPost, "/api/v1/jobs", `{"prompt":"hello","topic":"job.test"}`, role))
}

func permBindingApprove(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, bus, safety := permBindingGateway(t, st)
		jobID := seedApprovalJob(t, s, "")
		req := permBindingRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", `{"reason":"permission binding"}`, "secops")
		req.SetPathValue("job_id", jobID)
		rec := permBindingServe(s.handleApproveJob, req)
		if state, err := s.jobStore.GetState(context.Background(), jobID); err != nil || state != model.JobStateApproval {
			t.Fatalf("job state = %s (err %v), want %s: %d %s", state, err, model.JobStateApproval, rec.Code, rec.Body.String())
		}
		permBindingNoEffect(t, bus, safety)
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingPolicyEvaluate(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, bus, safety := permBindingGateway(t, st)
		rec := permBindingServe(s.handlePolicyEvaluate, permBindingRequest(http.MethodPost, "/api/v1/policy/evaluate", `{"topic":"job.test"}`, "operator"))
		permBindingNoEffect(t, bus, safety)
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingAuditRead(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, _, _ := permBindingGateway(t, st)
		seedAuditEvents(t, s, "default", []audit.SIEMEvent{
			{EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, Action: "permbinding-marker"},
		})
		rec := permBindingServe(s.handleListAuditEvents, permBindingRequest(http.MethodGet, "/api/v1/audit/events?tenant=default", "", "secops"))
		if strings.Contains(rec.Body.String(), "permbinding-marker") {
			t.Fatalf("refused audit read returned the event: %d %s", rec.Code, rec.Body.String())
		}
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingDLQRetry(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, bus, safety := permBindingGateway(t, st)
		const jobID = "permbinding-dlq-job"
		if err := s.dlqStore.Add(context.Background(), store.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("seed dlq entry: %v", err)
		}
		req := permBindingRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", "", "operator")
		req.SetPathValue("job_id", jobID)
		rec := permBindingServe(s.handleRetryDLQ, req)
		if _, err := s.dlqStore.Get(context.Background(), jobID); err != nil {
			t.Fatalf("refused retry removed the dlq entry (err %v): %d %s", err, rec.Code, rec.Body.String())
		}
		permBindingNoEffect(t, bus, safety)
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingEdgeWrite(t *testing.T) {
	permBindingEach(t, permBindingAll, func(t *testing.T, st permBindingState) {
		s, _, _ := permBindingGateway(t, st)
		rec := permBindingCreateUpstream(s, "operator")
		if permBindingUpstreamStored(t, s) {
			t.Fatalf("refused edge write stored the upstream: %d %s", rec.Code, rec.Body.String())
		}
		permBindingStatus(t, rec, http.StatusForbidden)
	})
}

func permBindingCreateUpstream(s *server, role string) *httptest.ResponseRecorder {
	return permBindingServe(s.handleCreateMCPUpstream, permBindingRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams",
		`{"name":"permbinding-tools","transport":"stdio","command":["permbinding-mcp"],"risk":"low","enabled":true}`, role))
}

func permBindingUpstreamStored(t *testing.T, s *server) bool {
	t.Helper()
	_, found, err := edgecore.NewRedisMCPUpstreamRegistryFromClient(s.jobStore.Client()).Get(context.Background(), "default", "permbinding-tools")
	if err != nil {
		t.Fatalf("read mcp upstream: %v", err)
	}
	return found
}

func permBindingSilentCheck(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		s, _, _ := permBindingGateway(t, st)
		putTestRole(t, s, "permbinding-writer", auth.PermJobsWrite)
		check := func(role string) bool {
			return s.hasPermissionSilent(permBindingRequest(http.MethodGet, "/api/v1/copilot/sessions/s-1", "", role), auth.PermGovernanceRead)
		}
		if st.name == permBindingMissing.name && !check("viewer") {
			t.Fatal("viewer failed the silent governance.read check")
		}
		if check("permbinding-writer") {
			t.Fatal("custom role without governance.read passed the silent check")
		}
	})
}

// permBindingControls gives each control its own gateway, so it always runs.
func permBindingControls(t *testing.T) {
	permBindingEach(t, permBindingUngated, func(t *testing.T, st permBindingState) {
		t.Run("admin_config_write", func(t *testing.T) {
			s, _, _ := permBindingGateway(t, st)
			permBindingStatus(t, permBindingSetConfig(s, "admin"), http.StatusNoContent)
			if !permBindingConfigWritten(t, s) {
				t.Fatal("admin config write was not stored")
			}
		})
		t.Run("operator_submit", func(t *testing.T) {
			s, bus, _ := permBindingGateway(t, st)
			permBindingStatus(t, permBindingSubmitJob(s, "operator"), http.StatusOK)
			bus.mu.Lock()
			defer bus.mu.Unlock()
			if len(bus.published) != 1 || bus.published[0].subject != capsdk.SubjectSubmit {
				t.Fatalf("operator submit published %d messages, want one %s", len(bus.published), capsdk.SubjectSubmit)
			}
		})
		t.Run("admin_edge_write", func(t *testing.T) {
			s, _, _ := permBindingGateway(t, st)
			permBindingStatus(t, permBindingCreateUpstream(s, "admin"), http.StatusCreated)
			if !permBindingUpstreamStored(t, s) {
				t.Fatal("admin edge write was not stored")
			}
		})
	})
}
