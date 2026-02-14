package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

// tenantStrictAuth enforces tenant isolation — denies cross-tenant access.
type tenantStrictAuth struct {
	tenant string
	role   string
}

func (a *tenantStrictAuth) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	return &AuthContext{Tenant: a.tenant, Role: a.role}, nil
}
func (a *tenantStrictAuth) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return &AuthContext{Tenant: a.tenant, Role: a.role}, nil
}
func (a *tenantStrictAuth) RequireRole(r *http.Request, roles ...string) error {
	auth := authFromRequest(r)
	if auth == nil {
		return errors.New("unauthorized")
	}
	for _, role := range roles {
		if auth.Role == role {
			return nil
		}
	}
	return errors.New("forbidden")
}
func (a *tenantStrictAuth) ResolveTenant(_ *http.Request, requested, _ string) (string, error) {
	return requested, nil
}
func (a *tenantStrictAuth) RequireTenantAccess(r *http.Request, tenant string) error {
	auth := authFromRequest(r)
	if auth == nil {
		return errors.New("unauthorized")
	}
	if auth.Tenant != tenant {
		return errors.New("tenant access denied")
	}
	return nil
}
func (a *tenantStrictAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

func TestMemoryTenantIsolation_CrossTenantBlocked(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	// Create a job owned by tenant-B.
	jobID := "job-tenant-b"
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant-b")
	ctxKey := store.MakeContextKey(jobID)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"secret":"data"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	// Authenticate as admin in tenant-a.
	s.auth = &tenantStrictAuth{tenant: "tenant-a", role: "admin"}
	authCtx := &AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "user-a"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory?key="+ctxKey, nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleGetMemory(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant read, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMemoryTenantIsolation_OwnTenantAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	// Create a job owned by tenant-a.
	jobID := "job-tenant-a"
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant-a")
	ctxKey := store.MakeContextKey(jobID)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"data":"ok"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	// Authenticate as admin in tenant-a.
	s.auth = &tenantStrictAuth{tenant: "tenant-a", role: "admin"}
	authCtx := &AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "user-a"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory?key="+ctxKey, nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleGetMemory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for own-tenant read, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStatusHidesInternalsForNonAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Authenticate as viewer (not admin).
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	authCtx := &AuthContext{Tenant: "default", Role: "viewer"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	nats, ok := resp["nats"].(map[string]any)
	if !ok {
		t.Fatal("missing nats in response")
	}
	if _, hasURL := nats["url"]; hasURL {
		t.Fatal("nats.url should be hidden for non-admin")
	}

	redisResp, ok := resp["redis"].(map[string]any)
	if !ok {
		t.Fatal("missing redis in response")
	}
	if _, hasErr := redisResp["error"]; hasErr {
		t.Fatal("redis.error should be hidden for non-admin")
	}
}
