package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func TestHandleGetRoleMissingReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/rbac/roles/missing", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "missing")
	rr := httptest.NewRecorder()
	s.handleGetRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusNotFound, "RBAC_ROLE_NOT_FOUND")
}

func TestListRolesRequiresRBACEntitlement(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = false
	})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/rbac/roles", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	rr := httptest.NewRecorder()
	s.handleListRoles(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"limit":"rbac"`) {
		t.Fatalf("expected rbac tier limit response, got %s", rr.Body.String())
	}
}

func TestHandlePutRoleInvalidInheritanceReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/custom", bytes.NewBufferString(`{"inherits":["missing-parent"]}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "custom")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handlePutRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusBadRequest, "RBAC_PERMISSION_INVALID")
}

func TestPutRole_RejectsUnknownPermission(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	for _, permission := range []string{"admin.*", "evals.*"} {
		req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/escalator",
			bytes.NewBufferString(`{"permissions":["`+permission+`"]}`)), &auth.AuthContext{
			Tenant: "default",
			Role:   "admin",
		})
		req.SetPathValue("name", "escalator")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.handlePutRole(rr, req)

		requireStableErrorCode(t, rr, http.StatusBadRequest, "RBAC_PERMISSION_INVALID")
	}
}

func TestPutRole_ProtectsBuiltInPermissions(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/admin",
		bytes.NewBufferString(`{"permissions":["jobs.read"]}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "admin")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handlePutRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusConflict, "RBAC_ROLE_PROTECTED")
}

func TestPutRole_ProtectsBuiltInDescription(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/viewer",
		bytes.NewBufferString(`{"description":"Changed","permissions":["jobs.read"]}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "viewer")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handlePutRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusConflict, "RBAC_ROLE_PROTECTED")
}

func TestPutRole_RejectsOversizedRoleRequest(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	permissions := make([]string, maxRolePermissions+1)
	for i := range permissions {
		permissions[i] = auth.PermJobsRead
	}
	body, err := json.Marshal(map[string]any{"permissions": permissions})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/oversized", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "oversized")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handlePutRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusBadRequest, "RBAC_REQUEST_INVALID")
}

func TestHandleDeleteBuiltInRoleReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/rbac/roles/admin", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "admin")
	rr := httptest.NewRecorder()
	s.handleDeleteRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusBadRequest, "RBAC_ROLE_IN_USE")
}

func TestDeleteRole_RejectsWhenInherited(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	ctx := context.Background()
	for _, role := range []*auth.RoleDefinition{
		{Name: "parent_role", Permissions: []string{auth.PermJobsRead}},
		{Name: "child_role", Inherits: []string{"parent_role"}},
	} {
		if err := s.rbacStore.PutRole(ctx, role); err != nil {
			t.Fatalf("put role %s: %v", role.Name, err)
		}
	}

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/rbac/roles/parent_role", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "parent_role")
	rr := httptest.NewRecorder()
	s.handleDeleteRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusConflict, "RBAC_ROLE_IN_USE")
	if !strings.Contains(rr.Body.String(), "child_role") {
		t.Fatalf("expected referencing role in response body, got %s", rr.Body.String())
	}
}

func TestRoleName_RegexValidation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})

	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/rbac/roles/bad_role%21",
		bytes.NewBufferString(`{"permissions":["jobs.read"]}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("name", "bad_role!")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handlePutRole(rr, req)

	requireStableErrorCode(t, rr, http.StatusBadRequest, "RBAC_REQUEST_INVALID")
}
