package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/tracescan"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// Retention proofs cited by docs/contract/deletion-proofs.md for the JetStream
// streams (DI-C21) and the bus idempotency markers (DI-C34). Exports: these
// writers reach none beyond the streams and Redis keys named here.

// TestRetentionProofDIC21 publishes a marker to one durable subject of each
// stream and requires JetStream MaxAge to remove it. 2m is the lowest MaxAge
// the streams accept: their Duplicates window is 2m, and nats-server refuses a
// MaxAge below it (server/stream.go, "duplicates window can not be larger then
// max age"). The subtests run in order because t.Setenv forbids t.Parallel.
func TestRetentionProofDIC21(t *testing.T) {
	const maxAge = 2 * time.Minute
	t.Run("new_streams", func(t *testing.T) {
		t.Setenv(envUseJetStream, "1")
		t.Setenv(envJSMaxAge, maxAge.String())
		ns := startTestNATSServer(t, true)
		proveStreamMaxAge(t, newRetentionProofBus(t, ns), maxAge)
	})
	t.Run("lowered_on_existing_streams", func(t *testing.T) {
		t.Setenv(envUseJetStream, "1")
		t.Setenv(envJSMaxAge, "")
		ns := startTestNATSServer(t, true)
		newRetentionProofBus(t, ns) // creates the streams at the 7d default
		t.Setenv(envJSMaxAge, maxAge.String())
		proveStreamMaxAge(t, newRetentionProofBus(t, ns), maxAge) // an operator restart
	})
}

// TestRetentionProofDIC34 covers the cordum:bus:inflight and
// cordum:bus:processed families of DI-C34 through both JetStream callbacks:
// Subscribe and SubscribeWithContext. The markers are the key names, built
// from each PubAck's stream and sequence.
func TestRetentionProofDIC34(t *testing.T) {
	t.Run("bus_markers", func(t *testing.T) {
		t.Setenv(envUseJetStream, "1")
		ns := startTestNATSServer(t, true)
		client, mr := newTestRedis(t)
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		b := newRetentionProofBus(t, ns).WithRedis(client)
		entered, release := make(chan struct{}, 2), make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		block := func() { entered <- struct{}{}; <-release }
		if err := b.Subscribe("job.dic34.plain", "", func(*pb.BusPacket) error { block(); return nil }); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := b.SubscribeWithContext("job.dic34.ctx", "", func(context.Context, *pb.BusPacket) error { block(); return nil }); err != nil {
			t.Fatalf("SubscribeWithContext: %v", err)
		}
		var inflight, processed []string
		for _, subject := range []string{"job.dic34.plain", "job.dic34.ctx"} {
			ack, err := b.js.Publish(subject, retentionProofPacket(t, subject))
			if err != nil {
				t.Fatalf("publish %s: %v", subject, err)
			}
			inflight = append(inflight, inflightKey(ack.Stream, ack.Sequence))
			processed = append(processed, processedKey(ack.Stream, ack.Sequence))
		}
		for range inflight {
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("handler not entered")
			}
		}
		requireBusKeys(t, client, inflight, inflightKeyPrefix)
		mr.FastForward(inflightKeyTTL - time.Second)
		requireBusKeys(t, client, inflight, inflightKeyPrefix)
		mr.FastForward(2 * time.Second)
		requireBusKeys(t, client, inflight, "")
		close(release)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && slices.ContainsFunc(processed, func(key string) bool { return !mr.Exists(key) }) {
			time.Sleep(20 * time.Millisecond)
		}
		requireBusKeys(t, client, processed, processedKeyPrefix)
		mr.FastForward(processedKeyTTL - time.Second)
		requireBusKeys(t, client, processed, processedKeyPrefix)
		mr.FastForward(2 * time.Second)
		requireBusKeys(t, client, append(processed, inflight...), "")
	})
}

func newRetentionProofBus(t *testing.T, ns *natsserver.Server) *NatsBus {
	t.Helper()
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	b := &NatsBus{nc: nc}
	b.initJetStreamFromEnv()
	if !b.jsEnabled {
		t.Fatal("initJetStreamFromEnv left JetStream disabled")
	}
	return b
}

