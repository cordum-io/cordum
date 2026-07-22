package bus

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
)

const rawSettlementQueue = "production-raw-settlement"

type rawSettlementHarness struct {
	bus     *NatsBus
	stream  string
	subject string
	durable string
}

type rawSettlementPath struct {
	name      string
	subscribe func(*NatsBus, string, string, func(*pb.BusPacket) error) error
}

func TestRawAdmissionDuplicateACKsWithoutHandler(t *testing.T) {
	h := newRawSettlementHarness(t)
	var hookCalls, handlerCalls, dlqCalls atomic.Int32
	installRawSettlementHook(t, h.bus, func(context.Context, string, []byte) RawAdmissionResult {
		hookCalls.Add(1)
		return RawAdmissionResult{Disposition: RawAdmissionDuplicate}
	}, &dlqCalls)
	subscribeRawSettlement(t, h, rawSettlementPaths()[0], &handlerCalls)
	sequence := publishRawSettlement(t, h)
	waitRawSettlement(t, h, sequence)
	if hookCalls.Load() != 1 || handlerCalls.Load() != 0 || dlqCalls.Load() != 0 {
		t.Fatalf("calls hook/handler/dlq = %d/%d/%d, want 1/0/0",
			hookCalls.Load(), handlerCalls.Load(), dlqCalls.Load())
	}
}

func TestRawAdmissionRejectTERMsWithoutDLQ(t *testing.T) {
	h := newRawSettlementHarness(t)
	var hookCalls, handlerCalls, dlqCalls atomic.Int32
	installRawSettlementHook(t, h.bus, func(context.Context, string, []byte) RawAdmissionResult {
		hookCalls.Add(1)
		return RawAdmissionResult{Disposition: RawAdmissionRejected}
	}, &dlqCalls)
	subscribeRawSettlement(t, h, rawSettlementPaths()[0], &handlerCalls)
	sequence := publishRawSettlement(t, h)
	waitRawSettlement(t, h, sequence)
	if hookCalls.Load() != 1 || handlerCalls.Load() != 0 || dlqCalls.Load() != 0 {
		t.Fatalf("calls hook/handler/dlq = %d/%d/%d, want 1/0/0",
			hookCalls.Load(), handlerCalls.Load(), dlqCalls.Load())
	}
}

func TestRawAdmissionRetryNAKsAndRedelivers(t *testing.T) {
	for _, retryAfter := range []time.Duration{0, 75 * time.Millisecond} {
		t.Run(retryAfter.String(), func(t *testing.T) {
			testRawAdmissionRetry(t, retryAfter)
		})
	}
}

func testRawAdmissionRetry(t *testing.T, retryAfter time.Duration) {
	t.Helper()
	h := newRawSettlementHarness(t)
	var hookCalls, handlerCalls, dlqCalls atomic.Int32
	times := make(chan time.Time, 2)
	installRawSettlementHook(t, h.bus, func(context.Context, string, []byte) RawAdmissionResult {
		times <- time.Now()
		if hookCalls.Add(1) == 1 {
			return RawAdmissionResult{Disposition: RawAdmissionRetry, RetryAfter: retryAfter}
		}
		return RawAdmissionResult{Disposition: RawAdmissionDuplicate}
	}, &dlqCalls)
	subscribeRawSettlement(t, h, rawSettlementPaths()[0], &handlerCalls)
	sequence := publishRawSettlement(t, h)
	first, second := awaitRawSettlementTime(t, times), awaitRawSettlementTime(t, times)
	if retryAfter > 0 && second.Sub(first) < retryAfter/2 {
		t.Fatalf("redelivery delay = %v, want at least %v", second.Sub(first), retryAfter/2)
	}
	waitRawSettlement(t, h, sequence)
	if handlerCalls.Load() != 0 || dlqCalls.Load() != 0 {
		t.Fatalf("calls handler/dlq = %d/%d, want 0/0", handlerCalls.Load(), dlqCalls.Load())
	}
}

func TestRawAdmissionPrecedesMaxDeliveryTermination(t *testing.T) {
	finals := []RawAdmissionDisposition{RawAdmissionAccepted, RawAdmissionRejected}
	for _, path := range rawSettlementPaths() {
		for _, final := range finals {
			name := path.name + "/" + rawDispositionName(final)
			t.Run(name, func(t *testing.T) {
				testRawAdmissionAtMaxDelivery(t, path, final)
			})
		}
	}
}

