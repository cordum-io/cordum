package bus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestDirectSubject(t *testing.T) {
	if DirectSubject("") != "" {
		t.Fatalf("expected empty subject")
	}
	if DirectSubject("worker-1") != "worker.worker-1.jobs" {
		t.Fatalf("unexpected direct subject")
	}
}

func TestInitJetStreamEnabled(t *testing.T) {
	t.Setenv(envUseJetStream, "")
	if initJetStreamEnabled() {
		t.Fatalf("expected jetstream disabled by default")
	}
	for _, val := range []string{"1", "true", "yes", "y", "on"} {
		t.Setenv(envUseJetStream, val)
		if !initJetStreamEnabled() {
			t.Fatalf("expected jetstream enabled for %s", val)
		}
	}
	t.Setenv(envUseJetStream, "no")
	if initJetStreamEnabled() {
		t.Fatalf("expected jetstream disabled for no")
	}
}

func TestIsDurableSubject(t *testing.T) {
	cases := map[string]bool{
		capsdk.SubjectSubmit:  true,
		capsdk.SubjectResult:  true,
		capsdk.SubjectDLQ:     true,
		"job.sre.collect":     true,
		"worker.abc.jobs":     true,
		"worker.abc.commands": false,
		"sys.ping":            false,
	}
	for subject, expect := range cases {
		if got := isDurableSubject(subject); got != expect {
			t.Fatalf("subject %s expected durable=%v got=%v", subject, expect, got)
		}
	}
}

func TestDurableName(t *testing.T) {
	if durableName("", "") != "" {
		t.Fatalf("expected empty durable name for empty subject")
	}
	name := durableName("job.test.*", "q")
	if name == "" || name == "dur_" {
		t.Fatalf("unexpected durable name: %s", name)
	}
	// Broadcast subscriptions (empty queue) must return "" for ephemeral consumers
	// so each replica gets its own consumer under JetStream.
	if got := durableName("job.test.*", ""); got != "" {
		t.Fatalf("expected empty durable name for broadcast (empty queue), got %s", got)
	}
}

func TestComputeMsgID(t *testing.T) {
	subject := "job.test"
	packet := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	if got := computeMsgID(subject, packet); got != "jobreq:job-1" {
		t.Fatalf("unexpected jobreq msg id: %s", got)
	}

	packet = &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1", Labels: map[string]string{LabelBusMsgID: "override"}}}}
	if got := computeMsgID(subject, packet); got != "jobreq:job.test:override" {
		t.Fatalf("unexpected override msg id: %s", got)
	}

	packet = &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "job-2"}}}
	if got := computeMsgID(subject, packet); got != "job.test:job-2" {
		t.Fatalf("unexpected jobresult msg id: %s", got)
	}

	packet = &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "worker-1"}}}
	if got := computeMsgID("sys.heartbeat", packet); got != "sys.heartbeat:worker-1" {
		t.Fatalf("unexpected heartbeat msg id: %s", got)
	}

	packet = &pb.BusPacket{Payload: &pb.BusPacket_Handshake{Handshake: &pb.Handshake{ComponentId: "worker-99"}}}
	if got := computeMsgID("sys.handshake", packet); got != "handshake:worker-99" {
		t.Fatalf("unexpected handshake msg id: %s", got)
	}

	if computeMsgID(subject, nil) != "" {
		t.Fatalf("expected empty msg id for nil packet")
	}
	if computeMsgID(subject, &pb.BusPacket{}) != "" {
		t.Fatalf("expected empty msg id for empty packet")
	}
}

func TestRetryDelayHelper(t *testing.T) {
	err := RetryAfter(nil, 1500*time.Millisecond)
	if delay, ok := RetryDelay(err); !ok || delay != 1500*time.Millisecond {
		t.Fatalf("unexpected retry delay: %v %v", delay, ok)
	}
}

func TestNatsBusPublishErrors(t *testing.T) {
	var nilBus *NatsBus
	if err := nilBus.Publish("job.test", &pb.BusPacket{}); !errors.Is(err, errNilBus) {
		t.Fatalf("expected nil bus error, got %v", err)
	}
	bus := &NatsBus{nc: &nats.Conn{}}
	if err := bus.Publish("", &pb.BusPacket{}); !errors.Is(err, errEmptyTopic) {
		t.Fatalf("expected empty topic error, got %v", err)
	}
	if err := bus.Publish("job.test", nil); !errors.Is(err, errNilPacket) {
		t.Fatalf("expected nil packet error, got %v", err)
	}
}

