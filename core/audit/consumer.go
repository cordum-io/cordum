package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	// QueueAuditExporters is the NATS queue group for audit consumers.
	// Ensures exactly one consumer replica processes each event.
	QueueAuditExporters = "audit-exporters"

	// EnvChainFailMode selects the consumer's behaviour when the audit
	// chain append fails. Values: "strict" (default) or "permissive".
	EnvChainFailMode = "CORDUM_AUDIT_CHAIN_FAIL"
)

// ChainFailMode controls consumer behaviour when Chainer.Append fails.
//
// In ChainFailStrict (the default) an event that cannot be chained is
// dropped — the consumer acks the NATS message (so it is not redelivered
// indefinitely) and does NOT forward the event to the SIEM exporter.
// Exporting an un-chained event would leave a SIEM entry the verify
// endpoint cannot cover, so strict is the safer production default.
//
// ChainFailPermissive logs a WARN and still forwards to the exporter.
// Useful for dev/staging where Redis may be unavailable, and for incident
// recovery when operators choose to accept non-tamper-evident export in
// exchange for visibility.
type ChainFailMode int

const (
	// ChainFailStrict drops un-chained events.
	ChainFailStrict ChainFailMode = iota
	// ChainFailPermissive exports un-chained events after a WARN log.
	ChainFailPermissive
)

// String renders the mode for log fields.
func (m ChainFailMode) String() string {
	switch m {
	case ChainFailPermissive:
		return "permissive"
	default:
		return "strict"
	}
}

// ParseChainFailMode accepts "strict" or "permissive" (case-insensitive).
// Any other value — including empty — resolves to ChainFailStrict so the
// safer default applies when operators mis-configure the env var.
func ParseChainFailMode(raw string) ChainFailMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "permissive":
		return ChainFailPermissive
	default:
		return ChainFailStrict
	}
}

// NATSAuditConsumer subscribes to NATS subject sys.audit.export and forwards
// events to the underlying SIEM Exporter. The queue group audit-exporters
// ensures each event is delivered to exactly one consumer across replicas.
//
// When a Chainer is configured, every event is linked into its tenant's
// append-only hash chain before Export. Chain append happens at the
// consumer (rather than the publisher) so the single queue-group replica
// owns chain ordering — racing producers across replicas do not shift
// seq numbers under each other.
//
// When JetStream is enabled the bus provides at-least-once delivery:
// the handler only returns nil (triggering ack) after a successful Export.
// On Export failure the handler returns an error (triggering nak and
// redelivery).
type NATSAuditConsumer struct {
	exporter Exporter
	chainer  *Chainer
	failMode ChainFailMode
}

// ConsumerOption configures a NATSAuditConsumer.
type ConsumerOption func(*NATSAuditConsumer)

// WithChainer installs a Chainer so every event is appended to its
// tenant's hash chain before SIEM export. Nil means no chaining.
func WithChainer(c *Chainer) ConsumerOption {
	return func(n *NATSAuditConsumer) { n.chainer = c }
}

// WithChainFailMode overrides the default strict fail mode.
func WithChainFailMode(m ChainFailMode) ConsumerOption {
	return func(n *NATSAuditConsumer) { n.failMode = m }
}

// NewNATSAuditConsumer creates a consumer and subscribes to sys.audit.export.
// The exporter receives deserialized SIEMEvents for each NATS message.
//
// If the CORDUM_AUDIT_CHAIN_FAIL env var is set it overrides the default
// fail mode (unless a WithChainFailMode option is passed, in which case
// the option wins — tests and explicit wiring take precedence over env).
func NewNATSAuditConsumer(bus AuditBus, exporter Exporter, opts ...ConsumerOption) (*NATSAuditConsumer, error) {
	c := &NATSAuditConsumer{
		exporter: exporter,
		failMode: ParseChainFailMode(os.Getenv(EnvChainFailMode)),
	}
	for _, o := range opts {
		o(c)
	}
	if err := bus.Subscribe(capsdk.SubjectAuditExport, QueueAuditExporters, c.handle); err != nil {
		return nil, fmt.Errorf("audit consumer subscribe: %w", err)
	}
	slog.Info("audit NATS consumer started",
		"subject", capsdk.SubjectAuditExport,
		"queue", QueueAuditExporters,
		"chain_enabled", c.chainer != nil,
		"chain_fail_mode", c.failMode.String(),
	)
	return c, nil
}

// handle processes a single BusPacket from NATS. It extracts the SIEMEvent
// from the Alert payload, links it into the per-tenant hash chain when a
// Chainer is configured, and exports it.
//
// Return values map to JetStream ack semantics:
//   - nil: ack (message consumed, no redelivery)
//   - non-nil: nak (JetStream redelivers after the configured backoff)
//
// Chain append errors are intentionally NOT returned — the event would be
// re-chained on redelivery and produce a new seq for a payload that was
// already partially observed, which is worse than the two documented
// outcomes (strict: drop-and-ack; permissive: export-and-ack).
func (c *NATSAuditConsumer) handle(packet *pb.BusPacket) error {
	alert := packet.GetAlert()
	if alert == nil || alert.SourceComponent != "audit-export" {
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

	if c.chainer != nil {
		if err := c.chainer.Append(ctx, &event); err != nil {
			// Never log the full payload — it may carry tenant PII that
			// SIEM retention policies do not cover. Log only the coarse
			// identifiers an operator needs to correlate the failure
			// with its source.
			slog.Error("audit chain append failed",
				"event_type", event.EventType,
				"tenant_id", event.TenantID,
				"job_id", event.JobID,
				"fail_mode", c.failMode.String(),
				"error", err,
			)
			if c.failMode == ChainFailStrict {
				// Strict: ack (so JetStream does not redeliver) and
				// drop. Better to lose visibility than to produce a
				// SIEM entry that verify cannot reason about.
				return nil
			}
			// Permissive: fall through to export with empty chain fields.
		}
	}

	if err := c.exporter.Export(ctx, []SIEMEvent{event}); err != nil {
		// Export failed — return error to nak for redelivery.
		return fmt.Errorf("audit consumer export: %w", err)
	}
	return nil
}

// Close shuts down the underlying SIEM exporter.
func (c *NATSAuditConsumer) Close() error {
	if c == nil || c.exporter == nil {
		return nil
	}
	return c.exporter.Close()
}

