package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

const adoptActor = "admin@example.com"

// handTrimmed chains seq 1-6 and drops 1-3 the way the old docs advised
// (XTRIM MAXLEN), leaving no anchor.
func handTrimmed(t *testing.T, f *retentionFixture) []*SIEMEvent {
	t.Helper()
	events := f.appendAt("acme", retentionT0, 6)
	if err := f.client.XTrimMaxLen(context.Background(), f.chainer.StreamKey("acme"), 3).Err(); err != nil {
		t.Fatalf("xtrim: %v", err)
	}
	return events
}

func adopt(f *retentionFixture, expectedOldest int64, replaceInvalid bool) (*AdoptAnchorResult, error) {
	return f.chainer.AdoptRetentionAnchor(context.Background(), "acme", AdoptAnchorRequest{
		ExpectedOldestSeq: expectedOldest,
		ReplaceInvalid:    replaceInvalid,
		Actor:             adoptActor,
	})
}

func (f *retentionFixture) rawAnchor(tenant string) string {
	f.t.Helper()
	raw, err := f.client.Get(context.Background(), f.chainer.AnchorKey(tenant)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		f.t.Fatalf("get anchor: %v", err)
	}
	return raw
}

// lastEvent is the newest event in the tenant's chain.
func (f *retentionFixture) lastEvent(tenant string) SIEMEvent {
	f.t.Helper()
	entries, err := f.client.XRevRangeN(context.Background(), f.chainer.StreamKey(tenant), "+", "-", 1).Result()
	if err != nil || len(entries) == 0 {
		f.t.Fatalf("read chain tail: %v (%d entries)", err, len(entries))
	}
	var ev SIEMEvent
	if err := json.Unmarshal([]byte(entries[0].Values[chainStreamFieldEvent].(string)), &ev); err != nil {
		f.t.Fatalf("decode tail event: %v", err)
	}
	return ev
}

func TestAdoptRetentionAnchor_FixesHandTrimmedChain(t *testing.T) {
	for name, newFixture := range map[string]func(*testing.T) *retentionFixture{
		"single node": func(t *testing.T) *retentionFixture {
			return newRetentionFixture(t, nil, WithHMACKey(testHMACKey()))
		},
		"cluster": func(t *testing.T) *retentionFixture {
			return newClusterRetentionFixture(t, WithHMACKey(testHMACKey())).retentionFixture
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			events := handTrimmed(t, f)
			if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusCompromised {
				t.Fatalf("hand-trimmed chain = %+v, want compromised", res)
			}

			oldest, err := f.client.XRangeN(context.Background(), f.chainer.StreamKey("acme"), "-", "+", 1).Result()
			if err != nil || len(oldest) != 1 {
				t.Fatalf("read oldest entry: %v", err)
			}
			res, err := adopt(f, 4, false)
			if err != nil {
				t.Fatalf("adopt: %v", err)
			}
			if res.AdoptedSeq != 3 || res.AdoptedHash != events[2].EventHash || res.AdoptedStreamID != xRevBefore(oldest[0].ID) ||
				res.PreviousAnchor != "" || res.PreviousAnchorState != "none" || res.EventSeq != 7 {
				t.Fatalf("result = %+v, want seq 3 just below entry %s over no anchor, recorded as seq 7", res, oldest[0].ID)
			}
			a := f.anchorValue("acme")
			if a.Seq != 3 || a.Hash != events[2].EventHash || a.StreamID != res.AdoptedStreamID ||
				!a.validMAC(f.chainer.AnchorKey("acme"), testHMACKey()) {
				t.Fatalf("anchor = %+v, want a signed anchor at seq 3", a)
			}
			ver := f.verify("acme", VerifyOptions{})
			if ver.Status != VerifyStatusPartial || ver.FirstSeq != 4 || ver.LastSeq != 7 || ver.VerifiedEvents != 4 ||
				!slices.Equal(gapsOfType(ver, GapTypeRetentionTrimmed), []int64{1, 2, 3}) || len(ver.Gaps) != 3 {
				t.Fatalf("verify after adopting = %+v, want partial with seqs 1-3 trimmed", ver)
			}
			// The adopted stream ID sits just below seq 4, so a window
			// from the trimmed events' millisecond reports the trim.
			window := f.verify("acme", VerifyOptions{SinceMs: retentionT0.UnixMilli()})
			if window.Status != VerifyStatusPartial || !slices.Equal(gapsOfType(window, GapTypeRetentionTrimmed), []int64{3}) {
				t.Fatalf("window = %+v, want partial with seq 3 trimmed", window)
			}

			// The sweeper carries on from the adopted anchor.
			if n := f.sweepAt(retentionSweepTime); n != 3 {
				t.Fatalf("sweep after adopting trimmed %d, want 3", n)
			}
			if res := f.verify("acme", VerifyOptions{}); res.Status != VerifyStatusPartial || res.FirstSeq != 7 {
				t.Fatalf("verify after the sweep = %+v, want partial from seq 7", res)
			}
		})
	}
}

