package bus

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestRawAdmissionPrecedesUnmarshalAndUsesActualSubject(t *testing.T) {
	ns := startTestNATSServer(t, false)
	b := newTestNatsBus(t, ns, false)
	raw := []byte("intentionally-not-protobuf")
	actualSubject := "worker.worker-7.jobs"
	accepted := &pb.BusPacket{TraceId: "admitted"}
	seen := make(chan struct{}, 1)

	b.SetRawPacketAdmission(func(_ context.Context, subject string, data []byte) RawAdmissionResult {
		if subject != actualSubject {
			t.Errorf("subject = %q, want %q", subject, actualSubject)
		}
		if !bytes.Equal(data, raw) {
			t.Errorf("raw bytes = %q, want %q", data, raw)
		}
		return RawAdmissionResult{Disposition: RawAdmissionAccepted, Packet: accepted}
	})
	if err := b.Subscribe("worker.*.jobs", "", func(packet *pb.BusPacket) error {
		if packet != accepted {
			t.Errorf("handler packet = %p, want admitted packet %p", packet, accepted)
		}
		seen <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := b.nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}
	if err := b.nc.Publish(actualSubject, raw); err != nil {
		t.Fatalf("publish raw: %v", err)
	}
	if err := b.nc.Flush(); err != nil {
		t.Fatalf("flush publish: %v", err)
	}

	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("admitted message did not reach handler")
	}
}

func TestRawAdmissionDispositionMapsToDeliveryAction(t *testing.T) {
	cases := []struct {
		name       string
		result     RawAdmissionResult
		wantAction msgAction
		wantDelay  time.Duration
	}{
		{name: "duplicate", result: RawAdmissionResult{Disposition: RawAdmissionDuplicate}, wantAction: msgActionAck},
		{name: "reject", result: RawAdmissionResult{Disposition: RawAdmissionRejected}, wantAction: msgActionTerm},
		{name: "replay store unavailable", result: RawAdmissionResult{Disposition: RawAdmissionRetry}, wantAction: msgActionNak},
		{name: "retry later", result: RawAdmissionResult{Disposition: RawAdmissionRetry, RetryAfter: 2 * time.Second}, wantAction: msgActionNakDelay, wantDelay: 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			b := &NatsBus{}
			b.SetRawPacketAdmission(func(context.Context, string, []byte) RawAdmissionResult {
				return tc.result
			})
			action, delay := b.processInboundMsgCtx(context.Background(), "job.actual", []byte("raw"), func(context.Context, *pb.BusPacket) error {
				called = true
				return nil
			}, 1)
			if action != tc.wantAction || delay != tc.wantDelay {
				t.Fatalf("action/delay = %v/%v, want %v/%v", action, delay, tc.wantAction, tc.wantDelay)
			}
			if called {
				t.Fatal("handler called for non-accepted admission result")
			}
		})
	}
}

func TestRawAdmissionAcceptedInvokesHandler(t *testing.T) {
	packet := &pb.BusPacket{TraceId: "accepted"}
	b := &NatsBus{}
	b.SetRawPacketAdmission(func(context.Context, string, []byte) RawAdmissionResult {
		return RawAdmissionResult{Disposition: RawAdmissionAccepted, Packet: packet}
	})
	called := false
	action, delay := b.processInboundMsgCtx(context.Background(), "job.actual", []byte("raw"), func(_ context.Context, got *pb.BusPacket) error {
		called = got == packet
		return nil
	}, 1)
	if action != msgActionAck || delay != 0 || !called {
		t.Fatalf("action/delay/called = %v/%v/%v, want ack/0/true", action, delay, called)
	}
}

