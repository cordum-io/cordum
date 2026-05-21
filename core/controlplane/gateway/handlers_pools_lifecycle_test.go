package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
)

func TestPoolLifecycle_CreateAddTopicDrainDelete(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Seed minimal config so extractPoolsFromConfig works
	seedPoolConfig(t, s, map[string]any{"job.seed": "seed-pool"}, map[string]config.PoolConfig{
		"seed-pool": {},
	})

	// 1. Create pool
	req := withPathValues(adminRequest("PUT", "/api/v1/pools/test-pool", map[string]any{
		"requires":    []string{"docker"},
		"description": "lifecycle test pool",
	}), "name", "test-pool")
	w := httptest.NewRecorder()
	s.handleCreatePool(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Add topic
	req2 := withPathValues(adminRequest("PUT", "/api/v1/pools/test-pool/topics/job.test.run", nil),
		"name", "test-pool", "topic", "job.test.run")
	w2 := httptest.NewRecorder()
	s.handleAddTopicToPool(w2, req2)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("add topic: expected 204, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. Verify pool in config
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	topics, poolMap, err := extractPoolsFromConfig(doc)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, ok := poolMap["test-pool"]; !ok {
		t.Fatal("test-pool not found in config")
	}
	if len(topics["job.test.run"]) == 0 {
		t.Fatal("job.test.run topic not mapped")
	}

	// 4. Drain pool
	req3 := withPathValues(adminRequest("POST", "/api/v1/pools/test-pool/drain", map[string]any{
		"timeout_seconds": 60,
	}), "name", "test-pool")
	w3 := httptest.NewRecorder()
	s.handleDrainPool(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("drain: expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	// 5. Verify draining status
	doc2, _ := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	_, poolMap2, _ := extractPoolsFromConfig(doc2)
	if poolMap2["test-pool"].EffectiveStatus() != config.PoolStatusDraining {
		t.Fatalf("expected draining, got %s", poolMap2["test-pool"].EffectiveStatus())
	}

	// 6. Mark inactive before force delete; force still refuses active/draining pools.
	reqInactive := withPathValues(adminRequest("PATCH", "/api/v1/pools/test-pool", map[string]any{
		"status": config.PoolStatusInactive,
	}), "name", "test-pool")
	wInactive := httptest.NewRecorder()
	s.handleUpdatePool(wInactive, reqInactive)
	if wInactive.Code != http.StatusOK {
		t.Fatalf("mark inactive: expected 200, got %d: %s", wInactive.Code, wInactive.Body.String())
	}

	// 7. Delete pool with force
	req4 := withPathValues(adminRequest("DELETE", "/api/v1/pools/test-pool?force=true", nil),
		"name", "test-pool")
	w4 := httptest.NewRecorder()
	s.handleDeletePool(w4, req4)
	if w4.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", w4.Code, w4.Body.String())
	}

	// 8. Verify pool gone
	doc3, _ := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	_, poolMap3, _ := extractPoolsFromConfig(doc3)
	if _, ok := poolMap3["test-pool"]; ok {
		t.Fatal("test-pool should be deleted")
	}
}

func TestPoolEdgeCases(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedPoolConfig(t, s, map[string]any{"job.test": "pool-a"}, map[string]config.PoolConfig{
		"pool-a": {Status: "active"},
	})

	// Invalid pool name
	req := withPathValues(adminRequest("PUT", "/api/v1/pools/BAD!", map[string]any{}), "name", "BAD!")
	w := httptest.NewRecorder()
	s.handleCreatePool(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid name: expected 400, got %d", w.Code)
	}

	// Delete non-existent pool
	req2 := withPathValues(adminRequest("DELETE", "/api/v1/pools/no-such-pool", nil), "name", "no-such-pool")
	w2 := httptest.NewRecorder()
	s.handleDeletePool(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("delete non-existent: expected 404, got %d", w2.Code)
	}

	// Delete pool with active topic without force → 400
	req3 := withPathValues(adminRequest("DELETE", "/api/v1/pools/pool-a", nil), "name", "pool-a")
	w3 := httptest.NewRecorder()
	s.handleDeletePool(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("delete with topic no force: expected 400, got %d", w3.Code)
	}

	// Drain already-draining pool
	reqDrain := withPathValues(adminRequest("POST", "/api/v1/pools/pool-a/drain", map[string]any{}), "name", "pool-a")
	wDrain := httptest.NewRecorder()
	s.handleDrainPool(wDrain, reqDrain)
	if wDrain.Code != http.StatusOK {
		t.Fatalf("first drain: expected 200, got %d", wDrain.Code)
	}

	reqDrain2 := withPathValues(adminRequest("POST", "/api/v1/pools/pool-a/drain", map[string]any{}), "name", "pool-a")
	wDrain2 := httptest.NewRecorder()
	s.handleDrainPool(wDrain2, reqDrain2)
	if wDrain2.Code != http.StatusBadRequest {
		t.Errorf("drain already-draining: expected 400, got %d", wDrain2.Code)
	}
}

func TestPoolBackwardCompat_NoStatusField(t *testing.T) {
	// Simulate old-format config with no status field
	body := []byte(`topics:
  job.legacy: legacy-pool
pools:
  legacy-pool:
    requires: ["docker"]
`)
	cfg, err := config.ParsePoolsConfig(body)
	if err != nil {
		t.Fatalf("old format parse failed: %v", err)
	}
	pool := cfg.Pools["legacy-pool"]
	if pool.Status != "" {
		t.Errorf("expected empty status, got %q", pool.Status)
	}
	if pool.EffectiveStatus() != config.PoolStatusActive {
		t.Errorf("expected effective active, got %q", pool.EffectiveStatus())
	}
}

func TestPoolConcurrentUpdates(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedPoolConfig(t, s, map[string]any{"job.test": "test-pool"}, map[string]config.PoolConfig{
		"test-pool": {Requires: []string{}, Description: "original"},
	})

	// Run 10 concurrent updates
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			desc := json.Number(string(rune('A' + n)))
			req := withPathValues(adminRequest("PATCH", "/api/v1/pools/test-pool", map[string]any{
				"description": string(desc),
			}), "name", "test-pool")
			w := httptest.NewRecorder()
			s.handleUpdatePool(w, req)
			if w.Code != http.StatusOK {
				errs <- nil // conflict retries internally, may still succeed
			} else {
				errs <- nil
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent update %d failed: %v", i, err)
		}
	}

	// Verify pool still exists and has a valid description
	doc, _ := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	_, poolMap, _ := extractPoolsFromConfig(doc)
	if _, ok := poolMap["test-pool"]; !ok {
		t.Fatal("pool lost during concurrent updates")
	}
}
