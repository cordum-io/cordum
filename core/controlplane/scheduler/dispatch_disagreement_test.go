package scheduler

// Step-6 disagreement-emitter test coverage. Drives the real
// Engine.recordDispatchDisagreements + emitHeartbeatDisagreement path
// via a recording audit sink, and cross-checks that DispatchGate
// produces the disagreement payload the emitter consumes.

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// newEngineWithSink assembles the smallest Engine that can exercise
// the dispatch-disagreement emitter. No mocks: jobStore, bus, safety,
// registry, strategy are all nil here because recordDispatchDisagreements
// never touches them.
func newEngineWithSink(t *testing.T) (*Engine, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	e := &Engine{}
	e.ctx, e.cancel = context.Background(), func() {}
	e.WithDispatchAuditSink(sink)
	return e, sink
}

func TestEngine_RecordDispatchDisagreements_EmitsSIEMEvent(t *testing.T) {
	t.Parallel()
	e, sink := newEngineWithSink(t)

	dis := HeartbeatDisagreement{
		WorkerID:         "worker-disagree",
		Tenant:           "tenant-warn",
		JTI:              "jti-123",
		SessionAuthAlive: true,
		HeartbeatAlive:   false,
		Direction:        "session_allows_heartbeat_blocks",
	}
	e.recordDispatchDisagreements("job-1", "job.topic.run", []HeartbeatDisagreement{dis})

	if sink.count() != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", sink.count())
	}
	ev := sink.last()
	if ev.EventType != audit.EventHeartbeatDisagreement {
		t.Errorf("event_type=%q want %q", ev.EventType, audit.EventHeartbeatDisagreement)
	}
	if ev.Severity != audit.SeverityMedium {
		t.Errorf("severity=%q want %q", ev.Severity, audit.SeverityMedium)
	}
	if ev.TenantID != "tenant-warn" || ev.AgentID != "worker-disagree" || ev.JobID != "job-1" {
		t.Errorf("identity fields incorrect: tenant=%q agent=%q job=%q", ev.TenantID, ev.AgentID, ev.JobID)
	}
	if ev.Reason != "session_allows_heartbeat_blocks" {
		t.Errorf("reason=%q want direction string", ev.Reason)
	}
	if ev.Extra["jti"] != "jti-123" {
		t.Errorf("extra.jti=%q", ev.Extra["jti"])
	}
	if ev.Extra["session_auth_alive"] != "true" || ev.Extra["heartbeat_alive"] != "false" {
		t.Errorf("extra bool fields wrong: %+v", ev.Extra)
	}
	if ev.Extra["topic"] != "job.topic.run" {
		t.Errorf("extra.topic=%q want job.topic.run", ev.Extra["topic"])
	}
}

func TestEngine_RecordDispatchDisagreements_NoOpOnEmpty(t *testing.T) {
	t.Parallel()
	e, sink := newEngineWithSink(t)
	e.recordDispatchDisagreements("job-2", "job.topic.run", nil)
	e.recordDispatchDisagreements("job-2", "job.topic.run", []HeartbeatDisagreement{})
	if sink.count() != 0 {
		t.Fatalf("no events must be emitted on empty input, got %d", sink.count())
	}
}

func TestEngine_RecordDispatchDisagreements_NilSinkStillLogs(t *testing.T) {
	// nil sink: the SIEM emission is a no-op but the slog ERROR must
	// still fire. We assert that the call returns cleanly (panic
	// would fail the test).
	t.Parallel()
	e := &Engine{}
	e.ctx, e.cancel = context.Background(), func() {}
	// NB: intentionally do not wire a sink.
	e.recordDispatchDisagreements("job-nil", "job.topic", []HeartbeatDisagreement{{
		WorkerID: "w", Tenant: "t", Direction: "session_blocks_heartbeat_allows",
	}})
}

func TestEngine_WarnModeFlowEmitsPerWorker(t *testing.T) {
	// Integration: gate produces disagreements, engine emits them.
	// Uses the real Engine + real DispatchGate + real TrustResolver +
	// real MemoryRegistry + real SessionTokenIssuer + miniredis.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 1 * time.Hour,
		Now:      clk.Now,
	})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()
	// valid + stale ⇒ session_allows_heartbeat_blocks
	if _, _, err := issuer.Issue(ctx, "w-allow", "tenant-w", "v1"); err != nil {
		t.Fatalf("issue allow: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-allow", Pool: "p"})
	// revoked + fresh ⇒ session_blocks_heartbeat_allows
	_, claims, err := issuer.Issue(ctx, "w-block", "tenant-w", "v1")
	if err != nil {
		t.Fatalf("issue block: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-block", Pool: "p"})

	// Backdate w-allow's heartbeat past the TTL (stale) while
	// leaving w-block fresh — the two rows diverge per the plan.
	reg.mu.Lock()
	reg.workers["w-allow"].lastSeen = time.Now().Add(-2 * reg.ttl)
	reg.mu.Unlock()

	sink := &recordingSink{}
	e := &Engine{registry: reg}
	e.ctx, e.cancel = context.Background(), func() {}
	e.WithDispatchGate(NewDispatchGate(resolver, HeartbeatModeWarn))
	e.WithDispatchAuditSink(sink)

	workers, dis := e.eligibleWorkers(ctx)
	e.recordDispatchDisagreements("job-warn", "job.topic", dis)
	if _, ok := workers["w-allow"]; !ok {
		t.Fatalf("w-allow must be eligible (valid session)")
	}
	if _, ok := workers["w-block"]; ok {
		t.Fatalf("w-block must not be eligible (revoked session)")
	}

	if sink.count() < 2 {
		t.Fatalf("expected >=2 SIEM events (one per disagreeing worker), got %d", sink.count())
	}
	directions := map[string]bool{}
	for _, ev := range sink.events {
		if ev.EventType != audit.EventHeartbeatDisagreement {
			t.Errorf("unexpected event_type=%q", ev.EventType)
		}
		directions[ev.Reason] = true
	}
	if !directions["session_allows_heartbeat_blocks"] || !directions["session_blocks_heartbeat_allows"] {
		t.Fatalf("expected both directions; got %+v", directions)
	}
}

func TestEngine_TelemetryModeDoesNotEmitDisagreement(t *testing.T) {
	// Telemetry mode stops computing heartbeat recency on the
	// decision path, so no disagreements are produced and no SIEM
	// events fire.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 1 * time.Hour,
		Now:      clk.Now,
	})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w1", "t", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "p"})
	// Make heartbeat stale.
	reg.mu.Lock()
	reg.workers["w1"].lastSeen = time.Now().Add(-2 * reg.ttl)
	reg.mu.Unlock()

	sink := &recordingSink{}
	e := &Engine{registry: reg}
	e.ctx, e.cancel = context.Background(), func() {}
	e.WithDispatchGate(NewDispatchGate(resolver, HeartbeatModeTelemetry))
	e.WithDispatchAuditSink(sink)

	_, dis := e.eligibleWorkers(ctx)
	if len(dis) != 0 {
		t.Fatalf("telemetry mode must not compute disagreements, got %+v", dis)
	}
	e.recordDispatchDisagreements("job-t", "job.topic", dis)
	if sink.count() != 0 {
		t.Fatalf("no events must be emitted in telemetry mode, got %d", sink.count())
	}
}

func TestEngine_EmitHeartbeatDisagreement_NilEngineNoPanic(t *testing.T) {
	t.Parallel()
	var e *Engine
	// Must not panic — defensive bounds on the receiver.
	e.emitHeartbeatDisagreement("j", "t", HeartbeatDisagreement{})
}
