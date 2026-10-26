package audit

import (
	"context"
	"crypto/sha1" // #nosec G505 -- matches the EVAL script hash go-redis computes
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// clusterRetentionFixture is a retentionFixture whose chainer, legal hold
// store and sweeper run on a Redis Cluster client over the same miniredis.
// The embedded plain client and verify do not take the cluster paths.
type clusterRetentionFixture struct {
	*retentionFixture
	cluster *redis.ClusterClient
}

func newClusterRetentionFixture(t *testing.T, opts ...ChainerOption) *clusterRetentionFixture {
	t.Helper()
	f := newRetentionFixture(t, nil, opts...)
	cluster := testredis.NewMiniClusterClient(t, f.mr.Addr())
	f.chainer = NewChainer(cluster, "", opts...)
	f.holds = NewLegalHoldStoreFromClient(cluster)
	f.sweeper = NewRetentionSweeper(cluster, f.chainer, f.holds, nil)
	return &clusterRetentionFixture{retentionFixture: f, cluster: cluster}
}

// pendingAnchor is the pending anchor a cluster sweep writes before trimming
// the tenant's chain through seq, signed when the chain has an HMAC key.
func (f *clusterRetentionFixture) pendingAnchor(tenant string, seq int64, prev retentionAnchor) retentionAnchor {
	f.t.Helper()
	entries, err := f.client.XRange(context.Background(), f.chainer.StreamKey(tenant), "-", "+").Result()
	if err != nil {
		f.t.Fatalf("xrange: %v", err)
	}
	for _, e := range entries {
		a, err := anchorForEntry(e)
		if err != nil || a.Seq != seq {
			continue
		}
		a.Pending, a.PrevSeq, a.PrevHash, a.PrevStreamID = true, prev.Seq, prev.Hash, prev.StreamID
		if key := f.chainer.HMACKeyForVerify(); key != nil {
			a.MAC = a.mac(f.chainer.AnchorKey(tenant), key)
		}
		return a
	}
	f.t.Fatalf("no event with seq %d", seq)
	return retentionAnchor{}
}

// xdelSeqs deletes the stream entries carrying the given seqs.
func xdelSeqs(t *testing.T, client redis.UniversalClient, streamKey string, seqs ...int64) {
	t.Helper()
	ctx := context.Background()
	entries, err := client.XRange(ctx, streamKey, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	for _, e := range entries {
		seq, _ := strconv.ParseInt(fmt.Sprint(e.Values["seq"]), 10, 64)
		if slices.Contains(seqs, seq) {
			if err := client.XDel(ctx, streamKey, e.ID).Err(); err != nil {
				t.Fatalf("xdel: %v", err)
			}
		}
	}
}

// afterScriptHook runs fn once, right after the client first runs script.
type afterScriptHook struct {
	script *redis.Script
	once   sync.Once
	fn     func()
}

func (h *afterScriptHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *afterScriptHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if err != nil || len(args) < 2 {
			return err
		}
		body, _ := args[1].(string)
		sum := sha1.Sum([]byte(body)) // #nosec G401 -- script hash, not security
		if (cmd.Name() == "evalsha" && body == h.script.Hash()) ||
			(cmd.Name() == "eval" && hex.EncodeToString(sum[:]) == h.script.Hash()) {
			h.once.Do(h.fn)
		}
		return err
	}
}

func (h *afterScriptHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// oldAndRecentChain chains seq 1-3 at T0 and seq 4-6 six days later: a sweep
// one hour past the 7-day window trims 1-3.
func (f *clusterRetentionFixture) oldAndRecentChain(tenant string) []*SIEMEvent {
	f.t.Helper()
	events := f.appendAt(tenant, retentionT0, 3)
	return append(events, f.appendAt(tenant, retentionT0.Add(6*day), 3)...)
}

var retentionSweepTime = retentionT0.Add(7*day + time.Hour)

func TestRetentionCluster_TrimsAndConfirmsAnchor(t *testing.T) {
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	events := f.oldAndRecentChain("acme")

	if n := f.sweepAt(retentionSweepTime); n != 3 {
		t.Fatalf("trimmed %d, want 3", n)
	}
	a := f.anchorValue("acme")
	if a.Pending || a.Seq != 3 || a.Hash != events[2].EventHash || !a.validMAC(f.chainer.AnchorKey("acme"), testHMACKey()) {
		t.Fatalf("anchor = %+v, want a confirmed, signed anchor at seq 3", a)
	}
	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusPartial || res.FirstSeq != 4 || len(gapsOfType(res, GapTypeRetentionTrimmed)) != len(res.Gaps) {
		t.Fatalf("unexpected verify result: %+v", res)
	}
	if n := f.sweepAt(retentionSweepTime); n != 0 {
		t.Fatalf("second sweep trimmed %d, want 0", n)
	}
}

func TestRetentionCluster_TrimsInBatches(t *testing.T) {
	f := newClusterRetentionFixture(t)
	ctx := context.Background()
	f.mr.SetTime(retentionT0)
	total := 2*retentionTrimBatch + 5
	pipe := f.client.Pipeline()
	for i := 1; i <= total; i++ {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: f.chainer.StreamKey("bulk"),
			Values: map[string]any{"seq": strconv.Itoa(i), "event": fmt.Sprintf(`{"event_hash":"%064x"}`, i)},
		})
	}
	pipe.Set(ctx, f.chainer.HeadKey("bulk"), fmt.Sprintf("%d:%064x", total, total), 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != int64(total-1) {
		t.Fatalf("trimmed %d, want %d", n, total-1)
	}
	if got := f.streamLen("bulk"); got != 1 {
		t.Fatalf("stream len = %d, want 1", got)
	}
	if a := f.anchorValue("bulk"); a.Pending || a.Seq != int64(total-1) {
		t.Fatalf("anchor = %+v, want confirmed at seq %d", a, total-1)
	}
}

// Crash state: the sweeper wrote its pending anchor, then died before the
// stream trim. The events stay and still verify end to end.
func TestRetentionCluster_CrashAfterPendingAnchor(t *testing.T) {
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	f.oldAndRecentChain("acme")
	f.setAnchor("acme", f.pendingAnchor("acme", 3, retentionAnchor{}))

	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusOK || res.TotalEvents != 6 || len(res.Gaps) != 0 {
		t.Fatalf("pending anchor ahead of the stream: %+v, want ok from genesis", res)
	}

	if n := f.sweepAt(retentionSweepTime); n != 3 {
		t.Fatalf("finishing sweep trimmed %d, want 3", n)
	}
	if a := f.anchorValue("acme"); a.Pending || a.Seq != 3 {
		t.Fatalf("anchor = %+v, want confirmed at seq 3", a)
	}
	if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 4 {
		t.Fatalf("after finishing the trim: %+v, want partial from seq 4", res)
	}
}

// Crash state: the stream was trimmed but the anchor was never confirmed.
// That looks the same as someone deleting those events, so verify reports
// them missing until an unheld sweep confirms the trim.
func TestRetentionCluster_CrashAfterStreamTrim(t *testing.T) {
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	ctx := context.Background()
	f.oldAndRecentChain("acme")
	pending := f.pendingAnchor("acme", 3, retentionAnchor{})
	f.setAnchor("acme", pending)
	if n, err := clusterTrimScript.Run(ctx, f.cluster, []string{f.chainer.StreamKey("acme")}, pending.StreamID).Int64(); err != nil || n != 3 {
		t.Fatalf("stream trim = %d, %v", n, err)
	}

	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeMissing), []int64{1, 2, 3}) {
		t.Fatalf("unconfirmed trim: %+v, want compromised with seqs 1-3 missing", res)
	}

	hold, err := f.holds.CreateHold(ctx, "acme", "litigation", "test")
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	f.sweepAt(retentionSweepTime)
	if a := f.anchorValue("acme"); !a.Pending {
		t.Fatalf("held sweep confirmed the trim: %+v", a)
	}
	if err := f.holds.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("release hold: %v", err)
	}
	if n := f.sweepAt(retentionSweepTime); n != 0 {
		t.Fatalf("confirming sweep trimmed %d, want 0", n)
	}
	if a := f.anchorValue("acme"); a.Pending || a.Seq != 3 {
		t.Fatalf("anchor = %+v, want confirmed at seq 3", a)
	}
	if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 4 {
		t.Fatalf("after confirming: %+v, want partial from seq 4", res)
	}
}

