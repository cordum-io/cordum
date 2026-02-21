package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	// QueueAuditExporters is the NATS queue group for audit consumers.
	// Ensures exactly one consumer replica processes each event.
	QueueAuditExporters = "audit-exporters"
)

// NATSAuditConsumer subscribes to NATS subject sys.audit.export and forwards
// events to the underlying SIEM Exporter. The queue group audit-exporters
// ensures each event is delivered to exactly one consumer across replicas.
//
// When JetStream is enabled the bus provides at-least-once delivery:
// the handler only returns nil (triggering ack) after a successful Export.
// On Export failure the handler returns an error (triggering nak and redelivery).
type NATSAuditConsumer struct {
	exporter Exporter
}

// NewNATSAuditConsumer creates a consumer and subscribes to sys.audit.export.
// The exporter receives deserialized SIEMEvents for each NATS message.
func NewNATSAuditConsumer(bus AuditBus, exporter Exporter) (*NATSAuditConsumer, error) {
	c := &NATSAuditConsumer{exporter: exporter}
	if err := bus.Subscribe(capsdk.SubjectAuditExport, QueueAuditExporters, c.handle); err != nil {
		return nil, fmt.Errorf("audit consumer subscribe: %w", err)
	}
	slog.Info("audit NATS consumer started", "subject", capsdk.SubjectAuditExport, "queue", QueueAuditExporters)
	return c, nil
}

// handle processes a single BusPacket from NATS. It extracts the SIEMEvent
// from the Alert payload and exports it. Returns nil on success (ack) or
// error on failure (nak → JetStream redelivery).
func (c *NATSAuditConsumer) handle(packet *pb.BusPacket) error {
	alert := packet.GetAlert()
	if alert == nil || alert.Component != "audit-export" {
		// Not an audit event — ack and skip.
		return nil
	}

	var event SIEMEvent
	if err := json.Unmarshal([]byte(alert.Message), &event); err != nil {
		slog.Error("audit consumer: unmarshal event failed", "error", err)
		// Malformed payload — ack to prevent infinite redelivery loop.
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultExportTimeout)
	defer cancel()

	if err := c.exporter.Export(ctx, []SIEMEvent{event}); err != nil {
		// Export failed — return error to nak the message for redelivery.
		return fmt.Errorf("audit consumer export: %w", err)
	}
	return nil
}

// Close shuts down the underlying SIEM exporter.
func (c *NATSAuditConsumer) Close() error {
	return c.exporter.Close()
}
