package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/licensing"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

const day = 24 * time.Hour

// retentionT0 stamps the "old" stream IDs; miniredis.SetTime drives both XADD
// IDs and the TIME command the sweeper cuts off by.
var retentionT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type retentionFixture struct {
	t       *testing.T
	mr      *miniredis.Miniredis
	client  *redis.Client
	chainer *Chainer
	holds   *LegalHoldStore
	sweeper *RetentionSweeper
}

// newRetentionFixture builds an enforcing sweeper; a nil resolver means the
// Community plan (7-day retention).
func newRetentionFixture(t *testing.T, resolver *licensing.EntitlementResolver, opts ...ChainerOption) *retentionFixture {
	t.Helper()
	t.Setenv(EnvRetentionEnforce, "true")
	t.Setenv(EnvRetentionHours, "")
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := testredis.NewClient(t, mr.Addr())
	chainer := NewChainer(client, "", opts...)
	holds := NewLegalHoldStoreFromClient(client)
	return &retentionFixture{
		t: t, mr: mr, client: client, chainer: chainer, holds: holds,
		sweeper: NewRetentionSweeper(client, chainer, holds, resolver),
	}
}

// appendAt chains n events for tenant with stream IDs stamped at ts.
func (f *retentionFixture) appendAt(tenant string, ts time.Time, n int) []*SIEMEvent {
	f.t.Helper()
	f.mr.SetTime(ts)
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

func (f *retentionFixture) sweepAt(ts time.Time) int64 {
	f.t.Helper()
	f.mr.SetTime(ts)
	n, err := f.sweeper.SweepOnce(context.Background())
	if err != nil {
		f.t.Fatalf("sweep: %v", err)
	}
	return n
}

func (f *retentionFixture) streamLen(tenant string) int64 {
	f.t.Helper()
	n, err := f.client.XLen(context.Background(), f.chainer.StreamKey(tenant)).Result()
	if err != nil {
		f.t.Fatalf("xlen: %v", err)
	}
	return n
}

// verify walks the chain the way /api/v1/audit/verify does: with the
// persisted retention anchor and the chain's HMAC key.
func (f *retentionFixture) verify(tenant string, opts VerifyOptions) *VerifyResult {
	f.t.Helper()
	opts.RetentionAnchorKey = f.chainer.AnchorKey(tenant)
	opts.HMACKey = f.chainer.HMACKeyForVerify()
	res, err := VerifyChain(context.Background(), f.client, f.chainer.StreamKey(tenant), opts)
	if err != nil {
		f.t.Fatalf("verify: %v", err)
	}
	return res
}

// anchor returns the tenant's retention anchor (0, "" when never trimmed).
func (f *retentionFixture) anchor(tenant string) (int64, string) {
	f.t.Helper()
	a := f.anchorValue(tenant)
	return a.Seq, a.Hash
}

func (f *retentionFixture) anchorValue(tenant string) retentionAnchor {
	f.t.Helper()
	raw, err := f.client.Get(context.Background(), f.chainer.AnchorKey(tenant)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		f.t.Fatalf("get anchor: %v", err)
	}
	a, err := parseRetentionAnchor(raw)
	if err != nil {
		f.t.Fatalf("parse anchor: %v", err)
	}
	return a
}

func (f *retentionFixture) setAnchor(tenant string, a retentionAnchor) {
	f.t.Helper()
	if err := f.client.Set(context.Background(), f.chainer.AnchorKey(tenant), a.String(), 0).Err(); err != nil {
		f.t.Fatalf("set anchor: %v", err)
	}
}

func gapsOfType(res *VerifyResult, typ VerifyGapType) []int64 {
	var seqs []int64
	for _, g := range res.Gaps {
		if g.Type == typ {
			seqs = append(seqs, g.AtSeq)
		}
	}
	return seqs
}

// trimmedChain chains seq 1-3 at T0 and seq 4-6 six days later, then sweeps
// one hour past the 7-day window: 1-3 are trimmed, the anchor is seq 3.
func trimmedChain(t *testing.T, opts ...ChainerOption) (*retentionFixture, []*SIEMEvent) {
	t.Helper()
	f := newRetentionFixture(t, nil, opts...)
	events := f.appendAt("acme", retentionT0, 3)
	events = append(events, f.appendAt("acme", retentionT0.Add(6*day), 3)...)
	if n := f.sweepAt(retentionT0.Add(7*day + time.Hour)); n != 3 {
		t.Fatalf("trimmed %d events, want 3", n)
	}
	return f, events
}

func TestRetentionWindow(t *testing.T) {
	cases := []struct {
		name  string
		plan  licensing.Plan
		hours string
		want  time.Duration
	}{
		{"community entitlement", licensing.PlanCommunity, "", 7 * day},
		{"team entitlement", licensing.PlanTeam, "", 90 * day},
		{"enterprise unlimited", licensing.PlanEnterprise, "", 0},
		{"env narrows entitlement", licensing.PlanTeam, "24", 24 * time.Hour},
		{"env cannot widen entitlement", licensing.PlanCommunity, "720", 7 * day},
		{"env narrows unlimited", licensing.PlanEnterprise, "48", 48 * time.Hour},
		{"fractional hours", licensing.PlanCommunity, "1.5", 90 * time.Minute},
		{"zero ignored", licensing.PlanTeam, "0", 90 * day},
		{"negative ignored", licensing.PlanTeam, "-5", 90 * day},
		{"garbage ignored", licensing.PlanTeam, "abc", 90 * day},
		{"NaN ignored", licensing.PlanTeam, "NaN", 90 * day},
		{"overflow ignored", licensing.PlanTeam, "1e300", 90 * day},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvRetentionHours, tc.hours)
			if got := RetentionWindow(licensing.DefaultEntitlements(tc.plan)); got != tc.want {
				t.Fatalf("RetentionWindow = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRetentionEnforced(t *testing.T) {
	for raw, want := range map[string]bool{
		"":        DefaultRetentionEnforce,
		"true":    true,
		"1":       true,
		"yes":     true,
		"false":   false,
		"0":       false,
		"garbage": false,
	} {
		t.Setenv(EnvRetentionEnforce, raw)
		if got := RetentionEnforced(); got != want {
			t.Errorf("RetentionEnforced() with %q = %v, want %v", raw, got, want)
		}
	}
}

func TestRetentionSweeper_TrimsOnlyEventsOlderThanWindow(t *testing.T) {
	f, events := trimmedChain(t)

	if got := f.streamLen("acme"); got != 3 {
		t.Fatalf("stream len = %d, want 3", got)
	}
	if seq, hash := f.anchor("acme"); seq != 3 || hash != events[2].EventHash {
		t.Fatalf("anchor = %d:%s, want 3:%s", seq, hash, events[2].EventHash)
	}
	if n := f.sweepAt(retentionT0.Add(7*day + time.Hour)); n != 0 {
		t.Fatalf("second sweep trimmed %d, want 0 (idempotent)", n)
	}

	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusPartial {
		t.Fatalf("status = %q, want partial: %+v", res.Status, res)
	}
	if res.RetentionBoundarySeq != 4 || res.FirstSeq != 4 || res.LastSeq != 6 || res.VerifiedEvents != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if trimmed := gapsOfType(res, GapTypeRetentionTrimmed); len(trimmed) != 3 || len(trimmed) != len(res.Gaps) {
		t.Fatalf("gaps = %+v, want only retention_trimmed 1-3", res.Gaps)
	}
}

func TestRetentionSweeper_NeverTrimsChainHead(t *testing.T) {
	f := newRetentionFixture(t, nil)
	events := f.appendAt("idle", retentionT0, 4)

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 3 {
		t.Fatalf("trimmed %d, want 3 (head kept)", n)
	}
	if got := f.streamLen("idle"); got != 1 {
		t.Fatalf("stream len = %d, want 1", got)
	}
	if seq, hash := f.anchor("idle"); seq != 3 || hash != events[2].EventHash {
		t.Fatalf("anchor = %d:%s, want 3:%s", seq, hash, events[2].EventHash)
	}

	// The chain keeps growing from the retained head and still verifies.
	f.appendAt("idle", retentionT0.Add(30*day), 1)
	res := f.verify("idle", VerifyOptions{})
	if res.Status != VerifyStatusPartial || res.FirstSeq != 4 || res.LastSeq != 5 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(gapsOfType(res, GapTypeRetentionTrimmed)) != len(res.Gaps) {
		t.Fatalf("gaps = %+v, want only retention_trimmed", res.Gaps)
	}
}

func TestRetentionSweeper_TrimsInBatches(t *testing.T) {
	f := newRetentionFixture(t, nil)
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
	if seq, _ := f.anchor("bulk"); seq != int64(total-1) {
		t.Fatalf("anchor seq = %d, want %d", seq, total-1)
	}
}

func TestRetentionSweeper_UnlimitedNeverTrims(t *testing.T) {
	f := newRetentionFixture(t, newAuditEntitlementResolver(t, licensing.PlanEnterprise, nil))
	f.appendAt("big", retentionT0, 3)

	if n := f.sweepAt(retentionT0.Add(400 * day)); n != 0 {
		t.Fatalf("trimmed %d, want 0", n)
	}
	if got := f.streamLen("big"); got != 3 {
		t.Fatalf("stream len = %d, want 3", got)
	}
	if seq, _ := f.anchor("big"); seq != 0 {
		t.Fatalf("anchor seq = %d, want none", seq)
	}
}

func TestRetentionSweeper_EnforceOffNeverTrims(t *testing.T) {
	f := newRetentionFixture(t, nil)
	t.Setenv(EnvRetentionEnforce, "false")
	f.appendAt("acme", retentionT0, 3)

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 0 {
		t.Fatalf("trimmed %d, want 0", n)
	}
	// Run logs once and returns instead of starting the sweep loop.
	done := make(chan struct{})
	go func() {
		f.sweeper.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept running with enforcement off")
	}
	if got := f.streamLen("acme"); got != 3 {
		t.Fatalf("stream len = %d, want 3", got)
	}
}

func TestRetentionSweeper_SkipsTenantUnderLegalHold(t *testing.T) {
	f := newRetentionFixture(t, nil)
	ctx := context.Background()
	f.appendAt("held", retentionT0, 3)
	f.appendAt("free", retentionT0, 3)
	hold, err := f.holds.CreateHold(ctx, "held", "litigation", "test")
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	skipped := auditRetentionSkipped.WithLabelValues("legal_hold")
	before := testutil.ToFloat64(skipped)

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 2 {
		t.Fatalf("trimmed %d, want 2 (free tenant only)", n)
	}
	if got := f.streamLen("held"); got != 3 {
		t.Fatalf("held stream len = %d, want 3", got)
	}
	if got := f.streamLen("free"); got != 1 {
		t.Fatalf("free stream len = %d, want 1", got)
	}
	if got := testutil.ToFloat64(skipped) - before; got != 1 {
		t.Fatalf("legal_hold skips = %v, want 1", got)
	}

	// Releasing the hold lets retention resume.
	if err := f.holds.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 2 {
		t.Fatalf("trimmed %d after release, want 2", n)
	}
}

func TestRetentionSweeper_HoldCheckErrorSkipsTenant(t *testing.T) {
	f := newRetentionFixture(t, nil)
	ctx := context.Background()
	f.appendAt("broken", retentionT0, 3)
	f.appendAt("free", retentionT0, 3)
	// An unreadable hold record must never read as "no hold".
	if err := f.client.SAdd(ctx, legalHoldSetPrefix+"broken", "h1").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	if err := f.client.Set(ctx, legalHoldKeyPrefix+"h1", "{not json", 0).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}
	skipped := auditRetentionSkipped.WithLabelValues("hold_check_error")
	before := testutil.ToFloat64(skipped)

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 2 {
		t.Fatalf("trimmed %d, want 2 (free tenant only)", n)
	}
	if got := f.streamLen("broken"); got != 3 {
		t.Fatalf("broken stream len = %d, want 3", got)
	}
	if got := testutil.ToFloat64(skipped) - before; got != 1 {
		t.Fatalf("hold_check_error skips = %v, want 1", got)
	}

	// Without a legal hold store nothing is trimmed at all.
	f.appendAt("other", retentionT0, 3)
	f.mr.SetTime(retentionT0.Add(30 * day))
	noHolds := NewRetentionSweeper(f.client, f.chainer, nil, nil)
	if _, err := noHolds.SweepOnce(ctx); !errors.Is(err, errLegalHoldStoreUnavailable) {
		t.Fatalf("err = %v, want errLegalHoldStoreUnavailable", err)
	}
	if got := f.streamLen("other"); got != 3 {
		t.Fatalf("other stream len = %d, want 3", got)
	}
}

func TestRetentionSweeper_KeepsMalformedAnchorCandidate(t *testing.T) {
	f := newRetentionFixture(t, nil)
	ctx := context.Background()
	f.mr.SetTime(retentionT0)
	hash := fmt.Sprintf("%064x", 1)
	for _, values := range []map[string]any{
		{"seq": "1", "event": `{"event_hash":"` + hash + `"}`},
		{"seq": "2", "event": "not json"},
		{"seq": "3", "event": `{"event_hash":"` + hash + `"}`},
	} {
		if err := f.client.XAdd(ctx, &redis.XAddArgs{Stream: f.chainer.StreamKey("weird"), Values: values}).Err(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
	}
	if err := f.client.Set(ctx, f.chainer.HeadKey("weird"), "3:"+hash, 0).Err(); err != nil {
		t.Fatalf("set head: %v", err)
	}
	skipped := auditRetentionSkipped.WithLabelValues("trim_error")
	before := testutil.ToFloat64(skipped)

	if n := f.sweepAt(retentionT0.Add(30 * day)); n != 0 {
		t.Fatalf("trimmed %d, want 0", n)
	}
	if got := f.streamLen("weird"); got != 3 {
		t.Fatalf("stream len = %d, want 3", got)
	}
	if got := testutil.ToFloat64(skipped) - before; got != 1 {
		t.Fatalf("trim_error skips = %v, want 1", got)
	}
}

func TestRetentionSweeper_OneLeaderPerInterval(t *testing.T) {
	f := newRetentionFixture(t, nil)
	f.appendAt("acme", retentionT0, 3)
	f.mr.SetTime(retentionT0.Add(30 * day))
	f.sweeper.sweepIfLeader(context.Background())
	if got := f.streamLen("acme"); got != 1 {
		t.Fatalf("stream len = %d, want 1", got)
	}

	// Another replica inside the same interval does not sweep.
	f.appendAt("other", retentionT0, 3)
	f.mr.SetTime(retentionT0.Add(30 * day))
	NewRetentionSweeper(f.client, f.chainer, f.holds, nil).sweepIfLeader(context.Background())
	if got := f.streamLen("other"); got != 3 {
		t.Fatalf("other stream len = %d, want 3 (lock held)", got)
	}
	if ttl := f.mr.TTL(retentionLockKey); ttl <= 0 || ttl > DefaultRetentionSweepInterval {
		t.Fatalf("lock ttl = %s, want (0, %s]", ttl, DefaultRetentionSweepInterval)
	}
}

// trimBeforeRangeHook runs trim once, just before the first command batch
// that reads the chain range, i.e. "a sweep commits while verify starts".
type trimBeforeRangeHook struct {
	once sync.Once
	trim func()
}

func (h *trimBeforeRangeHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *trimBeforeRangeHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "xrange" {
			h.once.Do(h.trim)
		}
		return next(ctx, cmd)
	}
}

func (h *trimBeforeRangeHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if cmd.Name() == "xrange" {
				h.once.Do(h.trim)
			}
		}
		return next(ctx, cmds)
	}
}

