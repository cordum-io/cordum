package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/redis/go-redis/v9"
)

func identityWriterSweep(t *testing.T, platform string) (*crossTenantSweep, *auth.RedisUserStore) {
	t.Helper()
	t.Setenv("CORDUM_BCRYPT_COST", "4")
	sw := newCrossTenantSweep(t)
	sw.server.tenant = platform
	if platform == "" {
		platform = "default"
	}
	a, b, p, x := sw.tenantA, sw.tenantB, systemPolicyPlatform, systemPolicySpanning
	p.tenantID = platform
	basic := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin","allow_cross_tenant":true}]`,
			a.apiKey, a.tenantID, b.apiKey, b.tenantID, p.apiKey, p.tenantID, x.apiKey, x.tenantID),
	})
	client, ok := sw.server.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatalf("Redis client type: %T", sw.server.jobStore.Client())
	}
	users := auth.NewRedisUserStoreFromClient(client)
	keys := auth.NewRedisKeyStoreFromClient(client)
	basic.SetUserStore(users)
	basic.SetKeyStore(keys)
	t.Cleanup(basic.DrainUsage)
	sw.server.auth, sw.server.userStore, sw.server.keyStore = basic, users, keys
	sw.handler = apiKeyMiddleware(basic, tenantMiddleware(basic, maxBodyMiddleware(sw.mux, sw.server.entitlements)))
	return sw, users
}

func TestIdentityWritersKeepTheCallerTenant(t *testing.T) {
	t.Run("users-create", testIdentityUserCreate)
	t.Run("users-update", testIdentityUserUpdate)
	t.Run("keys-create", testIdentityKeyCreate)
	t.Run("tenant-header", testIdentityWriterHeaders)
}

func testIdentityUserCreate(t *testing.T) {
	for _, tc := range []struct {
		name, bodyTenant, caller, gateway, stored string
	}{
		{"other", edgeRouteOtherTenant, "a", "platform", ""},
		{"gateway", "platform", "a", "platform", ""},
		{"trimmed-other", " \t" + edgeRouteOtherTenant + "\n", "a", "platform", ""},
		{"case-distinct", strings.ToUpper(edgeRouteTenant), "a", "platform", ""},
		{"own", edgeRouteTenant, "a", "platform", edgeRouteTenant},
		{"omitted", "", "a", "platform", edgeRouteTenant},
		{"trimmed-own", " " + edgeRouteTenant + " ", "a", "platform", edgeRouteTenant},
		{"spanning", edgeRouteOtherTenant, "spanning", "platform", edgeRouteOtherTenant},
		{"platform", edgeRouteOtherTenant, "platform", "platform", edgeRouteOtherTenant},
		{"default-platform", edgeRouteOtherTenant, "platform", "", edgeRouteOtherTenant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw, users := identityWriterSweep(t, tc.gateway)
			who := sw.tenantA
			gateway := tc.gateway
			if gateway == "" {
				gateway = "default"
			}
			if tc.caller == "spanning" {
				who = systemPolicySpanning
			} else if tc.caller == "platform" {
				who = systemPolicyPlatform
				who.tenantID = gateway
			}
			body := identityUserBody("new-user", tc.bodyTenant)
			rr := sw.as(t, who, http.MethodPost, "/api/v1/users", body)
			want := http.StatusCreated
			if tc.stored == "" {
				want = http.StatusForbidden
			}
			if rr.Code != want {
				t.Errorf("create status=%d, want %d", rr.Code, want)
			}
			assertIdentityUserTenant(t, users, "new-user", tc.stored,
				sw.tenantA.tenantID, sw.tenantB.tenantID, gateway, strings.TrimSpace(tc.bodyTenant))
		})
	}
}

func identityUserBody(username, tenant string) string {
	body := fmt.Sprintf(`{"username":%q,"password":"Writer-Passw0rd!"`, username)
	if tenant != "" {
		body += fmt.Sprintf(`,"tenant":%q`, tenant)
	}
	return body + "}"
}

func assertIdentityUserTenant(t *testing.T, users *auth.RedisUserStore, username, stored string, tenants ...string) {
	t.Helper()
	for _, tenant := range tenants {
		if tenant == "" {
			continue
		}
		user, err := users.GetByUsername(context.Background(), username, tenant)
		if tenant == stored {
			if err != nil || user == nil || user.Tenant != stored {
				t.Errorf("user in %q: present=%t, error=%v", tenant, user != nil, err)
			}
		} else if !errors.Is(err, auth.ErrUserNotFound) || user != nil {
			t.Errorf("expected no user in %q: present=%t, error=%v", tenant, user != nil, err)
		}
	}
}

