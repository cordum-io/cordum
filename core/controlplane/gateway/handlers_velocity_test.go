package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

func velocityRulePayload(id string) map[string]any {
	return map[string]any{
		"id":   id,
		"name": "Login burst guard",
		"match": map[string]any{
			"topics":    []string{"job.auth.login"},
			"tenants":   []string{"default"},
			"risk_tags": []string{"auth"},
		},
		"window":    "1m",
		"key":       "tenant",
		"threshold": 3,
		"decision":  "require_approval",
		"reason":    "Repeated login attempts require review",
	}
}

func performVelocityJSONRequest(t *testing.T, req *http.Request, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req = adminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestVelocityRuleHandlersCRUDAndStorage(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.VelocityRules = true
	})

	createBody, _ := json.Marshal(velocityRulePayload("login-burst"))
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(createBody))
	createRec := performVelocityJSONRequest(t, createReq, s.handleCreateVelocityRule)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create velocity rule: %d %s", createRec.Code, createRec.Body.String())
	}

	var created velocityRuleResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}
	if created.ID != "login-burst" {
		t.Fatalf("created id = %q, want login-burst", created.ID)
	}
	if created.Window != "1m0s" {
		t.Fatalf("created window = %q, want 1m0s", created.Window)
	}
	if !created.Enabled {
		t.Fatalf("expected created rule to default enabled")
	}

	listReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/velocity-rules", nil))
	listRec := httptest.NewRecorder()
	s.handleVelocityRules(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list velocity rules: %d %s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Items []velocityRuleResponse `json:"items"`
		Count int                    `json:"count"`
		Limit int64                  `json:"limit"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Items) != 1 {
		t.Fatalf("expected 1 listed rule, got count=%d items=%d", listResp.Count, len(listResp.Items))
	}
	if listResp.Limit != defaultTeamVelocityRuleLimit {
		t.Fatalf("list limit = %d, want %d", listResp.Limit, defaultTeamVelocityRuleLimit)
	}

	bundles, _, err := s.loadPolicyBundles(context.Background())
	if err != nil {
		t.Fatalf("load policy bundles: %v", err)
	}
	rawBundle, ok := bundles["velocity/login-burst"]
	if !ok {
		t.Fatalf("expected velocity/login-burst fragment to be stored")
	}
	content, ok := policybundles.PolicyBundleContent(rawBundle)
	if !ok {
		t.Fatalf("expected stored fragment content")
	}
	policy, err := config.ParseSafetyPolicy([]byte(content))
	if err != nil {
		t.Fatalf("parse stored policy bundle: %v", err)
	}
	if policy == nil || len(policy.Rules) != 1 {
		t.Fatalf("expected exactly one stored rule, got %#v", policy)
	}
	if policy.Rules[0].Velocity == nil || policy.Rules[0].Velocity.MaxRequests != 3 || policy.Rules[0].Velocity.WindowSeconds != 60 {
		t.Fatalf("unexpected stored velocity config: %#v", policy.Rules[0].Velocity)
	}

	updatePayload := velocityRulePayload("login-burst")
	updatePayload["name"] = "Session burst guard"
	updatePayload["window"] = "2m"
	updatePayload["threshold"] = 5
	updatePayload["key"] = "labels.session_id"
	updatePayload["enabled"] = false
	updateBody, _ := json.Marshal(updatePayload)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/velocity-rules/login-burst", bytes.NewReader(updateBody))
	updateReq.SetPathValue("id", "login-burst")
	updateRec := performVelocityJSONRequest(t, updateReq, s.handlePutVelocityRule)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update velocity rule: %d %s", updateRec.Code, updateRec.Body.String())
	}
	var updated velocityRuleResponse
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated rule: %v", err)
	}
	if updated.Name != "Session burst guard" || updated.Key != "labels.session_id" || updated.Threshold != 5 {
		t.Fatalf("unexpected updated rule: %#v", updated)
	}
	if updated.Enabled {
		t.Fatalf("expected updated rule to be disabled")
	}

	deleteReq := adminCtx(httptest.NewRequest(http.MethodDelete, "/api/v1/policy/velocity-rules/login-burst", nil))
	deleteReq.SetPathValue("id", "login-burst")
	deleteRec := httptest.NewRecorder()
	s.handleDeleteVelocityRule(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete velocity rule: %d %s", deleteRec.Code, deleteRec.Body.String())
	}

	bundles, _, err = s.loadPolicyBundles(context.Background())
	if err != nil {
		t.Fatalf("reload policy bundles: %v", err)
	}
	if _, ok := bundles["velocity/login-burst"]; ok {
		t.Fatalf("expected velocity fragment to be deleted")
	}
	if len(bus.published) < 3 {
		t.Fatalf("expected config change notifications for create/update/delete, got %d", len(bus.published))
	}
}

func TestVelocityRuleCreateEnforcesTierLimits(t *testing.T) {
	t.Run("community blocked", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		setTestEntitlements(t, s, licensing.PlanCommunity, nil)

		body, _ := json.Marshal(velocityRulePayload("community-rule"))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(body))
		rec := performVelocityJSONRequest(t, req, s.handleCreateVelocityRule)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"tier_limit_exceeded"`) || !strings.Contains(rec.Body.String(), `"limit":"velocity_rules"`) {
			t.Fatalf("expected velocity_rules tier limit error, got %s", rec.Body.String())
		}
	})

	t.Run("team add-on limit enforced", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
			entitlements.VelocityRules = true
			entitlements.Limits = map[string]int64{"velocity_rule_count": 1}
		})

		firstBody, _ := json.Marshal(velocityRulePayload("team-rule-1"))
		firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(firstBody))
		firstRec := performVelocityJSONRequest(t, firstReq, s.handleCreateVelocityRule)
		if firstRec.Code != http.StatusCreated {
			t.Fatalf("first create: %d %s", firstRec.Code, firstRec.Body.String())
		}

		secondPayload := velocityRulePayload("team-rule-2")
		secondPayload["name"] = "Second velocity rule"
		secondBody, _ := json.Marshal(secondPayload)
		secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(secondBody))
		secondRec := performVelocityJSONRequest(t, secondReq, s.handleCreateVelocityRule)
		if secondRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", secondRec.Code, secondRec.Body.String())
		}
		if !strings.Contains(secondRec.Body.String(), `"limit":"velocity_rules"`) {
			t.Fatalf("expected velocity_rules limit error, got %s", secondRec.Body.String())
		}
	})
}

