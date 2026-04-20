package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockAuthProvider implements AuthProvider for testing.
type mockAuthProvider struct {
	authHTTP func(*http.Request) (*AuthContext, error)
	authGRPC func(context.Context) (*AuthContext, error)
	role     func(*http.Request, ...string) error
	tenant   func(*http.Request, string, string) (string, error)
	access   func(*http.Request, string) error
	princ    func(*http.Request, string) (string, error)
}

func (m *mockAuthProvider) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	if m.authHTTP != nil {
		return m.authHTTP(r)
	}
	return nil, errors.New("mock: not configured")
}

func (m *mockAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	if m.authGRPC != nil {
		return m.authGRPC(ctx)
	}
	return nil, errors.New("mock: not configured")
}

func (m *mockAuthProvider) RequireRole(r *http.Request, roles ...string) error {
	if m.role != nil {
		return m.role(r, roles...)
	}
	return nil
}

func (m *mockAuthProvider) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	if m.tenant != nil {
		return m.tenant(r, requested, fallback)
	}
	return fallback, nil
}

func (m *mockAuthProvider) RequireTenantAccess(r *http.Request, tenant string) error {
	if m.access != nil {
		return m.access(r, tenant)
	}
	return nil
}

func (m *mockAuthProvider) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	if m.princ != nil {
		return m.princ(r, requested)
	}
	return requested, nil
}

// mockPublicPathProvider implements both AuthProvider and PublicPathProvider.
type mockPublicPathProvider struct {
	mockAuthProvider
	publicPaths map[string]bool
}

func (m *mockPublicPathProvider) IsPublicPath(path string) bool {
	return m.publicPaths[path]
}

func TestNewCompositeAuthProvider_RequiresAtLeastOne(t *testing.T) {
	_, err := NewCompositeAuthProvider()
	if err == nil {
		t.Fatal("expected error for zero providers")
	}
}

func TestCompositeAuthHTTP_FirstProviderSucceeds(t *testing.T) {
	p1 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return &AuthContext{APIKey: "key1", Tenant: "t1", Role: "admin"}, nil
		},
	}
	p2 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			t.Fatal("second provider should not be called")
			return nil, nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	ctx, err := comp.AuthenticateHTTP(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.APIKey != "key1" || ctx.Tenant != "t1" {
		t.Fatalf("got %+v", ctx)
	}
}

func TestCompositeAuthHTTP_FallthroughToSecondProvider(t *testing.T) {
	p1 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return nil, errors.New("p1: no token")
		},
	}
	p2 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return &AuthContext{APIKey: "key2", Tenant: "t2", AuthSource: AuthSource("basic")}, nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	ctx, err := comp.AuthenticateHTTP(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.APIKey != "key2" || ctx.AuthSource != "basic" {
		t.Fatalf("got %+v", ctx)
	}
}

func TestCompositeAuthHTTP_AllProvidersFail(t *testing.T) {
	p1 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return nil, errors.New("p1: fail")
		},
	}
	p2 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return nil, errors.New("p2: fail")
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	_, err = comp.AuthenticateHTTP(r)
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	// Should return last provider's error
	if err.Error() != "p2: fail" {
		t.Fatalf("expected last error, got %v", err)
	}
}

