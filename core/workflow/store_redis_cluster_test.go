package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
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

func TestWorkflowActiveRunCapConcurrent(t *testing.T) {
	const n, limit = 8, 3
	forEachStoreBackend(t, func(t *testing.T, s *RedisStore, c redis.UniversalClient) {
		ctx := context.Background()
		// A member whose record is gone and one whose record is terminal hold no slot.
		done := &WorkflowRun{ID: "done", WorkflowID: "wf-1", OrgID: "org-a", Status: RunStatusSucceeded}
		if err := s.CreateRun(ctx, done); err != nil {
			t.Fatalf("CreateRun done: %v", err)
		}
		if err := c.SAdd(ctx, "wf:runs:active:org-a", "ghost", "done").Err(); err != nil {
			t.Fatalf("seed active set: %v", err)
		}
		locks := store.NewRedisJobStoreFromClient(c)
		var mu sync.Mutex
		var admitted []string
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				token, err := "", error(nil)
				for token == "" && err == nil {
					if token, err = locks.TryAcquireLock(ctx, "test:admission:org-a", 5*time.Second); token == "" {
						time.Sleep(time.Millisecond)
					}
				}
				if err != nil {
					t.Errorf("lock: %v", err)
					return
				}
				defer func() { _ = locks.ReleaseLock(ctx, "test:admission:org-a", token) }()
				count, err := s.CountActiveRuns(ctx, "org-a")
				if err != nil {
					t.Errorf("CountActiveRuns: %v", err)
					return
				}
				if count >= limit {
					return
				}
				if err := s.CreateRun(ctx, &WorkflowRun{ID: id, WorkflowID: "wf-1", OrgID: "org-a"}); err != nil {
					t.Errorf("CreateRun %s: %v", id, err)
					return
				}
				mu.Lock()
				admitted = append(admitted, id)
				mu.Unlock()
			}(fmt.Sprintf("run-%d", i))
		}
		wg.Wait()
		if len(admitted) != limit {
			t.Fatalf("admitted %v, want %d runs", admitted, limit)
		}
		if count, err := s.CountActiveRuns(ctx, "org-a"); err != nil || count != limit {
			t.Fatalf("CountActiveRuns = %d, %v; want %d", count, err, limit)
		}
		members, err := c.SMembers(ctx, "wf:runs:active:org-a").Result()
		if err != nil {
			t.Fatalf("smembers: %v", err)
		}
		slices.Sort(members)
		slices.Sort(admitted)
		if !slices.Equal(members, admitted) {
			t.Fatalf("active set %v, want %v", members, admitted)
		}
	})
}

var errInjected = errors.New("injected redis failure")

// failHook fails every command named name on key, and any pipeline holding
// one, until it is re-armed.
type failHook struct {
	mu        sync.Mutex
	name, key string
}

func (h *failHook) arm(name, key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.name, h.key = name, key
}

func (h *failHook) hits(cmd redis.Cmder) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := cmd.Args()
	return h.name != "" && cmd.Name() == h.name && len(args) > 1 && fmt.Sprint(args[1]) == h.key
}

func (h *failHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *failHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.hits(cmd) {
			cmd.SetErr(errInjected)
			return errInjected
		}
		return next(ctx, cmd)
	}
}

func (h *failHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if h.hits(cmd) {
				for _, c := range cmds {
					c.SetErr(errInjected)
				}
				return errInjected
			}
		}
		return next(ctx, cmds)
	}
}