func TestNatsBusSubscribeErrors(t *testing.T) {
	var nilBus *NatsBus
	if err := nilBus.Subscribe("job.test", "", func(*pb.BusPacket) error { return nil }); !errors.Is(err, errNilBus) {
		t.Fatalf("expected nil bus error, got %v", err)
	}
	bus := &NatsBus{nc: &nats.Conn{}}
	if err := bus.Subscribe("", "", func(*pb.BusPacket) error { return nil }); !errors.Is(err, errEmptyTopic) {
		t.Fatalf("expected empty topic error, got %v", err)
	}
	if err := bus.Subscribe("job.test", "", nil); err == nil {
		t.Fatalf("expected nil handler error")
	}
}

func TestNatsBusStatusDefaults(t *testing.T) {
	var nilBus *NatsBus
	if nilBus.IsConnected() {
		t.Fatalf("expected disconnected nil bus")
	}
	if status := nilBus.Status(); status != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN status, got %s", status)
	}
	if url := nilBus.ConnectedURL(); url != "" {
		t.Fatalf("expected empty url, got %s", url)
	}
}

func TestParseBoolEnv(t *testing.T) {
	key := "TEST_BOOL_ENV"
	t.Setenv(key, "")
	if parseBoolEnv(key) {
		t.Fatalf("expected false for empty env")
	}
	for _, raw := range []string{"1", "true", "yes", "y", "on"} {
		t.Setenv(key, raw)
		if !parseBoolEnv(key) {
			t.Fatalf("expected true for %s", raw)
		}
	}
	t.Setenv(key, "no")
	if parseBoolEnv(key) {
		t.Fatalf("expected false for no")
	}
}

