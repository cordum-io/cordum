package scheduler

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	agentregistry "github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestRetentionProofDIC53 is cited by docs/contract/deletion-proofs.md. The
// worker id set cordum:workers:count expires after the fixed workerCountTTL
// (2m); the worker snapshot sys:workers:snapshot, written through PutResult as
// cmd/cordum-scheduler does, expires after REDIS_DATA_TTL. The writers reach
// no export.
func TestRetentionProofDIC53(t *testing.T) {
	const dataTTL = 5 * time.Minute
	t.Setenv("REDIS_DATA_TTL", dataTTL.String())
	ctx := context.Background()
	mr, client, marker := sessionRetentionFixture(t)
	reg := NewMemoryRegistry()
	reg.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId: marker + "-worker", Pool: marker + "-pool", Region: marker,
		Labels: map[string]string{"proof": marker},
	})
	NewEngine(nil, nil, reg, nil, nil, nil).WithCounterClient(client).persistWorkerCount(ctx)
	snapshot, err := json.Marshal(agentregistry.BuildSnapshot(reg.Snapshot(), nil))
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := store.NewRedisStoreFromClient(client).PutResult(ctx, agentregistry.SnapshotKey, snapshot); err != nil {
		t.Fatalf("PutResult: %v", err)
	}
	both := []string{workerCountKey, agentregistry.SnapshotKey}
	// families lists which of both hold the marker; a hit elsewhere fails.
	families := func() []string {
		t.Helper()
		hits := sessionRetentionTrace(t, client, marker)
		var out []string
		for _, family := range both {
			if slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
				out = append(out, family)
			}
		}
		for _, hit := range hits {
			if !slices.ContainsFunc(both, func(family string) bool { return strings.HasPrefix(hit, family) }) {
				t.Fatalf("trace outside the named families: %v", hits)
			}
		}
		return out
	}
	steps := []struct {
		advance time.Duration
		want    []string
	}{
		{0, both},
		{workerCountTTL - time.Second, both},
		{2 * time.Second, []string{agentregistry.SnapshotKey}},
		{dataTTL - workerCountTTL - 2*time.Second, []string{agentregistry.SnapshotKey}},
		{2 * time.Second, nil},
	}
	for i, step := range steps {
		mr.FastForward(step.advance)
		if got := families(); !slices.Equal(got, step.want) {
			t.Fatalf("step %d: marker in %v; want %v", i, got, step.want)
		}
	}
}
