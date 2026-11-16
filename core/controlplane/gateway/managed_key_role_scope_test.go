package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

func TestManagedKeyRoleScope_Minting(t *testing.T) {
	f := newEdgeScopeFixture(t)
	for _, tc := range []struct {
		name, held, requested string
		code                  int
	}{
		{"refuse admin", "write", "admin", http.StatusForbidden},
		{"scope gate", "jobs:write", "jobs:write", http.StatusForbidden},
		{"held resource", "write", "jobs:write", http.StatusCreated},
		{"held bare scope", "write", "write", http.StatusCreated},
		{"held admin", "admin", "admin", http.StatusCreated},
		{"admin resource wildcard", "admin", "jobs:*", http.StatusCreated},
		{"refuse resource wildcard", "write", "jobs:*", http.StatusForbidden},
		{"normalized bare scope", " A D M I N ,write", "admin", http.StatusCreated},
		{"one unheld scope", "write", "jobs:write,admin", http.StatusForbidden},
		{"static admin", "static", "admin", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secret := edgeScopeStaticKey
			if tc.held != "static" {
				_, secret = f.mint(t, strings.Split(tc.held, ",")...)
			}
			before := managedRoleKeyCount(t, f)
			body := `{"name":"role-scope","scopes":["` + strings.ReplaceAll(tc.requested, ",", `","`) + `"]}`
			rec := f.post(t, secret, "/api/v1/auth/keys", body)
			want := before
			if tc.code == http.StatusCreated {
				want++
			}
			if after := managedRoleKeyCount(t, f); rec.Code != tc.code || after != want {
				t.Fatalf("status=%d keys=%d->%d, want %d/%d: %s", rec.Code, before, after, tc.code, want, rec.Body.String())
			}
		})
	}
}

func TestManagedKeyRoleScope_Cancel(t *testing.T) {
	for _, rbac := range []bool{false, true} {
		for _, scope := range []string{"jobs:write", "jobs:*", "admin", ""} {
			t.Run(fmt.Sprintf("rbac=%v/scope=%s", rbac, scope), func(t *testing.T) {
				f := newEdgeScopeFixture(t)
				setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.RBAC = rbac })
				if auth.RBACEntitled(f.s.currentEntitlements()) != rbac {
					t.Fatal("wrong RBAC entitlement")
				}
				var scopes []string
				if scope != "" {
					scopes = []string{scope}
				}
				key, secret := f.mint(t, scopes...)
				jobID := managedRoleRunningJob(t, f, key.ID)
				rec := f.post(t, secret, "/api/v1/jobs/"+jobID+"/cancel", "{}")
				wantCode, wantState := http.StatusOK, model.JobStateCancelled
				if scope == "" {
					wantCode, wantState = http.StatusForbidden, model.JobStateRunning
				}
				state, err := f.s.jobStore.GetState(context.Background(), jobID)
				if rec.Code != wantCode || err != nil || state != wantState {
					t.Fatalf("status=%d state=%s err=%v, want %d/%s: %s", rec.Code, state, err, wantCode, wantState, rec.Body.String())
				}
			})
		}
	}
}

func TestManagedKeyRoleScope_LegalHold(t *testing.T) {
	for _, scope := range []string{"audit:write", "audit:*", "write"} {
		t.Run(scope, func(t *testing.T) {
			f := newEdgeScopeFixture(t)
			setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.RBAC = false })
			client, ok := f.s.jobStore.Client().(*redis.Client)
			if !ok {
				t.Fatal("expected Redis client")
			}
			f.s.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
			_, secret := f.mint(t, scope)
			rec := f.post(t, secret, "/api/v1/audit/legal-hold", `{"reason":"scope test"}`)
			wantCode, wantCount := http.StatusForbidden, 0
			if scope == "write" {
				wantCode, wantCount = http.StatusCreated, 1
			}
			holds, err := f.s.legalHoldStore.ListHolds(context.Background(), "default")
			if rec.Code != wantCode || err != nil || len(holds) != wantCount {
				t.Fatalf("status=%d holds=%d err=%v, want %d/%d: %s", rec.Code, len(holds), err, wantCode, wantCount, rec.Body.String())
			}
		})
	}
}

func TestManagedKeyRoleScope_LegalHoldPaths(t *testing.T) {
	for _, tc := range []struct{ method, path, scope string }{
		{"POST", "/api/v1/audit/legal-hold", "legalhold:write"},
		{"DELETE", "/api/v1/audit/legal-hold/hold-id", "legalhold:write"},
		{"GET", "/api/v1/audit/legal-holds/", "legalhold:read"},
		{"GET", "/api/v1/audit/legal-holdings", "audit:read"},
		{"GET", "/api/v1/audit/events", "audit:read"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got, ok := auth.PathToScope(tc.method, tc.path); !ok || got != tc.scope {
				t.Fatalf("scope=%q/%v, want %q", got, ok, tc.scope)
			}
		})
	}
}