func testIdentityUserUpdate(t *testing.T) {
	for _, owner := range []string{"a", "b"} {
		t.Run(owner, func(t *testing.T) {
			sw, users := identityWriterSweep(t, "platform")
			who, want := sw.tenantA, http.StatusBadRequest
			if owner == "b" {
				who, want = sw.tenantB, http.StatusNotFound
			}
			rr := sw.as(t, who, http.MethodPost, "/api/v1/users", identityUserBody("existing-user", ""))
			if rr.Code != http.StatusCreated {
				t.Fatalf("seed status=%d", rr.Code)
			}
			before, err := users.GetByUsername(context.Background(), "existing-user", who.tenantID)
			if err != nil {
				t.Fatal(err)
			}
			rr = sw.as(t, sw.tenantA, http.MethodPut, "/api/v1/users/"+before.ID,
				`{"tenant":"platform","display_name":"updated"}`)
			if rr.Code != want {
				t.Errorf("update status=%d, want %d", rr.Code, want)
			}
			after, err := users.GetByID(context.Background(), before.ID)
			if err != nil || after == nil {
				t.Fatalf("read updated user: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Error("user changed after tenant field was refused")
			}
			if owner == "a" {
				rr = sw.as(t, who, http.MethodPut, "/api/v1/users/"+before.ID, `{"display_name":"updated"}`)
				after, err = users.GetByID(context.Background(), before.ID)
				if rr.Code != http.StatusOK || err != nil || after == nil || after.DisplayName != "updated" || after.Tenant != who.tenantID {
					t.Fatalf("valid update status=%d, error=%v", rr.Code, err)
				}
			}
			assertIdentityUserTenant(t, users, "existing-user", who.tenantID,
				sw.tenantA.tenantID, sw.tenantB.tenantID, "platform")
		})
	}
}

func testIdentityKeyCreate(t *testing.T) {
	sw, _ := identityWriterSweep(t, "platform")
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/auth/keys", `{"name":"writer-key"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("key create status=%d", rr.Code)
	}
	before, err := sw.server.keyStore.List(context.Background(), sw.tenantA.tenantID)
	if err != nil || len(before) != 1 || before[0].Tenant != sw.tenantA.tenantID {
		t.Fatalf("caller keys count=%d, error=%v", len(before), err)
	}
	for _, tenant := range []string{edgeRouteOtherTenant, "platform"} {
		t.Run(tenant, func(t *testing.T) {
			rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/auth/keys", fmt.Sprintf(`{"name":"new-key","tenant":%q}`, tenant))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("key tenant field status=%d", rr.Code)
			}
			for _, target := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, "platform"} {
				keys, err := sw.server.keyStore.List(context.Background(), target)
				want := 0
				if target == sw.tenantA.tenantID {
					want = 1
				}
				if err != nil || len(keys) != want {
					t.Fatalf("stored keys in %q: count=%d, err=%v", target, len(keys), err)
				}
				if want == 1 && !reflect.DeepEqual(keys, before) {
					t.Error("caller key changed")
				}
			}
		})
	}
}

func testIdentityWriterHeaders(t *testing.T) {
	for _, target := range []string{edgeRouteOtherTenant, "platform"} {
		t.Run(target, func(t *testing.T) {
			sw := newCrossTenantSweep(t)
			seed := seedFleetAgents(t, sw)
			sw.server.tenant = "platform"
			who := sw.tenantA
			who.tenantID = target
			for _, tc := range []struct{ name, path, body string }{
				{"agent", "/api/v1/agents", fleetAgentBody("writer-agent", "")},
				{"credential", "/api/v1/workers/credentials", fleetCredentialBody()},
				{"delegation", "/api/v1/agents/" + seed.source.ID + "/delegate", fleetDelegateBody(seed)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := identityWriterState(t, sw)
					rr := sw.as(t, who, http.MethodPost, tc.path, tc.body)
					if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "tenant access denied") {
						t.Errorf("tenant header status=%d, body=%s", rr.Code, rr.Body.String())
					}
					if after := identityWriterState(t, sw); after != before {
						t.Error("stored identities changed")
					}
				})
			}
		})
	}
}

func identityWriterState(t *testing.T, sw *crossTenantSweep) string {
	t.Helper()
	var state strings.Builder
	for _, tenant := range []string{sw.tenantA.tenantID, sw.tenantB.tenantID, sw.server.tenant} {
		agents, next, err := sw.server.agentIdentityStore.List(context.Background(), tenant, "", 100, store.AgentIdentityFilter{})
		if err != nil || next != "" {
			t.Fatalf("list agents: %v, next=%q", err, next)
		}
		keys, err := sw.server.workerCredentialStore.List(context.Background(), tenant)
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := sw.server.delegationListStore().ListAll(context.Background(), tenant, delegation.DelegationListFilter{}, "", 100)
		if err != nil || tokens.NextCursor != "" {
			t.Fatalf("list delegations: %v", err)
		}
		enc := json.NewEncoder(&state)
		if err := enc.Encode(agents); err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode(keys); err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode(tokens); err != nil {
			t.Fatal(err)
		}
	}
	return state.String()
}
