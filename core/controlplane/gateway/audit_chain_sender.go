package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/model"
)

const auditChainAppendTimeout = 5 * time.Second

// auditChainSender is the gateway's local audit sink. It keeps the
// tamper-evident Redis chain populated even when external SIEM export is
// disabled, then forwards the same event to the configured exporter when one
// exists.
type auditChainSender struct {
	chainer    *audit.Chainer
	downstream audit.AuditSender
	stepSink   audit.StepHashSink
}

func newAuditChainSender(chainer *audit.Chainer, downstream audit.AuditSender, stepSink ...audit.StepHashSink) audit.AuditSender {
	if chainer == nil {
		return downstream
	}
	var sink audit.StepHashSink
	if len(stepSink) > 0 {
		sink = stepSink[0]
	}
	return &auditChainSender{
		chainer:    chainer,
		downstream: downstream,
		stepSink:   sink,
	}
}

func (s *auditChainSender) Send(event audit.SIEMEvent) {
	if s == nil {
		return
	}
	if s.chainer != nil {
		// Defense-in-depth: producer sites SHOULD attribute TenantID
		// explicitly via model.ResolveTenantForAudit (see task-3fad45d3
		// for the 5 wired sites — middleware.auditReadMiddleware,
		// edge.SendSIEMEvent, gateway.mcpDenyAuditor, mcp.auditor's
		// Start{Inbound,Outbound}, gateway's mcpTool{Call,Approval}
		// AuditHook). This fallback prevents data loss if a NEW
		// producer site is added without proper attribution. Logged at
		// slog.Warn (downgraded from Debug 2026-05-16) so CI/dev
		// surfaces the producer regression loudly rather than letting
		// it accumulate silently in production audit logs.
		//
		// Per-tenant chain semantics stay intact ("default" is its own
		// stream — no cross-tenant leakage). Task rail #1 keeps this
		// fallback in place until a CI gate prevents tenantless emissions.
		if strings.TrimSpace(event.TenantID) == "" {
			slog.Warn("audit chain: tenantless event — PRODUCER BUG, falling back to default tenant",
				"event_type", event.EventType,
				"action", event.Action,
				"identity_hash", redactedAuditIdentity(event.Identity),
			)
			event.TenantID = model.DefaultTenant
		}
		ctx, cancel := context.WithTimeout(context.Background(), auditChainAppendTimeout)
		defer cancel()
		if err := s.chainer.Append(ctx, &event); err != nil {
			slog.Error("audit chain append failed",
				"event_type", event.EventType,
				"tenant_id", event.TenantID,
				"job_id", event.JobID,
				"error", err,
			)
		} else if s.stepSink != nil && event.EventHash != "" && event.JobID != "" {
			if err := s.stepSink.UpdateAuditHash(ctx, event.JobID, event.EventHash); err != nil {
				slog.Warn("audit chain sender step-hash write-back failed",
					"event_type", event.EventType,
					"tenant_id", event.TenantID,
					"job_id", event.JobID,
					"error", err,
				)
			}
		}
	}
	if s.downstream != nil {
		s.downstream.Send(event)
	}
}

func (s *auditChainSender) Close() error {
	if s == nil || s.downstream == nil {
		return nil
	}
	return s.downstream.Close()
}

func redactedAuditIdentity(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	role, principal, ok := strings.Cut(identity, ":")
	role = strings.TrimSpace(role)
	principal = strings.TrimSpace(principal)
	if !ok {
		role = "unknown"
		principal = identity
	}
	if role == "" {
		role = "unknown"
	}
	if principal == "" {
		principal = identity
	}
	sum := sha256.Sum256([]byte(principal))
	return role + ":" + hex.EncodeToString(sum[:8])
}
