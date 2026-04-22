package scheduler

// Dispatch-gate wiring on the Engine. The Engine owns four pieces
// needed for the heartbeat-demotion rollout plus the session-token
// handshake rollout:
//
//   - dispatchGate      — WorkerTrustState-aware replacement for the
//                         legacy heartbeat-TTL filter on dispatch.
//   - trustMetrics      — Prometheus counters for trust transitions.
//   - sessionMiddleware — verifies session tokens on inbound worker
//                         packets (heartbeat / job_result / ...).
//   - dispatchAuditSink — SIEM events for heartbeat/session
//                         disagreements the gate detects in warn mode.
//
// Every builder returns the receiver so boot wiring can chain them.
// Every method is nil-safe on the receiver so unit tests can exercise
// narrow paths without standing up the full Engine.

import (
	"context"
	"strconv"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// WithDispatchGate wires the session-aware eligibility gate. A nil
// gate is allowed and disables the session-authority path (the Engine
// falls back to the legacy registry Snapshot).
func (e *Engine) WithDispatchGate(gate *DispatchGate) *Engine {
	if e == nil {
		return e
	}
	e.dispatchGate = gate
	return e
}

// WithTrustMetrics wires the Prometheus metrics bridge for dispatch
// trust transitions. nil disables the export — the scheduler still
// functions, just without the worker-trust counters.
func (e *Engine) WithTrustMetrics(m *WorkerTrustMetrics) *Engine {
	if e == nil {
		return e
	}
	e.trustMetrics = m
	return e
}

// WithSessionMiddleware wires the session-token verifier used by
// HandlePacket on heartbeat / job_result / job_cancel paths. A nil
// middleware degrades verifySessionToken to "always admit" (back-compat
// for deploys that haven't turned on the handshake yet).
func (e *Engine) WithSessionMiddleware(mw *SessionTokenMiddleware) *Engine {
	if e == nil {
		return e
	}
	e.sessionMiddleware = mw
	return e
}

// WithDispatchAuditSink wires the SIEM sink that receives
// heartbeat_disagreement events. A nil sink makes emission a no-op,
// leaving only the structured slog line.
func (e *Engine) WithDispatchAuditSink(sink AuditSink) *Engine {
	if e == nil {
		return e
	}
	e.dispatchAuditSink = sink
	return e
}

// eligibleWorkers returns the dispatch-eligible snapshot along with
// any heartbeat/session disagreements the gate detected. When the
// gate is nil or in HeartbeatModeAuthority, this degenerates to the
// registry's own Snapshot() with zero disagreements — the legacy
// path.
func (e *Engine) eligibleWorkers(ctx context.Context) (map[string]*pb.Heartbeat, []HeartbeatDisagreement) {
	if e == nil || e.registry == nil {
		return map[string]*pb.Heartbeat{}, nil
	}
	gate := e.dispatchGate
	if gate == nil {
		return e.registry.Snapshot(), nil
	}
	return gate.EligibleWorkers(ctx, e.registry)
}

// recordDispatchDisagreements emits one SIEM heartbeat_disagreement
// event per entry in the given slice. jobID / topic scope the event
// so operators can reconstruct which dispatch attempt produced the
// divergence. Empty or nil input is a no-op — the SIEM stream stays
// quiet when the gate produces no disagreements.
func (e *Engine) recordDispatchDisagreements(jobID, topic string, disagreements []HeartbeatDisagreement) {
	for _, d := range disagreements {
		e.emitHeartbeatDisagreement(jobID, topic, d)
	}
}

// emitHeartbeatDisagreement fires a single SIEM event for a worker
// whose session-token authority and heartbeat-TTL authority disagree.
// Safe on a nil receiver, a nil sink, and a zero-value disagreement.
func (e *Engine) emitHeartbeatDisagreement(jobID, topic string, d HeartbeatDisagreement) {
	if e == nil {
		return
	}
	if e.dispatchAuditSink == nil {
		return
	}
	ev := audit.SIEMEvent{
		EventType: audit.EventHeartbeatDisagreement,
		Severity:  audit.SeverityMedium,
		TenantID:  d.Tenant,
		AgentID:   d.WorkerID,
		JobID:     jobID,
		Action:    "dispatch.heartbeat_disagreement",
		Reason:    d.Direction,
		Extra: map[string]string{
			"jti":                d.JTI,
			"session_auth_alive": strconv.FormatBool(d.SessionAuthAlive),
			"heartbeat_alive":    strconv.FormatBool(d.HeartbeatAlive),
			"topic":              topic,
		},
	}
	e.dispatchAuditSink.Emit(e.ctx, ev)
}

// verifySessionToken runs an inbound worker packet through the
// session-token middleware. Returns true if the packet should be
// admitted (no middleware wired, Off mode, warn-mode missing, valid
// token), false if the middleware rejects it (enforce-mode missing,
// invalid/revoked token).
//
// msgType is a label — "heartbeat", "job_result", "job_cancel" — used
// for structured logging so operators can trace which wire path
// rejected.
func (e *Engine) verifySessionToken(packet *pb.BusPacket, workerID, msgType string) bool {
	if e == nil || e.sessionMiddleware == nil {
		return true
	}
	result := e.sessionMiddleware.Verify(e.ctx, workerID, packet)
	switch result.Verdict {
	case TokenVerdictPass, TokenVerdictWarnMissing:
		return true
	case TokenVerdictRejectMissing, TokenVerdictRejectInvalid:
		return false
	default:
		return true
	}
}
