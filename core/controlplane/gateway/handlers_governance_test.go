package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type governanceAuth struct{}

func (governanceAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	if auth := auth.FromRequest(r); auth != nil {
		return auth, nil
	}
	return nil, errors.New("unauthorized")
}

func (governanceAuth) AuthenticateGRPC(ctx context.Context) (*auth.AuthContext, error) {
	if auth := auth.FromContext(ctx); auth != nil {
		return auth, nil
	}
	return nil, errors.New("unauthorized")
}

func (governanceAuth) RequireRole(r *http.Request, roles ...string) error {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		return errors.New("unauthorized")
	}
	role := auth.NormalizeRole(authCtx.Role)
	for _, candidate := range roles {
		if auth.NormalizeRole(candidate) == role {
			return nil
		}
	}
	return errors.New("forbidden")
}

func (governanceAuth) ResolveTenant(r *http.Request, requested, _ string) (string, error) {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		return "", errors.New("unauthorized")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return strings.TrimSpace(authCtx.Tenant), nil
	}
	if requested != strings.TrimSpace(authCtx.Tenant) && !authCtx.AllowCrossTenant {
		return "", errors.New("tenant access denied")
	}
	return requested, nil
}

func (governanceAuth) RequireTenantAccess(r *http.Request, tenant string) error {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		return errors.New("unauthorized")
	}
	if authCtx.AllowCrossTenant || strings.TrimSpace(authCtx.Tenant) == strings.TrimSpace(tenant) {
		return nil
	}
	return errors.New("tenant access denied")
}

func (governanceAuth) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		return "", errors.New("unauthorized")
	}
	return authCtx.PrincipalID, nil
}

type stubDecisionLogStore struct {
	lastQuery model.DecisionQuery
	page      model.DecisionPage
	err       error
	queryFn   func(model.DecisionQuery) (model.DecisionPage, error)
}

func (s *stubDecisionLogStore) AppendDecision(context.Context, model.DecisionLogRecord) error {
	return nil
}

func (s *stubDecisionLogStore) QueryDecisions(_ context.Context, query model.DecisionQuery) (model.DecisionPage, error) {
	s.lastQuery = query
	if s.queryFn != nil {
		return s.queryFn(query)
	}
	return s.page, s.err
}

func TestHandleListGovernanceDecisionsRoundTripsFilters(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	store := &stubDecisionLogStore{
		page: model.DecisionPage{
			Items: []model.DecisionLogRecord{
				{
					JobID:            "job-1",
					Tenant:           "tenant-a",
					AgentID:          "agent-1",
					Topic:            "job.test",
					Verdict:          model.SafetyAllowWithConstraints,
					RuleID:           "rule-1",
					PolicyVersion:    "snap-1",
					Reason:           "constraint applied",
					Constraints:      &pb.PolicyConstraints{Budgets: &pb.BudgetConstraints{MaxRetries: 2}},
					ApprovalStatus:   model.ApprovalStatusPending,
					ApprovalDecision: model.ApprovalDecisionApprove,
					Timestamp:        time.Date(2026, time.April, 20, 10, 30, 0, 0, time.UTC).UnixMilli(),
				},
			},
			NextCursor: "cursor-2",
		},
	}
	s.decisionLogStore = store

	cursor := model.EncodeDecisionCursor(1776680400000, "decision-1")
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/governance/decisions?since=2026-04-20T09:00:00Z&until=1776681000000&topic=job.test&rule_id=rule-1&verdict=constrain&agent_id=agent-1&cursor="+cursor+"&limit=25", nil), &auth.AuthContext{
		Tenant:      "tenant-a",
		Role:        "viewer",
		PrincipalID: "viewer-a",
	})
	rr := httptest.NewRecorder()

	s.handleListGovernanceDecisions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if store.lastQuery.Tenant != "tenant-a" {
		t.Fatalf("Tenant=%q want tenant-a", store.lastQuery.Tenant)
	}
	if store.lastQuery.Topic != "job.test" {
		t.Fatalf("Topic=%q want job.test", store.lastQuery.Topic)
	}
	if store.lastQuery.RuleID != "rule-1" {
		t.Fatalf("RuleID=%q want rule-1", store.lastQuery.RuleID)
	}
	if store.lastQuery.AgentID != "agent-1" {
		t.Fatalf("AgentID=%q want agent-1", store.lastQuery.AgentID)
	}
	if store.lastQuery.Verdict != model.SafetyAllowWithConstraints {
		t.Fatalf("Verdict=%q want %q", store.lastQuery.Verdict, model.SafetyAllowWithConstraints)
	}
	if store.lastQuery.Limit != 25 {
		t.Fatalf("Limit=%d want 25", store.lastQuery.Limit)
	}
	if store.lastQuery.Cursor != cursor {
		t.Fatalf("Cursor=%q want %q", store.lastQuery.Cursor, cursor)
	}

	var resp governanceDecisionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.NextCursor != "cursor-2" {
		t.Fatalf("NextCursor=%q want cursor-2", resp.NextCursor)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(resp.Items)=%d want 1", len(resp.Items))
	}
	item := resp.Items[0]
	if item.MatchedRule != "rule-1" || item.Verdict != "constrain" || item.Reason != "constraint applied" {
		t.Fatalf("unexpected item: %#v", item)
	}
	if item.AgentID != "agent-1" || item.PolicyVersion != "snap-1" {
		t.Fatalf("unexpected metadata: %#v", item)
	}
	if item.Timestamp != "2026-04-20T10:30:00Z" {
		t.Fatalf("Timestamp=%q want 2026-04-20T10:30:00Z", item.Timestamp)
	}
	if item.Constraints == nil || item.Constraints.GetBudgets().GetMaxRetries() != 2 {
		t.Fatalf("constraints missing from response: %#v", item.Constraints)
	}
}