// TestVerifyChain_SnapshotsAnchorWithRange: verify reads the anchor and the
// range atomically, so a trim committing as verify starts can never read as
// missing events (a false "compromised").
func TestVerifyChain_SnapshotsAnchorWithRange(t *testing.T) {
	f, _ := trimmedChain(t)
	verifyClient := testredis.NewClient(t, f.mr.Addr())
	verifyClient.AddHook(&trimBeforeRangeHook{trim: func() {
		if n := f.sweepAt(retentionT0.Add(13*day + 2*time.Hour)); n != 2 {
			t.Errorf("racing trim removed %d, want 2", n)
		}
	}})
	res, err := VerifyChain(context.Background(), verifyClient, f.chainer.StreamKey("acme"),
		VerifyOptions{RetentionAnchorKey: f.chainer.AnchorKey("acme")})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != VerifyStatusPartial || res.FirstSeq != 6 || res.RetentionBoundarySeq != 6 || len(gapsOfType(res, GapTypeMissing)) != 0 {
		t.Fatalf("verify racing a trim: %+v, want partial from seq 6 with no missing gaps", res)
	}
}

func TestVerifyChain_AfterRetentionTrim(t *testing.T) {
	ctx := context.Background()

	t.Run("windowed walk links to the anchor", func(t *testing.T) {
		f, _ := trimmedChain(t)
		opts := VerifyOptions{SinceMs: retentionT0.Add(6 * day).UnixMilli()}
		if res := f.verify("acme", opts); res.Status != VerifyStatusOK || res.VerifiedEvents != 3 {
			t.Fatalf("with anchor: %+v, want ok/3", res)
		}
		// Without the anchor the absent prefix is unexplained.
		res, err := VerifyChain(ctx, f.client, f.chainer.StreamKey("acme"), opts)
		if err != nil || res.Status != VerifyStatusCompromised {
			t.Fatalf("without anchor: %+v (err %v), want compromised", res, err)
		}
		// A window reaching back past the newest trimmed event lost events.
		if res := f.verify("acme", VerifyOptions{SinceMs: retentionT0.UnixMilli()}); res.Status != VerifyStatusPartial ||
			len(gapsOfType(res, GapTypeRetentionTrimmed)) != 1 || len(res.Gaps) != 1 {
			t.Fatalf("window from T0: %+v, want partial with one retention_trimmed gap", res)
		}
		// The link is really checked: a forged anchor breaks it.
		forged := f.anchorValue("acme")
		forged.Hash = fmt.Sprintf("%064x", 7)
		f.setAnchor("acme", forged)
		if res := f.verify("acme", opts); res.Status != VerifyStatusCompromised {
			t.Fatalf("forged anchor: %+v, want compromised", res)
		}
	})

	t.Run("modified retained event is compromised", func(t *testing.T) {
		f, _ := trimmedChain(t)
		key := f.chainer.StreamKey("acme")
		entries, err := f.client.XRange(ctx, key, "-", "+").Result()
		if err != nil {
			t.Fatalf("xrange: %v", err)
		}
		target := entries[1] // seq 5
		var ev SIEMEvent
		if err := json.Unmarshal([]byte(target.Values["event"].(string)), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		ev.Action = "MUTATED"
		mutated, _ := json.Marshal(ev)
		// Miniredis cannot overwrite an entry: delete it and re-add the
		// mutated copy at the tail.
		if err := f.client.XDel(ctx, key, target.ID).Err(); err != nil {
			t.Fatalf("xdel: %v", err)
		}
		if err := f.client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"seq": "5", "event": string(mutated)}}).Err(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusCompromised || len(gapsOfType(res, GapTypeHashMismatch)) == 0 {
			t.Fatalf("result = %+v, want compromised with hash_mismatch", res)
		}
	})

	t.Run("deleting the oldest retained event is missing, not trimmed", func(t *testing.T) {
		f, _ := trimmedChain(t)
		key := f.chainer.StreamKey("acme")
		entries, err := f.client.XRange(ctx, key, "-", "+").Result()
		if err != nil {
			t.Fatalf("xrange: %v", err)
		}
		if err := f.client.XDel(ctx, key, entries[0].ID).Err(); err != nil { // seq 4
			t.Fatalf("xdel: %v", err)
		}
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusCompromised {
			t.Fatalf("status = %q, want compromised: %+v", res.Status, res)
		}
		if missing := gapsOfType(res, GapTypeMissing); len(missing) != 1 || missing[0] != 4 {
			t.Fatalf("missing gaps = %v, want [4]", missing)
		}
		if trimmed := gapsOfType(res, GapTypeRetentionTrimmed); len(trimmed) != 3 {
			t.Fatalf("retention_trimmed gaps = %v, want 1-3", trimmed)
		}
	})

	t.Run("deleted stream is compromised", func(t *testing.T) {
		f, _ := trimmedChain(t)
		f.mr.Del(f.chainer.StreamKey("acme"))
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusCompromised || len(gapsOfType(res, GapTypeMissing)) != 1 {
			t.Fatalf("result = %+v, want compromised with one missing gap", res)
		}
	})
}

