package main

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	gnats "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
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

func TestBootstrapConfigUpdatesOnHashChange(t *testing.T) {
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
	poolsV1 := &config.PoolsConfig{Topics: map[string][]string{"job.test": {"pool-a"}}}

	// Initial bootstrap
	if err := bootstrapConfig(ctx, svc, poolsV1, nil); err != nil {
		t.Fatalf("bootstrap v1: %v", err)
	}
	doc, _ := svc.Get(ctx, configsvc.ScopeSystem, "default")
	rev1 := doc.Revision

	// Same config → should NOT update (revision unchanged)
	if err := bootstrapConfig(ctx, svc, poolsV1, nil); err != nil {
		t.Fatalf("bootstrap same: %v", err)
	}
	doc, _ = svc.Get(ctx, configsvc.ScopeSystem, "default")
	if doc.Revision != rev1 {
		t.Fatalf("expected no update for same config, revision changed from %d to %d", rev1, doc.Revision)
	}

	// Different config → SHOULD update
	poolsV2 := &config.PoolsConfig{Topics: map[string][]string{"job.test": {"pool-a"}, "job.bank": {"pool-b"}}}
	if err := bootstrapConfig(ctx, svc, poolsV2, nil); err != nil {
		t.Fatalf("bootstrap v2: %v", err)
	}
	doc, _ = svc.Get(ctx, configsvc.ScopeSystem, "default")
	if doc.Revision == rev1 {
		t.Fatalf("expected config update for changed pools, revision still %d", rev1)
	}
}

func TestBootstrapConfigStoresFileHash(t *testing.T) {
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
	timeouts := &config.TimeoutsConfig{Reconciler: config.ReconcilerTimeout{DispatchTimeoutSeconds: 10}}

	if err := bootstrapConfig(ctx, svc, pools, timeouts); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := doc.Data["_poolsFileHash"]; !ok {
		t.Fatal("expected _poolsFileHash in stored document")
	}
	if _, ok := doc.Data["_timeoutsFileHash"]; !ok {
		t.Fatal("expected _timeoutsFileHash in stored document")
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

	go watchConfigChanges(ctx, svc, nil, nil, strategy, reconciler, nil)
	time.Sleep(20 * time.Millisecond)

	routing := strategy.CurrentRouting()
	if len(routing.Topics) == 0 || routing.Topics["job.test"][0] != "pool-a" {
		t.Fatalf("expected routing update, got %#v", routing.Topics)
	}
}

func startEmbeddedNATS(t *testing.T) *gnats.Server {
	t.Helper()
	opts := &gnats.Options{
		Host:           "127.0.0.1",
		Port:           -1,
		NoLog:          true,
		NoSigs:         true,
		JetStream:      true,
		StoreDir:       t.TempDir(),
		MaxPayload:     4 * 1024 * 1024,
		MaxControlLine: 4096,
	}
	ns, err := gnats.NewServer(opts)
	if err != nil {
		t.Fatalf("embedded NATS: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

func TestWatchConfigChangesNotificationTriggersReload(t *testing.T) {
	ns := startEmbeddedNATS(t)

	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer redisSrv.Close()

	svc, err := configsvc.New("redis://" + redisSrv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	// Seed config with pools data.
	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.notify-test": []any{"pool-notify"}},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config: %v", err)
	}

	strategy := scheduler.NewLeastLoadedStrategy(scheduler.PoolRouting{})
	reconciler := scheduler.NewReconciler(nil, time.Second, time.Second, time.Second)

	// Use a very long poll interval so poll can't trigger the reload — only notifications will.
	t.Setenv("SCHEDULER_CONFIG_RELOAD_INTERVAL", "1h")

	natsBus, err := bus.NewNatsBus(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer natsBus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watchConfigChanges(ctx, svc, nil, nil, strategy, reconciler, natsBus)

	// Give the subscriber time to establish.
	time.Sleep(100 * time.Millisecond)

	// Publish a config-changed notification (simulating what the gateway does).
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("direct NATS connect: %v", err)
	}
	defer nc.Close()

	packet := &pb.BusPacket{
		TraceId:  "test-notify",
		SenderId: "test-gateway",
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Message: "config changed",
			},
		},
	}
	data, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	if err := nc.Publish(capsdk.SubjectConfigChanged, data); err != nil {
		t.Fatalf("publish notification: %v", err)
	}
	nc.Flush()

	// Wait for the notification to trigger reload.
	deadline := time.After(3 * time.Second)
	for {
		routing := strategy.CurrentRouting()
		if len(routing.Topics) > 0 && routing.Topics["job.notify-test"] != nil {
			// Notification triggered config reload successfully.
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for notification-triggered config reload, topics=%v", strategy.CurrentRouting().Topics)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestWatchConfigChangesFallbackPoll(t *testing.T) {
	redisSrv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer redisSrv.Close()

	svc, err := configsvc.New("redis://" + redisSrv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.fallback": []any{"pool-fb"}},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config: %v", err)
	}

	strategy := scheduler.NewLeastLoadedStrategy(scheduler.PoolRouting{})
	reconciler := scheduler.NewReconciler(nil, time.Second, time.Second, time.Second)

	// Very short poll interval, nil bus (no NATS).
	t.Setenv("SCHEDULER_CONFIG_RELOAD_INTERVAL", "10ms")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watchConfigChanges(ctx, svc, nil, nil, strategy, reconciler, nil)
	time.Sleep(50 * time.Millisecond)

	routing := strategy.CurrentRouting()
	if len(routing.Topics) == 0 || routing.Topics["job.fallback"] == nil {
		t.Fatalf("expected poll-based config reload, got %#v", routing.Topics)
	}
}
