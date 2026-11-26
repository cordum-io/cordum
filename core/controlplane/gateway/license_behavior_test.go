package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// Gateway cells of docs/contract/license-behavior.md. Every expectation is a
// literal copied from the contract's States table or TestLicenseBreakGlassMatrix:
// jobs.write and jobs.approve pass the license gate only where break-glass is
// valid, and refusal is the 503 code the gate writes. None is computed by the
// gate under test.
type licenseBehaviorState struct {
	name    string
	plan    licensing.Plan
	status  string
	gate    licensing.BreakGlassState
	rbac    bool
	refusal string // break-glass code refusing jobs.write/jobs.approve; "" = allowed
}

var licenseBehaviorStates = []licenseBehaviorState{
	{"valid", licensing.PlanEnterprise, "active", licensing.BreakGlassStateValid, true, ""},
	{"warning", licensing.PlanEnterprise, "warning", licensing.BreakGlassStateValid, true, ""},
	{"grace", licensing.PlanEnterprise, "grace", licensing.BreakGlassStateGrace, true, "license.grace_restricted"},
	{"degraded", licensing.PlanCommunity, "fallback", licensing.BreakGlassStateInvalid, false, "license.degraded"},
	{"invalid", licensing.PlanCommunity, "fallback", licensing.BreakGlassStateInvalid, false, "license.degraded"},
	{"missing", licensing.PlanCommunity, "active", licensing.BreakGlassStateValid, false, ""},
}

func licenseBehaviorStateNamed(t *testing.T, name string) licenseBehaviorState {
	t.Helper()
	for _, st := range licenseBehaviorStates {
		if st.name == name {
			return st
		}
	}
	t.Fatalf("unknown license state %q", name)
	return licenseBehaviorState{}
}

// licenseBehaviorGateway forces st on the gateway's resolver and checks it
// reaches both the break-glass gate and the permission checker.
func licenseBehaviorGateway(t *testing.T, st licenseBehaviorState) (*server, *stubBus, *stubSafetyClient) {
	t.Helper()
	s, bus, safety := newTestGateway(t)
	s.tenant = "default"
	enableTestAuth(s)
	s.entitlements.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
	if got := s.currentBreakGlassState(); got != st.gate {
		t.Fatalf("break-glass state = %q, want %q", got, st.gate)
	}
	if got := auth.RBACEntitled(s.currentEntitlements()); got != st.rbac {
		t.Fatalf("rbac entitled = %t, want %t", got, st.rbac)
	}
	return s, bus, safety
}

func licenseBehaviorRequest(method, target, body, role string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	return withAuth(req, &auth.AuthContext{Tenant: "default", Role: role, PrincipalID: role + "-1"})
}

func licenseBehaviorSubmit(s *server, role string) (*httptest.ResponseRecorder, string) {
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, licenseBehaviorRequest(http.MethodPost, "/api/v1/jobs", `{"prompt":"hello","topic":"job.test"}`, role))
	var body struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body.JobID
}

func licenseBehaviorDecide(s *server, action, jobID, role string) *httptest.ResponseRecorder {
	req := licenseBehaviorRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/"+action, `{"reason":"license behavior"}`, role)
	req.SetPathValue("job_id", jobID)
	rec := httptest.NewRecorder()
	if action == "approve" {
		s.handleApproveJob(rec, req)
	} else {
		s.handleRejectJob(rec, req)
	}
	return rec
}

func licenseBehaviorSubjects(bus *stubBus) []string {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	subjects := make([]string, 0, len(bus.published))
	for _, msg := range bus.published {
		subjects = append(subjects, msg.subject)
	}
	return subjects
}

func licenseBehaviorCount(subjects []string, subject string) int {
	n := 0
	for _, got := range subjects {
		if got == subject {
			n++
		}
	}
	return n
}

func licenseBehaviorKernelCalled(safety *stubSafetyClient) bool {
	safety.mu.Lock()
	defer safety.mu.Unlock()
	return safety.lastReq != nil
}

func licenseBehaviorRedis(t *testing.T, s *server) *redis.Client {
	t.Helper()
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", s.jobStore.Client())
	}
	return client
}

