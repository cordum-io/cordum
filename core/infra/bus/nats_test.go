package bus

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
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
		t.Fatalf("expected empty durable name")
	}
	name := durableName("job.test.*", "q")
	if name == "" || name == "dur_" {
		t.Fatalf("unexpected durable name: %s", name)
	}
	name = durableName("job.test.*", "")
	if name == "" || name == "dur_" {
		t.Fatalf("unexpected durable name for empty queue: %s", name)
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

	// Malformed data should trigger NakDelay (poison pill), handler should NOT be called
	action, delay := processBusMsg([]byte("not-protobuf-data"), handler)
	if action != msgActionNakDelay {
		t.Fatalf("expected NakDelay for poison pill, got action=%d", action)
	}
	if delay != 5*time.Second {
		t.Fatalf("expected 5s delay, got %v", delay)
	}
	if handlerCalled {
		t.Fatal("handler should not be called for poison pill")
	}

	// Valid protobuf should ACK and call handler
	valid := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}}
	data, err := proto.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	action, _ = processBusMsg(data, handler)
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

	action, delay := processBusMsg(data, handler)
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

	action, _ := processBusMsg(data, handler)
	if action != msgActionAck {
		t.Fatalf("expected Ack for non-retryable error, got action=%d", action)
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