// heldPendingChain chains seq 1-3 old and 4-6 recent and sweeps; a legal
// hold is placed right after the sweep writes its pending anchor, so its
// second hold check stops the stream trim.
func heldPendingChain(t *testing.T) (*clusterRetentionFixture, *LegalHold) {
	t.Helper()
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	f.oldAndRecentChain("acme")
	var hold *LegalHold
	f.cluster.AddHook(&afterScriptHook{script: anchorSwapScript, fn: func() {
		var err error
		if hold, err = f.holds.CreateHold(context.Background(), "acme", "litigation", "test"); err != nil {
			t.Errorf("create hold: %v", err)
		}
	}})
	if n := f.sweepAt(retentionSweepTime); n != 0 {
		t.Fatalf("sweep trimmed %d after the hold appeared, want 0", n)
	}
	if a := f.anchorValue("acme"); !a.Pending || a.Seq != 3 || a.PrevSeq != 0 {
		t.Fatalf("anchor = %+v, want pending at seq 3 over no previous anchor", a)
	}
	if got := f.streamLen("acme"); got != 6 {
		t.Fatalf("stream len = %d, want 6", got)
	}
	return f, hold
}

func TestRetentionCluster_HoldAfterPendingAnchorKeepsEvents(t *testing.T) {
	f, hold := heldPendingChain(t)
	if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusOK || res.TotalEvents != 6 || len(res.Gaps) != 0 {
		t.Fatalf("held events under a pending anchor: %+v, want ok", res)
	}
	if n := f.sweepAt(retentionT0.Add(8 * day)); n != 0 {
		t.Fatalf("held sweep trimmed %d", n)
	}

	if err := f.holds.ReleaseHold(context.Background(), hold.ID, "test"); err != nil {
		t.Fatalf("release hold: %v", err)
	}
	if n := f.sweepAt(retentionT0.Add(8 * day)); n != 3 {
		t.Fatalf("sweep after release trimmed %d, want 3", n)
	}
	if a := f.anchorValue("acme"); a.Pending || a.Seq != 3 {
		t.Fatalf("anchor = %+v, want confirmed at seq 3", a)
	}
	if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 4 {
		t.Fatalf("after release: %+v, want partial from seq 4", res)
	}
}

