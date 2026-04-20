package audit

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

// TestChainer_Soak100x100 runs 100 goroutines, each appending 100
// events to the same tenant, and asserts:
//
//	(a) all 10000 events landed in the stream,
//	(b) seqs are exactly 1..10000 with no gaps or duplicates,
//	(c) every prev_hash points at its predecessor's event_hash,
//	(d) every stored event_hash recomputes to itself.
//
// The CAS-based Lua append is the only synchronization primitive in
// play — if the hot-path loop lost a retry or the stream ordering
// drifted, this test would surface it. 10k total events keeps runtime
// bounded while still exercising contention.
func TestChainer_Soak100x100(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak in -short mode")
	}
	c, _, client := newTestChainer(t)
	ctx := context.Background()

	const (
		producers     = 100
		perProducer   = 100
		totalExpected = producers * perProducer
	)

	var (
		wg       sync.WaitGroup
		errCount atomic.Int64
	)
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(producerID int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				ev := &SIEMEvent{
					EventType: EventSafetyDecision,
					Severity:  SeverityInfo,
					TenantID:  "soak-tenant",
					Action:    "soak",
				}
				if err := c.Append(ctx, ev); err != nil {
					t.Errorf("producer[%d] i=%d: %v", producerID, i, err)
					errCount.Add(1)
					return
				}
			}
		}(p)
	}
	wg.Wait()
	if errCount.Load() != 0 {
		t.Fatalf("%d append errors", errCount.Load())
	}

	entries, err := client.XRange(ctx, c.StreamKey("soak-tenant"), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(entries) != totalExpected {
		t.Fatalf("(a) stream length = %d, want %d", len(entries), totalExpected)
	}

	var (
		prevHash string
		seenSeq  = make(map[int64]bool, totalExpected)
	)
	for i, e := range entries {
		payload, ok := e.Values[chainStreamFieldEvent].(string)
		if !ok {
			t.Fatalf("entry[%d] missing event field", i)
		}
		var ev SIEMEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("unmarshal[%d]: %v", i, err)
		}

		// (b) monotonic with no gaps or dupes.
		wantSeq := int64(i + 1)
		if ev.Seq != wantSeq {
			t.Fatalf("(b) Seq[%d] = %d, want %d", i, ev.Seq, wantSeq)
		}
		if seenSeq[ev.Seq] {
			t.Fatalf("(b) duplicate Seq=%d at index %d", ev.Seq, i)
		}
		seenSeq[ev.Seq] = true

		// (c) prev_hash linkage.
		if ev.PrevHash != prevHash {
			t.Fatalf("(c) Seq=%d PrevHash=%q, want %q", ev.Seq, ev.PrevHash, prevHash)
		}

		// (d) stored event_hash recomputes.
		ok, err := VerifyEventHash(&ev)
		if err != nil {
			t.Fatalf("(d) Seq=%d verify: %v", ev.Seq, err)
		}
		if !ok {
			t.Fatalf("(d) Seq=%d event_hash did not recompute", ev.Seq)
		}

		prevHash = ev.EventHash
	}
	if len(seenSeq) != totalExpected {
		t.Fatalf("(b) distinct seqs = %d, want %d", len(seenSeq), totalExpected)
	}
}
