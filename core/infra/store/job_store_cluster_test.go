package store

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/internal/testredis"
)

const (
	clusterProofJobID = "job-cluster-proof"
	clusterProofPtr   = "res://cluster-proof"
)

// SetResultPtr writes job:result_ptr:<id> and job:meta:<id> in one MULTI.
// Those keys hash to different slots, so a go-redis ClusterClient refuses the
// transaction client-side and one miniredis node is enough to show it. This
// is how SetResultPtr fails in production, where REDIS_CLUSTER_ADDRESSES makes
// redisutil.NewClient return a ClusterClient. The job-store fix row of the
// Redis Cluster slots feature turns this test into a success case.
func TestSetResultPtrClusterCrossSlot(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisJobStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))

	err := s.SetResultPtr(context.Background(), clusterProofJobID, clusterProofPtr)
	if !errors.Is(err, redis.ErrCrossSlot) {
		t.Fatalf("SetResultPtr on a cluster client: got %v, want redis.ErrCrossSlot", err)
	}
	for _, key := range []string{jobResultPtrKey(clusterProofJobID), jobMetaKey(clusterProofJobID)} {
		if mr.Exists(key) {
			t.Fatalf("%s was written although the transaction was refused", key)
		}
	}
}

// The control: the same call on a single-node client writes both keys.
func TestSetResultPtrSingleNode(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisJobStoreFromClient(testredis.NewClient(t, mr.Addr()))

	if err := s.SetResultPtr(context.Background(), clusterProofJobID, clusterProofPtr); err != nil {
		t.Fatalf("SetResultPtr on a single-node client: %v", err)
	}
	if got, err := mr.Get(jobResultPtrKey(clusterProofJobID)); err != nil || got != clusterProofPtr {
		t.Fatalf("%s = %q, %v; want %q", jobResultPtrKey(clusterProofJobID), got, err, clusterProofPtr)
	}
	if got := mr.HGet(jobMetaKey(clusterProofJobID), "result_ptr"); got != clusterProofPtr {
		t.Fatalf("%s result_ptr = %q; want %q", jobMetaKey(clusterProofJobID), got, clusterProofPtr)
	}
}
