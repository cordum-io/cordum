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

	// maxAuditEventBytes bounds the JSON payload the consumer will attempt
	// to unmarshal. A normal SIEMEvent is kilobytes at worst (identity +
	// reason + a small labels map); anything north of 1 MiB is treated as
	// malformed / malicious and dropped at the subscription boundary so a
	// crafted producer cannot pin the json decoder on a large allocation
	// and starve the queue-group worker. Oversized messages are acked so
	// JetStream does not redeliver them indefinitely — they would just
	// re-trigger the same guard and block legitimate traffic behind them.
	maxAuditEventBytes = 1 << 20 // 1 MiB
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
//
// The lifecycle ctx/cancel pair bounds every chain-append and exporter call:
// handle() derives per-message timeouts from lifecycleCtx so Close() can
// abort in-flight work by cancelling the parent. Without this the handler
// would orphan its timeout from context.Background() and could hang past
// the NATS ack deadline during shutdown, leaving messages re-delivered.
type NATSAuditConsumer struct {
	exporter     Exporter
	chainer      *Chainer
	failMode     ChainFailMode
	stepHashSink StepHashSink

	// lifecycleCtx is cancelled by Close(); handle() derives per-message
	// timeouts from it so in-flight chain-append and Export calls observe
	// shutdown promptly and return within the JetStream ack window.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
}

// StepHashSink is the optional dependency the audit consumer uses to write
// per-step audit-chain hashes back into the workflow store after a
// successful Append. Workflow runs already store JobID per StepRun
// (see core/workflow engine.go where stepRun.JobID = res.JobId is set
// when a JobResult bus message returns). This interface lets the audit
// consumer populate StepRun.AuditHash without core/audit importing
// core/workflow directly — keeps the dependency one-way.
//
// Architectural choice per task-a45b8eb1 architect msg-dc9ac33d (Option A):
// eventual-consistency write-back from the audit consumer goroutine.
// Workflow engine cannot synchronously compute the chain hash at
// applyResult time because PrevHash depends on the chain head held by
// the chainer; that's why the population path lives here, post-Append,
// not in the workflow engine.
//
// Implementations live in core/workflow (RedisStore-backed). The
// nil-sink case (no implementation wired) is a no-op — every audit
// chain Append still produces a durable EventHash; only the
// dashboard governance overlay's runtime audit-hash chip stays
// unpopulated until a sink is wired.
type StepHashSink interface {
	// UpdateAuditHash finds the workflow-run step whose JobID matches the
	// supplied jobID and persists auditHash on it. Implementations MUST
	// be idempotent (JetStream redelivery may invoke this twice for the
	// same SIEMEvent) and SHOULD treat "no matching step" as a no-op
	// (returns nil) — many SIEMEvents are emitted for non-workflow jobs
	// (edge sessions, MCP approvals, ad-hoc safety decisions) and do not
	// have a corresponding StepRun.
	UpdateAuditHash(ctx context.Context, jobID, auditHash string) error
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

// WithStepHashSink installs an optional sink that receives the populated
// SIEMEvent.EventHash after a successful chainer.Append. Used to
// back-fill the dashboard governance overlay's runtime audit-hash chip
// per task-a45b8eb1 (Option A).
//
// Nil means no write-back; default. Wiring is done in runner.go where
// the audit consumer is constructed alongside the workflow store.
func WithStepHashSink(sink StepHashSink) ConsumerOption {
	return func(n *NATSAuditConsumer) { n.stepHashSink = sink }
}

// NewNATSAuditConsumer creates a consumer and subscribes to sys.audit.export.
// The exporter receives deserialized SIEMEvents for each NATS message.
//
// If the CORDUM_AUDIT_CHAIN_FAIL env var is set it overrides the default
// fail mode (unless a WithChainFailMode option is passed, in which case
// the option wins — tests and explicit wiring take precedence over env).
func NewNATSAuditConsumer(bus AuditBus, exporter Exporter, opts ...ConsumerOption) (*NATSAuditConsumer, error) {
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	c := &NATSAuditConsumer{
		exporter:        exporter,
		failMode:        ParseChainFailMode(os.Getenv(EnvChainFailMode)),
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
	}
	for _, o := range opts {
		o(c)
	}
	if err := bus.Subscribe(capsdk.SubjectAuditExport, QueueAuditExporters, c.handle); err != nil {
		lifecycleCancel()
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

	// Defensive size bound. A single SIEMEvent is expected to be at most
	// a few kilobytes of JSON; a multi-MiB payload is either a
	// misconfigured producer (e.g. a runaway reason string) or an attempt
	// to starve the queue-group worker. We ack-skip — the subscription
	// loop must NOT fail, otherwise one malformed-large event would block
	// every well-formed event queued behind it on the same JetStream
	// consumer.
	if len(alert.Message) > maxAuditEventBytes {
		slog.Warn("audit consumer: oversized event dropped",
			"bytes", len(alert.Message),
			"max_bytes", maxAuditEventBytes,
			"source", alert.SourceComponent,
		)
		return nil
	}

	var event SIEMEvent
	if err := json.Unmarshal([]byte(alert.Message), &event); err != nil {
		slog.Error("audit consumer: unmarshal event failed", "error", err)
		// Malformed payload — ack to prevent infinite redelivery loop.
		return nil
	}

	// Derive the per-message timeout from the consumer's lifecycle context so
	// Close() can abort in-flight chain-append / Export calls. Orphaning from
	// context.Background() would let a stalled handler run past the JetStream
	// ack deadline and trigger redelivery storms.
	parent := c.lifecycleCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, defaultExportTimeout)
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
		} else if c.stepHashSink != nil && event.EventHash != "" && event.JobID != "" {
			// Option A write-back per task-a45b8eb1 (architect msg-dc9ac33d):
			// the chain Append just produced EventHash; if a workflow-store
			// sink is wired AND the event corresponds to a job (most safety
			// decisions do, but tenant-level events e.g. SSO login do not),
			// back-fill the matching StepRun.AuditHash. Eventual consistency:
			// the dashboard governance overlay's audit-hash chip lights up
			// the moment this returns. Errors are intentionally non-fatal —
			// the audit chain entry is durable; the workflow store can be
			// back-filled by an offline tool if the sink misses transiently.
			if err := c.stepHashSink.UpdateAuditHash(ctx, event.JobID, event.EventHash); err != nil {
				slog.Warn("audit step-hash write-back failed",
					"event_type", event.EventType,
					"tenant_id", event.TenantID,
					"job_id", event.JobID,
					"error", err,
				)
			}
		}
	}

	if err := c.exporter.Export(ctx, []SIEMEvent{event}); err != nil {
		// Export failed — return error to nak for redelivery.
		return fmt.Errorf("audit consumer export: %w", err)
	}
	return nil
}

// Close cancels the lifecycle context — aborting any in-flight chain-append
// or Export calls — then shuts down the underlying SIEM exporter.
//
// Cancelling before the exporter close ensures stalled calls observe ctx.Done
// and return promptly; without it Exporter.Close() could race with a handler
// still blocked on a network round-trip.
func (c *NATSAuditConsumer) Close() error {
	if c == nil {
		return nil
	}
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
	}
	if c.exporter == nil {
		return nil
	}
	return c.exporter.Close()
}