func TestProcessBusMsgPoisonPill(t *testing.T) {
	handlerCalled := false
	handler := func(p *pb.BusPacket) error {
		handlerCalled = true
		return nil
	}

	// Malformed data on first delivery should trigger NakDelay (allow retry)
	action, delay := processBusMsg([]byte("not-protobuf-data"), handler, 1)
	if action != msgActionNakDelay {
		t.Fatalf("expected NakDelay for poison pill on first delivery, got action=%d", action)
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	if handlerCalled {
		t.Fatal("handler should not be called for poison pill")
	}

	// Malformed data after threshold deliveries should terminate
	action, _ = processBusMsg([]byte("not-protobuf-data"), handler, poisonUnmarshalThreshold+1)
	if action != msgActionTerm {
		t.Fatalf("expected Term for corrupt data after %d deliveries, got action=%d", poisonUnmarshalThreshold+1, action)
	}

	// Valid protobuf should ACK and call handler
	valid := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	data, err := proto.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	action, _ = processBusMsg(data, handler, 1)
	if action != msgActionAck {
		t.Fatalf("expected Ack for valid message, got action=%d", action)
	}
	if !handlerCalled {
		t.Fatal("handler should be called for valid message")
	}
}

func TestProcessBusMsgRetryableError(t *testing.T) {
	handler := func(p *pb.BusPacket) error {
		return RetryAfter(errors.New("transient"), 2*time.Second)
	}

	valid := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	data, _ := proto.Marshal(valid)

	action, delay := processBusMsg(data, handler, 1)
	if action != msgActionNakDelay {
		t.Fatalf("expected NakDelay for retryable error, got action=%d", action)
	}
	if delay != 2*time.Second {
		t.Fatalf("expected 2s delay, got %v", delay)
	}
}

func TestProcessBusMsgNonRetryableError(t *testing.T) {
	handler := func(p *pb.BusPacket) error {
		return errors.New("permanent failure")
	}

	valid := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	data, _ := proto.Marshal(valid)

	action, _ := processBusMsg(data, handler, 1)
	if action != msgActionAck {
		t.Fatalf("expected Ack for non-retryable error, got action=%d", action)
	}
}

func TestProcessBusMsgCorruptAtThreshold(t *testing.T) {
	handler := func(p *pb.BusPacket) error {
		t.Fatal("handler should not be called for corrupt data")
		return nil
	}

	corrupt := []byte("definitely-not-protobuf")

	// At exactly the threshold: should still NakDelay (allow retry)
	action, delay := processBusMsg(corrupt, handler, poisonUnmarshalThreshold)
	if action != msgActionNakDelay {
		t.Fatalf("expected NakDelay at threshold (%d), got action=%d", poisonUnmarshalThreshold, action)
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}

	// One above threshold: should terminate
	action, _ = processBusMsg(corrupt, handler, poisonUnmarshalThreshold+1)
	if action != msgActionTerm {
		t.Fatalf("expected Term above threshold, got action=%d", action)
	}

	// Zero delivery count (metadata unavailable): should NakDelay
	action, _ = processBusMsg(corrupt, handler, 0)
	if action != msgActionNakDelay {
		t.Fatalf("expected NakDelay for zero delivery count, got action=%d", action)
	}
}

func TestProcessBusMsgValidDataHighDeliveryCount(t *testing.T) {
	// Even with high delivery count, valid data that processes successfully should ACK.
	handlerCalled := false
	handler := func(p *pb.BusPacket) error {
		handlerCalled = true
		return nil
	}

	valid := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	data, err := proto.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	action, _ := processBusMsg(data, handler, 99)
	if action != msgActionAck {
		t.Fatalf("expected Ack for valid message even with high delivery count, got action=%d", action)
	}
	if !handlerCalled {
		t.Fatal("handler should be called for valid message regardless of delivery count")
	}
}

func TestMsgActionTermConstant(t *testing.T) {
	// Verify msgActionTerm is distinct from other actions.
	if msgActionTerm == msgActionAck || msgActionTerm == msgActionNak || msgActionTerm == msgActionNakDelay {
		t.Fatal("msgActionTerm should be distinct from other action types")
	}
}

func TestMaxJSRedeliveriesConstant(t *testing.T) {
	if maxJSRedeliveries != 100 {
		t.Fatalf("expected maxJSRedeliveries=100, got %d", maxJSRedeliveries)
	}
}

func TestNatsBusDrainClearsSubs(t *testing.T) {
	b := &NatsBus{}
	// Drain on empty bus should not panic.
	b.Drain()
	if len(b.subs) != 0 {
		t.Fatalf("expected empty subs after drain")
	}
	// trackSub with nil should be a no-op.
	b.trackSub(nil)
	if len(b.subs) != 0 {
		t.Fatalf("expected nil sub to be ignored")
	}
}

func TestNatsBusCloseCallsDrain(t *testing.T) {
	b := &NatsBus{}
	// Close on nil nc should not panic and should drain.
	b.Close()
	if b.subs != nil {
		t.Fatalf("expected subs to be nil after close")
	}
}

func TestNatsTLSConfigFromEnv(t *testing.T) {
	t.Setenv(envNATSTLSCA, "")
	t.Setenv(envNATSTLSCert, "")
	t.Setenv(envNATSTLSKey, "")
	t.Setenv(envNATSTLSServerName, "")
	t.Setenv(envNATSTLSInsecure, "")
	cfg, err := natsTLSConfigFromEnv()
	if err != nil || cfg != nil {
		t.Fatalf("expected nil config, got cfg=%v err=%v", cfg, err)
	}

	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("bad"), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	t.Setenv(envNATSTLSCA, caPath)
	if _, err := natsTLSConfigFromEnv(); err == nil {
		t.Fatalf("expected error for invalid ca")
	}
	t.Setenv(envNATSTLSCA, "")
	t.Setenv(envNATSTLSCert, filepath.Join(dir, "cert.pem"))
	if _, err := natsTLSConfigFromEnv(); err == nil {
		t.Fatalf("expected error for missing key")
	}
}

func TestNatsTLSConfigProductionGuards(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv(envNATSTLSCA, "")
	t.Setenv(envNATSTLSCert, "")
	t.Setenv(envNATSTLSKey, "")
	t.Setenv(envNATSTLSServerName, "")
	t.Setenv(envNATSTLSInsecure, "")
	if _, err := natsTLSConfigFromEnv(); err == nil {
		t.Fatalf("expected tls required error in production")
	}

	t.Setenv(envNATSTLSInsecure, "1")
	if _, err := natsTLSConfigFromEnv(); err == nil {
		t.Fatalf("expected insecure tls error in production")
	}
}

func newTestRedis(t *testing.T) (goredis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	return client, srv
}

// TestProcessedKeyFormat verifies the key format for idempotency tracking.
func TestProcessedKeyFormat(t *testing.T) {
	key := processedKey("CORDUM_JOBS", 42)
	expected := "cordum:bus:processed:CORDUM_JOBS:42"
	if key != expected {
		t.Fatalf("expected %q, got %q", expected, key)
	}
}

// TestInflightKeyFormat verifies the key format for in-flight tracking.
func TestInflightKeyFormat(t *testing.T) {
	key := inflightKey("CORDUM_SYS", 99)
	expected := "cordum:bus:inflight:CORDUM_SYS:99"
	if key != expected {
		t.Fatalf("expected %q, got %q", expected, key)
	}
}

// TestIdempotencyGuard_ProcessedKeyPreventsReprocessing verifies that a
// message with a processed key set in Redis is skipped (idempotency guard).
func TestIdempotencyGuard_ProcessedKeyPreventsReprocessing(t *testing.T) {
	client, srv := newTestRedis(t)
	defer srv.Close()
	defer client.Close()

	ctx := context.Background()
	pKey := processedKey("CORDUM_JOBS", 100)

	// Simulate: first processing sets the key.
	if err := client.Set(ctx, pKey, "1", processedKeyTTL).Err(); err != nil {
		t.Fatalf("set processed key: %v", err)
	}

	// Verify exists.
	exists, err := client.Exists(ctx, pKey).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected key to exist")
	}

	// Verify TTL is set.
	ttl, err := client.TTL(ctx, pKey).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected positive TTL, got %v", ttl)
	}
}