type managedRoleValidationStore struct {
	auth.KeyStore
	record *auth.ManagedKey
	err    error
}

func (s managedRoleValidationStore) ValidateKey(context.Context, string) (*auth.ManagedKey, error) {
	return s.record, s.err
}

func TestManagedKeyRoleScope_Revalidation(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		scopes   []string
		err      error
		code     int
	}{
		{"empty scopes", "same", nil, nil, http.StatusForbidden},
		{"blank scopes", "same", []string{"  "}, nil, http.StatusForbidden},
		{"different identity", "different", []string{"admin"}, nil, http.StatusForbidden},
		{"nil record", "", nil, nil, http.StatusForbidden},
		{"store error", "", nil, errors.New("store offline"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEdgeScopeFixture(t)
			_, req := managedRoleAuthenticatedMint(t, f)
			var record *auth.ManagedKey
			if tc.id != "" {
				record = &auth.ManagedKey{ID: tc.id, Scopes: tc.scopes}
				if tc.id == "same" {
					record.ID = auth.FromRequest(req).KeyID
				}
			}
			f.s.keyStore = managedRoleValidationStore{KeyStore: f.keys, record: record, err: tc.err}
			rec := httptest.NewRecorder()
			before := managedRoleKeyCount(t, f)
			f.s.handleCreateKey(rec, req)
			if after := managedRoleKeyCount(t, f); rec.Code != tc.code || after != before {
				t.Fatalf("status=%d keys=%d->%d, want %d/unchanged", rec.Code, before, after, tc.code)
			}
		})
	}
}

func TestManagedKeyRoleScope_RevokedAfterAuthentication(t *testing.T) {
	f := newEdgeScopeFixture(t)
	key, req := managedRoleAuthenticatedMint(t, f)
	if err := f.keys.Revoke(req.Context(), key.ID, "default"); err != nil {
		t.Fatal(err)
	}
	before := managedRoleKeyCount(t, f)
	rec := httptest.NewRecorder()
	f.s.handleCreateKey(rec, req)
	if after := managedRoleKeyCount(t, f); rec.Code != http.StatusForbidden || after != before {
		t.Fatalf("status=%d keys=%d->%d, want 403/unchanged", rec.Code, before, after)
	}
}

func TestManagedKeyRoleScope_NonKeyCallers(t *testing.T) {
	for _, source := range []auth.AuthSource{auth.AuthSourceSession, auth.AuthSourceJWT, auth.AuthSourceOIDC} {
		t.Run(string(source), func(t *testing.T) {
			f := newEdgeScopeFixture(t)
			f.s.keyStore = managedRoleValidationStore{KeyStore: f.keys, err: errors.New("must not validate")}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/keys", strings.NewReader(`{"name":"role-scope","scopes":["admin"]}`))
			req = withAuth(req, &auth.AuthContext{AuthSource: source, Role: "admin", Tenant: "default"})
			rec := httptest.NewRecorder()
			f.s.handleCreateKey(rec, req)
			if after := managedRoleKeyCount(t, f); rec.Code != http.StatusCreated || after != 1 {
				t.Fatalf("status=%d keys=%d, want 201/1: %s", rec.Code, after, rec.Body.String())
			}
		})
	}
}

func managedRoleAuthenticatedMint(t *testing.T, f edgeScopeFixture) (*auth.ManagedKey, *http.Request) {
	t.Helper()
	key, secret := f.mint(t, "write")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/keys", strings.NewReader(`{"name":"role-scope","scopes":["jobs:write"]}`))
	req.Header.Set("X-API-Key", secret)
	req.Header.Set("X-Tenant-ID", "default")
	caller, err := f.provider.AuthenticateHTTP(req)
	f.provider.DrainUsage()
	if err != nil {
		t.Fatal(err)
	}
	return key, withAuth(req, caller)
}

func managedRoleKeyCount(t *testing.T, f edgeScopeFixture) int {
	t.Helper()
	keys, err := f.keys.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List keys: %v", err)
	}
	return len(keys)
}

func managedRoleRunningJob(t *testing.T, f edgeScopeFixture, principal string) string {
	t.Helper()
	identity := &pb.IdentityBinding{TenantId: "default", PrincipalId: principal, ActorId: principal}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "role-scope-job", Topic: "job.test"}, identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := f.s.jobStore.SetTenant(ctx, req.GetJobId(), "default"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.jobStore.SetState(ctx, req.GetJobId(), model.JobStateRunning); err != nil {
		t.Fatal(err)
	}
	return req.GetJobId()
}
