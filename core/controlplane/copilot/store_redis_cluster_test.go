package copilot

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

var errClusterCopilotInjected = errors.New("injected session write failure")

// clusterCopilotGuard stands in for Redis Cluster's CROSSSLOT refusal, which
// the one-node mini cluster does not enforce on a Watch callback's MULTI: it
// fails a MULTI that names more than one key. Given failSet, it also fails a
// MULTI that SETs that key.
type clusterCopilotGuard struct{ failSet string }

func (clusterCopilotGuard) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (clusterCopilotGuard) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (g clusterCopilotGuard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) == 0 || cmds[0].Name() != "multi" {
			return next(ctx, cmds)
		}
		keys := map[string]bool{}
		var err error
		for _, cmd := range cmds {
			name := cmd.Name()
			if name == "multi" || name == "exec" || len(cmd.Args()) < 2 {
				continue
			}
			key := fmt.Sprint(cmd.Args()[1])
			keys[key] = true
			if name == "set" && g.failSet != "" && key == g.failSet {
				err = errClusterCopilotInjected
			}
		}
		if len(keys) > 1 {
			err = fmt.Errorf("MULTI names %d keys; a cluster refuses it with CROSSSLOT", len(keys))
		}
		if err != nil {
			for _, cmd := range cmds {
				cmd.SetErr(err)
			}
			return err
		}
		return next(ctx, cmds)
	}
}

func clusterCopilotStore(t *testing.T, failSet string) (*RedisStore, *redis.ClusterClient) {
	t.Helper()
	client := testredis.NewMiniClusterClient(t, miniredis.RunT(t).Addr())
	// A Watch Tx clones only its node's hooks, so guard every node before the
	// first command creates one.
	client.OnNewNode(func(node *redis.Client) { node.AddHook(clusterCopilotGuard{failSet: failSet}) })
	return NewRedisStore(client), client
}

func TestRedisStoreAppendOnClusterClient(t *testing.T) {
	s, client := clusterCopilotStore(t, "")
	ctx := context.Background()
	t0 := time.Now().UTC()
	m1 := CopilotMessage{ID: "m1", Role: "user", Content: "first", Timestamp: t0}
	m2 := CopilotMessage{ID: "m2", Role: "tool", Content: "second", Timestamp: t0.Add(time.Second)}
	for _, m := range []CopilotMessage{m1, m2, m1} {
		if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-1", m); err != nil {
			t.Fatalf("AppendMessage(%s): %v", m.ID, err)
		}
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-1", "alice")
	if err != nil || len(got.Messages) != 2 || got.Messages[0].ID != "m1" || got.Messages[1].ID != "m2" {
		t.Fatalf("GetSession = %+v, %v; want messages m1, m2", got, err)
	}
	idx := sessionIndexKey("tenant-a", "alice")
	score, err := client.ZScore(ctx, idx, "sess-1").Result()
	if want := float64(m2.Timestamp.UnixNano()); err != nil || score != want {
		t.Fatalf("index score = %v, %v; want %v: m2 sets it and the m1 replay keeps it", score, err, want)
	}
	if ttl, err := client.TTL(ctx, idx).Result(); err != nil || ttl <= 0 {
		t.Fatalf("index TTL = %v, %v; want > 0", ttl, err)
	}
}

func TestRedisStoreAppendSessionBeforeIndex(t *testing.T) {
	s, client := clusterCopilotStore(t, sessionKey("tenant-a", "sess-1"))
	ctx := context.Background()
	err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-1", CopilotMessage{ID: "m1", Role: "user", Content: "first"})
	if !errors.Is(err, errClusterCopilotInjected) {
		t.Fatalf("AppendMessage err = %v; want the injected session write failure", err)
	}
	if n, err := client.Exists(ctx, sessionIndexKey("tenant-a", "alice")).Result(); err != nil || n != 0 {
		t.Fatalf("index exists = %d, %v; want 0: the index is written only after its session", n, err)
	}
}