func TestCompositeAuthGRPC_FallthroughToSecond(t *testing.T) {
	p1 := &mockAuthProvider{
		authGRPC: func(context.Context) (*AuthContext, error) {
			return nil, errors.New("p1: no metadata")
		},
	}
	p2 := &mockAuthProvider{
		authGRPC: func(context.Context) (*AuthContext, error) {
			return &AuthContext{PrincipalID: "user1", Tenant: "t1"}, nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := comp.AuthenticateGRPC(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.PrincipalID != "user1" {
		t.Fatalf("got %+v", ctx)
	}
}

func TestCompositeAuthGRPC_AllFail(t *testing.T) {
	p1 := &mockAuthProvider{
		authGRPC: func(context.Context) (*AuthContext, error) {
			return nil, errors.New("p1: fail")
		},
	}
	comp, err := NewCompositeAuthProvider(p1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = comp.AuthenticateGRPC(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCompositeRequireRole_DelegatesToPrimary(t *testing.T) {
	roleCalled := false
	p1 := &mockAuthProvider{
		role: func(_ *http.Request, roles ...string) error {
			roleCalled = true
			if len(roles) != 1 || roles[0] != "admin" {
				t.Fatalf("expected [admin], got %v", roles)
			}
			return nil
		},
	}
	p2 := &mockAuthProvider{
		role: func(*http.Request, ...string) error {
			t.Fatal("second provider role should not be called")
			return nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	if err := comp.RequireRole(r, "admin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !roleCalled {
		t.Fatal("primary RequireRole not called")
	}
}

func TestCompositeResolveTenant_DelegatesToPrimary(t *testing.T) {
	p1 := &mockAuthProvider{
		tenant: func(_ *http.Request, requested, fallback string) (string, error) {
			if requested != "" {
				return requested, nil
			}
			return fallback, nil
		},
	}
	p2 := &mockAuthProvider{
		tenant: func(*http.Request, string, string) (string, error) {
			t.Fatal("second provider should not be called")
			return "", nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	tenant, err := comp.ResolveTenant(r, "requested-t", "fallback-t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "requested-t" {
		t.Fatalf("expected requested-t, got %s", tenant)
	}
}

func TestCompositeRequireTenantAccess_DelegatesToPrimary(t *testing.T) {
	accessErr := errors.New("forbidden")
	p1 := &mockAuthProvider{
		access: func(_ *http.Request, tenant string) error {
			if tenant == "blocked" {
				return accessErr
			}
			return nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	if err := comp.RequireTenantAccess(r, "allowed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := comp.RequireTenantAccess(r, "blocked"); !errors.Is(err, accessErr) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestCompositeResolvePrincipal_DelegatesToPrimary(t *testing.T) {
	p1 := &mockAuthProvider{
		princ: func(_ *http.Request, requested string) (string, error) {
			return "resolved-" + requested, nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	princ, err := comp.ResolvePrincipal(r, "bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if princ != "resolved-bob" {
		t.Fatalf("expected resolved-bob, got %s", princ)
	}
}

func TestCompositeIsPublicPath_AnyProviderReturnsTrue(t *testing.T) {
	p1 := &mockPublicPathProvider{
		publicPaths: map[string]bool{"/health": true},
	}
	p2 := &mockPublicPathProvider{
		publicPaths: map[string]bool{"/metrics": true},
	}
	comp, err := NewCompositeAuthProvider(p1, p2)
	if err != nil {
		t.Fatal(err)
	}
	if !comp.IsPublicPath("/health") {
		t.Fatal("/health should be public")
	}
	if !comp.IsPublicPath("/metrics") {
		t.Fatal("/metrics should be public")
	}
	if comp.IsPublicPath("/api/secret") {
		t.Fatal("/api/secret should not be public")
	}
}

func TestCompositeIsPublicPath_NoPublicPathProviders(t *testing.T) {
	p1 := &mockAuthProvider{}
	comp, err := NewCompositeAuthProvider(p1)
	if err != nil {
		t.Fatal(err)
	}
	if comp.IsPublicPath("/anything") {
		t.Fatal("should return false when no PublicPathProvider exists")
	}
}

func TestCompositeSingleProvider(t *testing.T) {
	p1 := &mockAuthProvider{
		authHTTP: func(*http.Request) (*AuthContext, error) {
			return &AuthContext{APIKey: "single", Tenant: "t1"}, nil
		},
	}
	comp, err := NewCompositeAuthProvider(p1)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	ctx, err := comp.AuthenticateHTTP(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.APIKey != "single" {
		t.Fatalf("got %+v", ctx)
	}
}
