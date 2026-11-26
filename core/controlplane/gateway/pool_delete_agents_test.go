package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
)

func TestPoolDeleteCountsAgentsOfEveryTenant(t *testing.T) {
	for _, tc := range []struct {
		name, status, query, allowedPool string
		ownTenant                        bool
	}{
		{"other-tenant", "", "", "pool-a", false},
		{"other-tenant-force", config.PoolStatusInactive, "?force=true", "pool-a", false},
		{"own-tenant", "", "", "pool-a", true},
		{"no-agent", "", "", "pool-b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw, _ := poolPlatformSweep(t, "")
			s := sw.server
			seedPoolConfig(t, s, nil, map[string]config.PoolConfig{"pool-a": {Status: tc.status}})
			tenant := sw.tenantB.tenantID
			if tc.ownTenant {
				tenant = poolPlatformAdmin.tenantID
			}
			agent, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
				ID: "pool-agent", TenantID: tenant, Name: "Pool agent", Owner: "ops",
				RiskTier: "low", AllowedPools: []string{tc.allowedPool},
			})
			if err != nil {
				t.Fatalf("create agent identity: %v", err)
			}
			before, mark := poolPlatformPools(t, sw), sw.publishMark()
			rr := sw.as(t, poolPlatformAdmin, http.MethodDelete, "/api/v1/pools/pool-a"+tc.query, "")
			if tc.allowedPool == "pool-b" {
				if rr.Code != http.StatusNoContent {
					t.Fatalf("delete: status=%d, want 204; body=%s", rr.Code, rr.Body.String())
				}
				poolDeleteAgentAssertAbsent(t, sw, "pool-a")
				return
			}
			poolPlatformUnchanged(t, sw, before, mark)
			poolDeleteAgentAssertInUse(t, rr, agent.ID)
		})
	}
}

func TestPoolDeleteCountsPagedAgents(t *testing.T) {
	sw, _ := poolPlatformSweep(t, "")
	seedPoolConfig(t, sw.server, nil, map[string]config.PoolConfig{"pool-a": {}})
	ctx := context.Background()
	var ids []string
	for i := 200; i >= 0; i-- {
		tenant := []string{sw.tenantA.tenantID, sw.tenantB.tenantID}[i%2]
		agent, err := sw.server.agentIdentityStore.Create(ctx, store.AgentIdentity{
			ID: fmt.Sprintf("pool-agent-%03d", i), TenantID: tenant, Name: "Pool agent", Owner: "ops",
			RiskTier: "low", Status: []string{"active", "suspended", "revoked"}[i%3], AllowedPools: []string{"pool-a"},
		})
		if err != nil {
			t.Fatalf("create agent identity: %v", err)
		}
		if i == 0 {
			agent.TenantID = ""
			data, err := json.Marshal(agent)
			if err != nil {
				t.Fatal(err)
			}
			if err := sw.server.redisClient().Set(ctx, "agent:identity:"+agent.ID, data, 0).Err(); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, agent.ID)
	}
	slices.Sort(ids)
	before, mark := poolPlatformPools(t, sw), sw.publishMark()
	rr := sw.as(t, poolPlatformAdmin, http.MethodDelete, "/api/v1/pools/pool-a", "")
	poolPlatformUnchanged(t, sw, before, mark)
	poolDeleteAgentAssertInUse(t, rr, ids...)
}

func TestPoolDeletePlatformWithoutTenant(t *testing.T) {
	sw, _ := poolPlatformSweep(t, "")
	seedPoolConfig(t, sw.server, nil, map[string]config.PoolConfig{"pool-a": {}})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/pools/pool-a", nil)
	req = withAuth(withPathValues(req, "name", "pool-a"), &auth.AuthContext{Role: "admin", AllowCrossTenant: true})
	rr := httptest.NewRecorder()
	sw.server.handleDeletePool(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	poolDeleteAgentAssertAbsent(t, sw, "pool-a")
}

func poolDeleteAgentAssertInUse(t *testing.T, rr *httptest.ResponseRecorder, ids ...string) {
	t.Helper()
	var body struct {
		Code     string   `json:"code"`
		AgentIDs []string `json:"agent_ids"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode delete response: %v; status=%d", err, rr.Code)
	}
	if rr.Code != http.StatusConflict || body.Code != "POOL_IN_USE" || !slices.Equal(body.AgentIDs, ids) {
		t.Fatalf("delete: status=%d, code=%q, agent_ids=%v; want 409 POOL_IN_USE %v", rr.Code, body.Code, body.AgentIDs, ids)
	}
}

func poolDeleteAgentAssertAbsent(t *testing.T, sw *crossTenantSweep, name string) {
	t.Helper()
	doc, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("read pools config: %v", err)
	}
	_, pools, err := extractPoolsFromConfig(doc)
	if err != nil {
		t.Fatalf("decode pools config: %v", err)
	}
	if _, exists := pools[name]; exists {
		t.Fatalf("pool %q remains in system/default", name)
	}
}