func TestWorkflowStoreIndexRepair(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*RedisStore, *redis.Client, *failHook) {
		client := testredis.NewClient(t, miniredis.RunT(t).Addr())
		hook := &failHook{}
		client.AddHook(hook)
		return NewRedisWorkflowStoreFromClient(client), client, hook
	}
	newRun := func(status RunStatus) *WorkflowRun {
		return &WorkflowRun{ID: "run-1", WorkflowID: "wf-1", OrgID: "org-a", Status: status,
			Steps: map[string]*StepRun{"a": {StepID: "a", JobID: "run-1:a@1"}}}
	}
	listed := func(t *testing.T, s *RedisStore, orgID string) int {
		t.Helper()
		wfs, err := s.ListWorkflows(ctx, orgID, 50)
		if err != nil {
			t.Fatalf("ListWorkflows: %v", err)
		}
		return len(wfs)
	}

	t.Run("save-index", func(t *testing.T) {
		s, _, hook := setup(t)
		hook.arm("zadd", "wf:index:all")
		if err := s.SaveWorkflow(ctx, &Workflow{ID: "wf-1", OrgID: "org-a"}); !errors.Is(err, errInjected) {
			t.Fatalf("SaveWorkflow err = %v, want the injected failure", err)
		}
		if n := listed(t, s, "") + listed(t, s, "org-a"); n != 0 {
			t.Fatalf("workflow listed %d times after a failed save", n)
		}
		hook.arm("", "")
		if err := s.SaveWorkflow(ctx, &Workflow{ID: "wf-1", OrgID: "org-a"}); err != nil {
			t.Fatalf("re-save: %v", err)
		}
		if listed(t, s, "") != 1 || listed(t, s, "org-a") != 1 {
			t.Fatal("re-save did not list the workflow")
		}
	})

	t.Run("create-undo", func(t *testing.T) {
		s, c, hook := setup(t)
		hook.arm("zadd", "wf:runs:status:pending")
		if err := s.CreateRun(ctx, newRun(RunStatusPending)); !errors.Is(err, errInjected) {
			t.Fatalf("CreateRun err = %v, want the injected failure", err)
		}
		expectEntries(t, c, false, [2]string{"wf:run:run-1", ""}, [2]string{"wf:runs:wf-1", "run-1"},
			[2]string{"wf:runs:all", "run-1"}, [2]string{"wf:runs:status:pending", "run-1"},
			[2]string{"wf:run:job:run-1:a@1", ""}, [2]string{"wf:runs:active:org-a", "run-1"})
	})

	t.Run("terminal-repair", func(t *testing.T) {
		s, c, hook := setup(t)
		run := newRun(RunStatusRunning)
		if err := s.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		run.Status = RunStatusSucceeded
		hook.arm("zrem", "wf:runs:status:running")
		if err := s.UpdateRun(ctx, run); err != nil {
			t.Fatalf("UpdateRun: %v", err)
		}
		expectEntries(t, c, true, [2]string{"wf:runs:status:running", "run-1"}, [2]string{"wf:runs:active:org-a", "run-1"})
		hook.arm("", "")
		newReconciler(s, NewEngine(s, &stubBus{}), store.NewRedisJobStoreFromClient(c), time.Millisecond, 10).reconcileRun(ctx, "run-1")
		for _, status := range allRunStatuses {
			expectEntries(t, c, status == RunStatusSucceeded, [2]string{runStatusIndexKey(status), "run-1"})
		}
		expectEntries(t, c, false, [2]string{"wf:runs:active:org-a", "run-1"})
	})

	t.Run("delete-count", func(t *testing.T) {
		s, c, hook := setup(t)
		if err := s.CreateRun(ctx, newRun(RunStatusRunning)); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		hook.arm("srem", "wf:runs:active:org-a")
		if err := s.DeleteRun(ctx, "run-1"); !errors.Is(err, errInjected) {
			t.Fatalf("DeleteRun err = %v, want the injected failure", err)
		}
		hook.arm("", "")
		if count, err := s.CountActiveRuns(ctx, "org-a"); err != nil || count != 0 {
			t.Fatalf("CountActiveRuns = %d, %v; want 0", count, err)
		}
		expectEntries(t, c, false, [2]string{"wf:runs:active:org-a", "run-1"})
	})

	t.Run("list-skips-gone", func(t *testing.T) {
		s, c, _ := setup(t)
		if err := s.SaveWorkflow(ctx, &Workflow{ID: "wf-1", OrgID: "org-a"}); err != nil {
			t.Fatalf("SaveWorkflow: %v", err)
		}
		if err := s.CreateRun(ctx, newRun(RunStatusRunning)); err != nil {
			t.Fatalf("CreateRun: %v", err)
		}
		if err := c.Del(ctx, "wf:def:wf-1", "wf:run:run-1").Err(); err != nil {
			t.Fatalf("del records: %v", err)
		}
		runs, err := s.ListRunsByWorkflow(ctx, "wf-1", 50)
		if err != nil {
			t.Fatalf("ListRunsByWorkflow: %v", err)
		}
		all, err := s.ListRuns(ctx, 0, 50)
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if n := listed(t, s, "") + listed(t, s, "org-a") + len(runs) + len(all); n != 0 {
			t.Fatalf("lists returned %d entries whose record is gone", n)
		}
	})
}