func TestAdoptRetentionAnchor_RecordsTheAdoptionInTheChain(t *testing.T) {
	f := newRetentionFixture(t, nil)
	handTrimmed(t, f)
	res, err := adopt(f, 4, false)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	ev := f.lastEvent("acme")
	if ev.Seq != res.EventSeq || ev.EventType != EventRetentionAnchorAdopted || ev.Identity != adoptActor ||
		ev.Extra["adopted_seq"] != "3" || ev.Extra["adopted_stream_id"] != res.AdoptedStreamID ||
		ev.Extra["previous_anchor"] != "" || ev.Extra["previous_anchor_state"] != "none" {
		t.Fatalf("chain tail = %+v, want the adoption event for %+v", ev, res)
	}
}

func TestAdoptRetentionAnchor_ReplacesAnInvalidAnchorOnRequest(t *testing.T) {
	cases := map[string]struct {
		setup    func(*testing.T) *retentionFixture
		expected int64
		state    string
	}{
		"unlinked": {
			setup: func(t *testing.T) *retentionFixture {
				f, _ := trimmedChain(t, WithHMACKey(testHMACKey()))
				xdelSeqs(t, f.client, f.chainer.StreamKey("acme"), 4) // by hand, after the sweeper
				return f
			},
			expected: 5, state: "unlinked",
		},
		"wrong hash": {
			setup: func(t *testing.T) *retentionFixture {
				f, events := trimmedChain(t)
				a := f.anchorValue("acme")
				a.Hash = events[1].EventHash
				f.setAnchor("acme", a)
				return f
			},
			expected: 4, state: "unlinked",
		},
		"legacy anchor with HMAC": {
			setup: func(t *testing.T) *retentionFixture {
				f, events := trimmedChain(t, WithHMACKey(testHMACKey()))
				f.setLegacyAnchor("acme", 3, events[2].EventHash)
				return f
			},
			expected: 4, state: "hmac_mismatch",
		},
		"malformed": {
			setup: func(t *testing.T) *retentionFixture {
				f, _ := trimmedChain(t)
				if err := f.client.Set(context.Background(), f.chainer.AnchorKey("acme"), "garbage", 0).Err(); err != nil {
					t.Fatalf("set anchor: %v", err)
				}
				return f
			},
			expected: 4, state: "malformed",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := tc.setup(t)
			previous := f.rawAnchor("acme")
			if _, err := adopt(f, tc.expected, false); !errors.Is(err, ErrAdoptRefused) || !strings.Contains(err.Error(), tc.state) {
				t.Fatalf("adopt without replace = %v, want refused as %s", err, tc.state)
			}
			res, err := adopt(f, tc.expected, true)
			if err != nil {
				t.Fatalf("adopt: %v", err)
			}
			if res.AdoptedSeq != tc.expected-1 || res.PreviousAnchor != previous || res.PreviousAnchorState != tc.state {
				t.Fatalf("result = %+v, want seq %d replacing %q (%s)", res, tc.expected-1, previous, tc.state)
			}
			if ev := f.lastEvent("acme"); ev.Extra["previous_anchor"] != previous || ev.Extra["previous_anchor_state"] != tc.state {
				t.Fatalf("adoption event extra = %v, want the replaced anchor", ev.Extra)
			}
			if ver := f.verify("acme", VerifyOptions{}); ver.Status != VerifyStatusPartial || ver.FirstSeq != tc.expected {
				t.Fatalf("verify after adopting = %+v, want partial from seq %d", ver, tc.expected)
			}
		})
	}
}

