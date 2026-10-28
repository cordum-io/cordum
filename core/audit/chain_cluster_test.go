package audit

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// clusterChainFixture runs a Chainer on a Redis Cluster client over
// miniredis; plain reaches the same data without the cluster code paths.
type clusterChainFixture struct {
	t       *testing.T
	mr      *miniredis.Miniredis
	cluster *redis.ClusterClient
	plain   *redis.Client
	chainer *Chainer
}

func newClusterChainFixture(t *testing.T, opts ...ChainerOption) *clusterChainFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	cluster := testredis.NewMiniClusterClient(t, mr.Addr())
	f := &clusterChainFixture{
		t: t, mr: mr, cluster: cluster,
		plain:   testredis.NewClient(t, mr.Addr()),
		chainer: NewChainer(cluster, "", opts...),
	}
	if !f.chainer.cluster {
		t.Fatal("chainer did not detect the cluster client")
	}
	return f
}

func (f *clusterChainFixture) append(tenant string, n int) []*SIEMEvent {
	f.t.Helper()
	events := make([]*SIEMEvent, 0, n)
	for i := 0; i < n; i++ {
		ev := baseEvent(tenant, "act-"+strconv.Itoa(i))
		if err := f.chainer.Append(context.Background(), ev); err != nil {
			f.t.Fatalf("append: %v", err)
		}
		events = append(events, ev)
	}
	return events
}

func (f *clusterChainFixture) head(tenant string) string {
	f.t.Helper()
	raw, err := f.plain.Get(context.Background(), f.chainer.HeadKey(tenant)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		f.t.Fatalf("get head: %v", err)
	}
	return raw
}

func (f *clusterChainFixture) verify(tenant string) *VerifyResult {
	f.t.Helper()
	res, err := VerifyChain(context.Background(), f.plain, f.chainer.StreamKey(tenant), VerifyOptions{
		RetentionAnchorKey: f.chainer.AnchorKey(tenant),
		HMACKey:            f.chainer.HMACKeyForVerify(),
	})
	if err != nil {
		f.t.Fatalf("verify: %v", err)
	}
	return res
}

// deleteSeqs XDELs the stream entries carrying the given seqs.
func (f *clusterChainFixture) deleteSeqs(tenant string, seqs ...int64) {
	f.t.Helper()
	ctx := context.Background()
	entries, err := f.plain.XRange(ctx, f.chainer.StreamKey(tenant), "-", "+").Result()
	if err != nil {
		f.t.Fatalf("xrange: %v", err)
	}
	for _, e := range entries {
		seq, _ := strconv.ParseInt(e.Values["seq"].(string), 10, 64)
		if slices.Contains(seqs, seq) {
			if err := f.plain.XDel(ctx, f.chainer.StreamKey(tenant), e.ID).Err(); err != nil {
				f.t.Fatalf("xdel: %v", err)
			}
		}
	}
}

func TestChainerCluster_AppendLinksToStreamTail(t *testing.T) {
	f := newClusterChainFixture(t, WithHMACKey(testHMACKey()))
	events := f.append("acme", 5)

	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d", i, ev.Seq)
		}
		if i > 0 && ev.PrevHash != events[i-1].EventHash {
			t.Fatalf("event %d does not link to its predecessor", ev.Seq)
		}
	}
	if events[0].PrevHash != "" {
		t.Fatalf("genesis PrevHash = %q", events[0].PrevHash)
	}
	if got, want := f.head("acme"), "5:"+events[4].EventHash; got != want {
		t.Fatalf("head hint = %q, want %q", got, want)
	}
	res := f.verify("acme")
	if res.Status != VerifyStatusOK || res.VerifiedEvents != 5 || res.HMACVerified != 5 {
		t.Fatalf("unexpected verify result: %+v", res)
	}
}

