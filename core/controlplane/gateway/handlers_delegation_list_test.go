package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func TestHandleListDelegationsFiltersAndPagination(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})
	putTestRole(t, s, "delegation-reader", auth.PermDelegationRead)
	putTestRole(t, s, "restricted", auth.PermJobsRead)

	store := s.delegationListStore()
	now := time.Now().UTC()
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-active",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []delegation.ChainLink{{AgentID: "agent-a"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
	})
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-revoked",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-a",
		Audience:       "agent-c",
		AllowedActions: []string{"deploy"},
		AllowedTopics:  []string{"job.beta"},
		Chain:          []delegation.ChainLink{{AgentID: "agent-a"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-2 * time.Minute),
		ExpiresAt:      now.Add(2 * time.Hour),
		Revoked:        true,
		RevokedAt:      now.Add(-30 * time.Second),
		RevokedReason:  "manual",
	})
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-expired",
		Tenant:         "default",
		Issuer:         "agent-z",
		Subject:        "agent-z",
		Audience:       "agent-y",
		AllowedActions: []string{"delete"},
		AllowedTopics:  []string{"job.gamma"},
		Chain:          []delegation.ChainLink{{AgentID: "agent-z"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-3 * time.Minute),
		ExpiresAt:      now.Add(-time.Minute),
	})
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-other",
		Tenant:         "other",
		Issuer:         "other-agent",
		Subject:        "other-agent",
		Audience:       "other-target",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.other"},
		Chain:          []delegation.ChainLink{{AgentID: "other-agent"}},
		ChainDepth:     1,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
	})

	rr := delegationList(t, s, "/api/v1/delegations?status=revoked&limit=10", "delegation-reader")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	page := decodeDelegationListPage(t, rr)
	if len(page.Items) != 1 || page.Items[0].JTI != "dlg-revoked" {
		t.Fatalf("unexpected revoked page: %#v", page)
	}

	rr = delegationList(t, s, "/api/v1/delegations?scope=ploy", "delegation-reader")
	page = decodeDelegationListPage(t, rr)
	if len(page.Items) != 1 || page.Items[0].JTI != "dlg-revoked" {
		t.Fatalf("unexpected scope page: %#v", page)
	}

	rr = delegationList(t, s, "/api/v1/delegations?before_expiry="+now.Add(5*time.Minute).Format(time.RFC3339), "delegation-reader")
	page = decodeDelegationListPage(t, rr)
	if len(page.Items) != 1 || page.Items[0].JTI != "dlg-expired" {
		t.Fatalf("unexpected before_expiry page: %#v", page)
	}

	rr = delegationList(t, s, "/api/v1/delegations?limit=1", "delegation-reader")
	page = decodeDelegationListPage(t, rr)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("expected paginated page, got %#v", page)
	}
	rr = delegationList(t, s, "/api/v1/delegations?limit=1&cursor="+page.NextCursor, "delegation-reader")
	page2 := decodeDelegationListPage(t, rr)
	if len(page2.Items) != 1 || page2.Items[0].JTI == page.Items[0].JTI {
		t.Fatalf("unexpected second page: %#v", page2)
	}
}

func TestHandleListAgentDelegationsAndRBAC(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})
	putTestRole(t, s, "delegation-reader", auth.PermDelegationRead)

	store := s.delegationListStore()
	now := time.Now().UTC()
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-agent-a",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []delegation.ChainLink{{AgentID: "agent-a"}},
		ChainDepth:     1,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
	})
	seedDelegationView(t, store, delegation.DelegationView{
		JTI:            "dlg-agent-z",
		Tenant:         "default",
		Issuer:         "agent-z",
		Subject:        "agent-z",
		Audience:       "agent-y",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []delegation.ChainLink{{AgentID: "agent-z"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
	})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a/delegations", nil), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "reader",
		Role:        "delegation-reader",
	})
	req.SetPathValue("id", "agent-a")
	rr := httptest.NewRecorder()
	s.handleListAgentDelegations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	page := decodeDelegationListPage(t, rr)
	if len(page.Items) != 1 || page.Items[0].JTI != "dlg-agent-a" {
		t.Fatalf("unexpected agent page: %#v", page)
	}

	deniedReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/delegations", nil), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "viewer",
		Role:        "restricted",
	})
	deniedRec := httptest.NewRecorder()
	s.handleListDelegations(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden without delegation permission, got %d", deniedRec.Code)
	}
}

func seedDelegationView(t *testing.T, store *delegation.RedisListStore, view delegation.DelegationView) {
	t.Helper()
	if err := store.RecordIssuedToken(context.Background(), view); err != nil {
		t.Fatalf("RecordIssuedToken(%s) error = %v", view.JTI, err)
	}
	if view.Revoked {
		if err := store.MarkRevoked(context.Background(), view.Tenant, view.JTI, view.RevokedReason, view.RevokedAt); err != nil {
			t.Fatalf("MarkRevoked(%s) error = %v", view.JTI, err)
		}
	}
}

func delegationList(t *testing.T, s *server, path, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, path, nil), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "reader",
		Role:        role,
	})
	rr := httptest.NewRecorder()
	s.handleListDelegations(rr, req)
	return rr
}

func decodeDelegationListPage(t *testing.T, rr *httptest.ResponseRecorder) delegationListResponse {
	t.Helper()
	var page delegationListResponse
	if err := json.NewDecoder(rr.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	return page
}
