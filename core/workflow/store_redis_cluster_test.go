package workflow

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// forEachStoreBackend runs fn against a single-node client and a Redis
// Cluster client, which refuses a multi-slot MULTI client-side.
func forEachStoreBackend(t *testing.T, fn func(t *testing.T, store *RedisStore, client redis.UniversalClient)) {
	backends := []struct {
		name string
		open func(t testing.TB, addr string) redis.UniversalClient
	}{
		{"single", func(t testing.TB, addr string) redis.UniversalClient { return testredis.NewClient(t, addr) }},
		{"cluster", func(t testing.TB, addr string) redis.UniversalClient { return testredis.NewMiniClusterClient(t, addr) }},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			client := b.open(t, miniredis.RunT(t).Addr())
			fn(t, NewRedisWorkflowStoreFromClient(client), client)
		})
	}
}

// present reports whether member is in the set or sorted set at key, or, for
// an empty member, whether key exists.
func present(t *testing.T, client redis.UniversalClient, key, member string) bool {
	t.Helper()
	ctx := context.Background()
	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		t.Fatalf("type %s: %v", key, err)
	}
	switch typ {
	case "none":
		return false
	case "zset":
		_, err := client.ZScore(ctx, key, member).Result()
		if errors.Is(err, redis.Nil) {
			return false
		}
		if err != nil {
			t.Fatalf("zscore %s: %v", key, err)
		}
		return true
	case "set":
		ok, err := client.SIsMember(ctx, key, member).Result()
		if err != nil {
			t.Fatalf("sismember %s: %v", key, err)
		}
		return ok
	default:
		return member == ""
	}
}

// expectEntries checks each {key, member} pair with present.
func expectEntries(t *testing.T, client redis.UniversalClient, want bool, entries ...[2]string) {
	t.Helper()
	for _, e := range entries {
		if got := present(t, client, e[0], e[1]); got != want {
			t.Errorf("%s %q present = %v, want %v", e[0], e[1], got, want)
		}
	}
}

func TestWorkflowStoreWritesCluster(t *testing.T) {
	forEachStoreBackend(t, func(t *testing.T, s *RedisStore, c redis.UniversalClient) {
		ctx := context.Background()
		wfEntries := [][2]string{{"wf:def:wf-1", ""}, {"wf:index:all", "wf-1"}, {"wf:index:org:org-a", "wf-1"}}
		if err := s.SaveWorkflow(ctx, &Workflow{ID: "wf-1", OrgID: "org-a"}); err != nil {
			t.Fatalf("SaveWorkflow: %v", err)
		}
		expectEntries(t, c, true, wfEntries...)
		if err := s.DeleteWorkflow(ctx, "wf-1"); err != nil {
			t.Fatalf("DeleteWorkflow: %v", err)
		}
		expectEntries(t, c, false, wfEntries...)

		run := &WorkflowRun{ID: "run-1", WorkflowID: "wf-1", OrgID: "org-a", Status: RunStatusPending,
			Steps: map[string]*StepRun{"a": {StepID: "a", JobID: "run-1:a@1"}}}
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		listed := [][2]string{{"wf:run:run-1", ""}, {"wf:runs:wf-1", "run-1"}, {"wf:runs:all", "run-1"}}
		expectEntries(t, c, true, append(listed, [2]string{"wf:runs:status:pending", "run-1"},
			[2]string{"wf:run:job:run-1:a@1", ""}, [2]string{"wf:runs:active:org-a", "run-1"})...)

		run.Status = RunStatusSucceeded
		run.Steps = map[string]*StepRun{"b": {StepID: "b", JobID: "run-1:b@1"}}
		if err := s.UpdateRun(ctx, run); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}
		expectEntries(t, c, true, append(listed, [2]string{"wf:runs:status:succeeded", "run-1"},
			[2]string{"wf:run:job:run-1:b@1", ""})...)
		expectEntries(t, c, false, [2]string{"wf:runs:status:pending", "run-1"},
			[2]string{"wf:run:job:run-1:a@1", ""}, [2]string{"wf:runs:active:org-a", "run-1"})

		if err := s.AppendTimelineEvent(ctx, "run-1", &TimelineEvent{Type: "test"}); err != nil {
			t.Fatalf("AppendTimelineEvent: %v", err)
		}
		if err := s.DeleteRun(ctx, "run-1"); err != nil {
			t.Fatalf("DeleteRun: %v", err)
		}
		expectEntries(t, c, false, append(listed, [2]string{"wf:runs:status:succeeded", "run-1"},
			[2]string{"wf:run:job:run-1:b@1", ""}, [2]string{"wf:run:timeline:run-1", ""})...)
	})
}