func testRawAdmissionAtMaxDelivery(t *testing.T, path rawSettlementPath, final RawAdmissionDisposition) {
	t.Helper()
	h := newRawSettlementHarness(t)
	var hookCalls, handlerCalls, dlqCalls, badOrder atomic.Int32
	installRawSettlementHook(t, h.bus, func(context.Context, string, []byte) RawAdmissionResult {
		if hookCalls.Add(1) < int32(maxJSRedeliveries) {
			return RawAdmissionResult{Disposition: RawAdmissionRetry}
		}
		return rawMaxDeliveryResult(final)
	}, nil)
	h.bus.OnMessageTerminated = func(string, []byte, uint64) error {
		if hookCalls.Load() != int32(maxJSRedeliveries) {
			badOrder.Add(1)
		}
		dlqCalls.Add(1)
		return nil
	}
	subscribeRawSettlement(t, h, path, &handlerCalls)
	sequence := publishRawSettlement(t, h)
	waitRawSettlement(t, h, sequence)
	wantDLQ := int32(0)
	if final == RawAdmissionAccepted {
		wantDLQ = 1
	}
	if hookCalls.Load() != int32(maxJSRedeliveries) || handlerCalls.Load() != 0 ||
		dlqCalls.Load() != wantDLQ || badOrder.Load() != 0 {
		t.Fatalf("calls hook/handler/dlq/bad-order = %d/%d/%d/%d, want %d/0/%d/0",
			hookCalls.Load(), handlerCalls.Load(), dlqCalls.Load(), badOrder.Load(), maxJSRedeliveries, wantDLQ)
	}
}

func newRawSettlementHarness(t *testing.T) rawSettlementHarness {
	t.Helper()
	ns := startTestNATSServer(t, true)
	b := newTestNatsBus(t, ns, true)
	b.ackWait = 150 * time.Millisecond
	name := strings.ToUpper(strings.ReplaceAll(t.Name(), "/", "_"))
	stream, subject := "RAW_SETTLE_"+name, "job.raw.settle."+strings.ToLower(strings.ReplaceAll(t.Name(), "/", "."))
	if _, err := b.js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	return rawSettlementHarness{bus: b, stream: stream, subject: subject, durable: durableName(subject, rawSettlementQueue)}
}

func rawSettlementPaths() []rawSettlementPath {
	return []rawSettlementPath{
		{name: "Subscribe", subscribe: func(b *NatsBus, subject, queue string, handler func(*pb.BusPacket) error) error {
			return b.Subscribe(subject, queue, handler)
		}},
		{name: "SubscribeWithContext", subscribe: func(b *NatsBus, subject, queue string, handler func(*pb.BusPacket) error) error {
			return b.SubscribeWithContext(subject, queue, func(_ context.Context, packet *pb.BusPacket) error { return handler(packet) })
		}},
	}
}

func installRawSettlementHook(
	t *testing.T,
	b *NatsBus,
	hook RawPacketAdmission,
	dlqCalls *atomic.Int32,
) {
	t.Helper()
	if err := b.SetRawPacketAdmission(hook); err != nil {
		t.Fatalf("set raw admission: %v", err)
	}
	if dlqCalls != nil {
		b.OnMessageTerminated = func(string, []byte, uint64) error { dlqCalls.Add(1); return nil }
	}
}

func subscribeRawSettlement(t *testing.T, h rawSettlementHarness, path rawSettlementPath, calls *atomic.Int32) {
	t.Helper()
	if err := path.subscribe(h.bus, h.subject, rawSettlementQueue, func(*pb.BusPacket) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

func publishRawSettlement(t *testing.T, h rawSettlementHarness) uint64 {
	t.Helper()
	ack, err := h.bus.js.Publish(h.subject, []byte("untrusted-raw-packet"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return ack.Sequence
}

func waitRawSettlement(t *testing.T, h rawSettlementHarness, sequence uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := h.bus.js.ConsumerInfo(h.stream, h.durable)
		if err == nil && info.AckFloor.Stream >= sequence && info.NumAckPending == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery %d was not terminally settled", sequence)
}

func awaitRawSettlementTime(t *testing.T, times <-chan time.Time) time.Time {
	t.Helper()
	select {
	case observed := <-times:
		return observed
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for raw admission delivery")
		return time.Time{}
	}
}

func rawMaxDeliveryResult(final RawAdmissionDisposition) RawAdmissionResult {
	if final == RawAdmissionAccepted {
		return RawAdmissionResult{Disposition: final, Packet: &pb.BusPacket{TraceId: "admitted"}}
	}
	return RawAdmissionResult{Disposition: final}
}

func rawDispositionName(disposition RawAdmissionDisposition) string {
	if disposition == RawAdmissionAccepted {
		return "accepted"
	}
	return "rejected"
}