// A refused adoption changes nothing: no anchor write, no chain event.
func TestAdoptRetentionAnchor_Refusals(t *testing.T) {
	handTrimmedFixture := func(t *testing.T) *retentionFixture {
		f := newRetentionFixture(t, nil)
		handTrimmed(t, f)
		return f
	}
	cases := map[string]struct {
		setup    func(*testing.T) *retentionFixture
		expected int64
		replace  bool
		want     string
	}{
		"wrong expected oldest seq": {
			setup:    handTrimmedFixture,
			expected: 5, want: "oldest retained event is seq 4, not 5",
		},
		"untrimmed chain": {
			setup: func(t *testing.T) *retentionFixture {
				f := newRetentionFixture(t, nil)
				f.appendAt("acme", retentionT0, 3)
				return f
			},
			expected: 2, want: "seq 1, not 2",
		},
		"seq 1": {
			setup:    handTrimmedFixture,
			expected: 1, want: "at least 2",
		},
		"empty chain": {
			setup:    func(t *testing.T) *retentionFixture { return newRetentionFixture(t, nil) },
			expected: 4, want: "empty",
		},
		"valid anchor, even when asked to replace an invalid one": {
			setup:    func(t *testing.T) *retentionFixture { f, _ := trimmedChain(t, WithHMACKey(testHMACKey())); return f },
			expected: 4, replace: true, want: "already links",
		},
		"pending cluster trim": {
			setup: func(t *testing.T) *retentionFixture {
				f := newClusterRetentionFixture(t, WithHMACKey(testHMACKey()))
				f.oldAndRecentChain("acme")
				f.sweepAt(retentionSweepTime)
				f.setAnchor("acme", f.pendingAnchor("acme", 5, f.anchorValue("acme")))
				return f.retentionFixture
			},
			expected: 4, replace: true, want: "in progress",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := tc.setup(t)
			anchor, length := f.rawAnchor("acme"), f.streamLen("acme")
			_, err := adopt(f, tc.expected, tc.replace)
			if !errors.Is(err, ErrAdoptRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("adopt = %v, want refused with %q", err, tc.want)
			}
			if got := f.rawAnchor("acme"); got != anchor {
				t.Fatalf("anchor = %q, want %q unchanged", got, anchor)
			}
			if got := f.streamLen("acme"); got != length {
				t.Fatalf("stream len = %d, want %d: a refusal appends no event", got, length)
			}
		})
	}
}

// An anchor that moves between the checks and the write (a sweep) is not
// overwritten. The adoption event is already in the chain by then.
func TestAdoptRetentionAnchor_AnchorMovedMeanwhile(t *testing.T) {
	f := newRetentionFixture(t, nil)
	handTrimmed(t, f)
	other := testredis.NewClient(t, f.mr.Addr())
	moved := fmt.Sprintf("3:%064x:1-0", 5)
	f.client.AddHook(&afterScriptHook{script: chainAppendScript, fn: func() {
		if err := other.Set(context.Background(), f.chainer.AnchorKey("acme"), moved, 0).Err(); err != nil {
			t.Errorf("move anchor: %v", err)
		}
	}})

	if _, err := adopt(f, 4, false); !errors.Is(err, ErrAdoptRefused) || !strings.Contains(err.Error(), "changed meanwhile") {
		t.Fatalf("adopt = %v, want refused because the anchor moved", err)
	}
	if got := f.rawAnchor("acme"); got != moved {
		t.Fatalf("anchor = %q, want the concurrent write %q kept", got, moved)
	}
	if ev := f.lastEvent("acme"); ev.EventType != EventRetentionAnchorAdopted {
		t.Fatalf("chain tail = %+v, want the adoption event", ev)
	}
}