func TestVelocityRuleStatsReturnsActivity(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.VelocityRules = true
	})

	for _, id := range []string{"login-burst", "tenant-storm"} {
		payload := velocityRulePayload(id)
		if id == "tenant-storm" {
			payload["threshold"] = 10
			payload["window"] = "5m"
			payload["name"] = "Tenant storm"
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(body))
		rec := performVelocityJSONRequest(t, req, s.handleCreateVelocityRule)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed velocity rule %s: %d %s", id, rec.Code, rec.Body.String())
		}
	}

	client := s.jobStore.Client()
	if client == nil {
		t.Fatal("expected test job store redis client")
	}
	ctx := context.Background()
	now := time.Now().Unix()
	if err := client.ZAdd(ctx, "cordum:velocity:login-burst:tenant-a",
		redis.Z{Score: float64(now - 10), Member: "req-1"},
		redis.Z{Score: float64(now - 20), Member: "req-2"},
		redis.Z{Score: float64(now - 30), Member: "req-3"},
		redis.Z{Score: float64(now - 40), Member: "req-7"},
	).Err(); err != nil {
		t.Fatalf("seed login-burst current bucket: %v", err)
	}
	if err := client.ZAdd(ctx, "cordum:velocity:login-burst:tenant-b",
		redis.Z{Score: float64(now - 90), Member: "req-4"},
	).Err(); err != nil {
		t.Fatalf("seed login-burst stale bucket: %v", err)
	}
	if err := client.ZAdd(ctx, "cordum:velocity:tenant-storm:default",
		redis.Z{Score: float64(now - 15), Member: "req-5"},
		redis.Z{Score: float64(now - 25), Member: "req-6"},
	).Err(); err != nil {
		t.Fatalf("seed tenant-storm bucket: %v", err)
	}

	statsReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/velocity-rules/stats", nil))
	statsRec := httptest.NewRecorder()
	s.handleVelocityRuleStats(statsRec, statsReq)
	if statsRec.Code != http.StatusOK {
		t.Fatalf("velocity rule stats: %d %s", statsRec.Code, statsRec.Body.String())
	}

	var statsResp struct {
		Items    []velocityRuleStatsResponse `json:"items"`
		TopRules []velocityRuleStatsResponse `json:"top_rules"`
	}
	if err := json.NewDecoder(statsRec.Body).Decode(&statsResp); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	if len(statsResp.Items) != 2 {
		t.Fatalf("expected stats for 2 rules, got %d", len(statsResp.Items))
	}

	statsByID := make(map[string]velocityRuleStatsResponse, len(statsResp.Items))
	for _, item := range statsResp.Items {
		statsByID[item.ID] = item
	}
	loginBurst := statsByID["login-burst"]
	if loginBurst.HitCount24h != 5 {
		t.Fatalf("login-burst hit_count_24h = %d, want 5", loginBurst.HitCount24h)
	}
	if loginBurst.CurrentWindowCount != 4 || loginBurst.CurrentWindowMax != 4 {
		t.Fatalf("unexpected login-burst current window stats: %#v", loginBurst)
	}
	if loginBurst.ActiveBuckets != 1 || loginBurst.ExceededBuckets != 1 {
		t.Fatalf("unexpected login-burst bucket stats: %#v", loginBurst)
	}
	if loginBurst.LastTriggered == "" {
		t.Fatalf("expected login-burst last_triggered to be populated")
	}

	tenantStorm := statsByID["tenant-storm"]
	if tenantStorm.HitCount24h != 2 || tenantStorm.CurrentWindowCount != 2 || tenantStorm.ExceededBuckets != 0 {
		t.Fatalf("unexpected tenant-storm stats: %#v", tenantStorm)
	}
	if len(statsResp.TopRules) == 0 || statsResp.TopRules[0].ID != "login-burst" {
		t.Fatalf("expected login-burst to be first top rule, got %#v", statsResp.TopRules)
	}
}

func TestVelocityRuleHandlersRejectInvalidInput(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.VelocityRules = true
	})

	payload := velocityRulePayload("bad-window")
	payload["window"] = "1500ms"
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(body))
	rec := performVelocityJSONRequest(t, req, s.handleCreateVelocityRule)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for fractional-second window, got %d: %s", rec.Code, rec.Body.String())
	}

	payload = velocityRulePayload("bad-key")
	payload["key"] = "email"
	body, _ = json.Marshal(payload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/policy/velocity-rules", bytes.NewReader(body))
	rec = performVelocityJSONRequest(t, req, s.handleCreateVelocityRule)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid key, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported key segment") {
		t.Fatalf("expected invalid key error, got %s", rec.Body.String())
	}
}
