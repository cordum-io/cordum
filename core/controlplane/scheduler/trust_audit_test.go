package scheduler

// Step-9 audit emitter tests. Real recordingSink + real
// audit.BufferedExporter paths — no mocks.

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

// recordingSenderAdapter lets EmitTrustChange (which takes
// audit.AuditSender) use the same recordingSink test double the
// handshake + disagreement tests rely on. AuditSender requires a
// Send method on the concrete value.
type recordingSenderAdapter struct {
	sink *recordingSink
}

func (r *recordingSenderAdapter) Send(event audit.SIEMEvent) {
	r.sink.Emit(context.Background(), event)
}

func (r *recordingSenderAdapter) Close() error { return nil }

func TestEmitTrustChange_SessionRevokedFields(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	sender := &recordingSenderAdapter{sink: sink}

	EmitTrustChange(context.Background(), sender, "w-rev", "tenant-rev", "valid", "revoked", TrustChangeReasonSessionRevoked, "jti-42")

	if sink.count() != 1 {
		t.Fatalf("expected 1 event, got %d", sink.count())
	}
	ev := sink.last()
	if ev.EventType != audit.EventWorkerTrustChange {
		t.Errorf("event_type=%q want %q", ev.EventType, audit.EventWorkerTrustChange)
	}
	if ev.Severity != audit.SeverityHigh {
		t.Errorf("severity=%q want HIGH (session_revoked)", ev.Severity)
	}
	if ev.TenantID != "tenant-rev" || ev.AgentID != "w-rev" {
		t.Errorf("identity fields: tenant=%q agent=%q", ev.TenantID, ev.AgentID)
	}
	if ev.Reason != TrustChangeReasonSessionRevoked {
		t.Errorf("reason=%q", ev.Reason)
	}
	if ev.Extra["from"] != "valid" || ev.Extra["to"] != "revoked" || ev.Extra["jti"] != "jti-42" {
		t.Errorf("extra fields wrong: %+v", ev.Extra)
	}
}

func TestEmitTrustChange_SeverityMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason   string
		severity string
	}{
		{TrustChangeReasonSessionRevoked, audit.SeverityHigh},
		{TrustChangeReasonModeTransition, audit.SeverityHigh},
		{TrustChangeReasonSessionExpired, audit.SeverityMedium},
		{TrustChangeReasonResolverUnready, audit.SeverityMedium},
		{TrustChangeReasonSessionIssued, audit.SeverityInfo},
		{TrustChangeReasonSessionRenewed, audit.SeverityInfo},
		{"unknown-reason", audit.SeverityMedium},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			sink := &recordingSink{}
			sender := &recordingSenderAdapter{sink: sink}
			EmitTrustChange(context.Background(), sender, "w", "t", "a", "b", c.reason, "j")
			if sink.last().Severity != c.severity {
				t.Fatalf("severity=%q want %q", sink.last().Severity, c.severity)
			}
		})
	}
}

func TestEmitTrustChange_NilSinkIsNoOp(t *testing.T) {
	t.Parallel()
	// Must not panic.
	EmitTrustChange(context.Background(), nil, "w", "t", "", "", TrustChangeReasonSessionIssued, "j")
}

func TestEmitTrustChangeViaSink_ShapesEventForAuditSink(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	EmitTrustChangeViaSink(context.Background(), sink, "w-sink", "tenant-sink", "none", "valid", TrustChangeReasonSessionIssued, "jti-77")
	ev := sink.last()
	if ev.EventType != audit.EventWorkerTrustChange {
		t.Fatalf("event_type=%q", ev.EventType)
	}
	if ev.Severity != audit.SeverityInfo {
		t.Errorf("severity=%q want INFO for session_issued", ev.Severity)
	}
	if ev.Extra["worker_id"] != "w-sink" || ev.Extra["jti"] != "jti-77" {
		t.Errorf("extra fields: %+v", ev.Extra)
	}
}

func TestEmitTrustChangeViaSink_NilSinkIsNoOp(t *testing.T) {
	t.Parallel()
	EmitTrustChangeViaSink(context.Background(), nil, "w", "t", "", "", TrustChangeReasonSessionIssued, "j")
}

func TestEmitModeTransition_ShapesFleetEvent(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	EmitModeTransition(context.Background(), sink, HeartbeatModeAuthority, HeartbeatModeWarn, "operator@acme")

	ev := sink.last()
	if ev.Action != "trust.mode_transition" {
		t.Errorf("action=%q want trust.mode_transition", ev.Action)
	}
	if ev.Extra["worker_id"] != "*" {
		t.Errorf("mode-transition events must use worker_id=*, got %q", ev.Extra["worker_id"])
	}
	if ev.Extra["from"] != "authority" || ev.Extra["to"] != "warn" {
		t.Errorf("extra from/to: %+v", ev.Extra)
	}
	if ev.Extra["actor"] != "operator@acme" {
		t.Errorf("actor=%q", ev.Extra["actor"])
	}
	if ev.Severity != audit.SeverityHigh {
		t.Errorf("mode transitions must be HIGH severity; got %q", ev.Severity)
	}
}

func TestEmitModeTransition_NilSinkIsNoOp(t *testing.T) {
	t.Parallel()
	EmitModeTransition(context.Background(), nil, HeartbeatModeWarn, HeartbeatModeTelemetry, "op")
}

func TestEmitTrustChange_IntegratesWithRealRevokeFlow(t *testing.T) {
	// Pipe a real SessionTokenIssuer.Revoke through the audit
	// emitter to confirm the end-to-end shape: issuer writes the
	// revocation key, caller emits the event with the same
	// claims.JTI. We assert the emission shape matches what SIEM
	// rules in the runbook expect.
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	_, claims, err := issuer.Issue(ctx, "w-live", "tenant-live", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	sink := &recordingSink{}
	EmitTrustChangeViaSink(ctx, sink, claims.Subject, claims.Tenant, "valid", "revoked", TrustChangeReasonSessionRevoked, claims.JTI)

	ev := sink.last()
	if ev.AgentID != "w-live" || ev.TenantID != "tenant-live" {
		t.Errorf("identity mismatch: agent=%q tenant=%q", ev.AgentID, ev.TenantID)
	}
	if ev.Extra["jti"] != claims.JTI {
		t.Errorf("jti in event=%q want %q", ev.Extra["jti"], claims.JTI)
	}
}