// Events a hold keeps under a pending anchor cannot be deleted unnoticed,
// including exactly the range the pending trim names.
func TestRetentionCluster_HoldAfterPendingAnchorDetectsDeletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		deleted []int64
	}{
		{"oldest retained event", []int64{1}},
		{"event inside the pending range", []int64{2}},
		{"exactly the pending range", []int64{1, 2, 3}},
		{"past the pending range", []int64{1, 2, 3, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := heldPendingChain(t)
			xdelSeqs(t, f.client, f.chainer.StreamKey("acme"), tc.deleted...)

			res := f.verify("acme", VerifyOptions{})
			if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeMissing), tc.deleted) {
				t.Fatalf("verify = %+v, want compromised with %v missing", res, tc.deleted)
			}
			f.sweepAt(retentionT0.Add(8 * day))
			if a := f.anchorValue("acme"); !a.Pending {
				t.Fatalf("held sweep confirmed over a deletion: %+v", a)
			}
		})
	}
}

func TestRetentionCluster_HoldCheckErrorAfterPendingAnchorKeepsEvents(t *testing.T) {
	f := newClusterRetentionFixture(t)
	ctx := context.Background()
	f.oldAndRecentChain("acme")
	f.cluster.AddHook(&afterScriptHook{script: anchorSwapScript, fn: func() {
		f.client.SAdd(ctx, legalHoldSetPrefix+"acme", "h1")
		f.client.Set(ctx, legalHoldKeyPrefix+"h1", "{not json", 0)
	}})
	holdErrs := auditRetentionSkipped.WithLabelValues("hold_check_error")
	trimErrs := auditRetentionSkipped.WithLabelValues("trim_error")
	holdBefore, trimBefore := testutil.ToFloat64(holdErrs), testutil.ToFloat64(trimErrs)

	if n := f.sweepAt(retentionSweepTime); n != 0 {
		t.Fatalf("trimmed %d with a failing hold check, want 0", n)
	}
	if got := f.streamLen("acme"); got != 6 {
		t.Fatalf("stream len = %d, want 6", got)
	}
	if got := testutil.ToFloat64(holdErrs) - holdBefore; got != 1 {
		t.Fatalf("hold_check_error skips = %v, want 1", got)
	}
	if got := testutil.ToFloat64(trimErrs) - trimBefore; got != 0 {
		t.Fatalf("trim_error skips = %v, want 0", got)
	}
}

