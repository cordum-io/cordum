package gateway

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
)

const auditChainAppendTimeout = 5 * time.Second

// auditChainSender is the gateway's local audit sink. It keeps the
// tamper-evident Redis chain populated even when external SIEM export is
// disabled, then forwards the same event to the configured exporter when one
// exists.
type auditChainSender struct {
	chainer    *audit.Chainer
	downstream audit.AuditSender
}

func newAuditChainSender(chainer *audit.Chainer, downstream audit.AuditSender) audit.AuditSender {
	if chainer == nil {
		return downstream
	}
	return &auditChainSender{
		chainer:    chainer,
		downstream: downstream,
	}
}

func (s *auditChainSender) Send(event audit.SIEMEvent) {
	if s == nil {
		return
	}
	if s.chainer != nil && strings.TrimSpace(event.TenantID) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), auditChainAppendTimeout)
		defer cancel()
		if err := s.chainer.Append(ctx, &event); err != nil {
			slog.Error("audit chain append failed",
				"event_type", event.EventType,
				"tenant_id", event.TenantID,
				"job_id", event.JobID,
				"error", err,
			)
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