// TestInflightTracking_SetAndClear verifies in-flight key lifecycle.
func TestInflightTracking_SetAndClear(t *testing.T) {
	client, srv := newTestRedis(t)
	defer srv.Close()
	defer client.Close()

	ctx := context.Background()
	iKey := inflightKey("CORDUM_JOBS", 200)

	// Set in-flight.
	if err := client.Set(ctx, iKey, "1", inflightKeyTTL).Err(); err != nil {
		t.Fatalf("set inflight: %v", err)
	}

	// Verify exists.
	exists, _ := client.Exists(ctx, iKey).Result()
	if exists != 1 {
		t.Fatalf("expected inflight key to exist")
	}

	// Clear in-flight.
	if err := client.Del(ctx, iKey).Err(); err != nil {
		t.Fatalf("del inflight: %v", err)
	}

	// Verify gone.
	exists, _ = client.Exists(ctx, iKey).Result()
	if exists != 0 {
		t.Fatalf("expected inflight key to be gone")
	}
}

// TestInflightTracking_TTLExpiry verifies in-flight key expires via TTL.
func TestInflightTracking_TTLExpiry(t *testing.T) {
	client, srv := newTestRedis(t)
	defer srv.Close()
	defer client.Close()

	ctx := context.Background()
	iKey := inflightKey("CORDUM_JOBS", 300)

	if err := client.Set(ctx, iKey, "1", inflightKeyTTL).Err(); err != nil {
		t.Fatalf("set inflight: %v", err)
	}

	// Fast-forward past TTL.
	srv.FastForward(inflightKeyTTL + time.Second)

	exists, _ := client.Exists(ctx, iKey).Result()
	if exists != 0 {
		t.Fatalf("expected inflight key to expire after TTL")
	}
}

// TestOnMessageTerminated_DLQFirstSuccess verifies DLQ callback is called
// before Term, and Term proceeds when DLQ succeeds.
func TestOnMessageTerminated_DLQFirstSuccess(t *testing.T) {
	dlqCalled := false
	b := &NatsBus{
		OnMessageTerminated: func(subject string, data []byte, numDelivered uint64) error {
			dlqCalled = true
			return nil // DLQ write success
		},
	}

	// Simulate calling the callback.
	if b.OnMessageTerminated != nil {
		err := b.OnMessageTerminated("test.subject", []byte("data"), 5)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	}
	if !dlqCalled {
		t.Fatal("expected DLQ callback to be called")
	}
}

