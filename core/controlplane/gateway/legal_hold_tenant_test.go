package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

var (
	holdScopePlatform = crossTenantSweepTenant{"hold-platform", "hold-platform-key", "hold-platform-admin"}
	holdScopeSpanning = crossTenantSweepTenant{edgeRouteTenant, "hold-spanning-key", "hold-spanning-admin"}
)

func TestAuditHoldsAndExportTestStayInTheCallersTenant(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *crossTenantSweep)
	}{
		{"export", holdScopeExport},
		{"create-other", holdScopeCreateOther},
		{"create-own", holdScopeCreateOwn},
		{"list-other", holdScopeListOther},
		{"list-own", holdScopeListOwn},
		{"release-other", holdScopeReleaseOther},
		{"owner", func(t *testing.T, sw *crossTenantSweep) { holdScopeLifecycle(t, sw, sw.tenantB) }},
		{"platform", func(t *testing.T, sw *crossTenantSweep) { holdScopeLifecycle(t, sw, holdScopePlatform) }},
		{"spanning", func(t *testing.T, sw *crossTenantSweep) { holdScopeLifecycle(t, sw, holdScopeSpanning) }},
		{"no-auth", func(t *testing.T, sw *crossTenantSweep) {
			sw.server.auth, sw.handler = nil, sw.mux
			holdScopeLifecycle(t, sw, crossTenantSweepTenant{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, holdScopeSweep(t)) })
	}
}

func holdScopeSweep(t *testing.T) *crossTenantSweep {
	t.Helper()
	sw := newCrossTenantSweep(t)
	s := sw.server
	s.tenant = holdScopePlatform.tenantID
	var keys []string
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB, holdScopePlatform, holdScopeSpanning} {
		keys = append(keys, fmt.Sprintf(`{"key":%q,"tenant":%q,"role":"admin","principal_id":%q,"allow_cross_tenant":%t}`,
			who.apiKey, who.tenantID, who.principalID, who == holdScopeSpanning))
	}
	s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": "[" + strings.Join(keys, ",") + "]"})
	sw.handler = apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(sw.mux, s.entitlements)))
	setTestEntitlements(t, s, licensing.PlanCommunity, func(ent *licensing.Entitlements) {
		ent.LegalHold, ent.AuditExport = true, true
	})
	client, ok := s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatal("hold fixture needs Redis client")
	}
	s.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
	for _, tenant := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, s.tenant} {
		seedAuditEvents(t, s, tenant, []audit.SIEMEvent{{EventType: "hold.seed", Reason: "private-" + tenant}})
	}
	s.auditExporter = newAuditChainSender(s.auditChainer, nil)
	return sw
}

type holdScopeSnapshot struct {
	Events []redis.XMessage
	Holds  map[string]audit.LegalHold
}

func holdScopeState(t *testing.T, sw *crossTenantSweep, tenant string) holdScopeSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := sw.server.redisClient().XRange(ctx, sw.server.auditChainer.StreamKey(tenant), "-", "+").Result()
	if err != nil || len(events) == 0 {
		t.Fatalf("read tenant stream: %v (events=%d)", err, len(events))
	}
	holds, err := sw.server.legalHoldStore.ListHolds(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	state := holdScopeSnapshot{Events: events, Holds: map[string]audit.LegalHold{}}
	for _, hold := range holds {
		state.Holds[hold.ID] = *hold
	}
	return state
}

func holdScopeUnchanged(t *testing.T, sw *crossTenantSweep) func() {
	t.Helper()
	b := holdScopeState(t, sw, sw.tenantB.tenantID)
	p := holdScopeState(t, sw, sw.server.tenant)
	return func() {
		t.Helper()
		if !reflect.DeepEqual(b, holdScopeState(t, sw, sw.tenantB.tenantID)) ||
			!reflect.DeepEqual(p, holdScopeState(t, sw, sw.server.tenant)) {
			t.Error("request changed another tenant's stream or holds")
		}
	}
}

func holdScopeSeed(t *testing.T, sw *crossTenantSweep, tenant string) *audit.LegalHold {
	t.Helper()
	hold, err := sw.server.legalHoldStore.CreateHold(t.Context(), tenant, "private-"+tenant, "seed")
	if err != nil {
		t.Fatal(err)
	}
	return hold
}

func holdScopeStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status=%d, want=%d; body=%s", rr.Code, want, rr.Body.String())
	}
}

func holdScopeExport(t *testing.T, sw *crossTenantSweep) {
	holdScopeSeed(t, sw, sw.tenantB.tenantID)
	check := holdScopeUnchanged(t, sw)
	before := holdScopeState(t, sw, sw.tenantA.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/audit/export/test?tenant="+sw.tenantB.tenantID,
		fmt.Sprintf(`{"tenant_id":%q}`, sw.tenantB.tenantID))
	check()
	holdScopeStatus(t, rr, http.StatusOK)
	after := holdScopeState(t, sw, sw.tenantA.tenantID)
	if len(after.Events) != len(before.Events)+1 || !reflect.DeepEqual(before.Holds, after.Holds) {
		t.Fatal("export test must append one event in the acting tenant")
	}
}