func licenseBehaviorKeys(t *testing.T, s *server, pattern string) []string {
	t.Helper()
	keys, err := licenseBehaviorRedis(t, s).Keys(context.Background(), pattern).Result()
	if err != nil {
		t.Fatalf("list %s keys: %v", pattern, err)
	}
	return keys
}

func licenseBehaviorJobState(t *testing.T, s *server, jobID string) model.JobState {
	t.Helper()
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("job %s state: %v", jobID, err)
	}
	return state
}

// licenseBehaviorBreakGlass asserts the license gate's own refusal.
func licenseBehaviorBreakGlass(t *testing.T, rec *httptest.ResponseRecorder, st licenseBehaviorState) {
	t.Helper()
	var body struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Code != st.refusal || body.State != string(st.gate) {
		t.Fatalf("status = %d body = %s, want 503 %s state %s", rec.Code, rec.Body.String(), st.refusal, st.gate)
	}
}

// licenseBehaviorNoEffect asserts no kernel decision and nothing published.
func licenseBehaviorNoEffect(t *testing.T, bus *stubBus, safety *stubSafetyClient) {
	t.Helper()
	if licenseBehaviorKernelCalled(safety) {
		t.Fatal("refused request reached the safety kernel")
	}
	if subjects := licenseBehaviorSubjects(bus); len(subjects) != 0 {
		t.Fatalf("refused request published %v", subjects)
	}
}

// licenseBehaviorRefusedSubmit asserts a refused submit stored no job.
func licenseBehaviorRefusedSubmit(t *testing.T, s *server, bus *stubBus, safety *stubSafetyClient) {
	t.Helper()
	licenseBehaviorNoEffect(t, bus, safety)
	if keys := licenseBehaviorKeys(t, s, "job:*"); len(keys) != 0 {
		t.Fatalf("refused submit stored %v", keys)
	}
}

func licenseBehaviorDeny(safety *stubSafetyClient, snapshot string) {
	safety.setResponse(&pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "license behavior deny", PolicySnapshot: snapshot})
}

func TestLicenseBehaviorGateway_ProtectedOperations(t *testing.T) {
	for _, st := range licenseBehaviorStates {
		t.Run(st.name, func(t *testing.T) {
			t.Run("kernel_deny", func(t *testing.T) {
				s, bus, safety := licenseBehaviorGateway(t, st)
				licenseBehaviorDeny(safety, "snap-test")
				rec, jobID := licenseBehaviorSubmit(s, "admin")
				if st.refusal != "" {
					licenseBehaviorBreakGlass(t, rec, st)
					licenseBehaviorRefusedSubmit(t, s, bus, safety)
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
				}
				if n := licenseBehaviorCount(licenseBehaviorSubjects(bus), capsdk.SubjectSubmit); n != 0 {
					t.Fatalf("denied submit published %d %s packets", n, capsdk.SubjectSubmit)
				}
				if got := licenseBehaviorJobState(t, s, jobID); got != model.JobStateDenied {
					t.Fatalf("denied job state = %s, want %s", got, model.JobStateDenied)
				}
			})
			t.Run("kernel_allow", func(t *testing.T) {
				s, bus, safety := licenseBehaviorGateway(t, st)
				rec, jobID := licenseBehaviorSubmit(s, "admin")
				if st.refusal != "" {
					licenseBehaviorBreakGlass(t, rec, st)
					licenseBehaviorRefusedSubmit(t, s, bus, safety)
					return
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
				}
				if subjects := licenseBehaviorSubjects(bus); len(subjects) != 1 || subjects[0] != capsdk.SubjectSubmit {
					t.Fatalf("published %v, want exactly one %s", subjects, capsdk.SubjectSubmit)
				}
				if got := licenseBehaviorJobState(t, s, jobID); got != model.JobStatePending {
					t.Fatalf("allowed job state = %s, want %s", got, model.JobStatePending)
				}
			})
		})
	}
}

// licenseBehaviorAudit returns the decisions of the policy audit entries
// recorded for action on resourceID.
func licenseBehaviorAudit(t *testing.T, s *server, action, resourceID string) []string {
	t.Helper()
	entries, err := s.loadPolicyAudit(context.Background())
	if err != nil {
		t.Fatalf("loadPolicyAudit: %v", err)
	}
	decisions := []string{}
	for _, entry := range entries {
		if entry.Action == action && entry.ResourceID == resourceID {
			decisions = append(decisions, entry.Decision)
		}
	}
	return decisions
}

