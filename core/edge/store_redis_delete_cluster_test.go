package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// deleteClusterExecutions gives 120 execution keys, two delete batches.
const deleteClusterExecutions = 30

type deleteClusterRecorder struct {
	NoopRecorder
	keysDeleted int
}

func (r *deleteClusterRecorder) AddSessionCleanupKeysDeleted(count int) { r.keysDeleted += count }

// TestDeleteSessionOnCluster deletes a session whose execution keys span two
// delete batches, on one node and on a two-master cluster that refuses any
// multi-key command.
func TestDeleteSessionOnCluster(t *testing.T) {
	for _, backend := range []string{"single", "cluster"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			servers, client := deleteClusterBackend(t, backend == "cluster")
			deleteClusterSeed(t, ctx, client)
			for _, mr := range servers {
				if len(mr.Keys()) == 0 {
					t.Fatalf("server %s holds no seeded key", mr.Addr())
				}
			}
			rec := &deleteClusterRecorder{}
			store := NewRedisStoreFromClient(client, WithRecorder(rec))
			if err := store.DeleteSession(ctx, "tenant-a", "session-a"); err != nil {
				t.Fatalf("DeleteSession: %v", err)
			}
			for _, mr := range servers {
				if keys := mr.Keys(); len(keys) != 0 {
					t.Fatalf("server %s still holds %d keys, first %q", mr.Addr(), len(keys), keys[0])
				}
			}
			if want := deleteClusterExecutions*4 + 4; rec.keysDeleted != want {
				t.Fatalf("keys deleted = %d, want %d", rec.keysDeleted, want)
			}
		})
	}
}

func deleteClusterBackend(t *testing.T, cluster bool) ([]*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	servers := []*miniredis.Miniredis{miniredis.RunT(t)}
	if !cluster {
		return servers, testredis.NewClient(t, servers[0].Addr())
	}
	servers = append(servers, miniredis.RunT(t))
	client := testredis.NewTwoMasterClusterClient(t, servers[0].Addr(), servers[1].Addr())
	client.AddHook(testredis.CrossSlotGuard())
	return servers, client
}

// deleteClusterSeed writes one session with its executions, events and
// indexes in the layout of CreateSession, CreateExecution and AppendEvents,
// one key per command.
func deleteClusterSeed(t *testing.T, ctx context.Context, client redis.UniversalClient) {
	t.Helper()
	write := func(cmd redis.Cmder) {
		t.Helper()
		if err := cmd.Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd.Args(), err)
		}
	}
	started := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	session := validStoreSession("tenant-a", "session-a", "principal-a", started)
	sid := session.SessionID
	write(client.Set(ctx, edgeSessionKey(sid), deleteClusterJSON(t, session), 0))
	write(client.Set(ctx, edgeSessionHeartbeatKey(sid), started.Format(time.RFC3339Nano), 0))
	for _, key := range []string{
		edgeTenantIndexKey(session.TenantID),
		edgePrincipalIndexKey(session.TenantID, session.PrincipalID),
		edgeSessionTraceIndexKey(session.TraceID),
		edgeSessionRunIndexKey(session.WorkflowRunID),
	} {
		write(client.ZAdd(ctx, key, redis.Z{Score: float64(started.UnixMicro()), Member: sid}))
	}
	for i := 0; i < deleteClusterExecutions; i++ {
		at := started.Add(time.Duration(i+1) * time.Second)
		execution := validStoreExecution(session.TenantID, sid, fmt.Sprintf("execution-%02d", i), at, nil)
		eid := execution.ExecutionID
		score := float64(at.UnixMicro())
		write(client.Set(ctx, edgeExecutionKey(eid), deleteClusterJSON(t, execution), 0))
		for _, key := range []string{
			edgeSessionExecutionsIndexKey(sid),
			edgeJobIndexKey(execution.JobID),
			edgeExecutionTraceIndexKey(execution.TraceID),
			edgeExecutionRunIndexKey(execution.WorkflowRunID),
		} {
			write(client.ZAdd(ctx, key, redis.Z{Score: score, Member: eid}))
		}
		event := AgentActionEvent{EventID: "event-" + eid, SessionID: sid, ExecutionID: eid, TenantID: session.TenantID, Seq: 1, Timestamp: at}
		write(client.RPush(ctx, edgeEventsKey(eid), deleteClusterJSON(t, event)))
		write(client.ZAdd(ctx, edgeSessionEventsIndexKey(sid), redis.Z{Score: score, Member: sessionEventIndexMember(event)}))
		write(client.HSet(ctx, edgeEventIDIndexKey(eid), event.EventID, event.Seq))
		write(client.Set(ctx, edgeEventSeqKey(eid), event.Seq, 0))
	}
}

func deleteClusterJSON(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return payload
}