// With an HMAC key the sweeper does not act on an anchor it did not sign.
// Confirming a forged pending anchor would sign the deletion it names, and
// carrying a forged anchor into a pending one would sign that too.
func TestRetentionCluster_DoesNotTrimOverUnsignedAnchor(t *testing.T) {
	forgeries := map[string]func(f *clusterRetentionFixture, events []*SIEMEvent) retentionAnchor{
		// Someone deleted seq 1-4 and names them as a trim the sweeper
		// only has to confirm.
		"pending anchor over a deletion": func(f *clusterRetentionFixture, _ []*SIEMEvent) retentionAnchor {
			a := f.pendingAnchor("acme", 4, retentionAnchor{})
			xdelSeqs(f.t, f.client, f.chainer.StreamKey("acme"), 1, 2, 3, 4)
			a.MAC = fmt.Sprintf("%064x", 9)
			return a
		},
		"unsigned anchor": func(f *clusterRetentionFixture, events []*SIEMEvent) retentionAnchor {
			xdelSeqs(f.t, f.client, f.chainer.StreamKey("acme"), 1)
			return retentionAnchor{Seq: 1, Hash: events[0].EventHash, StreamID: "1-0"}
		},
	}
	for name, forge := range forgeries {
		t.Run(name, func(t *testing.T) {
			f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
			events := f.oldAndRecentChain("acme")
			forged := forge(f, events)
			f.setAnchor("acme", forged)
			before := f.streamLen("acme")

			if n := f.sweepAt(retentionSweepTime); n != 0 {
				t.Fatalf("sweep trimmed %d over a forged anchor, want 0", n)
			}
			if got := f.anchorValue("acme"); got != forged {
				t.Fatalf("anchor = %+v, want the forged anchor left as it was", got)
			}
			if got := f.streamLen("acme"); got != before {
				t.Fatalf("stream len = %d, want %d", got, before)
			}
			if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusCompromised ||
				len(gapsOfType(res, GapTypeHMACMismatch)) != 1 {
				t.Fatalf("verify = %+v, want compromised with an hmac_mismatch", res)
			}
		})
	}
}

// The pending anchor's MAC covers the anchor it replaces: dropping or moving
// that part invalidates it.
func TestRetentionCluster_PendingAnchorMACCoversPreviousAnchor(t *testing.T) {
	f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
	events := f.oldAndRecentChain("acme")
	signed := f.pendingAnchor("acme", 3, retentionAnchor{})
	moved := signed
	moved.PrevSeq, moved.PrevHash, moved.PrevStreamID = 1, events[0].EventHash, f.pendingAnchor("acme", 1, retentionAnchor{}).StreamID

	for name, forged := range map[string]retentionAnchor{
		"previous anchor dropped": {Seq: signed.Seq, Hash: signed.Hash, StreamID: signed.StreamID, MAC: signed.MAC},
		"previous anchor moved":   moved,
	} {
		t.Run(name, func(t *testing.T) {
			f.setAnchor("acme", forged)
			res := f.verify("acme", VerifyOptions{})
			if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeHMACMismatch), []int64{3}) {
				t.Fatalf("verify = %+v, want compromised with an hmac_mismatch at seq 3", res)
			}
		})
	}
}

