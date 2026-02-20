package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return srv, rdb
}

func TestRegisterWritesKeyWithTTL(t *testing.T) {
	srv, rdb := newTestRedis(t)

	reg := NewInstanceRegistry(rdb, "api-gateway", "gw-1", "v0.2.0", "abc123")
	ctx := context.Background()
	reg.Start(ctx)
	defer reg.Stop()

	key := "cordum:instance:api-gateway:gw-1"
	if !srv.Exists(key) {
		t.Fatal("expected instance key to exist after Start")
	}

	ttl := srv.TTL(key)
	if ttl <= 0 || ttl > defaultInstanceTTL {
		t.Fatalf("expected TTL ~15s, got %s", ttl)
	}

	data, err := srv.Get(key)
	if err != nil {
		t.Fatalf("get key: %v", err)
	}
	var info InstanceInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.ID != "gw-1" || info.Service != "api-gateway" || info.Version != "v0.2.0" || info.Commit != "abc123" {
		t.Fatalf("unexpected instance info: %+v", info)
	}
	if info.StartedAt == "" {
		t.Fatal("expected started_at to be set")
	}
}

func TestStopDeletesKey(t *testing.T) {
	srv, rdb := newTestRedis(t)

	reg := NewInstanceRegistry(rdb, "scheduler", "sched-1", "v0.2.0", "def456")
	ctx := context.Background()
	reg.Start(ctx)

	key := "cordum:instance:scheduler:sched-1"
	if !srv.Exists(key) {
		t.Fatal("expected key to exist before Stop")
	}

	reg.Stop()

	if srv.Exists(key) {
		t.Fatal("expected key to be deleted after Stop")
	}
}

func TestHeartbeatRenewsTTL(t *testing.T) {
	srv, rdb := newTestRedis(t)

	reg := NewInstanceRegistry(rdb, "api-gateway", "gw-renew", "v0.2.0", "abc")
	// Use short TTL to speed up test.
	reg.ttl = 200 * time.Millisecond

	ctx := context.Background()
	reg.Start(ctx)
	defer reg.Stop()

	key := "cordum:instance:api-gateway:gw-renew"

	// Fast-forward miniredis past half the TTL to trigger a heartbeat.
	srv.FastForward(150 * time.Millisecond)
	time.Sleep(200 * time.Millisecond) // Let the heartbeat goroutine run.

	// Key should still exist (heartbeat renewed it).
	if !srv.Exists(key) {
		t.Fatal("expected key to exist after heartbeat renewal")
	}

	ttl := srv.TTL(key)
	if ttl <= 0 {
		t.Fatalf("expected positive TTL after renewal, got %s", ttl)
	}
}

func TestListInstances(t *testing.T) {
	_, rdb := newTestRedis(t)
	ctx := context.Background()

	reg1 := NewInstanceRegistry(rdb, "api-gateway", "gw-1", "v0.2.0", "aaa")
	reg2 := NewInstanceRegistry(rdb, "api-gateway", "gw-2", "v0.2.0", "bbb")
	reg3 := NewInstanceRegistry(rdb, "scheduler", "sched-1", "v0.2.0", "ccc")
	reg1.Start(ctx)
	reg2.Start(ctx)
	reg3.Start(ctx)
	defer reg1.Stop()
	defer reg2.Stop()
	defer reg3.Stop()

	instances, err := ListInstances(ctx, rdb, "api-gateway")
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 api-gateway instances, got %d", len(instances))
	}
	ids := map[string]bool{}
	for _, inst := range instances {
		ids[inst.ID] = true
		if inst.Service != "api-gateway" {
			t.Fatalf("expected service api-gateway, got %s", inst.Service)
		}
	}
	if !ids["gw-1"] || !ids["gw-2"] {
		t.Fatalf("expected gw-1 and gw-2, got %v", ids)
	}
}

func TestListAllInstances(t *testing.T) {
	_, rdb := newTestRedis(t)
	ctx := context.Background()

	reg1 := NewInstanceRegistry(rdb, "api-gateway", "gw-1", "v0.2.0", "aaa")
	reg2 := NewInstanceRegistry(rdb, "scheduler", "sched-1", "v0.2.0", "bbb")
	reg3 := NewInstanceRegistry(rdb, "workflow-engine", "wf-1", "v0.2.0", "ccc")
	reg1.Start(ctx)
	reg2.Start(ctx)
	reg3.Start(ctx)
	defer reg1.Stop()
	defer reg2.Stop()
	defer reg3.Stop()

	grouped, err := ListAllInstances(ctx, rdb)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(grouped) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(grouped), grouped)
	}
	if len(grouped["api-gateway"]) != 1 {
		t.Fatalf("expected 1 api-gateway, got %d", len(grouped["api-gateway"]))
	}
	if len(grouped["scheduler"]) != 1 {
		t.Fatalf("expected 1 scheduler, got %d", len(grouped["scheduler"]))
	}
	if len(grouped["workflow-engine"]) != 1 {
		t.Fatalf("expected 1 workflow-engine, got %d", len(grouped["workflow-engine"]))
	}
}

func TestNilRedisGracefulDegradation(t *testing.T) {
	reg := NewInstanceRegistry(nil, "api-gateway", "gw-nil", "v0.2.0", "aaa")
	ctx := context.Background()

	// Should not panic.
	reg.Start(ctx)
	reg.Stop()

	// List with nil Redis should error, not panic.
	_, err := ListInstances(ctx, nil, "api-gateway")
	if err == nil {
		t.Fatal("expected error from ListInstances with nil redis")
	}
	_, err = ListAllInstances(ctx, nil)
	if err == nil {
		t.Fatal("expected error from ListAllInstances with nil redis")
	}
}
