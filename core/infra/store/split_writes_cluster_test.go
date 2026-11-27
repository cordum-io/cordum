package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/internal/testredis"
)

const (
	splitWriteTenant  = "tenant-split"
	splitWriteAgentID = "agent-split"
)

// Each store writes a record and its derived index keys, which sit in
// different cluster slots. They must succeed on a cluster client.
func TestSplitWritesSucceedOnCluster(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		check func(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient)
	}{
		{"agent identity create", func(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient) {
			created, err := NewAgentIdentityStoreFromClient(client).Create(ctx, splitWriteIdentity())
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if !mr.Exists(agentIdentityKeyPrefix + created.ID) {
				t.Fatal("agent identity record missing")
			}
			if !splitWriteIndexed(mr, agentIdentityIndexKey, created.ID) {
				t.Fatal("agent identity index does not hold the id")
			}
		}},
		{"dlq add trims", func(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient) {
			s := NewDLQStoreFromClient(client, time.Hour)
			t.Setenv(dlqMaxEntriesEnv, "10")
			trimmed := []string{"dlq-1", "dlq-2", "dlq-3"}
			for i, id := range trimmed {
				if err := s.Add(ctx, splitWriteDLQEntry(id, int64(100+i))); err != nil {
					t.Fatalf("Add %s: %v", id, err)
				}
			}
			t.Setenv(dlqMaxEntriesEnv, "1")
			if err := s.Add(ctx, splitWriteDLQEntry("dlq-4", 200)); err != nil {
				t.Fatalf("Add with trim: %v", err)
			}
			if members, err := mr.ZMembers(dlqIndexKey()); err != nil || !slices.Equal(members, []string{"dlq-4"}) {
				t.Fatalf("dlq index = %v, %v; want [dlq-4]", members, err)
			}
			if slices.ContainsFunc(trimmed, func(id string) bool { return mr.Exists(dlqEntryKey(id)) }) {
				t.Fatal("a trimmed entry is still stored")
			}
			if got := mr.TTL(dlqEntryKey("dlq-4")); got != time.Hour {
				t.Fatalf("dlq-4 TTL = %v; want %v", got, time.Hour)
			}
		}},
		{"dlq delete", func(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient) {
			s := NewDLQStoreFromClient(client, time.Hour)
			if err := s.Add(ctx, splitWriteDLQEntry("dlq-1", 100)); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if err := s.Delete(ctx, "dlq-1"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if mr.Exists(dlqEntryKey("dlq-1")) || splitWriteIndexed(mr, dlqIndexKey(), "dlq-1") {
				t.Fatal("Delete left the entry or its index member")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			tc.check(t, mr, splitWriteClient(t, mr, splitWriteHook{}))
		})
	}
}

// A failed record write must leave no index entry pointing at it.
func TestSplitWritesRecordBeforeIndex(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		recordKey string
		indexKeys []string
		member    string
		write     func(redis.UniversalClient) error
	}{
		{"agent identity create", agentIdentityKeyPrefix + splitWriteAgentID, []string{agentIdentityIndexKey}, splitWriteAgentID,
			func(client redis.UniversalClient) error {
				identity := splitWriteIdentity()
				identity.ID = splitWriteAgentID
				_, err := NewAgentIdentityStoreFromClient(client).Create(ctx, identity)
				return err
			}},
		{"dlq add", dlqEntryKey("dlq-1"), []string{dlqIndexKey()}, "dlq-1",
			func(client redis.UniversalClient) error {
				return NewDLQStoreFromClient(client, time.Hour).Add(ctx, splitWriteDLQEntry("dlq-1", 100))
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			failure := errors.New("record write failed")
			client := splitWriteClient(t, mr, splitWriteHook{key: tc.recordKey, err: failure})
			if err := tc.write(client); !errors.Is(err, failure) {
				t.Fatalf("write error = %v; want %v", err, failure)
			}
			for _, key := range tc.indexKeys {
				if splitWriteIndexed(mr, key, tc.member) {
					t.Fatalf("%s holds %s although its record write failed", key, tc.member)
				}
			}
		})
	}
}

// Delete drops the index member before the entry, so a failed entry DEL
// leaves an unlisted entry, never a listed member without its entry.
func TestDLQDeleteRemovesIndexFirst(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	plain := NewDLQStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()), time.Hour)
	if err := plain.Add(ctx, splitWriteDLQEntry("dlq-1", 100)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	failure := errors.New("entry delete failed")
	hooked := NewDLQStoreFromClient(splitWriteClient(t, mr, splitWriteHook{key: dlqEntryKey("dlq-1"), err: failure}), time.Hour)
	if err := hooked.Delete(ctx, "dlq-1"); !errors.Is(err, failure) {
		t.Fatalf("Delete error = %v; want %v", err, failure)
	}
	if splitWriteIndexed(mr, dlqIndexKey(), "dlq-1") {
		t.Fatal("index still lists dlq-1 after a failed Delete")
	}
	if entry, err := plain.Get(ctx, "dlq-1"); err != nil || entry.JobID != "dlq-1" {
		t.Fatalf("Get after a failed Delete = %+v, %v; want the entry", entry, err)
	}
}

func TestAgentIdentityListSkipsIndexEntryWithoutRecord(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	s := NewAgentIdentityStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))
	created, err := s.Create(ctx, splitWriteIdentity())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := mr.ZAdd(agentIdentityIndexKey, 1, "agent-without-record"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.List(ctx, splitWriteTenant, "", 10, AgentIdentityFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("List returned %d identities; want only %s", len(got), created.ID)
	}
}

func splitWriteClient(t *testing.T, mr *miniredis.Miniredis, hook splitWriteHook) *redis.ClusterClient {
	t.Helper()
	client := testredis.NewMiniClusterClient(t, mr.Addr())
	client.AddHook(hook)
	return client
}

func splitWriteIdentity() AgentIdentity {
	return AgentIdentity{TenantID: splitWriteTenant, Name: "split-agent", Owner: "owner@test.com", RiskTier: "low"}
}

func splitWriteDLQEntry(jobID string, createdAt int64) DLQEntry {
	return DLQEntry{JobID: jobID, Topic: "job.split", CreatedAt: time.Unix(createdAt, 0).UTC()}
}

func splitWriteIndexed(mr *miniredis.Miniredis, key, member string) bool {
	members, err := mr.ZMembers(key)
	return err == nil && slices.Contains(members, member)
}

// splitWriteHook fails any command or pipeline whose first key is key with err,
// before it runs or, with afterRun, after it ran. It always refuses a DEL of
// several keys, as a real cluster does across slots and miniredis does not.
type splitWriteHook struct {
	key      string
	err      error
	afterRun bool
}

func (h splitWriteHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h splitWriteHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.run([]redis.Cmder{cmd}, func() error { return next(ctx, cmd) })
	}
}

func (h splitWriteHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return h.run(cmds, func() error { return next(ctx, cmds) })
	}
}

func (h splitWriteHook) run(cmds []redis.Cmder, next func() error) error {
	hit := false
	for _, cmd := range cmds {
		args := cmd.Args()
		if cmd.Name() == "del" && len(args) > 2 {
			return redis.ErrCrossSlot
		}
		hit = hit || (h.key != "" && len(args) > 1 && args[1] == h.key)
	}
	if hit && !h.afterRun {
		return h.err
	}
	if err := next(); err != nil || !hit {
		return err
	}
	return h.err
}