// A truncated prefix is only ever explained by the anchor the sweeper writes
// in the same script: without it the chain reads as deleted, not trimmed.
func TestVerifyChain_DeletedAnchorIsCompromised(t *testing.T) {
	f, _ := trimmedChain(t)
	f.mr.Del(f.chainer.AnchorKey("acme"))

	res := f.verify("acme", VerifyOptions{})
	if res.Status != VerifyStatusCompromised {
		t.Fatalf("status = %q, want compromised: %+v", res.Status, res)
	}
	if missing := gapsOfType(res, GapTypeMissing); len(missing) != 3 || missing[0] != 1 || missing[2] != 3 {
		t.Fatalf("missing gaps = %v, want 1-3", missing)
	}
	if trimmed := gapsOfType(res, GapTypeRetentionTrimmed); len(trimmed) != 0 {
		t.Fatalf("retention_trimmed gaps = %v, want none", trimmed)
	}
}

func TestVerifyChain_HMACSignedRetentionAnchor(t *testing.T) {
	ctx := context.Background()

	t.Run("legitimate trim is partial", func(t *testing.T) {
		f, events := trimmedChain(t, WithHMACKey(testHMACKey()))
		a := f.anchorValue("acme")
		if a.Seq != 3 || a.Hash != events[2].EventHash || a.MAC == "" {
			t.Fatalf("anchor = %+v, want seq 3 with a MAC", a)
		}
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusPartial || res.RetentionBoundarySeq != 4 || len(gapsOfType(res, GapTypeRetentionTrimmed)) != len(res.Gaps) {
			t.Fatalf("result = %+v, want partial with only retention_trimmed gaps", res)
		}
		// Without the key the MAC cannot be checked; verify says so.
		res, err := VerifyChain(ctx, f.client, f.chainer.StreamKey("acme"), VerifyOptions{RetentionAnchorKey: f.chainer.AnchorKey("acme")})
		if err != nil || res.Status != VerifyStatusPartial || !res.HMACSeen {
			t.Fatalf("no key: %+v (err %v), want partial with hmac_seen", res, err)
		}
	})

	forgeries := map[string]func(f *retentionFixture, a retentionAnchor) retentionAnchor{
		"mac stripped": func(_ *retentionFixture, a retentionAnchor) retentionAnchor {
			a.MAC = ""
			return a
		},
		"wrong mac": func(_ *retentionFixture, a retentionAnchor) retentionAnchor {
			a.MAC = fmt.Sprintf("%064x", 9)
			return a
		},
		"mac signed for another tenant": func(f *retentionFixture, a retentionAnchor) retentionAnchor {
			a.MAC = a.mac(f.chainer.AnchorKey("other"), testHMACKey())
			return a
		},
		"oldest retained event deleted, anchor moved over it": func(f *retentionFixture, _ retentionAnchor) retentionAnchor {
			entries, err := f.client.XRangeN(ctx, f.chainer.StreamKey("acme"), "-", "+", 1).Result()
			if err != nil {
				f.t.Fatalf("xrange: %v", err)
			}
			if err := f.client.XDel(ctx, f.chainer.StreamKey("acme"), entries[0].ID).Err(); err != nil {
				f.t.Fatalf("xdel: %v", err)
			}
			moved, err := anchorForEntry(entries[0])
			if err != nil {
				f.t.Fatalf("anchor for seq 4: %v", err)
			}
			moved.MAC = fmt.Sprintf("%064x", 9)
			return moved
		},
	}
	for name, forge := range forgeries {
		t.Run(name, func(t *testing.T) {
			f, _ := trimmedChain(t, WithHMACKey(testHMACKey()))
			f.setAnchor("acme", forge(f, f.anchorValue("acme")))
			res := f.verify("acme", VerifyOptions{})
			if res.Status != VerifyStatusCompromised || len(gapsOfType(res, GapTypeHMACMismatch)) != 1 {
				t.Fatalf("result = %+v, want compromised with an hmac_mismatch gap", res)
			}
			if trimmed := gapsOfType(res, GapTypeRetentionTrimmed); len(trimmed) != 0 {
				t.Fatalf("retention_trimmed gaps = %v, want none from an untrusted anchor", trimmed)
			}
		})
	}
}

