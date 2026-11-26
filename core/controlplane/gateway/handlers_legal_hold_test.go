package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

func TestLegalHold_NonTierBranchEmitsTierLimitShape(t *testing.T) {
	got := legalHoldTierLimitHTTPError(errors.New("entitlement resolver unavailable"))

	if got.Code != "tier_limit_exceeded" {
		t.Fatalf("Code = %q, want tier_limit_exceeded", got.Code)
	}
	if got.Limit != "legal_hold" {
		t.Fatalf("Limit = %q, want legal_hold", got.Limit)
	}
	if got.UpgradeURL != licensing.DefaultUpgradeURL {
		t.Fatalf("UpgradeURL = %q, want %q", got.UpgradeURL, licensing.DefaultUpgradeURL)
	}
	if got.Message == "" {
		t.Fatal("Message is empty, want dashboard-safe upgrade message")
	}
}

func TestLegalHold_TierLimitErrorPreservesLimitFields(t *testing.T) {
	got := legalHoldTierLimitHTTPError(&licensing.TierLimitError{
		Limit:      "legal_hold",
		Current:    1,
		Allowed:    0,
		UpgradeURL: "https://example.com/upgrade",
	})

	if got.Code != "tier_limit_exceeded" || got.Limit != "legal_hold" {
		t.Fatalf("tier error shape = %+v", got)
	}
	if got.Current != 1 || got.Allowed != 0 {
		t.Fatalf("Current/Allowed = %d/%d, want 1/0", got.Current, got.Allowed)
	}
	if got.UpgradeURL != "https://example.com/upgrade" {
		t.Fatalf("UpgradeURL = %q", got.UpgradeURL)
	}
}

var (
	tenantAAdmin     = &auth.AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-a"}
	crossTenantAdmin = &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "root", AllowCrossTenant: true}
)

// newLegalHoldTenantGateway returns an Enterprise gateway with a real legal
// hold store and an auth provider that enforces tenant access.
func newLegalHoldTenantGateway(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	setTestEntitlements(t, s, licensing.PlanEnterprise, nil)
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("job store client = %T, want *redis.Client", s.jobStore.Client())
	}
	s.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
	return s
}

func seedLegalHold(t *testing.T, s *server, tenant string) *audit.LegalHold {
	t.Helper()
	hold, err := s.legalHoldStore.CreateHold(context.Background(), tenant, "seeded", "seed")
	if err != nil {
		t.Fatalf("seed hold for %s: %v", tenant, err)
	}
	return hold
}

func createLegalHoldAs(s *server, who *auth.AuthContext, body string) *httptest.ResponseRecorder {
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/audit/legal-hold", strings.NewReader(body)), who)
	rec := httptest.NewRecorder()
	s.handleCreateLegalHold(rec, req)
	return rec
}

func releaseLegalHoldAs(s *server, who *auth.AuthContext, id string) *httptest.ResponseRecorder {
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/audit/legal-hold/"+id, nil), who)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleReleaseLegalHold(rec, req)
	return rec
}