func TestLicenseBehaviorGateway_Audit(t *testing.T) {
	for _, st := range licenseBehaviorStates {
		t.Run(st.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, action, decision string
				kernel                 pb.DecisionType
			}{
				{"deny", "submit_denied", "deny", pb.DecisionType_DECISION_TYPE_DENY},
				{"allow", "submit", "allow", pb.DecisionType_DECISION_TYPE_ALLOW},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s, bus, safety := licenseBehaviorGateway(t, st)
					safety.setResponse(&pb.PolicyCheckResponse{Decision: tc.kernel, Reason: "license behavior", PolicySnapshot: "snap-test"})
					rec, jobID := licenseBehaviorSubmit(s, "admin")
					if st.refusal != "" {
						licenseBehaviorBreakGlass(t, rec, st)
						licenseBehaviorRefusedSubmit(t, s, bus, safety)
						if got := licenseBehaviorAudit(t, s, "submit_denied", ""); len(got) != 1 {
							t.Fatalf("license refusal audit entries = %v, want one", got)
						}
						return
					}
					if !licenseBehaviorKernelCalled(safety) {
						t.Fatal("submit made no kernel decision")
					}
					if got := licenseBehaviorAudit(t, s, tc.action, jobID); len(got) != 1 || got[0] != tc.decision {
						t.Fatalf("%s audit decisions for %s = %v, want [%s]", tc.action, jobID, got, tc.decision)
					}
				})
			}
		})
	}
}

func TestLicenseBehaviorGateway_Approvals(t *testing.T) {
	for _, st := range licenseBehaviorStates {
		t.Run(st.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, action string
				policyDeny   bool
				code         int
				state        model.JobState
				submits      int
			}{
				{"approve", "approve", false, http.StatusOK, model.JobStatePending, 1},
				{"reject", "reject", false, http.StatusOK, model.JobStateDenied, 0},
				{"approve_after_policy_deny", "approve", true, http.StatusConflict, model.JobStateApproval, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s, bus, safety := licenseBehaviorGateway(t, st)
					jobID := seedApprovalJob(t, s, "")
					if tc.policyDeny {
						safety.setSnapshots([]string{"snap-changed"})
						licenseBehaviorDeny(safety, "snap-changed")
					}
					rec := licenseBehaviorDecide(s, tc.action, jobID, "admin")
					if st.refusal != "" {
						licenseBehaviorBreakGlass(t, rec, st)
						licenseBehaviorNoEffect(t, bus, safety)
						if got := licenseBehaviorJobState(t, s, jobID); got != model.JobStateApproval {
							t.Fatalf("job state = %s, want %s", got, model.JobStateApproval)
						}
						return
					}
					if rec.Code != tc.code {
						t.Fatalf("status = %d, want %d: %s", rec.Code, tc.code, rec.Body.String())
					}
					if n := licenseBehaviorCount(licenseBehaviorSubjects(bus), capsdk.SubjectSubmit); n != tc.submits {
						t.Fatalf("%s packets = %d, want %d", capsdk.SubjectSubmit, n, tc.submits)
					}
					if got := licenseBehaviorJobState(t, s, jobID); got != tc.state {
						t.Fatalf("job state = %s, want %s", got, tc.state)
					}
				})
			}
			t.Run("submit_requires_approval", func(t *testing.T) {
				s, bus, safety := licenseBehaviorGateway(t, st)
				safety.setResponse(&pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, Reason: "license behavior approval", ApprovalRequired: true, PolicySnapshot: "snap-test"})
				rec, jobID := licenseBehaviorSubmit(s, "admin")
				if st.refusal != "" {
					licenseBehaviorBreakGlass(t, rec, st)
					licenseBehaviorRefusedSubmit(t, s, bus, safety)
					return
				}
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "approval_required") {
					t.Fatalf("status = %d, want 200 approval_required: %s", rec.Code, rec.Body.String())
				}
				if subjects := licenseBehaviorSubjects(bus); len(subjects) != 0 {
					t.Fatalf("approval-gated submit published %v", subjects)
				}
				if got := licenseBehaviorJobState(t, s, jobID); got != model.JobStateApproval {
					t.Fatalf("job state = %s, want %s", got, model.JobStateApproval)
				}
			})
		})
	}
}