// The head hint is written after the append commits, so a crash in between
// leaves it behind the stream. The next append still links to the stream.
func TestChainerCluster_HeadHintBehindStreamIsHarmless(t *testing.T) {
	f := newClusterChainFixture(t)
	events := f.append("acme", 3)
	ctx := context.Background()

	for _, tc := range []struct{ name, lagging string }{
		{"lagging", "2:" + events[1].EventHash},
		{"missing", ""},
	} {
		lagging := tc.lagging
		t.Run(tc.name, func(t *testing.T) {
			if lagging == "" {
				f.plain.Del(ctx, f.chainer.HeadKey("acme"))
			} else {
				f.plain.Set(ctx, f.chainer.HeadKey("acme"), lagging, 0)
			}
			ev := f.append("acme", 1)[0]
			prev := events[len(events)-1]
			if ev.Seq != prev.Seq+1 || ev.PrevHash != prev.EventHash {
				t.Fatalf("appended seq %d prev %q, want seq %d linked to %q", ev.Seq, ev.PrevHash, prev.Seq+1, prev.EventHash)
			}
			if got, want := f.head("acme"), strconv.FormatInt(ev.Seq, 10)+":"+ev.EventHash; got != want {
				t.Fatalf("head hint = %q, want %q", got, want)
			}
			events = append(events, ev)
		})
	}
	if res := f.verify("acme"); res.Status != VerifyStatusOK || res.TotalEvents != 5 {
		t.Fatalf("unexpected verify result: %+v", res)
	}
}

// A head hint ahead of the stream means entries were deleted from the tail.
// The next append numbers past the head, so the deletion stays visible.
func TestChainerCluster_TailDeletionStaysVisible(t *testing.T) {
	f := newClusterChainFixture(t)
	events := f.append("acme", 5)
	f.deleteSeqs("acme", 4, 5)

	ev := f.append("acme", 1)[0]
	if ev.Seq != 6 || ev.PrevHash != events[2].EventHash {
		t.Fatalf("appended seq %d prev %q, want seq 6 linked to seq 3", ev.Seq, ev.PrevHash)
	}
	res := f.verify("acme")
	if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeMissing), []int64{4, 5}) {
		t.Fatalf("want compromised with seqs 4-5 missing: %+v", res)
	}
}

func TestChainerCluster_StreamDeletionStaysVisible(t *testing.T) {
	f := newClusterChainFixture(t)
	f.append("acme", 3)
	f.plain.Del(context.Background(), f.chainer.StreamKey("acme"))

	if ev := f.append("acme", 1)[0]; ev.Seq != 4 || ev.PrevHash != "" {
		t.Fatalf("appended seq %d prev %q, want seq 4 after the deleted head", ev.Seq, ev.PrevHash)
	}
	res := f.verify("acme")
	if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeMissing), []int64{1, 2, 3}) {
		t.Fatalf("want compromised with seqs 1-3 missing: %+v", res)
	}
}

func TestChainerCluster_HeadHintOnlyMovesForward(t *testing.T) {
	f := newClusterChainFixture(t)
	ctx := context.Background()
	headKey := f.chainer.HeadKey("acme")
	f.plain.Set(ctx, headKey, "7:"+strings.Repeat("a", 64), 0)

	for seq, want := range map[string]int{"6": 0, "7": 0, "8": 1} {
		got, err := clusterHeadScript.Run(ctx, f.cluster, []string{headKey}, seq, seq+":"+strings.Repeat("b", 64)).Int()
		if err != nil || got != want {
			t.Fatalf("head script with seq %s = %d, %v; want %d", seq, got, err, want)
		}
	}
	if got := f.head("acme"); got != "8:"+strings.Repeat("b", 64) {
		t.Fatalf("head hint = %q", got)
	}
}

func TestChainerCluster_RefusesMalformedTail(t *testing.T) {
	f := newClusterChainFixture(t)
	f.append("acme", 2)
	f.plain.XAdd(context.Background(), &redis.XAddArgs{
		Stream: f.chainer.StreamKey("acme"),
		Values: map[string]any{"seq": "3", "event": "not json"},
	})

	err := f.chainer.Append(context.Background(), baseEvent("acme", "next"))
	if err == nil || !strings.Contains(err.Error(), "read tail") {
		t.Fatalf("Append = %v, want a read tail error", err)
	}
}

// Two replicas appending concurrently serialize on the stream tail.
func TestChainerCluster_ConcurrentAppendsStayLinked(t *testing.T) {
	f := newClusterChainFixture(t)
	replicas := []*Chainer{f.chainer, NewChainer(f.cluster, "")}
	const producers, perProducer = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, producers)
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if err := replicas[p%2].Append(context.Background(), baseEvent("acme", "p"+strconv.Itoa(p))); err != nil {
					errs <- err
					return
				}
			}
		}(p)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("append: %v", err)
	}
	res := f.verify("acme")
	if res.Status != VerifyStatusOK || res.TotalEvents != producers*perProducer || res.LastSeq != producers*perProducer {
		t.Fatalf("unexpected verify result: %+v", res)
	}
}