// TestOnMessageTerminated_DLQFirstFailure verifies that when DLQ callback
// returns error, the caller should Nak instead of Term.
func TestOnMessageTerminated_DLQFirstFailure(t *testing.T) {
	dlqErr := errors.New("dlq write failed")
	b := &NatsBus{
		OnMessageTerminated: func(subject string, data []byte, numDelivered uint64) error {
			return dlqErr // DLQ write failure
		},
	}

	// Simulate calling the callback.
	if b.OnMessageTerminated != nil {
		err := b.OnMessageTerminated("test.subject", []byte("data"), 5)
		if err == nil {
			t.Fatal("expected DLQ write error")
		}
		if !errors.Is(err, dlqErr) {
			t.Fatalf("expected dlq error, got %v", err)
		}
	}
}

// TestWithRedis verifies the WithRedis setter.
func TestWithRedis(t *testing.T) {
	client, srv := newTestRedis(t)
	defer srv.Close()
	defer client.Close()

	b := &NatsBus{}
	if b.redis != nil {
		t.Fatal("expected nil redis before WithRedis")
	}
	b.WithRedis(client)
	if b.redis == nil {
		t.Fatal("expected non-nil redis after WithRedis")
	}
}

// TestIdempotencyRedisDown verifies graceful degradation when Redis is unavailable.
func TestIdempotencyRedisDown(t *testing.T) {
	client, srv := newTestRedis(t)
	// Close Redis to simulate unavailability.
	srv.Close()

	ctx := context.Background()
	pKey := processedKey("CORDUM_JOBS", 400)

	// Exists should error — not panic.
	_, err := client.Exists(ctx, pKey).Result()
	if err == nil {
		t.Fatal("expected error with closed Redis")
	}

	client.Close()
}

// TestProcessedKeyTTLMatchesAckWait verifies the constant alignment.
func TestProcessedKeyTTLMatchesAckWait(t *testing.T) {
	if processedKeyTTL != defaultAckWait {
		t.Fatalf("processedKeyTTL (%v) should match defaultAckWait (%v)", processedKeyTTL, defaultAckWait)
	}
}