func TestVerifyChain_PendingAnchorMustNameTheStreamEvent(t *testing.T) {
	f := newClusterRetentionFixture(t)
	events := f.oldAndRecentChain("acme")
	a := f.pendingAnchor("acme", 3, retentionAnchor{})
	a.Hash = events[1].EventHash
	f.setAnchor("acme", a)

	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeHashMismatch), []int64{3}) {
		t.Fatalf("verify = %+v, want compromised with a hash_mismatch at seq 3", res)
	}
}

// A legacy anchor (no stream ID) becomes the previous anchor of the next
// cluster trim, and verify links the stream to it until that trim confirms.
func TestRetentionCluster_TrimsOverLegacyAnchor(t *testing.T) {
	nextSweep := retentionT0.Add(13*day + 2*time.Hour) // trims seq 4-5
	setup := func(t *testing.T) *clusterRetentionFixture {
		f := newClusterRetentionFixture(t)
		events := f.oldAndRecentChain("acme")
		f.sweepAt(retentionSweepTime)
		f.setLegacyAnchor("acme", 3, events[2].EventHash)
		return f
	}

	t.Run("sweep", func(t *testing.T) {
		f := setup(t)
		if n := f.sweepAt(nextSweep); n != 2 {
			t.Fatalf("trimmed %d, want 2", n)
		}
		if a := f.anchorValue("acme"); a.Pending || a.Seq != 5 || a.StreamID == "" {
			t.Fatalf("anchor = %+v, want confirmed at seq 5 with a stream ID", a)
		}
	})

	t.Run("crash after pending anchor", func(t *testing.T) {
		f := setup(t)
		f.setAnchor("acme", f.pendingAnchor("acme", 5, f.anchorValue("acme")))
		if a := f.anchorValue("acme"); !a.Pending || a.PrevSeq != 3 || a.PrevStreamID != "" {
			t.Fatalf("anchor = %+v, want pending over the legacy anchor", a)
		}
		if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 4 ||
			!slices.Equal(gapsOfType(res, GapTypeRetentionTrimmed), []int64{1, 2, 3}) {
			t.Fatalf("verify = %+v, want partial from seq 4", res)
		}
		if n := f.sweepAt(nextSweep); n != 2 {
			t.Fatalf("finishing sweep trimmed %d, want 2", n)
		}
		if a := f.anchorValue("acme"); a.Pending || a.Seq != 5 {
			t.Fatalf("anchor = %+v, want confirmed at seq 5", a)
		}
	})
}

func TestParseRetentionAnchor_Pending(t *testing.T) {
	h, id := strings.Repeat("a", 64), "1700000000000-0"
	for _, raw := range []string{
		"5:" + h + ":" + id + ":0::",
		"5:" + h + ":" + id + ":2:" + h + ":" + id,
		"5:" + h + ":" + id + ":2:" + h + ":" + id + ":" + h,
		"5:" + h + ":" + id + ":2:" + h + ":", // over a legacy anchor
	} {
		a, err := parseRetentionAnchor(raw)
		if err != nil || !a.Pending || a.String() != raw {
			t.Errorf("parse %q = %+v, %v; want a pending anchor that round-trips", raw, a, err)
		}
	}
	for _, raw := range []string{
		"5:" + h + ":" + id + ":5:" + h + ":" + id, // previous anchor not before
		"5:" + h + ":" + id + ":0:" + h + ":",      // seq 0 with a hash
		"5:" + h + ":" + id + ":2::",               // previous anchor without hash
		"5:" + h + ":" + id + ":2:" + h,            // five fields
		"0:" + h + ":" + id + ":0::",               // pending anchor at seq 0
		"5:" + h + ":" + id + ":0:::nomac",         // bad MAC
		"5:" + h + "::0::",                         // pending anchor without stream ID
	} {
		if a, err := parseRetentionAnchor(raw); err == nil {
			t.Errorf("parse %q = %+v, want malformed", raw, a)
		}
	}
}
