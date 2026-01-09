package main

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/config"
)

func TestReconcilerTimeoutsDefaults(t *testing.T) {
	dispatch, running, scan := reconcilerTimeouts(nil)
	if dispatch != 2*time.Minute {
		t.Fatalf("expected default dispatch timeout")
	}
	if running != 5*time.Minute {
		t.Fatalf("expected default running timeout")
	}
	if scan != 30*time.Second {
		t.Fatalf("expected default scan interval")
	}

	cfg := &config.TimeoutsConfig{
		Reconciler: config.ReconcilerTimeout{
			DispatchTimeoutSeconds: 10,
			RunningTimeoutSeconds:  20,
			ScanIntervalSeconds:    5,
		},
	}
	dispatch, running, scan = reconcilerTimeouts(cfg)
	if dispatch != 10*time.Second || running != 20*time.Second || scan != 5*time.Second {
		t.Fatalf("unexpected reconciler timeouts: %s %s %s", dispatch, running, scan)
	}
}

func TestBuildRoutingClonesPools(t *testing.T) {
	cfg := &config.PoolsConfig{
		Topics: map[string][]string{"job.test": {"pool-a"}},
		Pools:  map[string]config.PoolConfig{"pool-a": {Requires: []string{"gpu"}}},
	}
	routing := buildRouting(cfg)
	cfg.Topics["job.test"][0] = "pool-b"
	cfg.Pools["pool-a"].Requires[0] = "cpu"

	if routing.Topics["job.test"][0] != "pool-a" {
		t.Fatalf("expected routing topics to be cloned")
	}
	if routing.Pools["pool-a"].Requires[0] != "gpu" {
		t.Fatalf("expected routing pools to be cloned")
	}
}

func TestParsePoolsAndHash(t *testing.T) {
	raw := map[string]any{
		"topics": map[string]any{"job.test": []any{"pool-a"}},
		"pools": map[string]any{
			"pool-a": map[string]any{"requires": []any{"gpu"}},
		},
	}
	cfg, hash, err := parsePools(raw)
	if err != nil {
		t.Fatalf("parse pools: %v", err)
	}
	if cfg == nil || cfg.Topics["job.test"][0] != "pool-a" {
		t.Fatalf("unexpected parsed pools: %#v", cfg)
	}
	if hash == "" {
		t.Fatalf("expected pools hash")
	}

	raw2 := map[string]any{
		"pools":  raw["pools"],
		"topics": raw["topics"],
	}
	hash2, err := hashAny(raw2)
	if err != nil {
		t.Fatalf("hash any: %v", err)
	}
	if hash2 != hash {
		t.Fatalf("expected stable hash across key order")
	}
}

func TestParseTimeoutsAndHash(t *testing.T) {
	raw := map[string]any{
		"reconciler": map[string]any{
			"dispatch_timeout_seconds": 15,
			"running_timeout_seconds":  25,
			"scan_interval_seconds":    5,
		},
	}
	cfg, hash, err := parseTimeouts(raw)
	if err != nil {
		t.Fatalf("parse timeouts: %v", err)
	}
	if cfg == nil || cfg.Reconciler.DispatchTimeoutSeconds != 15 {
		t.Fatalf("unexpected timeouts config: %#v", cfg)
	}
	if hash == "" {
		t.Fatalf("expected timeouts hash")
	}
}

func TestToMapRoundtrip(t *testing.T) {
	cfg := &config.PoolsConfig{
		Topics: map[string][]string{"job.test": {"pool-a"}},
		Pools:  map[string]config.PoolConfig{"pool-a": {Requires: []string{"gpu"}}},
	}
	out, err := toMap(cfg)
	if err != nil {
		t.Fatalf("toMap: %v", err)
	}
	if out["Topics"] == nil && out["topics"] == nil {
		t.Fatalf("expected topics in map output")
	}
}

func TestBootstrapConfigWritesDefaults(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	ctx := context.Background()
	pools := &config.PoolsConfig{Topics: map[string][]string{"job.test": {"pool-a"}}}
	timeouts := &config.TimeoutsConfig{Reconciler: config.ReconcilerTimeout{DispatchTimeoutSeconds: 12}}

	if err := bootstrapConfig(ctx, svc, pools, timeouts); err != nil {
		t.Fatalf("bootstrap config: %v", err)
	}

	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get config doc: %v", err)
	}
	if doc.Data == nil || doc.Data["pools"] == nil || doc.Data["timeouts"] == nil {
		t.Fatalf("expected pools and timeouts in config doc")
	}
}

func TestLoadConfigSnapshot(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.test": []any{"pool-a"}},
			},
			"timeouts": map[string]any{
				"reconciler": map[string]any{"dispatch_timeout_seconds": 12},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config: %v", err)
	}

	snap, err := loadConfigSnapshot(context.Background(), svc, nil, nil)
	if err != nil {
		t.Fatalf("load config snapshot: %v", err)
	}
	if snap.Pools == nil || snap.PoolsHash == "" {
		t.Fatalf("expected pools snapshot")
	}
	if snap.Timeouts == nil || snap.TimeoutsHash == "" {
		t.Fatalf("expected timeouts snapshot")
	}
}

func TestWatchConfigChangesUpdatesRouting(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.test": []any{"pool-a"}},
			},
			"timeouts": map[string]any{
				"reconciler": map[string]any{"dispatch_timeout_seconds": 7},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config: %v", err)
	}

	strategy := scheduler.NewLeastLoadedStrategy(scheduler.PoolRouting{})
	reconciler := scheduler.NewReconciler(nil, time.Second, time.Second, time.Second)

	t.Setenv("SCHEDULER_CONFIG_RELOAD_INTERVAL", "5ms")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watchConfigChanges(ctx, svc, nil, nil, strategy, reconciler)
	time.Sleep(20 * time.Millisecond)

	routing := strategy.CurrentRouting()
	if len(routing.Topics) == 0 || routing.Topics["job.test"][0] != "pool-a" {
		t.Fatalf("expected routing update, got %#v", routing.Topics)
	}
}
