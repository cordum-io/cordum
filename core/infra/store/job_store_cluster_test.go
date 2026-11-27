package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
)

const (
	clusterProofJobID = "job-cluster-proof"
	clusterProofPtr   = "res://cluster-proof"
)

// The two keys hash to different slots. Each write must stay on one key so
// go-redis's ClusterClient accepts it, including when backed by miniredis.
func TestSetResultPtrClusterCrossSlot(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisJobStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))

	if err := s.SetResultPtr(context.Background(), clusterProofJobID, clusterProofPtr); err != nil {
		t.Fatalf("SetResultPtr on a cluster client: %v", err)
	}
	if got, err := mr.Get(jobResultPtrKey(clusterProofJobID)); err != nil || got != clusterProofPtr {
		t.Fatalf("result pointer = %q, %v; want %q", got, err, clusterProofPtr)
	}
	if got := mr.HGet(jobMetaKey(clusterProofJobID), "result_ptr"); got != clusterProofPtr {
		t.Fatalf("meta result_ptr = %q; want %q", got, clusterProofPtr)
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

func TestSetSafetyAndOutputDecisionCluster(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisJobStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))
	ctx := context.Background()
	safety := model.SafetyDecisionRecord{Decision: model.SafetyAllow, CheckedAt: 123}
	if err := s.SetSafetyDecision(ctx, clusterProofJobID, safety); err != nil {
		t.Fatalf("SetSafetyDecision: %v", err)
	}
	if got := mr.HGet(jobMetaKey(clusterProofJobID), metaFieldSafetyDecision); got != string(safety.Decision) {
		t.Fatalf("meta safety decision = %q; want %q", got, safety.Decision)
	}
	history, err := mr.List(jobDecisionKey(clusterProofJobID))
	if err != nil || len(history) != 1 {
		t.Fatalf("decision history = %v, %v; want one entry", history, err)
	}
	var entry struct{ Decision string }
	if err := json.Unmarshal([]byte(history[0]), &entry); err != nil || entry.Decision != string(safety.Decision) {
		t.Fatalf("history decision = %q, %v; want %q", entry.Decision, err, safety.Decision)
	}
	output := model.OutputSafetyRecord{Decision: model.OutputAllow, CheckedAt: 123}
	if err := s.SetOutputDecision(ctx, clusterProofJobID, output); err != nil {
		t.Fatalf("SetOutputDecision: %v", err)
	}
	want, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mr.Get(outputDecisionKey(clusterProofJobID)); err != nil || got != string(want) {
		t.Fatalf("output decision = %q, %v; want %s", got, err, want)
	}
	if got := mr.HGet(jobMetaKey(clusterProofJobID), metaFieldOutputSafety); got != string(want) {
		t.Fatalf("meta output decision = %q; want %s", got, want)
	}
	for _, key := range []string{jobMetaKey(clusterProofJobID), jobDecisionKey(clusterProofJobID), outputDecisionKey(clusterProofJobID)} {
		if got := mr.TTL(key); got != s.metaTTL {
			t.Fatalf("%s TTL = %v; want %v", key, got, s.metaTTL)
		}
	}
}

func TestJobPairWritesMetaLast(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		write func(*RedisJobStore) error
	}{
		{"result", jobResultPtrKey(clusterProofJobID), func(s *RedisJobStore) error {
			return s.SetResultPtr(context.Background(), clusterProofJobID, clusterProofPtr)
		}},
		{"safety", jobDecisionKey(clusterProofJobID), func(s *RedisJobStore) error {
			return s.SetSafetyDecision(context.Background(), clusterProofJobID, model.SafetyDecisionRecord{Decision: model.SafetyAllow})
		}},
		{"output", outputDecisionKey(clusterProofJobID), func(s *RedisJobStore) error {
			return s.SetOutputDecision(context.Background(), clusterProofJobID, model.OutputSafetyRecord{Decision: model.OutputAllow})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			client := testredis.NewMiniClusterClient(t, mr.Addr())
			failure := errors.New("secondary write failed")
			client.AddHook(jobPairFailureHook{key: tc.key, err: failure})
			if err := tc.write(NewRedisJobStoreFromClient(client)); !errors.Is(err, failure) {
				t.Fatalf("write error = %v; want %v", err, failure)
			}
			if mr.Exists(jobMetaKey(clusterProofJobID)) {
				t.Fatal("meta was written before the secondary copy")
			}
		})
	}
}

type jobPairFailureHook struct {
	key string
	err error
}

func (h jobPairFailureHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h jobPairFailureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if args := cmd.Args(); len(args) > 1 && args[1] == h.key {
			return h.err
		}
		return next(ctx, cmd)
	}
}

func (h jobPairFailureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if args := cmd.Args(); len(args) > 1 && args[1] == h.key {
				return h.err
			}
		}
		return next(ctx, cmds)
	}
}