func TestRawAdmissionAuthorityReachesHandlerUnchanged(t *testing.T) {
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	authority := &RawAdmissionAuthority{
		ActualSubject: "sys.job.result", SessionSubject: "worker-a",
		TenantID: "tenant-a", Identity: identity,
		MessageID: []byte("0123456789abcdef"), UnsignedDigest: []byte("digest-a"),
	}
	b := &NatsBus{}
	b.SetRawPacketAdmission(func(context.Context, string, []byte) RawAdmissionResult {
		return RawAdmissionResult{
			Disposition: RawAdmissionAccepted,
			Packet:      &pb.BusPacket{TraceId: "accepted"},
			Authority:   authority,
		}
	})

	action, _ := b.processInboundMsgCtx(
		context.Background(), authority.ActualSubject, []byte("raw"),
		func(ctx context.Context, _ *pb.BusPacket) error {
			got, ok := RawAdmissionAuthorityFromContext(ctx)
			if !ok {
				t.Fatal("handler context is missing verified raw authority")
			}
			if got.ActualSubject != authority.ActualSubject || got.SessionSubject != authority.SessionSubject ||
				got.TenantID != authority.TenantID || !proto.Equal(got.Identity, authority.Identity) ||
				!bytes.Equal(got.MessageID, authority.MessageID) || !bytes.Equal(got.UnsignedDigest, authority.UnsignedDigest) {
				t.Fatalf("handler authority = %#v, want %#v", got, authority)
			}
			got.Identity.TenantId = "mutated"
			got.MessageID[0] ^= 1
			got.UnsignedDigest[0] ^= 1
			return nil
		}, 1,
	)
	if action != msgActionAck {
		t.Fatalf("action = %v, want ack", action)
	}
	if authority.Identity.GetTenantId() != "tenant-a" || string(authority.MessageID) != "0123456789abcdef" || string(authority.UnsignedDigest) != "digest-a" {
		t.Fatalf("handler mutated admission-owned authority: %#v", authority)
	}
}

func TestRawAdmissionAuthorityIsAbsentInCompatibilityMode(t *testing.T) {
	packet := validNATSTestPacket(&pb.BusPacket{
		TraceId: "trace-compat", SenderId: "sender-compat",
		Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-compat"}},
	})
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal compatibility packet: %v", err)
	}
	b := &NatsBus{}
	action, _ := b.processInboundMsgCtx(context.Background(), "job.actual", raw, func(ctx context.Context, _ *pb.BusPacket) error {
		if _, ok := RawAdmissionAuthorityFromContext(ctx); ok {
			t.Fatal("compatibility handler received production authority")
		}
		return nil
	}, 1)
	if action != msgActionAck {
		t.Fatalf("action = %v, want ack", action)
	}
}

func TestRawAdmissionFailsClosedOnInvalidAcceptedResult(t *testing.T) {
	b := &NatsBus{}
	b.SetRawPacketAdmission(func(context.Context, string, []byte) RawAdmissionResult {
		return RawAdmissionResult{Disposition: RawAdmissionAccepted}
	})
	action, _ := b.processInboundMsgCtx(context.Background(), "job.actual", []byte("raw"), func(context.Context, *pb.BusPacket) error {
		t.Fatal("handler called with nil admitted packet")
		return nil
	}, 1)
	if action != msgActionTerm {
		t.Fatalf("action = %v, want term", action)
	}
}

func TestRawAdmissionNilHookPreservesLegacyDecode(t *testing.T) {
	packet := validNATSTestPacket(&pb.BusPacket{
		TraceId:  "trace-legacy",
		SenderId: "sender-legacy",
		Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
			JobId: "job-legacy",
		}},
	})
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal legacy packet: %v", err)
	}
	b := &NatsBus{}
	called := false
	action, delay := b.processInboundMsgCtx(context.Background(), "job.actual", raw, func(_ context.Context, got *pb.BusPacket) error {
		called = got.GetJobRequest().GetJobId() == "job-legacy"
		return nil
	}, 1)
	if action != msgActionAck || delay != 0 || !called {
		t.Fatalf("action/delay/called = %v/%v/%v, want ack/0/true", action, delay, called)
	}
}

