package bus

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type rawOutcomeRecorder struct {
	mu       sync.Mutex
	outcomes []RawAdmissionOutcome
}

func (r *rawOutcomeRecorder) settle(outcome RawAdmissionOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, outcome)
}

func (r *rawOutcomeRecorder) get() []RawAdmissionOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.outcomes)
}

// rawClaimHook asks for a retry before delivery acceptOn, accepts that
// delivery with a recording Settle, and reports every later one as a duplicate.
func rawClaimHook(rec *rawOutcomeRecorder, acceptOn int32) RawPacketAdmission {
	var calls atomic.Int32
	return func(context.Context, string, []byte) RawAdmissionResult {
		switch n := calls.Add(1); {
		case n < acceptOn:
			return RawAdmissionResult{Disposition: RawAdmissionRetry}
		case n == acceptOn:
			return RawAdmissionResult{Disposition: RawAdmissionAccepted, Packet: &pb.BusPacket{TraceId: "claimed"}, Settle: rec.settle}
		default:
			return RawAdmissionResult{Disposition: RawAdmissionDuplicate}
		}
	}
}

func assertRawOutcome(t *testing.T, rec *rawOutcomeRecorder, calls, wantCalls int32, want RawAdmissionOutcome) {
	t.Helper()
	if got := rec.get(); calls != wantCalls || !slices.Equal(got, []RawAdmissionOutcome{want}) {
		t.Fatalf("handler calls/outcomes = %d/%v, want %d/[%v]", calls, got, wantCalls, want)
	}
}

func TestRawAdmissionSettlesHandlerOutcome(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want RawAdmissionOutcome
	}{
		{name: "succeeded", want: RawAdmissionSucceeded},
		{name: "retry", err: RetryAfter(errors.New("interrupted"), 10*time.Millisecond), want: RawAdmissionReleased},
		{name: "failed", err: errors.New("rejected"), want: RawAdmissionFailed},
	}
	for _, path := range rawSettlementPaths() {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				h := newRawSettlementHarness(t)
				rec := &rawOutcomeRecorder{}
				installRawSettlementHook(t, h.bus, rawClaimHook(rec, 1), nil)
				var calls atomic.Int32
				if err := path.subscribe(h.bus, h.subject, rawSettlementQueue, func(*pb.BusPacket) error {
					calls.Add(1)
					return tc.err
				}); err != nil {
					t.Fatalf("subscribe: %v", err)
				}
				waitRawSettlement(t, h, publishRawSettlement(t, h))
				assertRawOutcome(t, rec, calls.Load(), 1, tc.want)
			})
		}
	}
}

func TestRawAdmissionSettlesFailedAtMaxDelivery(t *testing.T) {
	for _, path := range rawSettlementPaths() {
		t.Run(path.name, func(t *testing.T) {
			h := newRawSettlementHarness(t)
			rec := &rawOutcomeRecorder{}
			installRawSettlementHook(t, h.bus, rawClaimHook(rec, int32(maxJSRedeliveries)), nil)
			var calls atomic.Int32
			subscribeRawSettlement(t, h, path, &calls)
			waitRawSettlement(t, h, publishRawSettlement(t, h))
			assertRawOutcome(t, rec, calls.Load(), 0, RawAdmissionFailed)
		})
	}
}

// An accepted result without a packet is ended without running the handler,
// and its delivery settles as failed.
func TestRawAdmissionSettlesFailedWithoutPacket(t *testing.T) {
	rec := &rawOutcomeRecorder{}
	hook := func(context.Context, string, []byte) RawAdmissionResult {
		return RawAdmissionResult{Disposition: RawAdmissionAccepted, Settle: rec.settle}
	}
	var calls atomic.Int32
	processInboundWithAdmission(context.Background(), hook, "job.actual", []byte("raw"), func(context.Context, *pb.BusPacket) error {
		calls.Add(1)
		return nil
	}, 1)
	assertRawOutcome(t, rec, calls.Load(), 0, RawAdmissionFailed)
}

// The processed-key guard settles the accepted delivery it skips: a key that
// is present means the message was processed, and a failed check retries it.
func TestRawAdmissionSettlesAtProcessedKeyGuard(t *testing.T) {
	cases := []struct {
		name      string
		processed bool
		want      RawAdmissionOutcome
	}{
		{name: "processed", processed: true, want: RawAdmissionSucceeded},
		{name: "check_fails", want: RawAdmissionReleased},
	}
	for _, path := range rawSettlementPaths() {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				h := newRawSettlementHarness(t)
				client, srv := newTestRedis(t)
				t.Cleanup(srv.Close)
				t.Cleanup(func() { _ = client.Close() })
				h.bus.WithRedis(client)
				if tc.processed {
					if err := client.Set(context.Background(), processedKey(h.stream, 1), "1", time.Minute).Err(); err != nil {
						t.Fatalf("mark processed: %v", err)
					}
				} else {
					srv.SetError("ERR unavailable")
				}
				rec := &rawOutcomeRecorder{}
				installRawSettlementHook(t, h.bus, rawClaimHook(rec, 1), nil)
				var calls atomic.Int32
				subscribeRawSettlement(t, h, path, &calls)
				waitRawSettlement(t, h, publishRawSettlement(t, h))
				assertRawOutcome(t, rec, calls.Load(), 0, tc.want)
			})
		}
	}
}
