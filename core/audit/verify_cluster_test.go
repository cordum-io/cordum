package audit

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// anchorGetHook calls fn(n) after the client's n-th GET of key.
type anchorGetHook struct {
	key string
	mu  sync.Mutex
	n   int
	fn  func(n int)
}

func (h *anchorGetHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *anchorGetHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if args := cmd.Args(); cmd.Name() == "get" && len(args) == 2 && args[1] == h.key {
			h.mu.Lock()
			h.n++
			n := h.n
			h.mu.Unlock()
			h.fn(n)
		}
		return err
	}
}

func (h *anchorGetHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *clusterRetentionFixture) verifyWith(client redis.UniversalClient, tenant string, opts VerifyOptions) (*VerifyResult, error) {
	opts.RetentionAnchorKey = f.chainer.AnchorKey(tenant)
	opts.HMACKey = f.chainer.HMACKeyForVerify()
	return VerifyChain(context.Background(), client, f.chainer.StreamKey(tenant), opts)
}

// unconfirmedTrim leaves the stream trimmed through seq 3 under a pending
// anchor, as a cluster sweep does between its stream trim and confirmation.
func (f *clusterRetentionFixture) unconfirmedTrim(tenant string) retentionAnchor {
	f.t.Helper()
	f.oldAndRecentChain(tenant)
	pending := f.pendingAnchor(tenant, 3, retentionAnchor{})
	f.setAnchor(tenant, pending)
	if n, err := clusterTrimScript.Run(context.Background(), f.cluster, []string{f.chainer.StreamKey(tenant)}, pending.StreamID).Int64(); err != nil || n != 3 {
		f.t.Fatalf("stream trim = %d, %v", n, err)
	}
	return pending
}

// The single-node read is a MULTI over the stream and the anchor key, which
// a cluster client refuses (CROSSSLOT). The cluster read reaches the same
// verdicts.
func TestVerifyChainCluster_MatchesSingleNodeVerdicts(t *testing.T) {
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	f.oldAndRecentChain("acme")
	f.sweepAt(retentionSweepTime)

	for _, opts := range []VerifyOptions{
		{},
		{SinceMs: retentionT0.UnixMilli()},
		{SinceMs: retentionT0.Add(6 * day).UnixMilli()},
		{Limit: 2},
	} {
		got, err := f.verifyWith(f.cluster, "acme", opts)
		if err != nil {
			t.Fatalf("cluster verify %+v: %v", opts, err)
		}
		if want := f.verify("acme", opts); !reflect.DeepEqual(got, want) {
			t.Fatalf("cluster verify %+v = %+v, want %+v", opts, got, want)
		}
	}
}

// A sweep moving the anchor between the anchor read and the stream read
// must not read as missing events.
func TestVerifyChainCluster_AnchorMovedDuringReadIsNotMissing(t *testing.T) {
	f := newClusterRetentionFixture(t)
	f.appendAt("acme", retentionT0, 3)
	f.appendAt("acme", retentionT0.Add(6*day), 2)
	f.appendAt("acme", retentionT0.Add(13*day), 1)
	f.sweepAt(retentionSweepTime)
	verifyClient := testredis.NewMiniClusterClient(t, f.mr.Addr())
	verifyClient.AddHook(&trimBeforeRangeHook{trim: func() {
		if n := f.sweepAt(retentionT0.Add(13*day + 2*time.Hour)); n != 2 {
			t.Errorf("racing trim removed %d, want 2", n)
		}
	}})

	res, err := f.verifyWith(verifyClient, "acme", VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != VerifyStatusPartial || res.FirstSeq != 6 || res.RetentionBoundarySeq != 6 || len(gapsOfType(res, GapTypeMissing)) != 0 {
		t.Fatalf("verify racing a trim: %+v, want partial from seq 6 with no missing gaps", res)
	}
}

// A verify landing between a sweep's stream trim and its confirmation retries
// instead of reporting the trimmed events missing.
func TestVerifyChainCluster_RetriesTrimAwaitingConfirmation(t *testing.T) {
	f := newClusterRetentionFixture(t)
	pending := f.unconfirmedTrim("acme")
	confirmed := retentionAnchor{Seq: pending.Seq, Hash: pending.Hash, StreamID: pending.StreamID}
	verifyClient := testredis.NewMiniClusterClient(t, f.mr.Addr())
	verifyClient.AddHook(&anchorGetHook{key: f.chainer.AnchorKey("acme"), fn: func(n int) {
		if n == 2 { // after the first attempt's second anchor read
			f.setAnchor("acme", confirmed)
		}
	}})

	res, err := f.verifyWith(verifyClient, "acme", VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != VerifyStatusPartial || res.FirstSeq != 4 || len(gapsOfType(res, GapTypeMissing)) != 0 {
		t.Fatalf("verify across the confirmation: %+v, want partial from seq 4", res)
	}
}

func TestVerifyChainCluster_UnconfirmedTrimStaysCompromised(t *testing.T) {
	f := newClusterRetentionFixture(t)
	f.unconfirmedTrim("acme")

	res, err := f.verifyWith(f.cluster, "acme", VerifyOptions{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeMissing), []int64{1, 2, 3}) {
		t.Fatalf("verify = %+v, want compromised with seqs 1-3 missing", res)
	}
}

// When every attempt reads a different anchor and fails against both, verify
// errors instead of returning a verdict.
func TestVerifyChainCluster_AnchorThatKeepsMovingIsAnError(t *testing.T) {
	f := newClusterRetentionFixture(t)
	f.oldAndRecentChain("acme")
	stale := []retentionAnchor{f.pendingAnchor("acme", 1, retentionAnchor{}), f.pendingAnchor("acme", 2, retentionAnchor{})}
	for i := range stale {
		stale[i].Pending = false
	}
	f.sweepAt(retentionSweepTime)
	f.setAnchor("acme", stale[0])
	verifyClient := testredis.NewMiniClusterClient(t, f.mr.Addr())
	verifyClient.AddHook(&anchorGetHook{key: f.chainer.AnchorKey("acme"), fn: func(n int) {
		f.setAnchor("acme", stale[n%2]) // each read sees the other stale anchor
	}})

	if res, err := f.verifyWith(verifyClient, "acme", VerifyOptions{}); !errors.Is(err, errAnchorKeptMoving) {
		t.Fatalf("verify = %+v, %v; want errAnchorKeptMoving", res, err)
	}
}