func retentionProofPacket(t *testing.T, subject string) []byte {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + subject + "-" + hex.EncodeToString(b)
	data, err := proto.Marshal(validNATSTestPacket(&pb.BusPacket{
		TraceId: marker, SenderId: "sender-retention",
		Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: marker}},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// proveStreamMaxAge requires both streams at maxAge, publishes one marker to
// each, finds it, waits for MaxAge to empty the streams, then finds no trace.
func proveStreamMaxAge(t *testing.T, b *NatsBus, maxAge time.Duration) {
	t.Helper()
	streams := map[string]string{streamSys: capsdk.SubjectSubmit, streamJobs: "job.dic21.proof"}
	for stream := range streams {
		info, err := b.js.StreamInfo(stream)
		if err != nil {
			t.Fatalf("StreamInfo(%s): %v", stream, err)
		}
		if info.Config.MaxAge != maxAge {
			t.Fatalf("%s MaxAge = %s; want %s", stream, info.Config.MaxAge, maxAge)
		}
	}
	b1 := make([]byte, 6)
	if _, err := rand.Read(b1); err != nil {
		t.Fatalf("marker: %v", err)
	}
	marker := t.Name() + "-" + hex.EncodeToString(b1)
	for stream, subject := range streams {
		if err := b.Publish(subject, validNATSTestPacket(&pb.BusPacket{
			TraceId: marker + "-" + stream, SenderId: "sender-retention",
			Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: marker + "-" + stream}},
		})); err != nil {
			t.Fatalf("Publish(%s): %v", subject, err)
		}
		if seqs := streamMarkerSeqs(t, b.js, stream, marker); len(seqs) != 1 {
			t.Fatalf("%s holds the marker at %v; want one message", stream, seqs)
		}
	}
	deadline := time.Now().Add(maxAge + time.Minute)
	for stream := range streams {
		for {
			info, err := b.js.StreamInfo(stream)
			if err != nil {
				t.Fatalf("StreamInfo(%s): %v", stream, err)
			}
			if info.State.Msgs == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s still holds %d messages %s after publish", stream, info.State.Msgs, maxAge+time.Minute)
			}
			time.Sleep(time.Second)
		}
		if seqs := streamMarkerSeqs(t, b.js, stream, marker); len(seqs) != 0 {
			t.Fatalf("%s still holds the marker at %v", stream, seqs)
		}
	}
}

// streamMarkerSeqs reads every sequence the stream has issued and returns those
// whose data or headers contain the marker.
func streamMarkerSeqs(t *testing.T, js nats.JetStreamContext, stream, marker string) []uint64 {
	t.Helper()
	info, err := js.StreamInfo(stream)
	if err != nil {
		t.Fatalf("StreamInfo(%s): %v", stream, err)
	}
	var seqs []uint64
	for seq := uint64(1); seq <= info.State.LastSeq; seq++ {
		msg, err := js.GetMsg(stream, seq)
		if errors.Is(err, nats.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			t.Fatalf("GetMsg(%s, %d): %v", stream, seq, err)
		}
		if strings.Contains(string(msg.Data), marker) || strings.Contains(fmt.Sprint(msg.Header), marker) {
			seqs = append(seqs, seq)
		}
	}
	return seqs
}

// requireBusKeys requires a family hit for every key, or none when family is "".
func requireBusKeys(t *testing.T, client goredis.UniversalClient, keys []string, family string) {
	t.Helper()
	for _, key := range keys {
		hits, err := tracescan.Find(context.Background(), client, key)
		if err != nil {
			t.Fatalf("tracescan.Find: %v", err)
		}
		found := slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) })
		if family == "" && len(hits) != 0 {
			t.Fatalf("trace of %q remains after cleanup: %v", key, hits)
		}
		if family != "" && !found {
			t.Fatalf("no %s* trace of %q; hits=%v", family, key, hits)
		}
	}
}
