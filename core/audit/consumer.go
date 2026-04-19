package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
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
// ChainFailStrict (the default) acks the NATS message and DROPS the event
// — exporting an un-chained event would leave a SIEM entry the verify
// endpoint cannot cover, so strict is the safer production default.
//
// ChainFailPermissive logs a WARN and still forwards to the exporter.
// Useful for dev/staging where Redis may be unavailable.
type ChainFailMode int

const (
	ChainFailStrict ChainFailMode = iota
	ChainFailPermissive
)

func (m ChainFailMode) String() string {
	if m == ChainFailPermissive {
		return "permissive"
	}
	return "strict"
}

// ParseChainFailMode accepts "strict" or "permissive" (case-insensitive).
// Any other value resolves to ChainFailStrict so mis-configured env vars
// fail safe.
func ParseChainFailMode(raw string) ChainFailMode {
	if strings.EqualFold(strings.TrimSpace(raw), "permissive") {
		return ChainFailPermissive
	}
	return ChainFailStrict
}

// ParseChainFailModeFromEnv reads CORDUM_AUDIT_CHAIN_FAIL and returns
// the corresponding ChainFailMode. Wrapper around ParseChainFailMode +
// os.Getenv so callers don't have to plumb both.
func ParseChainFailModeFromEnv() ChainFailMode {
	return ParseChainFailMode(os.Getenv(EnvChainFailMode))
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
type NATSAuditConsumer struct {
	exporter Exporter
	chainer  *Chainer
	failMode ChainFailMode
}

// ConsumerOption configures a NATSAuditConsumer.
type ConsumerOption func(*NATSAuditConsumer)

// WithChainer installs a Chainer so every event is appended to its
// tenant's hash chain before SIEM export.
func WithChainer(c *Chainer) ConsumerOption {
	return func(n *NATSAuditConsumer) { n.chainer = c }
}

// WithChainFailMode overrides the default strict fail mode.
func WithChainFailMode(m ChainFailMode) ConsumerOption {
	return func(n *NATSAuditConsumer) { n.failMode = m }
}

// NewNATSAuditConsumer creates a consumer and subscribes to sys.audit.export.
// CORDUM_AUDIT_CHAIN_FAIL selects the default fail mode when no explicit
// WithChainFailMode option is passed.
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
// Chain append errors are intentionally NOT returned — redelivering an
// event would re-chain it at a new seq for a payload that was already
// partially observed, which is worse than the two documented outcomes
// (strict: drop-and-ack; permissive: export-and-ack).
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
			// Never log the full payload — it may carry tenant PII
			// that SIEM retention policies do not cover. Log only
			// the coarse identifiers an operator needs to correlate
			// the failure with its source.
			slog.Error("audit chain append failed",
				"event_type", event.EventType,
				"tenant_id", event.TenantID,
				"job_id", event.JobID,
				"fail_mode", c.failMode.String(),
				"error", err,
			)
			if c.failMode == ChainFailStrict {
				// Dead-letter the unchained payload instead of dropping
				// it to /dev/null. A Redis hiccup must not erase the
				// compliance signal — operators can drain the DLQ back
				// into the chain once Redis recovers.
				if dlqErr := c.deadLetter(ctx, &event, err); dlqErr != nil {
					slog.Error("audit chain dead-letter write failed",
						"event_type", event.EventType,
						"tenant_id", event.TenantID,
						"job_id", event.JobID,
						"error", dlqErr,
					)
				}
				return nil
			}
			// Permissive: fall through with empty chain fields.
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

// chainDLQStreamPrefix is the Redis Stream prefix for events that
// failed chain.Append in strict mode. One stream per tenant so a
// single noisy tenant cannot overflow the DLQ for everyone else.
const chainDLQStreamPrefix = "audit:chain:dlq:"

// chainDLQMaxLen caps the DLQ per tenant so a sustained Redis outage
// can't balloon the stream indefinitely. 10k entries per tenant is
// enough for a multi-hour recovery window at typical event rates.
const chainDLQMaxLen = 10_000

// deadLetter writes the unchained event to the tenant's DLQ stream so
// operators can re-chain it once Redis recovers. Best-effort: if the
// DLQ write itself fails we return the error, the caller logs it,
// and the event is still dropped from the live pipeline (strict mode
// has already decided not to export it). This is an upgrade from the
// previous /dev/null behaviour — a Redis blip that affects head
// writes usually doesn't affect non-CAS XADDs to a different stream.
func (c *NATSAuditConsumer) deadLetter(ctx context.Context, event *SIEMEvent, cause error) error {
	if c == nil || c.chainer == nil || c.chainer.client == nil || event == nil {
		return nil
	}
	tenant := strings.TrimSpace(event.TenantID)
	if tenant == "" {
		tenant = "unknown"
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("dlq marshal: %w", err)
	}
	streamKey := chainDLQStreamPrefix + tenant
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return c.chainer.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: chainDLQMaxLen,
		Approx: true,
		Values: map[string]any{
			"event":      string(payload),
			"reason":     reason,
			"recorded":   time.Now().UTC().Format(time.RFC3339Nano),
			"event_type": event.EventType,
		},
	}).Err()
}
