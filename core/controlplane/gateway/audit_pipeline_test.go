package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type auditBusLoopback struct {
	mu       sync.Mutex
	handlers map[string][]func(*pb.BusPacket) error
}

func newAuditBusLoopback() *auditBusLoopback {
	return &auditBusLoopback{handlers: make(map[string][]func(*pb.BusPacket) error)}
}

func (b *auditBusLoopback) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	handlers := append([]func(*pb.BusPacket) error{}, b.handlers[subject]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(packet); err != nil {
			return err
		}
	}
	return nil
}

func (b *auditBusLoopback) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[subject] = append(b.handlers[subject], handler)
	return nil
}

func TestInitAuditPipeline_NullBackendChainsEvents(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "null")
	t.Setenv("AUDIT_TRANSPORT", "nats")

	s, _, _ := newTestGateway(t)
	bus := newAuditBusLoopback()

	sender, chainer, err := initAuditPipeline(s.jobStore.Client(), bus, nil)
	if err != nil {
		t.Fatalf("initAuditPipeline: %v", err)
	}
	if sender == nil {
		t.Fatal("expected non-nil audit sender")
	}
	if chainer == nil {
		t.Fatal("expected non-nil audit chainer")
	}
	t.Cleanup(func() { _ = sender.Close() })

	sender.Send(audit.SIEMEvent{
		EventType: audit.EventSafetyDecision,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "pipeline-test",
		JobID:     "job-pipeline-test",
	})

	streamKey := chainer.StreamKey("default")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := audit.VerifyChain(context.Background(), s.redisClient(), streamKey, audit.VerifyOptions{})
		if err != nil {
			t.Fatalf("VerifyChain: %v", err)
		}
		if result.Status == audit.VerifyStatusOK && result.TotalEvents >= 1 && len(result.Gaps) == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	result, err := audit.VerifyChain(context.Background(), s.redisClient(), streamKey, audit.VerifyOptions{})
	if err != nil {
		t.Fatalf("VerifyChain final: %v", err)
	}
	t.Fatalf("verify result = %+v, want status=ok total_events>=1 gaps=0", result)
}