func TestRawAdmissionConfigurationFreezesOnFirstSubscription(t *testing.T) {
	ns := startTestNATSServer(t, false)
	b := newTestNatsBus(t, ns, false)
	var calls atomic.Int32
	first := func(context.Context, string, []byte) RawAdmissionResult {
		calls.Add(1)
		return RawAdmissionResult{Disposition: RawAdmissionAccepted, Packet: &pb.BusPacket{TraceId: "frozen"}}
	}
	if err := b.SetRawPacketAdmission(first); err != nil {
		t.Fatalf("set admission: %v", err)
	}
	seen := make(chan struct{}, 1)
	if err := b.Subscribe("job.freeze", "", func(*pb.BusPacket) error {
		seen <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := b.SetRawPacketAdmission(nil); !errors.Is(err, ErrRawAdmissionFrozen) {
		t.Fatalf("disable after subscribe error = %v, want %v", err, ErrRawAdmissionFrozen)
	}
	if err := b.SetRawPacketAdmission(func(context.Context, string, []byte) RawAdmissionResult {
		return RawAdmissionResult{Disposition: RawAdmissionRejected}
	}); !errors.Is(err, ErrRawAdmissionFrozen) {
		t.Fatalf("replace after subscribe error = %v, want %v", err, ErrRawAdmissionFrozen)
	}
	if err := b.nc.Publish("job.freeze", []byte("not-protobuf")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := b.nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frozen admission delivery")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("frozen admission calls = %d, want 1", got)
	}
}

func TestRawAdmissionPrecedesRedisSideEffectsOnBothJetStreamPaths(t *testing.T) {
	paths := []struct {
		name      string
		subscribe func(*NatsBus, string, chan<- []string) error
	}{
		{name: "Subscribe", subscribe: subscribeRawPacket},
		{name: "SubscribeWithContext", subscribe: subscribeRawPacketWithContext},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			testRawAdmissionRedisOrder(t, path.subscribe)
		})
	}
}

func testRawAdmissionRedisOrder(
	t *testing.T,
	subscribe func(*NatsBus, string, chan<- []string) error,
) {
	t.Helper()
	ns := startTestNATSServer(t, true)
	b := newTestNatsBus(t, ns, true)
	client, redisServer := newTestRedis(t)
	t.Cleanup(redisServer.Close)
	t.Cleanup(func() { _ = client.Close() })
	b.WithRedis(client)
	stream := "RAW_" + strings.ToUpper(strings.ReplaceAll(t.Name(), "/", "_"))
	subject := "job.raw." + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "."))
	if _, err := b.js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	observed := make(chan []string, 2)
	var calls atomic.Int32
	if err := b.SetRawPacketAdmission(func(ctx context.Context, _ string, _ []byte) RawAdmissionResult {
		calls.Add(1)
		observed <- redisKeys(ctx, client)
		return RawAdmissionResult{Disposition: RawAdmissionAccepted, Packet: &pb.BusPacket{TraceId: "admitted"}}
	}); err != nil {
		t.Fatalf("set admission: %v", err)
	}
	if err := subscribe(b, subject, observed); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := b.js.Publish(subject, []byte("raw-signed-packet")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if keys := awaitKeys(t, observed, "admission"); len(keys) != 0 {
		t.Fatalf("redis keys before admission = %v, want none", keys)
	}
	if keys := awaitKeys(t, observed, "handler"); !hasOnlyInflightKey(keys) {
		t.Fatalf("redis keys in handler = %v, want one inflight key", keys)
	}
	awaitProcessedRedisState(t, client)
	if got := calls.Load(); got != 1 {
		t.Fatalf("admission calls = %d, want 1", got)
	}
}

func subscribeRawPacket(b *NatsBus, subject string, observed chan<- []string) error {
	return b.Subscribe(subject, "", func(_ *pb.BusPacket) error {
		observed <- redisKeys(context.Background(), b.redis)
		return nil
	})
}

func subscribeRawPacketWithContext(b *NatsBus, subject string, observed chan<- []string) error {
	return b.SubscribeWithContext(subject, "", func(ctx context.Context, _ *pb.BusPacket) error {
		observed <- redisKeys(ctx, b.redis)
		return nil
	})
}

func redisKeys(ctx context.Context, client goredis.UniversalClient) []string {
	keys, err := client.Keys(ctx, "cordum:bus:*").Result()
	if err != nil {
		return []string{"read-error:" + err.Error()}
	}
	return keys
}

func awaitKeys(t *testing.T, observed <-chan []string, label string) []string {
	t.Helper()
	select {
	case keys := <-observed:
		return keys
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s redis observation", label)
		return nil
	}
}

func hasOnlyInflightKey(keys []string) bool {
	return len(keys) == 1 && strings.HasPrefix(keys[0], inflightKeyPrefix)
}

func awaitProcessedRedisState(t *testing.T, client goredis.UniversalClient) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		keys := redisKeys(context.Background(), client)
		if len(keys) == 1 && strings.HasPrefix(keys[0], processedKeyPrefix) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("processed redis key did not replace inflight key")
}
