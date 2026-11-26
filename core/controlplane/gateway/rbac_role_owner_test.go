package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func TestCustomRolesBelongToTheirTenant(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) { roleOwnerRoute(t, method) })
	}
	t.Run("list", roleOwnerList)
	t.Run("inherits", roleOwnerInheritance)
	t.Run("resolution", roleOwnerResolution)
	t.Run("stored-chain", roleOwnerStoredChain)
	t.Run("store-update", roleOwnerStoreUpdate)
	t.Run("stored-without-tenant", roleOwnerStored)
	t.Run("built-ins", roleOwnerBuiltIns)
}

func roleOwnerFixture(t *testing.T) *crossTenantSweep {
	t.Helper()
	sw := newCrossTenantSweep(t)
	setTestEntitlements(t, sw.server, licensing.PlanEnterprise, nil)
	return sw
}

func roleOwnerPut(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, name, body string) {
	t.Helper()
	rr := sw.as(t, who, http.MethodPut, "/api/v1/auth/roles/"+name, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func roleOwnerRoute(t *testing.T, method string) {
	sw := roleOwnerFixture(t)
	const name, marker = "role-owner-b", "role-b-description"
	roleOwnerPut(t, sw, sw.tenantB, name, `{"description":"`+marker+`","permissions":["jobs.read"]}`)
	before, err := sw.server.rbacStore.GetRole(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	mark := sw.publishMark()
	rr := sw.as(t, sw.tenantA, method, "/api/v1/auth/roles/"+name, `{"description":"replacement"}`)
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), name) || strings.Contains(rr.Body.String(), marker) {
		t.Errorf("non-owner response=%d %s", rr.Code, rr.Body.String())
	}
	after, err := sw.server.rbacStore.GetRole(context.Background(), name)
	if err != nil || !reflect.DeepEqual(before, after) || len(sw.publishedSince(mark)) != 0 {
		t.Errorf("role must stay equal with no event: before=%+v after=%+v err=%v", before, after, err)
	}
	rr = sw.as(t, sw.tenantB, method, "/api/v1/auth/roles/"+name, `{"description":"owner-update"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("owner response=%d %s", rr.Code, rr.Body.String())
	}
	after, err = sw.server.rbacStore.GetRole(context.Background(), name)
	switch method {
	case http.MethodGet:
		if !strings.Contains(rr.Body.String(), marker) || err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("owner GET must return the stored role")
		}
	case http.MethodPut:
		if err != nil || after.Description != "owner-update" {
			t.Fatal("owner PUT must persist the update")
		}
	case http.MethodDelete:
		if !errors.Is(err, auth.ErrRoleNotFound) {
			t.Fatalf("owner DELETE must remove the role: %v", err)
		}
	}
}

func roleOwnerList(t *testing.T) {
	sw := roleOwnerFixture(t)
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		roleOwnerPut(t, sw, who, "role-"+who.tenantID, fmt.Sprintf(`{"description":%q}`, "description-"+who.tenantID))
	}
	for _, tc := range []struct{ own, other crossTenantSweepTenant }{{sw.tenantA, sw.tenantB}, {sw.tenantB, sw.tenantA}} {
		rr := sw.as(t, tc.own, http.MethodGet, "/api/v1/auth/roles", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "description-"+tc.own.tenantID) ||
			strings.Contains(rr.Body.String(), "description-"+tc.other.tenantID) || strings.Contains(rr.Body.String(), "role-"+tc.other.tenantID) {
			t.Errorf("list for %s: %d %s", tc.own.tenantID, rr.Code, rr.Body.String())
		}
	}
}

func roleOwnerInheritance(t *testing.T) {
	sw := roleOwnerFixture(t)
	roleOwnerPut(t, sw, sw.tenantB, "parent-b", `{"permissions":["jobs.write"]}`)
	mark := sw.publishMark()
	rr := sw.as(t, sw.tenantA, http.MethodPut, "/api/v1/auth/roles/child-a", `{"inherits":["parent-b"]}`)
	_, err := sw.server.rbacStore.GetRole(context.Background(), "child-a")
	if rr.Code != http.StatusBadRequest || !errors.Is(err, auth.ErrRoleNotFound) || len(sw.publishedSince(mark)) != 0 {
		t.Fatalf("invalid parent must not store or emit: status=%d err=%v", rr.Code, err)
	}
	roleOwnerPut(t, sw, sw.tenantB, "child-b", `{"inherits":["parent-b","viewer"]}`)
	roleOwnerPermission(t, sw, "child-b", sw.tenantB.tenantID, true)
}

func roleOwnerPermission(t *testing.T, sw *crossTenantSweep, role, tenant string, allowed bool) {
	t.Helper()
	pc := auth.NewPermissionChecker(sw.server.rbacStore, sw.server.currentEntitlements)
	r := withAuth(httptest.NewRequest(http.MethodGet, "/", nil), &auth.AuthContext{Role: role, Tenant: tenant})
	// The selected management tenant does not change the principal's role owner.
	r.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
	if got := pc.RequirePermission(r, auth.PermJobsWrite) == nil; got != allowed {
		t.Errorf("HTTP permission for %s/%s=%v want=%v", tenant, role, got, allowed)
	}
	if got := pc.RequirePermissionGRPC(r.Context(), role, auth.PermJobsWrite) == nil; got != allowed {
		t.Errorf("gRPC permission for %s/%s=%v want=%v", tenant, role, got, allowed)
	}
}

func roleOwnerResolution(t *testing.T) {
	sw := roleOwnerFixture(t)
	body := fmt.Sprintf(`{"permissions":[%q,%q,%q]}`, auth.PermJobsWrite, auth.PermLLMIngest, auth.PermRuntimeIngest)
	roleOwnerPut(t, sw, sw.tenantB, "resolved-b", body)
	roleOwnerPut(t, sw, sw.tenantB, "resolved-child", `{"inherits":["resolved-b"]}`)
	for _, role := range []string{"resolved-b", "resolved-child"} {
		for _, tc := range []struct {
			tenant string
			allow  bool
		}{{sw.tenantA.tenantID, false}, {sw.tenantB.tenantID, true}} {
			roleOwnerPermission(t, sw, role, tc.tenant, tc.allow)
			r := withAuth(httptest.NewRequest(http.MethodPost, "/", nil), &auth.AuthContext{Role: role, Tenant: tc.tenant})
			r.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
			if sw.server.llmIngestPermissionAllowed(r, role) != tc.allow || sw.server.runtimeIngestPermissionAllowed(r, role) != tc.allow {
				t.Errorf("ingest permission for %s/%s must be %v", tc.tenant, role, tc.allow)
			}
		}
	}
}

func roleOwnerStored(t *testing.T) {
	sw := roleOwnerFixture(t)
	const name = "stored-role"
	err := sw.server.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{Name: name, Permissions: []string{auth.PermJobsWrite}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, sw.server.tenant, ""} {
		roleOwnerPermission(t, sw, name, tenant, tenant == sw.server.tenant || tenant == "")
	}
	for _, tenant := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, sw.server.tenant} {
		r := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/auth/roles", nil), &auth.AuthContext{Role: "admin", Tenant: tenant})
		rr := httptest.NewRecorder()
		sw.server.handleListRoles(rr, r)
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), name) != (tenant == sw.server.tenant) {
			t.Errorf("stored role list for %s=%d %s", tenant, rr.Code, rr.Body.String())
		}
	}
}

func roleOwnerStoredChain(t *testing.T) {
	sw := roleOwnerFixture(t)
	roleOwnerPut(t, sw, sw.tenantB, "chain-parent", `{"permissions":["jobs.write"]}`)
	ctx := context.Background()
	client := sw.server.jobStore.Client()
	data := fmt.Sprintf(`{"name":"chain-child","tenant":%q,"inherits":["chain-parent"]}`, sw.tenantA.tenantID)
	if err := client.Set(ctx, "rbac:role:chain-child", data, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "rbac:roles", "chain-child").Err(); err != nil {
		t.Fatal(err)
	}
	roleOwnerPermission(t, sw, "chain-child", sw.tenantA.tenantID, false)
	rr := sw.as(t, sw.tenantA, http.MethodPut, "/api/v1/auth/roles/chain-grandchild", `{"inherits":["chain-child"]}`)
	_, err := sw.server.rbacStore.GetRole(ctx, "chain-grandchild")
	if rr.Code != http.StatusBadRequest || !errors.Is(err, auth.ErrRoleNotFound) {
		t.Fatalf("chain parent must be owned at each depth: %d %v", rr.Code, err)
	}
}

func roleOwnerStoreUpdate(t *testing.T) {
	sw := roleOwnerFixture(t)
	roleOwnerPut(t, sw, sw.tenantB, "store-owned", `{"description":"owner-data"}`)
	ctx := context.Background()
	before, err := sw.server.rbacStore.GetRole(ctx, "store-owned")
	if err != nil {
		t.Fatal(err)
	}
	var replacement auth.RoleDefinition
	data := fmt.Sprintf(`{"name":"store-owned","tenant":%q,"description":"new-data"}`, sw.tenantA.tenantID)
	if err := json.Unmarshal([]byte(data), &replacement); err != nil {
		t.Fatal(err)
	}
	if err := sw.server.rbacStore.PutRole(ctx, &replacement); !errors.Is(err, auth.ErrRoleNotFound) {
		t.Errorf("store owner check: %v", err)
	}
	after, err := sw.server.rbacStore.GetRole(ctx, "store-owned")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("store must retain owner data: %+v %v", after, err)
	}
}

func roleOwnerBuiltIns(t *testing.T) {
	sw := roleOwnerFixture(t)
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB} {
		rr := sw.as(t, who, http.MethodGet, "/api/v1/auth/roles", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"name":"admin"`) || !strings.Contains(rr.Body.String(), `"name":"viewer"`) {
			t.Fatalf("built-in list=%d %s", rr.Code, rr.Body.String())
		}
		rr = sw.as(t, who, http.MethodPut, "/api/v1/auth/roles/admin", `{"description":"changed"}`)
		if rr.Code != http.StatusConflict {
			t.Fatalf("built-in change=%d %s", rr.Code, rr.Body.String())
		}
		roleOwnerPermission(t, sw, "admin", who.tenantID, true)
	}
}
