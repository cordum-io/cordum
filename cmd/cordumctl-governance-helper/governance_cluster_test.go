package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

var errClusterGovInjected = errors.New("injected redis failure")

func TestGovernanceBackfillOnClusterClient(t *testing.T) {
	h := newGovernanceTestHarness(t)
	defer func() { _ = h.runtime.Close() }()
	defer func() { _ = h.jobStore.Close() }()
	defer func() { _ = h.decisionLogStore.Close() }()

	// Seed through the single-node stores: the job store's own writes are
	// not this test's subject.
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	seedGovernanceJob(t, h, &pb.JobRequest{
		JobId:    "job-cluster-backfill",
		Topic:    "topic.cluster",
		TenantId: "tenant-cluster",
		Labels:   map[string]string{"agent_id": "agent-cluster"},
	}, model.SafetyDecisionRecord{Decision: model.SafetyDeny, RuleID: "rule-cluster", Reason: "denied", CheckedAt: now.UnixMicro()})

	addr := strings.TrimPrefix(h.runtime.redisURL, "redis://")
	t.Setenv("REDIS_CLUSTER_ADDRESSES", addr+","+addr)
	runtime, err := newGovernanceRuntime(h.runtime.redisURL)
	if err != nil {
		t.Fatalf("new governance runtime: %v", err)
	}
	defer func() { _ = runtime.Close() }()
	cluster, ok := runtime.rawClient.(*redis.ClusterClient)
	if !ok {
		t.Fatalf("runtime client = %T, want *redis.ClusterClient", runtime.rawClient)
	}
	// ForEachMaster's node clients do not run the cluster client's hooks, so
	// only a keyless SCAN on the cluster client itself fails here.
	cluster.AddHook(clusterGovHook{match: clusterGovCommand("scan"), err: errClusterGovInjected})

	summary, err := runGovernanceBackfill(context.Background(), runtime, governanceBackfillConfig{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("backfill on cluster client: %v", err)
	}
	if summary.Appended != 1 {
		t.Fatalf("appended = %d, want 1", summary.Appended)
	}

	ctx := context.Background()
	recordPrefix := governanceDecisionTenantPrefix("tenant-cluster") + "rec:"
	keys, err := h.runtime.rawClient.Keys(ctx, recordPrefix+"*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("record keys = %v (err %v), want one", keys, err)
	}
	if ttl := h.runtime.rawClient.TTL(ctx, keys[0]).Val(); ttl <= 0 {
		t.Fatalf("record ttl = %v, want > 0", ttl)
	}
	record := governanceDecisionLogRecord{Tenant: "tenant-cluster", RuleID: "rule-cluster", AgentID: "agent-cluster", Topic: "topic.cluster"}
	id := strings.TrimPrefix(keys[0], recordPrefix)
	for _, key := range governanceDecisionIndexKeys(record, "deny") {
		if err := h.runtime.rawClient.ZScore(ctx, key, id).Err(); err != nil {
			t.Fatalf("index %s member %s: %v", key, id, err)
		}
	}
}

func TestGovernanceAppendRecordBeforeIndex(t *testing.T) {
	record := clusterGovRecord()
	recordKey := governanceDecisionRecordKey(record.Tenant, governanceDecisionRecordID(record))
	runtime, mr := clusterGovRuntime(t, clusterGovHook{match: clusterGovKey(recordKey), err: errClusterGovInjected})

	if err := runtime.appendDecisionRecord(context.Background(), record); !errors.Is(err, errClusterGovInjected) {
		t.Fatalf("append error = %v, want %v", err, errClusterGovInjected)
	}
	for _, key := range governanceDecisionIndexKeys(record, "deny") {
		if mr.Exists(key) {
			t.Fatalf("index %s was written before the record", key)
		}
	}
}

func TestGovernanceAppendIndexFailureRemovesRecord(t *testing.T) {
	record := clusterGovRecord()
	recordKey := governanceDecisionRecordKey(record.Tenant, governanceDecisionRecordID(record))
	runtime, mr := clusterGovRuntime(t, clusterGovHook{match: clusterGovCommand("zadd"), after: true, err: errClusterGovInjected})

	if err := runtime.appendDecisionRecord(context.Background(), record); !errors.Is(err, errClusterGovInjected) {
		t.Fatalf("append error = %v, want %v", err, errClusterGovInjected)
	}
	if !mr.Exists(governanceDecisionPrimaryIndexKey(record.Tenant)) {
		t.Fatal("index step did not run before the injected failure")
	}
	if mr.Exists(recordKey) {
		t.Fatal("record kept after its index step failed")
	}
}

func clusterGovRecord() governanceDecisionLogRecord {
	return governanceDecisionLogRecord{
		JobID:     "job-cluster-order",
		Tenant:    "tenant-cluster",
		AgentID:   "agent-cluster",
		Topic:     "topic.cluster",
		Verdict:   governanceVerdictDeny,
		RuleID:    "rule-cluster",
		Timestamp: time.Now().UTC().UnixMilli(),
	}
}

// clusterGovRuntime builds a runtime on a Redis Cluster client over
// miniredis, which serves every slot from one node.
func clusterGovRuntime(t *testing.T, hook clusterGovHook) (*governanceRuntime, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{mr.Addr()}, PoolSize: 1})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(hook)
	return &governanceRuntime{rawClient: client, decisionTTL: time.Hour}, mr
}

// clusterGovHook fails any command, or any pipeline holding a command, that
// match accepts. With after set, it runs them before failing.
type clusterGovHook struct {
	match func(redis.Cmder) bool
	after bool
	err   error
}

func (h clusterGovHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h clusterGovHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.run(ctx, []redis.Cmder{cmd}, func(ctx context.Context, _ []redis.Cmder) error { return next(ctx, cmd) })
	}
}

func (h clusterGovHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { return h.run(ctx, cmds, next) }
}

func (h clusterGovHook) run(ctx context.Context, cmds []redis.Cmder, next redis.ProcessPipelineHook) error {
	if !slices.ContainsFunc(cmds, h.match) {
		return next(ctx, cmds)
	}
	if h.after {
		if err := next(ctx, cmds); err != nil {
			return err
		}
	}
	return h.err
}

func clusterGovCommand(name string) func(redis.Cmder) bool {
	return func(cmd redis.Cmder) bool { return cmd.Name() == name }
}

func clusterGovKey(key string) func(redis.Cmder) bool {
	return func(cmd redis.Cmder) bool {
		args := cmd.Args()
		return len(args) > 1 && args[1] == key
	}
}