func TestHandleListGovernanceDecisionsRejectsBadQuery(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}

	tests := []string{
		"/api/v1/governance/decisions?verdict=shadowban",
		"/api/v1/governance/decisions?since=2026-04-20T11:00:00Z&until=2026-04-20T10:00:00Z",
		"/api/v1/governance/decisions?limit=501",
	}

	for _, url := range tests {
		req := withAuth(httptest.NewRequest(http.MethodGet, url, nil), &auth.AuthContext{
			Tenant:      "tenant-a",
			Role:        "viewer",
			PrincipalID: "viewer-a",
		})
		rr := httptest.NewRecorder()
		s.handleListGovernanceDecisions(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("url=%s status=%d body=%s", url, rr.Code, rr.Body.String())
		}
	}
}

func TestHandleListGovernanceDecisionsRequiresAuth(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}

	handler := apiKeyMiddleware(s.auth, http.HandlerFunc(s.handleListGovernanceDecisions))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/decisions", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListGovernanceDecisionsRBACDenied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(ent *licensing.Entitlements) {
		ent.RBAC = true
	})
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        "jobs-only",
		Permissions: []string{auth.PermJobsRead},
	}); err != nil {
		t.Fatalf("PutRole() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/governance/decisions", nil), &auth.AuthContext{
		Tenant:      "tenant-a",
		Role:        "jobs-only",
		PrincipalID: "jobs-only-a",
	})
	rr := httptest.NewRecorder()

	s.handleListGovernanceDecisions(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListGovernanceDecisionsTenantIsolation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	store := &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			all := []model.DecisionLogRecord{
				{JobID: "job-a", Tenant: "tenant-a", Topic: "job.test", Verdict: model.SafetyAllow, Timestamp: time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC).UnixMilli()},
				{JobID: "job-b", Tenant: "tenant-b", Topic: "job.test", Verdict: model.SafetyDeny, Timestamp: time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC).UnixMilli()},
			}
			items := make([]model.DecisionLogRecord, 0, len(all))
			for _, record := range all {
				if record.Tenant == query.Tenant {
					items = append(items, record)
				}
			}
			return model.DecisionPage{Items: items}, nil
		},
	}
	s.decisionLogStore = store

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/governance/decisions", nil), &auth.AuthContext{
		Tenant:      "tenant-a",
		Role:        "viewer",
		PrincipalID: "viewer-a",
	})
	rr := httptest.NewRecorder()
	s.handleListGovernanceDecisions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp governanceDecisionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].JobID != "job-a" {
		t.Fatalf("unexpected items: %#v", resp.Items)
	}
	if store.lastQuery.Tenant != "tenant-a" {
		t.Fatalf("Tenant=%q want tenant-a", store.lastQuery.Tenant)
	}
}