func TestLicenseBehaviorGateway_Administration(t *testing.T) {
	for _, st := range licenseBehaviorStates {
		t.Run(st.name, func(t *testing.T) {
			t.Run("license_rotate", func(t *testing.T) {
				s, _, _ := licenseBehaviorGateway(t, st)
				installReloadableLicense(t, licensing.Claims{
					OrgID:     "org-license-behavior",
					LicenseID: "lic-license-behavior",
					Plan:      string(licensing.PlanEnterprise),
					ExpiresAt: time.Now().UTC().Add(90 * 24 * time.Hour).Format(time.RFC3339),
				})
				rec := httptest.NewRecorder()
				s.handleReloadLicense(rec, licenseBehaviorRequest(http.MethodPost, "/api/v1/license/reload", "", "admin"))
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reloaded"`) {
					t.Fatalf("license rotate status = %d, want 200 reloaded: %s", rec.Code, rec.Body.String())
				}
			})
			t.Run("admin_write", func(t *testing.T) {
				s, bus, _ := licenseBehaviorGateway(t, st)
				rec := licenseBehaviorSetConfig(s, "admin")
				written := licenseBehaviorConfigWritten(t, s)
				if st.refusal == "" {
					if rec.Code != http.StatusNoContent || !written {
						t.Fatalf("admin write status = %d written = %t, want 204 and written: %s", rec.Code, written, rec.Body.String())
					}
					return
				}
				licenseBehaviorBreakGlass(t, rec, st)
				if written {
					t.Fatal("refused admin write changed the config")
				}
				if subjects := licenseBehaviorSubjects(bus); len(subjects) != 0 {
					t.Fatalf("refused admin write published %v", subjects)
				}
			})
		})
	}
}

func licenseBehaviorSetConfig(s *server, role string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, licenseBehaviorRequest(http.MethodPost, "/api/v1/config", `{"license_behavior_probe":"on"}`, role))
	return rec
}

func licenseBehaviorConfigWritten(t *testing.T, s *server) bool {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("read system config: %v", err)
	}
	return doc != nil && doc.Data["license_behavior_probe"] == "on"
}

func TestLicenseBehaviorGateway_RBACLoss(t *testing.T) {
	// license-reader holds only jobs.read and the built-in operator role holds
	// no roles.write, so RBAC denies each its permission here; losing the RBAC
	// entitlement must not grant any of them.
	for _, name := range []string{"valid", "degraded", "invalid", "missing"} {
		st := licenseBehaviorStateNamed(t, name)
		t.Run(name, func(t *testing.T) {
			t.Run("jobs.write", func(t *testing.T) {
				s, bus, safety := licenseBehaviorGateway(t, st)
				putTestRole(t, s, "license-reader", auth.PermJobsRead)
				rec, _ := licenseBehaviorSubmit(s, "license-reader")
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
				}
				licenseBehaviorRefusedSubmit(t, s, bus, safety)
			})
			t.Run("jobs.approve", func(t *testing.T) {
				s, bus, safety := licenseBehaviorGateway(t, st)
				putTestRole(t, s, "license-reader", auth.PermJobsRead)
				jobID := seedApprovalJob(t, s, "")
				rec := licenseBehaviorDecide(s, "approve", jobID, "license-reader")
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
				}
				licenseBehaviorNoEffect(t, bus, safety)
				if got := licenseBehaviorJobState(t, s, jobID); got != model.JobStateApproval {
					t.Fatalf("job state = %s, want %s", got, model.JobStateApproval)
				}
			})
			t.Run("roles.write", func(t *testing.T) {
				s, _, _ := licenseBehaviorGateway(t, st)
				rec := licenseBehaviorPutRole(s, "operator")
				if st.rbac {
					if rec.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
					}
				} else {
					licenseBehaviorGated(t, rec, st, "rbac")
				}
				licenseBehaviorNoRole(t, s)
			})
		})
	}
}

func licenseBehaviorPutRole(s *server, role string) *httptest.ResponseRecorder {
	req := licenseBehaviorRequest(http.MethodPut, "/api/v1/auth/roles/license-writer", `{"permissions":["jobs.write"]}`, role)
	req.SetPathValue("name", "license-writer")
	rec := httptest.NewRecorder()
	s.handlePutRole(rec, req)
	return rec
}

