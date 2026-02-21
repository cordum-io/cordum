package audit

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// mockAuditBus implements AuditBus for testing.
type mockAuditBus struct {
	mu         sync.Mutex
	published  []mockPublished
	publishErr error

	// Subscribe captures the handler so tests can invoke it directly.
	handler    func(*pb.BusPacket) error
	subSubject string
	subQueue   string
}

type mockPublished struct {
	subject string
	packet  *pb.BusPacket
}

func (m *mockAuditBus) Publish(subject string, packet *pb.BusPacket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.publishErr != nil {
		return m.publishErr
	}
	m.published = append(m.published, mockPublished{subject, packet})
	return nil
}

func (m *mockAuditBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = handler
	m.subSubject = subject
	m.subQueue = queue
	return nil
}

func testEvent() SIEMEvent {
	return SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityMedium,
		TenantID:  "tenant-1",
		AgentID:   "agent-42",
		JobID:     "job-99",
		Action:    "evaluate",
		Decision:  "allow",
	}
}

func TestNATSAuditPublisher_PublishesToSubject(t *testing.T) {
	bus := &mockAuditBus{}
	fallback := &mockExporter{}
	bufExp := NewBufferedExporter(fallback, WithFlushInterval(10*time.Second))
	defer bufExp.Close()

	pub := NewNATSAuditPublisher(bus, bufExp)
	event := testEvent()
	pub.Send(event)

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(bus.published))
	}
	msg := bus.published[0]
	if msg.subject != capsdk.SubjectAuditExport {
		t.Fatalf("expected subject %q, got %q", capsdk.SubjectAuditExport, msg.subject)
	}

	alert := msg.packet.GetAlert()
	if alert == nil {
		t.Fatalf("expected alert payload, got nil")
	}
	if alert.Component != "audit-export" {
		t.Fatalf("expected component=audit-export, got %q", alert.Component)
	}

	// Verify the JSON payload round-trips.
	var decoded SIEMEvent
	if err := json.Unmarshal([]byte(alert.Message), &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.EventType != event.EventType {
		t.Fatalf("event type mismatch: got %q, want %q", decoded.EventType, event.EventType)
	}
	if decoded.TenantID != event.TenantID {
		t.Fatalf("tenant mismatch: got %q, want %q", decoded.TenantID, event.TenantID)
	}
	if decoded.JobID != event.JobID {
		t.Fatalf("job id mismatch: got %q, want %q", decoded.JobID, event.JobID)
	}

	// Verify fallback was NOT used.
	if fallback.totalEvents() != 0 {
		t.Fatalf("expected 0 fallback events, got %d", fallback.totalEvents())
	}
}

func TestNATSAuditPublisher_FallbackOnPublishFailure(t *testing.T) {
	bus := &mockAuditBus{publishErr: errors.New("nats down")}
	fallback := &mockExporter{}
	bufExp := NewBufferedExporter(fallback, WithBatchSize(1), WithFlushInterval(50*time.Millisecond))
	defer bufExp.Close()

	pub := NewNATSAuditPublisher(bus, bufExp)
	pub.Send(testEvent())

	// Wait for the BufferedExporter to flush the fallback event.
	time.Sleep(200 * time.Millisecond)

	if got := fallback.totalEvents(); got != 1 {
		t.Fatalf("expected 1 fallback event, got %d", got)
	}
}

func TestNATSAuditConsumer_ExportsEvent(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}

	consumer, err := NewNATSAuditConsumer(bus, mock)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}
	defer consumer.Close()

	// Verify subscription was set up correctly.
	bus.mu.Lock()
	if bus.subSubject != capsdk.SubjectAuditExport {
		t.Fatalf("expected subscription to %q, got %q", capsdk.SubjectAuditExport, bus.subSubject)
	}
	if bus.subQueue != QueueAuditExporters {
		t.Fatalf("expected queue group %q, got %q", QueueAuditExporters, bus.subQueue)
	}
	handler := bus.handler
	bus.mu.Unlock()

	// Build a BusPacket the publisher would send.
	event := testEvent()
	payload, _ := json.Marshal(event)
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Component: "audit-export",
				Message:   string(payload),
			},
		},
	}

	// Invoke the handler directly (simulates NATS delivery).
	if err := handler(packet); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("expected 1 exported event, got %d", got)
	}
	mock.mu.Lock()
	exported := mock.batches[0][0]
	mock.mu.Unlock()
	if exported.EventType != event.EventType {
		t.Fatalf("event type mismatch: got %q, want %q", exported.EventType, event.EventType)
	}
}

func TestNATSAuditConsumer_RetryOnExportFailure(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{failNext: 1}

	_, err := NewNATSAuditConsumer(bus, mock)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	event := testEvent()
	payload, _ := json.Marshal(event)
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Component: "audit-export",
				Message:   string(payload),
			},
		},
	}

	// First call should return error (export fails → nak → JetStream redelivery).
	if err := handler(packet); err == nil {
		t.Fatalf("expected error from handler on export failure")
	}

	// Second call should succeed (failNext exhausted).
	if err := handler(packet); err != nil {
		t.Fatalf("expected nil from handler on second attempt, got: %v", err)
	}

	if got := mock.totalEvents(); got != 1 {
		t.Fatalf("expected 1 exported event after retry, got %d", got)
	}
}

func TestNATSAuditConsumer_SkipsNonAuditAlerts(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}

	_, err := NewNATSAuditConsumer(bus, mock)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	// Send a non-audit alert (different component).
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Component: "api-gateway",
				Message:   "config changed",
			},
		},
	}

	if err := handler(packet); err != nil {
		t.Fatalf("expected nil for non-audit alert, got: %v", err)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("expected 0 events for non-audit alert, got %d", got)
	}
}

func TestNATSAuditConsumer_MalformedPayloadAcks(t *testing.T) {
	bus := &mockAuditBus{}
	mock := &mockExporter{}

	_, err := NewNATSAuditConsumer(bus, mock)
	if err != nil {
		t.Fatalf("NewNATSAuditConsumer: %v", err)
	}

	bus.mu.Lock()
	handler := bus.handler
	bus.mu.Unlock()

	// Send a malformed audit payload (invalid JSON).
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Component: "audit-export",
				Message:   "{invalid json",
			},
		},
	}

	// Should return nil (ack) to prevent infinite redelivery.
	if err := handler(packet); err != nil {
		t.Fatalf("expected nil for malformed payload, got: %v", err)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("expected 0 events for malformed payload, got %d", got)
	}
}

func TestAuditSenderInterface(t *testing.T) {
	// Verify both types satisfy AuditSender.
	var _ AuditSender = (*BufferedExporter)(nil)
	var _ AuditSender = (*NATSAuditPublisher)(nil)
}