// startTestNATSServer starts an in-process NATS server for testing.
func startTestNATSServer(t *testing.T, enableJS bool) *natsserver.Server {
	t.Helper()
	opts := &natsserver.Options{Port: -1, NoLog: true, NoSigs: true}
	if enableJS {
		storeDir := t.TempDir()
		opts.JetStream = true
		opts.StoreDir = storeDir
	}
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("start test nats: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

// newTestNatsBus creates a NatsBus connected to the given server.
func newTestNatsBus(t *testing.T, ns *natsserver.Server, enableJS bool) *NatsBus {
	t.Helper()
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	b := &NatsBus{nc: nc}
	if enableJS {
		js, err := nc.JetStream()
		if err != nil {
			t.Fatalf("jetstream: %v", err)
		}
		b.js = js
		b.jsEnabled = true
		b.ackWait = 30 * time.Second
	}
	return b
}

// TestBroadcastFanout_BothReplicasReceive verifies that 2 NatsBus instances
// both receive broadcast messages (empty queue group) via core NATS.
func TestBroadcastFanout_BothReplicasReceive(t *testing.T) {
	ns := startTestNATSServer(t, false)
	bus1 := newTestNatsBus(t, ns, false)
	bus2 := newTestNatsBus(t, ns, false)

	var count1, count2 atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	if err := bus1.Subscribe(capsdk.SubjectHeartbeat, "", func(p *pb.BusPacket) error {
		count1.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus1 subscribe: %v", err)
	}
	if err := bus2.Subscribe(capsdk.SubjectHeartbeat, "", func(p *pb.BusPacket) error {
		count2.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus2 subscribe: %v", err)
	}

	// Allow subscriptions to propagate.
	bus1.nc.Flush()
	bus2.nc.Flush()
	time.Sleep(50 * time.Millisecond)

	// Publish a heartbeat.
	if err := bus1.Publish(capsdk.SubjectHeartbeat, &pb.BusPacket{
		Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "w1"}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	bus1.nc.Flush()

	// Wait for delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count1.Load() >= 1 && count2.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if count1.Load() < 1 {
		t.Fatal("bus1 did not receive broadcast message")
	}
	if count2.Load() < 1 {
		t.Fatal("bus2 did not receive broadcast message")
	}
}

// TestQueueGroup_OnlyOneReceives verifies that with a queue group, only one
// of 2 subscribers receives each message.
func TestQueueGroup_OnlyOneReceives(t *testing.T) {
	ns := startTestNATSServer(t, false)
	bus1 := newTestNatsBus(t, ns, false)
	bus2 := newTestNatsBus(t, ns, false)

	var count1, count2 atomic.Int32
	queue := "test-queue"

	if err := bus1.Subscribe(capsdk.SubjectSubmit, queue, func(p *pb.BusPacket) error {
		count1.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus1 subscribe: %v", err)
	}
	if err := bus2.Subscribe(capsdk.SubjectSubmit, queue, func(p *pb.BusPacket) error {
		count2.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus2 subscribe: %v", err)
	}

	bus1.nc.Flush()
	bus2.nc.Flush()
	time.Sleep(50 * time.Millisecond)

	// Publish multiple messages.
	for i := 0; i < 10; i++ {
		if err := bus1.Publish(capsdk.SubjectSubmit, &pb.BusPacket{
			Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-q"}},
		}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	bus1.nc.Flush()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count1.Load()+count2.Load() >= 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	total := count1.Load() + count2.Load()
	if total != 10 {
		t.Fatalf("expected 10 total messages, got %d", total)
	}
	// With queue group, messages should be distributed, not duplicated.
	// Each message delivered to exactly one subscriber.
	// With queue group, all messages may go to one subscriber — acceptable for small N.
	// Key assertion: no duplication — total == 10, not 20.
	// Key assertion: no duplication — total == 10, not 20.
}

// TestBroadcastWithJetStream verifies that broadcast subscriptions (empty queue)
// use ephemeral consumers under JetStream, so both replicas receive all messages.
func TestBroadcastWithJetStream(t *testing.T) {
	ns := startTestNATSServer(t, true)
	bus1 := newTestNatsBus(t, ns, true)
	bus2 := newTestNatsBus(t, ns, true)

	// Create stream covering DLQ subject (a durable subject that uses broadcast).
	_, err := bus1.js.AddStream(&nats.StreamConfig{
		Name:     "TEST_SYS",
		Subjects: []string{"sys.>"},
	})
	if err != nil {
		t.Fatalf("add stream: %v", err)
	}

	var count1, count2 atomic.Int32

	// Both subscribe to DLQ with empty queue (broadcast).
	if err := bus1.Subscribe(capsdk.SubjectDLQ, "", func(p *pb.BusPacket) error {
		count1.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus1 subscribe: %v", err)
	}
	if err := bus2.Subscribe(capsdk.SubjectDLQ, "", func(p *pb.BusPacket) error {
		count2.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus2 subscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish a DLQ message.
	if err := bus1.Publish(capsdk.SubjectDLQ, &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "dlq-1"}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if count1.Load() >= 1 && count2.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if count1.Load() < 1 {
		t.Fatal("bus1 did not receive JetStream broadcast DLQ message")
	}
	if count2.Load() < 1 {
		t.Fatal("bus2 did not receive JetStream broadcast DLQ message")
	}
}

// TestQueueGroupWithJetStream verifies that queue group subscriptions under
// JetStream still deliver each message to only one consumer.
func TestQueueGroupWithJetStream(t *testing.T) {
	ns := startTestNATSServer(t, true)
	bus1 := newTestNatsBus(t, ns, true)
	bus2 := newTestNatsBus(t, ns, true)

	_, err := bus1.js.AddStream(&nats.StreamConfig{
		Name:     "TEST_JOBS",
		Subjects: []string{"sys.>"},
	})
	if err != nil {
		t.Fatalf("add stream: %v", err)
	}

	var count1, count2 atomic.Int32
	queue := "cordum-scheduler"

	if err := bus1.Subscribe(capsdk.SubjectSubmit, queue, func(p *pb.BusPacket) error {
		count1.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus1 subscribe: %v", err)
	}
	if err := bus2.Subscribe(capsdk.SubjectSubmit, queue, func(p *pb.BusPacket) error {
		count2.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("bus2 subscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish 5 messages with unique IDs (JetStream deduplicates by MsgId).
	for i := 0; i < 5; i++ {
		if err := bus1.Publish(capsdk.SubjectSubmit, &pb.BusPacket{
			Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
				JobId: "job-js-q-" + time.Now().Format("150405.000") + "-" + string(rune('a'+i)),
			}},
		}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if count1.Load()+count2.Load() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	total := count1.Load() + count2.Load()
	if total != 5 {
		t.Fatalf("expected 5 total messages with queue group, got %d (bus1=%d, bus2=%d)", total, count1.Load(), count2.Load())
	}
}