func listLegalHoldsAs(t *testing.T, s *server, who *auth.AuthContext, query string) (int, []*audit.LegalHold) {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/audit/legal-holds"+query, nil), who)
	rec := httptest.NewRecorder()
	s.handleListLegalHolds(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var body struct {
		Holds []*audit.LegalHold `json:"holds"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode holds: %v", err)
	}
	return rec.Code, body.Holds
}

func holdTenants(holds []*audit.LegalHold) map[string]bool {
	tenants := map[string]bool{}
	for _, hold := range holds {
		tenants[hold.TenantID] = true
	}
	return tenants
}

func TestLegalHold_TenantAdminCannotTouchOtherTenants(t *testing.T) {
	s := newLegalHoldTenantGateway(t)
	ctx := context.Background()
	holdB := seedLegalHold(t, s, "tenant-b")
	seedLegalHold(t, s, "tenant-a")

	if rec := createLegalHoldAs(s, tenantAAdmin, `{"tenant_id":"tenant-c","reason":"probe"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("create on tenant-c = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if held, err := s.legalHoldStore.IsUnderHold(ctx, "tenant-c"); err != nil || held {
		t.Fatalf("tenant-c held = %v (err %v) after a denied create", held, err)
	}

	unknown := releaseLegalHoldAs(s, tenantAAdmin, "no-such-hold")
	denied := releaseLegalHoldAs(s, tenantAAdmin, holdB.ID)
	if denied.Code != http.StatusNotFound || denied.Code != unknown.Code || denied.Body.String() != unknown.Body.String() {
		t.Fatalf("release other tenant's hold = %d %q, want the unknown-id answer %d %q",
			denied.Code, denied.Body.String(), unknown.Code, unknown.Body.String())
	}
	if held, err := s.legalHoldStore.IsUnderHold(ctx, "tenant-b"); err != nil || !held {
		t.Fatalf("tenant-b held = %v (err %v), want the hold to survive", held, err)
	}

	code, holds := listLegalHoldsAs(t, s, tenantAAdmin, "")
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200", code)
	}
	if got := holdTenants(holds); len(got) != 1 || !got["tenant-a"] {
		t.Fatalf("list tenants = %v, want only tenant-a", got)
	}
	if code, _ := listLegalHoldsAs(t, s, tenantAAdmin, "?tenant=tenant-b"); code != http.StatusForbidden {
		t.Fatalf("list ?tenant=tenant-b = %d, want 403", code)
	}
}

func TestLegalHold_CrossTenantAdminManagesAnyTenant(t *testing.T) {
	s := newLegalHoldTenantGateway(t)
	seedLegalHold(t, s, "tenant-a")

	rec := createLegalHoldAs(s, crossTenantAdmin, `{"tenant_id":"tenant-b","reason":"investigation"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create on tenant-b = %d %s, want 201", rec.Code, rec.Body.String())
	}
	var created struct {
		Hold audit.LegalHold `json:"hold"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created hold: %v", err)
	}
	if created.Hold.TenantID != "tenant-b" {
		t.Fatalf("hold tenant = %q, want tenant-b", created.Hold.TenantID)
	}

	if _, holds := listLegalHoldsAs(t, s, crossTenantAdmin, ""); !holdTenants(holds)["tenant-a"] || !holdTenants(holds)["tenant-b"] {
		t.Fatalf("list tenants = %v, want tenant-a and tenant-b", holdTenants(holds))
	}
	if code, holds := listLegalHoldsAs(t, s, crossTenantAdmin, "?tenant=tenant-b"); code != http.StatusOK || len(holds) != 1 {
		t.Fatalf("list ?tenant=tenant-b = %d with %d holds, want 200 with 1", code, len(holds))
	}

	if rec := releaseLegalHoldAs(s, crossTenantAdmin, created.Hold.ID); rec.Code != http.StatusOK {
		t.Fatalf("release tenant-b hold = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if held, err := s.legalHoldStore.IsUnderHold(context.Background(), "tenant-b"); err != nil || held {
		t.Fatalf("tenant-b held = %v (err %v) after release", held, err)
	}
}

func TestLegalHold_SameTenantFlow(t *testing.T) {
	s := newLegalHoldTenantGateway(t)

	// An omitted tenant_id holds the caller's own tenant.
	rec := createLegalHoldAs(s, tenantAAdmin, `{"reason":"litigation"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", rec.Code, rec.Body.String())
	}
	var created struct {
		Hold audit.LegalHold `json:"hold"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created hold: %v", err)
	}
	if created.Hold.TenantID != "tenant-a" {
		t.Fatalf("hold tenant = %q, want the caller's tenant-a", created.Hold.TenantID)
	}

	if code, holds := listLegalHoldsAs(t, s, tenantAAdmin, "?tenant=tenant-a"); code != http.StatusOK || len(holds) != 1 || holds[0].ID != created.Hold.ID {
		t.Fatalf("list ?tenant=tenant-a = %d %v, want the new hold", code, holds)
	}
	if rec := releaseLegalHoldAs(s, tenantAAdmin, created.Hold.ID); rec.Code != http.StatusOK {
		t.Fatalf("release = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := releaseLegalHoldAs(s, tenantAAdmin, created.Hold.ID); rec.Code != http.StatusConflict {
		t.Fatalf("second release = %d %s, want 409", rec.Code, rec.Body.String())
	}
}
