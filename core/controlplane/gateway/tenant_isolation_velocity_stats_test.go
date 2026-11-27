package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/timeutil"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

// TestTenantIsolationVelocityRuleStats: one rule has a bucket per tenant plus
// one bucket without a tenant. A caller without cross-tenant access is counted
// only its own tenant's bucket (none for an empty tenant); a cross-tenant
// caller, or any caller when auth is off, is counted every bucket.
func TestTenantIsolationVelocityRuleStats(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.VelocityRules = true
	})
	body, _ := json.Marshal(velocityRulePayload("iso-velocity"))
	create := performVelocityJSONRequest(t, httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(body)), s.handleCreateVelocityRule)
	if create.Code != http.StatusCreated {
		t.Fatalf("seed velocity rule: %d %s", create.Code, create.Body.String())
	}

	now := time.Now().Unix()
	seeds := []struct {
		tenant string
		ages   []int64
	}{{isoTenantA, []int64{30}}, {isoTenantB, []int64{10, 12}}, {"", []int64{20, 21, 22, 23}}}
	for _, seed := range seeds {
		key := "cordum:velocity:iso-velocity:v1"
		if seed.tenant != "" {
			key = "cordum:velocity:iso-velocity:" + base64.RawURLEncoding.EncodeToString([]byte(seed.tenant)) + ":v1"
		}
		for _, age := range seed.ages {
			if err := s.jobStore.Client().ZAdd(context.Background(), key, redis.Z{Score: float64(now - age), Member: fmt.Sprint(age)}).Err(); err != nil {
				t.Fatalf("seed %q: %v", key, err)
			}
		}
	}

	adminB := &auth.AuthContext{Tenant: isoTenantB, Role: "admin", PrincipalID: "admin-iso-b"}
	crossAdmin := &auth.AuthContext{Tenant: isoTenantA, Role: "admin", PrincipalID: "root-iso", AllowCrossTenant: true}
	noTenant := &auth.AuthContext{Role: "admin", PrincipalID: "admin-iso-none"}
	// hits, window count, window max, active, exceeded; newest is the age in
	// seconds of the newest member counted, 0 when none is.
	cases := []struct {
		name                               string
		caller                             *auth.AuthContext
		hits, count, max, active, exceeded int64
		newest                             int64
	}{
		{"tenant A", isoAdminA, 1, 1, 1, 1, 0, 30},
		{"tenant B", adminB, 2, 2, 2, 1, 0, 10},
		{"cross-tenant", crossAdmin, 7, 7, 4, 3, 1, 10},
		{"no tenant", noTenant, 0, 0, 0, 0, 0, 0},
		{"auth disabled", nil, 7, 7, 4, 3, 1, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/velocity-rules/stats", nil)
			if tc.caller == nil {
				s.auth = nil
				req = adminCtx(req)
			} else {
				s.auth = testAuthProvider{}
				req = withAuth(req, tc.caller)
			}
			rec := httptest.NewRecorder()
			s.handleVelocityRuleStats(rec, req)
			var resp struct {
				Items []velocityRuleStatsResponse `json:"items"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || len(resp.Items) != 1 || resp.Items[0].ID != "iso-velocity" {
				t.Fatalf("stats = %d %s, want the one rule listed", rec.Code, rec.Body.String())
			}
			got := resp.Items[0]
			var sampled int64
			for _, n := range got.HourlyHits {
				sampled += n
			}
			wantLast := ""
			if tc.newest > 0 {
				wantLast = timeutil.FromSeconds(now - tc.newest)
			}
			if got.HitCount24h != tc.hits || got.CurrentWindowCount != tc.count || got.CurrentWindowMax != tc.max ||
				int64(got.ActiveBuckets) != tc.active || int64(got.ExceededBuckets) != tc.exceeded ||
				got.LastTriggered != wantLast || sampled != tc.hits {
				t.Fatalf("stats hits=%d count=%d max=%d active=%d exceeded=%d last=%q sampled=%d; want hits=%d count=%d max=%d active=%d exceeded=%d last=%q sampled=%d",
					got.HitCount24h, got.CurrentWindowCount, got.CurrentWindowMax, got.ActiveBuckets, got.ExceededBuckets, got.LastTriggered, sampled,
					tc.hits, tc.count, tc.max, tc.active, tc.exceeded, wantLast, tc.hits)
			}
		})
	}
}