func licenseBehaviorNoRole(t *testing.T, s *server) {
	t.Helper()
	if _, err := s.rbacStore.GetRole(context.Background(), "license-writer"); !errors.Is(err, auth.ErrRoleNotFound) {
		t.Fatalf("refused role write: get role err = %v, want ErrRoleNotFound", err)
	}
}

// Community fallback states. degraded and invalid refuse every
// permission-gated Enterprise route at break-glass; missing reaches the
// feature gate.
var licenseBehaviorFallbacks = []string{"degraded", "invalid", "missing"}

func TestLicenseBehaviorGateway_CommunityFallback(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T, licenseBehaviorState)
	}{
		{"requests_per_second", licenseBehaviorRateLimit},
		{"audit_retention_days", licenseBehaviorRetention},
		{"sso", licenseBehaviorOIDC},
		{"saml", licenseBehaviorSAML},
		{"scim", licenseBehaviorSCIM},
		{"rbac", licenseBehaviorRoleWrite},
		{"audit_export", licenseBehaviorAuditExport},
		{"siem_export", licenseBehaviorSIEMExport},
		{"legal_hold", licenseBehaviorLegalHold},
	}
	for _, name := range licenseBehaviorFallbacks {
		st := licenseBehaviorStateNamed(t, name)
		t.Run(name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) { tc.run(t, st) })
			}
		})
	}
}

// licenseBehaviorGated asserts the refusal of a permission-gated Enterprise
// route: break-glass where the state refuses, else the tier limit.
func licenseBehaviorGated(t *testing.T, rec *httptest.ResponseRecorder, st licenseBehaviorState, limit string) {
	t.Helper()
	if st.refusal != "" {
		licenseBehaviorBreakGlass(t, rec, st)
		return
	}
	licenseBehaviorTierLimit(t, rec, limit)
}

func licenseBehaviorTierLimit(t *testing.T, rec *httptest.ResponseRecorder, limit string) {
	t.Helper()
	var body struct {
		Code  string `json:"code"`
		Limit string `json:"limit"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusForbidden || body.Code != "tier_limit_exceeded" || body.Limit != limit {
		t.Fatalf("status = %d body = %s, want 403 tier_limit_exceeded limit %s", rec.Code, rec.Body.String(), limit)
	}
}

// licenseBehaviorEvidence asserts a kernel-denied submit still leaves its
// submit_denied audit entry.
func licenseBehaviorEvidence(t *testing.T, s *server, safety *stubSafetyClient) {
	t.Helper()
	licenseBehaviorDeny(safety, "snap-test")
	_, jobID := licenseBehaviorSubmit(s, "admin")
	if got := licenseBehaviorAudit(t, s, "submit_denied", jobID); len(got) != 1 {
		t.Fatalf("submit_denied audit entries = %v, want one", got)
	}
}

func licenseBehaviorRateLimit(t *testing.T, st licenseBehaviorState) {
	s, bus, safety := licenseBehaviorGateway(t, st)
	if rps, burst := s.tierRateLimitDefaults(); rps != 500 || burst != 1000 {
		t.Fatalf("tier rate limit = %d/%d, want 500/1000", rps, burst)
	}
	rps, burst := clampRateLimitToEntitlements(10000, 20000, s.currentEntitlements())
	if rps != 500 || burst != 1000 {
		t.Fatalf("clamped rate limit = %d/%d, want 500/1000", rps, burst)
	}
	// Fill this tenant's window counters to the burst so the next request is
	// over the limit whichever second it lands in.
	client := licenseBehaviorRedis(t, s)
	now := time.Now().Unix()
	for sec := now - 1; sec <= now+30; sec++ {
		key := rateLimitKeyPrefix + "tenant:default:" + strconv.FormatInt(sec, 10)
		if err := client.Set(context.Background(), key, burst, time.Minute).Err(); err != nil {
			t.Fatalf("seed rate window: %v", err)
		}
	}
	handler := rateLimitMiddleware(s.auth, newRedisRateLimiter(client, rps, burst), nil, http.HandlerFunc(s.handleSubmitJobHTTP))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, licenseBehaviorRequest(http.MethodPost, "/api/v1/jobs", `{"prompt":"hello","topic":"job.test"}`, "admin"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	licenseBehaviorRefusedSubmit(t, s, bus, safety)
}

func licenseBehaviorRetention(t *testing.T, st licenseBehaviorState) {
	s, _, safety := licenseBehaviorGateway(t, st)
	if ttl := audit.RetentionTTLFromEntitlements(s.currentEntitlements()); ttl != 7*24*time.Hour {
		t.Fatalf("audit retention = %s, want 168h", ttl)
	}
	rec := httptest.NewRecorder()
	s.handleAuditExportConfig(rec, licenseBehaviorRequest(http.MethodGet, "/api/v1/audit/export/config", "", "admin"))
	if st.refusal != "" {
		licenseBehaviorBreakGlass(t, rec, st)
	} else if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"retention_days":7`) {
		t.Fatalf("export config status = %d, want 200 retention_days 7: %s", rec.Code, rec.Body.String())
	}
	licenseBehaviorEvidence(t, s, safety)
}