func holdScopeCreateOther(t *testing.T, sw *crossTenantSweep) {
	hold := holdScopeSeed(t, sw, sw.tenantB.tenantID)
	if err := sw.server.legalHoldStore.ReleaseHold(t.Context(), hold.ID, "seed"); err != nil {
		t.Fatal(err)
	}
	check := holdScopeUnchanged(t, sw)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/audit/legal-hold",
		fmt.Sprintf(`{"tenant_id":%q,"reason":"retention"}`, " "+sw.tenantB.tenantID+" "))
	check()
	holdScopeStatus(t, rr, http.StatusForbidden)
}

func holdScopeCreateOwn(t *testing.T, sw *crossTenantSweep) {
	holdScopeSeed(t, sw, sw.tenantB.tenantID)
	check := holdScopeUnchanged(t, sw)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/audit/legal-hold", `{"reason":"retention"}`)
	check()
	holdScopeStatus(t, rr, http.StatusCreated)
	holds := holdScopeState(t, sw, sw.tenantA.tenantID).Holds
	if len(holds) != 1 {
		t.Fatalf("acting tenant holds=%d, want 1", len(holds))
	}
	for _, hold := range holds {
		if hold.TenantID != sw.tenantA.tenantID || hold.CreatedBy != sw.tenantA.principalID {
			t.Fatal("hold must belong to the acting tenant and principal")
		}
		holdScopeStatus(t, sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/audit/legal-hold/"+hold.ID, ""), http.StatusOK)
		if holdScopeState(t, sw, sw.tenantA.tenantID).Holds[hold.ID].ReleasedAt == nil {
			t.Fatal("owner release must update the stored hold")
		}
	}
	check()
}

func holdScopeListOther(t *testing.T, sw *crossTenantSweep) {
	holdScopeSeed(t, sw, sw.tenantB.tenantID)
	check := holdScopeUnchanged(t, sw)
	rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/audit/legal-holds?tenant="+url.QueryEscape(" "+sw.tenantB.tenantID+" "), "")
	check()
	holdScopeStatus(t, rr, http.StatusForbidden)
}

func holdScopeListOwn(t *testing.T, sw *crossTenantSweep) {
	holdScopeSeed(t, sw, sw.tenantB.tenantID)
	own := holdScopeSeed(t, sw, sw.tenantA.tenantID)
	for _, query := range []string{"", "?tenant=" + url.QueryEscape(" "+sw.tenantA.tenantID+" ")} {
		check := holdScopeUnchanged(t, sw)
		rr := sw.as(t, sw.tenantA, http.MethodGet, "/api/v1/audit/legal-holds"+query, "")
		check()
		holdScopeStatus(t, rr, http.StatusOK)
		var result struct{ Holds []audit.LegalHold }
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || len(result.Holds) != 1 || result.Holds[0].ID != own.ID {
			t.Fatalf("list must contain only the acting tenant hold: %v; %s", err, rr.Body.String())
		}
	}
}

func holdScopeReleaseOther(t *testing.T, sw *crossTenantSweep) {
	hold := holdScopeSeed(t, sw, sw.tenantB.tenantID)
	check := holdScopeUnchanged(t, sw)
	rr := sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/audit/legal-hold/"+hold.ID, "")
	check()
	holdScopeStatus(t, rr, http.StatusNotFound)
	missing := sw.as(t, sw.tenantA, http.MethodDelete, "/api/v1/audit/legal-hold/missing", "")
	if rr.Body.String() != missing.Body.String() {
		t.Fatal("other tenant and absent hold must have the same answer")
	}
}

func holdScopeLifecycle(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	rr := sw.as(t, who, http.MethodPost, "/api/v1/audit/legal-hold",
		fmt.Sprintf(`{"tenant_id":%q,"reason":"retention"}`, " "+sw.tenantB.tenantID+" "))
	holdScopeStatus(t, rr, http.StatusCreated)
	var created struct{ Hold audit.LegalHold }
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil || created.Hold.ID == "" || created.Hold.TenantID != sw.tenantB.tenantID {
		t.Fatalf("create response: %v; %s", err, rr.Body.String())
	}
	for _, query := range []string{"", "?tenant=" + sw.tenantB.tenantID} {
		rr = sw.as(t, who, http.MethodGet, "/api/v1/audit/legal-holds"+query, "")
		holdScopeStatus(t, rr, http.StatusOK)
		if !strings.Contains(rr.Body.String(), created.Hold.ID) {
			t.Fatal("list must include the created hold")
		}
	}
	rr = sw.as(t, who, http.MethodDelete, "/api/v1/audit/legal-hold/"+created.Hold.ID, "")
	holdScopeStatus(t, rr, http.StatusOK)
	hold := holdScopeState(t, sw, sw.tenantB.tenantID).Holds[created.Hold.ID]
	if hold.ReleasedAt == nil {
		t.Fatal("release must update the stored hold")
	}
	acting := who.tenantID
	if acting == "" {
		acting = sw.server.tenant
	}
	before := holdScopeState(t, sw, acting)
	holdScopeStatus(t, sw.as(t, who, http.MethodPost, "/api/v1/audit/export/test", ""), http.StatusOK)
	if len(holdScopeState(t, sw, acting).Events) != len(before.Events)+1 {
		t.Fatal("export must append to the acting tenant stream")
	}
}
