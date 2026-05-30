package audit

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuditSender receives audit events for asynchronous export.
// Both BufferedExporter and NATSAuditPublisher implement this interface.
type AuditSender interface {
	Send(event SIEMEvent)
	Close() error
}

// AuditBus is the subset of the message bus needed by NATS audit components.
type AuditBus interface {
	Publish(subject string, packet *pb.BusPacket) error
	Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error
}

// NATSAuditPublisher publishes audit events to NATS for durable cross-replica
// delivery. If NATS publish fails, the event is forwarded to the fallback
// in-memory BufferedExporter so no audit data is silently lost.
type NATSAuditPublisher struct {
	bus      AuditBus
	fallback *BufferedExporter
}

// NewNATSAuditPublisher creates a publisher that sends audit events via NATS
// subject sys.audit.export. The fallback BufferedExporter handles events when
// NATS is unavailable.
func NewNATSAuditPublisher(bus AuditBus, fallback *BufferedExporter) *NATSAuditPublisher {
	return &NATSAuditPublisher{bus: bus, fallback: fallback}
}

// Send marshals the event to JSON and publishes it to NATS. On failure the
// event is forwarded to the fallback in-memory buffer.
func (p *NATSAuditPublisher) Send(event SIEMEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("audit nats publisher: marshal failed, using fallback", "error", err)
		p.fallback.Send(event)
		return
	}

	// CAP's BusPacket validator rejects an empty or whitespace-only trace_id,
	// so a packet without one is NAK-looped by the bus and never reaches the
	// audit consumer / SIEM exporter. Prefer the event's own correlation trace
	// so SIEM entries can be joined back to the originating governance event.
	packet := &pb.BusPacket{
		SenderId:        "audit-publisher",
		TraceId:         auditExportTraceID(event),
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Severity:        pb.AlertSeverity_ALERT_SEVERITY_INFO,
				Message:         string(payload),
				SourceComponent: "audit-export",
				Details: map[string]string{
					"event_type": event.EventType,
					"tenant_id":  event.TenantID,
				},
			},
		},
	}

	if err := p.bus.Publish(capsdk.SubjectAuditExport, packet); err != nil {
		slog.Warn("audit nats publish failed, using fallback buffer",
			"error", err,
			"event_type", event.EventType,
		)
		p.fallback.Send(event)
		return
	}
}

// auditExportTraceID returns a guaranteed non-empty, non-whitespace trace id
// for the audit-export BusPacket. Precedence is correlation-first: the event's
// own trace_id, then its chain EventHash, then a generated UUID so the packet
// always satisfies CAP validation even for synthetic events (e.g. the
// /api/v1/audit/export/test ping) that carry neither.
func auditExportTraceID(event SIEMEvent) string {
	if t := strings.TrimSpace(event.Extra["trace_id"]); t != "" {
		return t
	}
	if h := strings.TrimSpace(event.EventHash); h != "" {
		return h
	}
	return uuid.NewString()
}

// Close shuts down the fallback exporter.
func (p *NATSAuditPublisher) Close() error {
	return p.fallback.Close()
}