// setLegacyAnchor replaces the tenant's anchor with the "<seq>:<event_hash>"
// form anchors had before they carried a stream ID.
func (f *retentionFixture) setLegacyAnchor(tenant string, seq int64, hash string) {
	f.t.Helper()
	if err := f.client.Set(context.Background(), f.chainer.AnchorKey(tenant), fmt.Sprintf("%d:%s", seq, hash), 0).Err(); err != nil {
		f.t.Fatalf("set legacy anchor: %v", err)
	}
}

func TestParseRetentionAnchor_Legacy(t *testing.T) {
	h := fmt.Sprintf("%064x", 1)
	if a, err := parseRetentionAnchor("3:" + h); err != nil || a != (retentionAnchor{Seq: 3, Hash: h}) {
		t.Fatalf("parse legacy anchor = %+v, %v; want seq 3 with no stream ID or MAC", a, err)
	}
	for _, raw := range []string{"0:" + h, "-1:" + h, "x:" + h, "3:", "3:zz", ":"} {
		if a, err := parseRetentionAnchor(raw); err == nil {
			t.Errorf("parse %q = %+v, want malformed", raw, a)
		}
	}
}

func TestVerifyChain_LegacyAnchor(t *testing.T) {
	t.Run("without HMAC it anchors the chain", func(t *testing.T) {
		f, events := trimmedChain(t)
		f.setLegacyAnchor("acme", 3, events[2].EventHash)
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusPartial || res.FirstSeq != 4 || res.RetentionBoundarySeq != 4 ||
			len(res.Gaps) != 3 || !slices.Equal(gapsOfType(res, GapTypeRetentionTrimmed), []int64{1, 2, 3}) {
			t.Fatalf("verify = %+v, want partial with seqs 1-3 trimmed", res)
		}
		// With no stream ID, a window that reaches the oldest retained event
		// cannot tell whether it starts after the trim (ok with an ID).
		window := VerifyOptions{SinceMs: retentionT0.Add(6 * day).UnixMilli()}
		if res := f.verify("acme", window); res.Status != VerifyStatusPartial || res.VerifiedEvents != 3 ||
			len(res.Gaps) != 1 || !slices.Equal(gapsOfType(res, GapTypeRetentionTrimmed), []int64{3}) {
			t.Fatalf("window = %+v, want partial with seq 3 trimmed", res)
		}
		f.setLegacyAnchor("acme", 3, events[1].EventHash)
		if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusCompromised ||
			!slices.Equal(gapsOfType(res, GapTypeHashMismatch), []int64{4}) {
			t.Fatalf("wrong hash = %+v, want compromised at seq 4", res)
		}
	})

	t.Run("with HMAC it is unsigned", func(t *testing.T) {
		f, events := trimmedChain(t, WithHMACKey(testHMACKey()))
		f.setLegacyAnchor("acme", 3, events[2].EventHash)
		res := f.verify("acme", VerifyOptions{})
		if res.Status != VerifyStatusCompromised || !slices.Equal(gapsOfType(res, GapTypeHMACMismatch), []int64{3}) ||
			len(gapsOfType(res, GapTypeRetentionTrimmed)) != 0 {
			t.Fatalf("verify = %+v, want compromised with an hmac_mismatch at seq 3", res)
		}
	})

	t.Run("the next trim replaces it", func(t *testing.T) {
		f, events := trimmedChain(t, WithHMACKey(testHMACKey()))
		f.setLegacyAnchor("acme", 3, events[2].EventHash)
		if n := f.sweepAt(retentionT0.Add(13*day + 2*time.Hour)); n != 2 {
			t.Fatalf("trimmed %d, want 2", n)
		}
		if a := f.anchorValue("acme"); a.Seq != 5 || a.StreamID == "" || !a.validMAC(f.chainer.AnchorKey("acme"), testHMACKey()) {
			t.Fatalf("anchor = %+v, want a signed anchor at seq 5", a)
		}
		if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 6 {
			t.Fatalf("verify = %+v, want partial from seq 6", res)
		}
	})
}

// However many events are gone, a run of absent seqs lists at most maxGapRun
// gaps, so verify's payload stays bounded.
func TestVerifyChain_CapsMissingRun(t *testing.T) {
	f := newRetentionFixture(t, nil)
	// A head with no stream behind it: the next append is seq 5001.
	if err := f.client.Set(context.Background(), f.chainer.HeadKey("gone"), fmt.Sprintf("5000:%064x", 1), 0).Err(); err != nil {
		t.Fatalf("set head: %v", err)
	}
	f.appendAt("gone", retentionT0, 1)

	res := f.verify("gone", VerifyOptions{})
	if res.Status != VerifyStatusCompromised || res.FirstSeq != 5001 {
		t.Fatalf("result status %q first seq %d, want compromised from 5001", res.Status, res.FirstSeq)
	}
	if missing := gapsOfType(res, GapTypeMissing); len(missing) != maxGapRun || missing[0] != 1 {
		t.Fatalf("missing gaps = %d starting at %v, want %d starting at 1", len(missing), missing[:1], maxGapRun)
	}
}
