package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/licensing"
)

func newTestRBACStore(t *testing.T) (*RBACStore, *miniredis.Miniredis) {
	t.Helper()
	srv := newTestMiniredis(t)
	client := newTestRedisClient(t, srv.Addr())
	store := NewRBACStoreFromClient(client)
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, srv
}

func bootstrapRoles(t *testing.T, store *RBACStore) {
	t.Helper()
	if err := store.BootstrapDefaultRoles(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
}

func seedRBACRoleRaw(t *testing.T, store *RBACStore, role *RoleDefinition) {
	t.Helper()
	data, err := json.Marshal(role)
	if err != nil {
		t.Fatalf("marshal role: %v", err)
	}
	ctx := context.Background()
	if err := store.client.Set(ctx, rbacRoleKeyPrefix+role.Name, data, 0).Err(); err != nil {
		t.Fatalf("seed role %s: %v", role.Name, err)
	}
	if err := store.client.SAdd(ctx, rbacRoleSetKey, role.Name).Err(); err != nil {
		t.Fatalf("seed role set %s: %v", role.Name, err)
	}
}

// ---------------------------------------------------------------------------
// (1) Permission resolution with hierarchy (admin > operator > viewer)
// ---------------------------------------------------------------------------

func TestResolvePermissions_AdminHasAll(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	perms, err := store.ResolvePermissions(context.Background(), "admin")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsPerm(perms, PermAdminAll) {
		t.Fatalf("admin should have admin.*, got %v", perms)
	}
}

func TestResolvePermissions_OperatorInheritsViewer(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	perms, err := store.ResolvePermissions(context.Background(), "operator")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Operator inherits viewer; should have viewer's permissions
	if !containsPerm(perms, PermJobsRead) {
		t.Fatalf("operator should have jobs.read via inheritance, got %v", perms)
	}
	if !containsPerm(perms, PermJobsWrite) {
		t.Fatalf("operator should have jobs.write, got %v", perms)
	}
}

func TestResolvePermissions_ViewerHasReadOnly(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	perms, err := store.ResolvePermissions(context.Background(), "viewer")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsPerm(perms, PermJobsRead) {
		t.Fatalf("viewer should have jobs.read, got %v", perms)
	}
	if containsPerm(perms, PermJobsWrite) {
		t.Fatalf("viewer should NOT have jobs.write, got %v", perms)
	}
	if containsPerm(perms, PermAdminAll) {
		t.Fatalf("viewer should NOT have admin.*, got %v", perms)
	}
}

// ---------------------------------------------------------------------------
// (2) Custom role with specific permissions
// ---------------------------------------------------------------------------

func TestCustomRole_SpecificPermissions(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	custom := &RoleDefinition{
		Name:        "auditor",
		Description: "Audit-only access",
		Permissions: []string{PermAuditRead, PermJobsRead},
	}
	if err := store.PutRole(context.Background(), custom); err != nil {
		t.Fatalf("put role: %v", err)
	}

	perms, err := store.ResolvePermissions(context.Background(), "auditor")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsPerm(perms, PermAuditRead) {
		t.Fatalf("auditor should have audit.read, got %v", perms)
	}
	if !containsPerm(perms, PermJobsRead) {
		t.Fatalf("auditor should have jobs.read, got %v", perms)
	}
	if containsPerm(perms, PermJobsWrite) {
		t.Fatalf("auditor should NOT have jobs.write, got %v", perms)
	}
}

// ---------------------------------------------------------------------------
// (3) Role inheritance chain flattening
// ---------------------------------------------------------------------------

func TestInheritanceChain_Flattening(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	// Create a custom role that inherits from operator (which inherits viewer)
	custom := &RoleDefinition{
		Name:        "senior_operator",
		Description: "Operator with policy write access",
		Permissions: []string{PermPolicyWrite},
		Inherits:    []string{"operator"},
	}
	if err := store.PutRole(context.Background(), custom); err != nil {
		t.Fatalf("put role: %v", err)
	}

	perms, err := store.ResolvePermissions(context.Background(), "senior_operator")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Should have own permission
	if !containsPerm(perms, PermPolicyWrite) {
		t.Fatalf("should have policy.write, got %v", perms)
	}
	// Should have operator's permissions
	if !containsPerm(perms, PermJobsWrite) {
		t.Fatalf("should inherit jobs.write from operator, got %v", perms)
	}
	// Should have viewer's permissions (through operator)
	if !containsPerm(perms, PermWorkflowsRead) {
		t.Fatalf("should inherit workflows.read from viewer via operator, got %v", perms)
	}
}

func TestResolvePermissions_DiamondInheritance(t *testing.T) {
	store, _ := newTestRBACStore(t)
	ctx := context.Background()

	roles := []*RoleDefinition{
		{Name: "role_d", Permissions: []string{PermAuditRead}},
		{Name: "role_b", Inherits: []string{"role_d"}},
		{Name: "role_c", Inherits: []string{"role_d"}},
		{Name: "role_a", Inherits: []string{"role_b", "role_c"}},
	}
	for _, role := range roles {
		if err := store.PutRole(ctx, role); err != nil {
			t.Fatalf("put %s: %v", role.Name, err)
		}
	}

	if !HasPermission(ctx, store, "role_a", PermAuditRead, true) {
		t.Fatalf("diamond inheritance did not resolve %q through B/C -> D", PermAuditRead)
	}
}

// ---------------------------------------------------------------------------
// (4) Circular inheritance detection
// ---------------------------------------------------------------------------

func TestCircularInheritance_Detection(t *testing.T) {
	store, _ := newTestRBACStore(t)

	// Create a cycle: A -> B -> A
	roleA := &RoleDefinition{
		Name:     "role_a",
		Inherits: []string{"role_b"},
	}
	roleB := &RoleDefinition{
		Name:     "role_b",
		Inherits: []string{"role_a"},
	}
	seedRBACRoleRaw(t, store, roleA)
	seedRBACRoleRaw(t, store, roleB)

	_, err := store.ResolvePermissions(context.Background(), "role_a")
	if err == nil {
		t.Fatal("expected error for circular inheritance, got nil")
	}
}

func TestValidateInheritance_DetectsCycle(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	// Try to make viewer inherit admin (which doesn't inherit anything, but
	// then if we set admin to inherit viewer it would cycle)
	roleX := &RoleDefinition{Name: "role_x"}
	roleY := &RoleDefinition{Name: "role_y", Inherits: []string{"role_x"}}
	if err := store.PutRole(context.Background(), roleX); err != nil {
		t.Fatalf("put role_x: %v", err)
	}
	if err := store.PutRole(context.Background(), roleY); err != nil {
		t.Fatalf("put role_y: %v", err)
	}

	// Now try to validate making role_x inherit role_y — should detect cycle
	err := store.ValidateInheritance(context.Background(), "role_x", []string{"role_y"})
	if err == nil {
		t.Fatal("expected cycle detection error, got nil")
	}
}

func TestValidateInheritance_AllowsDiamond(t *testing.T) {
	store, _ := newTestRBACStore(t)
	ctx := context.Background()

	for _, role := range []*RoleDefinition{
		{Name: "role_d", Permissions: []string{PermAuditRead}},
		{Name: "role_b", Inherits: []string{"role_d"}},
		{Name: "role_c", Inherits: []string{"role_d"}},
	} {
		if err := store.PutRole(ctx, role); err != nil {
			t.Fatalf("put %s: %v", role.Name, err)
		}
	}

	if err := store.ValidateInheritance(ctx, "role_a", []string{"role_b", "role_c"}); err != nil {
		t.Fatalf("diamond inheritance should be valid: %v", err)
	}
}

func TestValidateInheritance_RejectsUnknownParent(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	err := store.ValidateInheritance(context.Background(), "new_role", []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown parent, got nil")
	}
}

// ---------------------------------------------------------------------------
// (5) Entitlement disabled → basic role fallback
// ---------------------------------------------------------------------------

func TestHasPermission_BasicFallback_Admin(t *testing.T) {
	// Without RBAC entitled, admin should have all permissions via basic mapping
	if !HasPermission(context.Background(), nil, "admin", PermJobsWrite, false) {
		t.Fatal("admin should have jobs.write in basic mode")
	}
	if !HasPermission(context.Background(), nil, "admin", PermConfigWrite, false) {
		t.Fatal("admin should have config.write in basic mode")
	}
}

func TestHasPermission_BasicFallback_Viewer(t *testing.T) {
	if !HasPermission(context.Background(), nil, "viewer", PermJobsRead, false) {
		t.Fatal("viewer should have jobs.read in basic mode")
	}
	if HasPermission(context.Background(), nil, "viewer", PermJobsWrite, false) {
		t.Fatal("viewer should NOT have jobs.write in basic mode")
	}
}

func TestHasPermission_BasicFallback_UnknownRole(t *testing.T) {
	if HasPermission(context.Background(), nil, "hacker", PermJobsRead, false) {
		t.Fatal("unknown role should have no permissions in basic mode")
	}
}

func TestHasPermission_EntitledMode(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	// With entitlement enabled, admin still has all via admin.*
	if !HasPermission(context.Background(), store, "admin", PermJobsWrite, true) {
		t.Fatal("admin should have jobs.write in entitled mode")
	}

	// Viewer can read but not write
	if !HasPermission(context.Background(), store, "viewer", PermJobsRead, true) {
		t.Fatal("viewer should have jobs.read in entitled mode")
	}
	if HasPermission(context.Background(), store, "viewer", PermJobsWrite, true) {
		t.Fatal("viewer should NOT have jobs.write in entitled mode")
	}
}

// ---------------------------------------------------------------------------
// (6) Role CRUD tests
// ---------------------------------------------------------------------------

func TestRoleCRUD(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	ctx := context.Background()

	// List should have 3 default roles
	roles, err := store.ListRoles(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("expected 3 default roles, got %d", len(roles))
	}

	// Create a custom role
	custom := &RoleDefinition{
		Name:        "devops",
		Description: "DevOps engineer",
		Permissions: []string{PermJobsRead, PermJobsWrite, PermConfigRead, PermConfigWrite},
		Inherits:    []string{"viewer"},
	}
	if err := store.PutRole(ctx, custom); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get the role
	got, err := store.GetRole(ctx, "devops")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "devops" || got.Description != "DevOps engineer" {
		t.Fatalf("unexpected role: %+v", got)
	}

	// Update the role
	got.Description = "Updated DevOps"
	if err := store.PutRole(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := store.GetRole(ctx, "devops")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Description != "Updated DevOps" {
		t.Fatalf("description not updated: %s", updated.Description)
	}

	// List should now have 4 roles
	roles, err = store.ListRoles(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(roles) != 4 {
		t.Fatalf("expected 4 roles, got %d", len(roles))
	}

	// Delete the custom role
	if err := store.DeleteRole(ctx, "devops"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify deleted
	_, err = store.GetRole(ctx, "devops")
	if err == nil {
		t.Fatal("expected error after delete")
	}

	// Cannot delete built-in role
	if err := store.DeleteRole(ctx, "admin"); err == nil {
		t.Fatal("should not be able to delete built-in admin role")
	}
}

func TestPutRole_DeleteRole_OptimisticLocking(t *testing.T) {
	store, _ := newTestRBACStore(t)
	ctx := context.Background()
	if err := store.PutRole(ctx, &RoleDefinition{Name: "role_b", Permissions: []string{PermJobsRead}}); err != nil {
		t.Fatalf("put role_b: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var putErr, deleteErr error
	go func() {
		defer wg.Done()
		<-start
		putErr = store.PutRole(ctx, &RoleDefinition{Name: "role_a", Inherits: []string{"role_b"}})
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteRole(ctx, "role_b")
	}()
	close(start)
	wg.Wait()

	successes := 0
	if putErr == nil {
		successes++
	}
	if deleteErr == nil {
		successes++
	}
	if successes != 1 {
		t.Fatalf("expected exactly one concurrent role mutation to commit, got putErr=%v deleteErr=%v", putErr, deleteErr)
	}
}

func TestGetRole_NotFound(t *testing.T) {
	store, _ := newTestRBACStore(t)
	_, err := store.GetRole(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent role")
	}
}

func TestBootstrapDefaultRoles_Idempotent(t *testing.T) {
	store, _ := newTestRBACStore(t)

	// Bootstrap twice — should not fail or duplicate
	bootstrapRoles(t, store)
	bootstrapRoles(t, store)

	roles, err := store.ListRoles(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("expected 3 roles after double bootstrap, got %d", len(roles))
	}
}

// ---------------------------------------------------------------------------
// (7) Permission middleware blocks unauthorized access
// ---------------------------------------------------------------------------

func TestPermissionChecker_RequirePermission(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	// Entitled mode
	checker := NewPermissionChecker(store, func() licensing.Entitlements {
		return licensing.Entitlements{RBAC: true}
	})

	// Admin should pass any permission check
	r := requestWithAuth("admin")
	if err := checker.RequirePermission(r, PermJobsWrite); err != nil {
		t.Fatalf("admin should have jobs.write: %v", err)
	}

	// Viewer should fail write permission
	r = requestWithAuth("viewer")
	if err := checker.RequirePermission(r, PermJobsWrite); err == nil {
		t.Fatal("viewer should NOT have jobs.write")
	}

	// Viewer should pass read permission
	if err := checker.RequirePermission(r, PermJobsRead); err != nil {
		t.Fatalf("viewer should have jobs.read: %v", err)
	}
}

func TestPermissionChecker_BasicFallback(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	// Not entitled — uses basic fallback
	checker := NewPermissionChecker(store, func() licensing.Entitlements {
		return licensing.Entitlements{RBAC: false}
	})

	r := requestWithAuth("admin")
	if err := checker.RequirePermission(r, PermJobsWrite); err != nil {
		t.Fatalf("admin should have jobs.write in basic mode: %v", err)
	}

	r = requestWithAuth("viewer")
	if err := checker.RequirePermission(r, PermJobsWrite); err == nil {
		t.Fatal("viewer should NOT have jobs.write in basic mode")
	}
}

func TestPermissionChecker_NilAuth(t *testing.T) {
	checker := NewPermissionChecker(nil, func() licensing.Entitlements {
		return licensing.Entitlements{RBAC: false}
	})

	// No auth context — should fail
	r := httptest.NewRequest("GET", "/", nil)
	if err := checker.RequirePermission(r, PermJobsRead); err == nil {
		t.Fatal("should fail when no auth context")
	}
}

func TestPermissionChecker_EmptyPermission(t *testing.T) {
	checker := NewPermissionChecker(nil, func() licensing.Entitlements {
		return licensing.Entitlements{RBAC: false}
	})

	r := requestWithAuth("viewer")
	// Empty permission should pass (no restriction)
	if err := checker.RequirePermission(r, ""); err != nil {
		t.Fatalf("empty permission should pass: %v", err)
	}
}

func TestPermissionChecker_Middleware(t *testing.T) {
	store, _ := newTestRBACStore(t)
	bootstrapRoles(t, store)

	checker := NewPermissionChecker(store, func() licensing.Entitlements {
		return licensing.Entitlements{RBAC: true}
	})

	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := checker.RequirePermissionMiddleware(PermConfigWrite)
	handler := middleware(inner)

	// Admin should pass
	r := requestWithAuth("admin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin should pass, got %d", w.Code)
	}
	if !innerCalled {
		t.Fatal("inner handler should have been called for admin")
	}

	// Viewer should be blocked
	innerCalled = false
	r = requestWithAuth("viewer")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer should be blocked, got %d", w.Code)
	}
	if innerCalled {
		t.Fatal("inner handler should NOT have been called for viewer")
	}
}

// ---------------------------------------------------------------------------
// matchPermission tests
// ---------------------------------------------------------------------------

func TestMatchPermission_Wildcard(t *testing.T) {
	perms := []string{PermAdminAll}
	if !matchPermission(perms, PermJobsRead) {
		t.Fatal("admin.* should match jobs.read")
	}
	if !matchPermission(perms, PermConfigWrite) {
		t.Fatal("admin.* should match config.write")
	}
}

func TestMatchPermission_NamespaceWildcard(t *testing.T) {
	perms := []string{"jobs.*"}
	if !matchPermission(perms, PermJobsRead) {
		t.Fatal("jobs.* should match jobs.read")
	}
	if !matchPermission(perms, PermJobsWrite) {
		t.Fatal("jobs.* should match jobs.write")
	}
	if matchPermission(perms, PermConfigRead) {
		t.Fatal("jobs.* should NOT match config.read")
	}
}

func TestMatchPermission_ExactMatch(t *testing.T) {
	perms := []string{PermJobsRead}
	if !matchPermission(perms, PermJobsRead) {
		t.Fatal("exact match should work")
	}
	if matchPermission(perms, PermJobsWrite) {
		t.Fatal("different permission should not match")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func requestWithAuth(role string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	ctx := context.WithValue(r.Context(), ContextKey{}, &AuthContext{
		Role:   role,
		Tenant: "default",
	})
	return r.WithContext(ctx)
}

func containsPerm(perms []string, target string) bool {
	return slices.Contains(perms, target)
}
