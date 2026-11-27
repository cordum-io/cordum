package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

var errClusterTelemetryInjected = errors.New("injected snapshot write failure")

// clusterTelemetryGuard stands in for Redis Cluster's CROSSSLOT refusal of a
// DEL that names several keys, which go-redis does not split by slot and
// miniredis accepts. Given failSet, it also fails a SET of that key, alone or
// in a pipeline.
type clusterTelemetryGuard struct{ failSet string }

func (clusterTelemetryGuard) DialHook(next redis.DialHook) redis.DialHook { return next }

func (g clusterTelemetryGuard) check(cmd redis.Cmder) error {
	args := cmd.Args()
	switch {
	case cmd.Name() == "del" && len(args) > 2:
		return fmt.Errorf("DEL names %d keys; a cluster refuses it with CROSSSLOT", len(args)-1)
	case cmd.Name() == "set" && g.failSet != "" && len(args) > 1 && fmt.Sprint(args[1]) == g.failSet:
		return errClusterTelemetryInjected
	}
	return nil
}

func (g clusterTelemetryGuard) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := g.check(cmd); err != nil {
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (g clusterTelemetryGuard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := g.check(cmd); err != nil {
				for _, queued := range cmds {
					queued.SetErr(err)
				}
				return err
			}
		}
		return next(ctx, cmds)
	}
}

func clusterTelemetryStore(t *testing.T, failSet string) (*Store, *redis.ClusterClient) {
	t.Helper()
	client := testredis.NewMiniClusterClient(t, miniredis.RunT(t).Addr())
	client.OnNewNode(func(node *redis.Client) { node.AddHook(clusterTelemetryGuard{failSet: failSet}) })
	return NewStoreWithClient(client), client
}

func clusterTelemetrySave(t *testing.T, s *Store, event string, at time.Time) {
	t.Helper()
	if err := s.SaveSnapshot(context.Background(), TelemetryPayload{Event: event, CollectedAt: at}); err != nil {
		t.Fatalf("SaveSnapshot(%s): %v", event, err)
	}
}

func clusterTelemetryAbsent(t *testing.T, client *redis.ClusterClient, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if n, err := client.Exists(context.Background(), key).Result(); err != nil || n != 0 {
			t.Fatalf("%s exists = %d, %v; want 0", key, n, err)
		}
	}
}

func TestStoreSaveSnapshotOnClusterClient(t *testing.T) {
	s, client := clusterTelemetryStore(t, "")
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	s.maxHistory = 0
	var keys []string
	for i := 0; i < 4; i++ {
		if i == 3 {
			s.maxHistory = 1 // the fourth save trims the three older snapshots
		}
		at := base.Add(time.Duration(i) * time.Second)
		keys = append(keys, snapshotKey(at))
		clusterTelemetrySave(t, s, fmt.Sprintf("save-%d", i), at)
	}
	if ttl, err := client.TTL(ctx, keys[3]).Result(); err != nil || ttl <= 0 {
		t.Fatalf("snapshot TTL = %v, %v; want > 0", ttl, err)
	}
	if members, err := client.ZRange(ctx, historyKey, 0, -1).Result(); err != nil || len(members) != 1 || members[0] != keys[3] {
		t.Fatalf("history = %v, %v; want only %s", members, err, keys[3])
	}
	if got, err := s.InspectPayload(ctx); err != nil || got == nil || got.Event != "save-3" {
		t.Fatalf("InspectPayload = %+v, %v; want save-3", got, err)
	}
	clusterTelemetryAbsent(t, client, keys[:3]...)
}

func TestStoreSnapshotBeforeHistory(t *testing.T) {
	at := time.Now().UTC()
	s, client := clusterTelemetryStore(t, snapshotKey(at))
	err := s.SaveSnapshot(context.Background(), TelemetryPayload{Event: "save", CollectedAt: at})
	if !errors.Is(err, errClusterTelemetryInjected) {
		t.Fatalf("SaveSnapshot err = %v; want the injected snapshot write failure", err)
	}
	// History and snapshot:last are written only after the snapshot.
	clusterTelemetryAbsent(t, client, historyKey, snapshotLatestKey)
}

func TestStoreHistorySkipsMemberWithoutSnapshot(t *testing.T) {
	s, client := clusterTelemetryStore(t, "")
	ctx := context.Background()
	older := time.Now().UTC().Add(-time.Minute)
	clusterTelemetrySave(t, s, "older", older)
	clusterTelemetrySave(t, s, "newer", older.Add(time.Second))
	if err := client.Del(ctx, snapshotKey(older)).Err(); err != nil {
		t.Fatalf("delete older snapshot: %v", err)
	}
	got, err := s.History(ctx, 10)
	if err != nil || len(got) != 1 || got[0].Event != "newer" {
		t.Fatalf("History = %+v, %v; want only the newer snapshot", got, err)
	}
}