// licenseBehaviorUserStore is the session-capable user store the SSO and SCIM
// adapters run on, over the gateway's own Redis.
func licenseBehaviorUserStore(t *testing.T, s *server) *auth.RedisUserStore {
	t.Helper()
	return auth.NewRedisUserStoreFromClient(licenseBehaviorRedis(t, s))
}

// licenseBehaviorNoLogin asserts a refused request stored no state or user.
func licenseBehaviorNoLogin(t *testing.T, s *server, store *auth.RedisUserStore, statePattern string) {
	t.Helper()
	if keys := licenseBehaviorKeys(t, s, statePattern); len(keys) != 0 {
		t.Fatalf("refused request stored %v", keys)
	}
	users, err := store.List(context.Background(), "default")
	if err != nil || len(users) != 0 {
		t.Fatalf("users = %d err = %v, want none", len(users), err)
	}
}

func licenseBehaviorServe(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func licenseBehaviorOIDC(t *testing.T, st licenseBehaviorState) {
	s, _, _ := licenseBehaviorGateway(t, st)
	t.Setenv("CORDUM_OIDC_ALLOW_PRIVATE", "true")
	t.Setenv("CORDUM_OIDC_ALLOW_HTTP", "true")
	var issuer string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/keys" {
			_, _ = w.Write([]byte(`{"keys":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": issuer, "jwks_uri": issuer + "/keys",
			"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
		})
	}))
	t.Cleanup(idp.Close)
	issuer = idp.URL
	provider, err := auth.NewOIDCProvider(auth.OIDCConfig{Enabled: true, IssuerURL: issuer, ClientID: "cordum", ClientSecret: "license-behavior", RedirectURI: issuer + "/callback"})
	if err != nil {
		t.Fatalf("oidc provider: %v", err)
	}
	t.Cleanup(provider.Close)
	store := licenseBehaviorUserStore(t, s)
	flow, err := auth.NewOIDCFlowAdapter(provider, store, "default", s.entitlements)
	if err != nil || !flow.Enabled() {
		t.Fatalf("oidc flow: err = %v, want an enabled adapter", err)
	}
	mux := http.NewServeMux()
	flow.RegisterRoutes(mux, nil)
	for _, target := range []string{auth.OIDCLoginPath, auth.OIDCCallbackPath + "?state=license&code=behavior"} {
		licenseBehaviorTierLimit(t, licenseBehaviorServe(mux, http.MethodGet, target, ""), "sso")
	}
	licenseBehaviorNoLogin(t, s, store, "oidc:state:*")
}

func licenseBehaviorSAML(t *testing.T, st licenseBehaviorState) {
	s, _, _ := licenseBehaviorGateway(t, st)
	certPath, keyPath := licenseBehaviorKeyPair(t)
	t.Setenv("CORDUM_SAML_ENABLED", "true")
	t.Setenv("CORDUM_SAML_IDP_METADATA_URL", "")
	t.Setenv("CORDUM_SAML_IDP_METADATA", `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="urn:license-behavior:idp"></EntityDescriptor>`)
	t.Setenv("CORDUM_SAML_CERT_PATH", certPath)
	t.Setenv("CORDUM_SAML_KEY_PATH", keyPath)
	store := licenseBehaviorUserStore(t, s)
	sp, err := auth.NewSAMLService(store, "default", s.entitlements)
	if err != nil || !sp.Enabled() {
		t.Fatalf("saml: err = %v, want an enabled adapter", err)
	}
	mux := http.NewServeMux()
	sp.RegisterRoutes(mux, nil)
	licenseBehaviorTierLimit(t, licenseBehaviorServe(mux, http.MethodGet, auth.SAMLLoginPath, ""), "sso")
	licenseBehaviorTierLimit(t, licenseBehaviorServe(mux, http.MethodPost, auth.SAMLACSPath, "SAMLResponse=license&RelayState=behavior"), "sso")
	licenseBehaviorNoLogin(t, s, store, "saml:state:*")
}

// licenseBehaviorKeyPair writes a self-signed SAML service-provider pair.
func licenseBehaviorKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "license-behavior-sp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "sp.crt"), filepath.Join(dir, "sp.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func licenseBehaviorSCIM(t *testing.T, st licenseBehaviorState) {
	s, _, _ := licenseBehaviorGateway(t, st)
	t.Setenv("CORDUM_SCIM_BEARER_TOKEN", "scim-license-behavior")
	store := licenseBehaviorUserStore(t, s)
	scim, err := auth.NewSCIMService(store, "default", s.entitlements)
	if err != nil || !scim.Enabled() {
		t.Fatalf("scim: err = %v, want an enabled service", err)
	}
	mux := http.NewServeMux()
	scim.RegisterRoutes(mux, nil)
	req := httptest.NewRequest(http.MethodPost, auth.SCIMUsersPath, strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"license-behavior"}`))
	req.Header.Set("Authorization", "Bearer scim-license-behavior")
	req.Header.Set("Content-Type", "application/scim+json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	licenseBehaviorTierLimit(t, rec, "scim")
	licenseBehaviorNoLogin(t, s, store, "scim:user:*")
}

func licenseBehaviorRoleWrite(t *testing.T, st licenseBehaviorState) {
	s, _, _ := licenseBehaviorGateway(t, st)
	licenseBehaviorGated(t, licenseBehaviorPutRole(s, "admin"), st, "rbac")
	licenseBehaviorNoRole(t, s)
}

func licenseBehaviorAuditExport(t *testing.T, st licenseBehaviorState) {
	s, _, safety := licenseBehaviorGateway(t, st)
	rec := httptest.NewRecorder()
	s.handleAuditExport(rec, licenseBehaviorRequest(http.MethodGet, "/api/v1/audit/export?format=json", "", "admin"))
	licenseBehaviorGated(t, rec, st, "siem_export")
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("refused export sent attachment %q", got)
	}
	licenseBehaviorEvidence(t, s, safety)
}

func licenseBehaviorSIEMExport(t *testing.T, st licenseBehaviorState) {
	s, _, safety := licenseBehaviorGateway(t, st)
	sink := &testAuditSender{}
	s.auditExporter = sink
	rec := httptest.NewRecorder()
	s.handleAuditExportTest(rec, licenseBehaviorRequest(http.MethodPost, "/api/v1/audit/export/test", "", "admin"))
	licenseBehaviorGated(t, rec, st, "siem_export")
	for i := 0; i < sink.Len(); i++ {
		if sink.Get(i).Action == "audit_export_test" {
			t.Fatal("refused SIEM export sent its test event")
		}
	}
	licenseBehaviorEvidence(t, s, safety)
}

func licenseBehaviorLegalHold(t *testing.T, st licenseBehaviorState) {
	s, _, _ := licenseBehaviorGateway(t, st)
	s.legalHoldStore = audit.NewLegalHoldStoreFromClient(licenseBehaviorRedis(t, s))
	hold := seedLegalHold(t, s, "default")
	rec := releaseLegalHoldAs(s, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"}, hold.ID)
	licenseBehaviorGated(t, rec, st, "legal_hold")
	got, err := s.legalHoldStore.GetHold(context.Background(), hold.ID)
	if err != nil || !got.IsActive() {
		t.Fatalf("hold after refused release: err = %v, want an active hold", err)
	}
	if held, err := s.legalHoldStore.IsUnderHold(context.Background(), "default"); err != nil || !held {
		t.Fatalf("tenant under hold = %t err = %v, want true", held, err)
	}
}
