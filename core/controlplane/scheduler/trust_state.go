package scheduler

// Worker trust state — the authority signal that demotes the
// heartbeat to telemetry. Built on top of the SessionTokenIssuer
// shipped by task-66b8fb92.
//
// A worker is "alive" for the purpose of dispatch and /api/v1/workers
// visibility iff its session token is valid AND not revoked. Heartbeat
// recency may inform the dashboard, Prometheus gauges, and the
// load-balancing strategy, but never the visibility/dispatch decision.
//
// The state is derived on demand from:
//
//   1. The per-agent active-token record at session:worker:<agent_id>
//      (written by SessionTokenIssuer.Issue) — gives us the JTI and
//      absolute expiry of the currently-trusted token.
//   2. The per-tenant revocation marker at session:revoked:<tenant>:<jti>
//      (written by SessionTokenIssuer.Revoke / RevokeByAgent) — flips
//      a worker to "revoked" until natural expiry sweeps the marker.
//
// Both keys are populated and TTL-managed by SessionTokenIssuer, so
// the trust-state resolver is a pure read path: no writes, no token
// minting, no signature verification. This keeps callers cheap (one
// or two Redis GETs per dispatch decision) and ensures the
// authoritative state lives in one place.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/redis/go-redis/v9"
)

// TrustReason enumerates why a worker is or isn't trusted. The
// resolver populates exactly one Reason per call so callers logging
// or auditing the decision can pin a stable string.
const (
	TrustReasonValid        = "valid"
	TrustReasonNoSession    = "no_session"
	TrustReasonExpired      = "session_expired"
	TrustReasonRevoked      = "session_revoked"
	TrustReasonStoreUnready = "trust_store_unready"
)

// WorkerTrustState is the authoritative trust signal for a worker.
// Returned by ResolveTrust.
type WorkerTrustState struct {
	// SessionValid is true iff the worker has a current, non-expired,
	// non-revoked session token on file.
	SessionValid bool `json:"session_valid"`

	// SessionExp is the absolute expiry of the active session token,
	// or the zero value when no session is on file.
	SessionExp time.Time `json:"session_exp,omitempty"`

	// RevokedAt is non-nil when the active session has been
	// explicitly revoked. Carries the wall-clock time the resolver
	// observed the revocation (the marker key's TTL gives us a
	// bound on when revocation happened, not the exact time).
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	// Reason is one of the TrustReason* constants explaining the
	// SessionValid value. Stable for auditing and structured logging.
	Reason string `json:"reason"`

	// JTI is the JWT ID of the active session token, when one is on
	// file. Useful for cross-referencing audit events.
	JTI string `json:"jti,omitempty"`

	// Tenant is the owning tenant of the active session token.
	Tenant string `json:"tenant,omitempty"`
}

// IsAlive returns true when the worker is trusted enough to receive
// dispatch. Mirrors the registry's previous IsAlive semantics so
// existing callers can swap their TTL gate for a trust check with
// minimal churn.
func (s WorkerTrustState) IsAlive() bool {
	return s.SessionValid && s.RevokedAt == nil
}

// TrustResolver reads WorkerTrustState from the session-token store.
// Wraps the Redis client so callers don't need to know the key
// layout. Safe for concurrent use; Redis client handles locking.
type TrustResolver struct {
	redis redis.UniversalClient
	now   func() time.Time
}

// NewTrustResolver constructs a TrustResolver. nil client is allowed
// (unit tests + dev deploys); ResolveTrust returns
// TrustReasonStoreUnready in that mode.
func NewTrustResolver(rdb redis.UniversalClient) *TrustResolver {
	return &TrustResolver{redis: rdb, now: time.Now}
}

// WithClock lets tests inject a deterministic clock.
func (r *TrustResolver) WithClock(now func() time.Time) *TrustResolver {
	if r == nil || now == nil {
		return r
	}
	r.now = now
	return r
}

// ResolveTrust returns the current trust state for agentID. Pure read
// — never writes, never mints tokens, never verifies signatures. The
// authority is whatever SessionTokenIssuer.Issue / Revoke have
// written to the underlying store.
func (r *TrustResolver) ResolveTrust(ctx context.Context, agentID string) (WorkerTrustState, error) {
	if r == nil || r.redis == nil {
		return WorkerTrustState{Reason: TrustReasonStoreUnready}, nil
	}
	if strings.TrimSpace(agentID) == "" {
		return WorkerTrustState{Reason: TrustReasonNoSession}, errors.New("scheduler: trust resolve requires agent id")
	}
	rec, err := r.loadActiveRecord(ctx, agentID)
	if err != nil {
		return WorkerTrustState{Reason: TrustReasonStoreUnready}, err
	}
	if rec == nil {
		return WorkerTrustState{Reason: TrustReasonNoSession}, nil
	}
	exp := time.Unix(rec.ExpUnix, 0).UTC()
	state := WorkerTrustState{
		SessionExp: exp,
		JTI:        rec.JTI,
		Tenant:     rec.Tenant,
	}
	if !exp.After(r.now().UTC()) {
		state.Reason = TrustReasonExpired
		return state, nil
	}
	revoked, err := r.checkRevocation(ctx, rec.Tenant, rec.JTI)
	if err != nil {
		return WorkerTrustState{Reason: TrustReasonStoreUnready, JTI: rec.JTI, Tenant: rec.Tenant, SessionExp: exp}, err
	}
	if revoked {
		now := r.now().UTC()
		state.RevokedAt = &now
		state.Reason = TrustReasonRevoked
		return state, nil
	}
	state.SessionValid = true
	state.Reason = TrustReasonValid
	return state, nil
}

// ResolveTrustBatch returns a state map for the given agent IDs. Useful
// when filtering a worker snapshot in one go — the underlying calls
// pipeline cleanly because each lookup is independent.
func (r *TrustResolver) ResolveTrustBatch(ctx context.Context, agentIDs []string) (map[string]WorkerTrustState, error) {
	out := make(map[string]WorkerTrustState, len(agentIDs))
	for _, id := range agentIDs {
		state, err := r.ResolveTrust(ctx, id)
		if err != nil {
			// Surface the first hard error but continue collecting so
			// a single bad ID doesn't blank the whole pool.
			out[id] = state
			return out, fmt.Errorf("resolve trust %q: %w", id, err)
		}
		out[id] = state
	}
	return out, nil
}

func (r *TrustResolver) loadActiveRecord(ctx context.Context, agentID string) (*activeRecord, error) {
	raw, err := r.redis.Get(ctx, workerKey(agentID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("scheduler: trust resolver get session record: %w", err)
	}
	rec, perr := parseActiveRecord(raw)
	if perr != nil {
		return nil, fmt.Errorf("scheduler: trust resolver parse session record: %w", perr)
	}
	return rec, nil
}

func (r *TrustResolver) checkRevocation(ctx context.Context, tenant, jti string) (bool, error) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(jti) == "" {
		return false, nil
	}
	res, err := r.redis.Exists(ctx, revokedKey(tenant, jti)).Result()
	if err != nil {
		return false, fmt.Errorf("scheduler: trust resolver check revocation: %w", err)
	}
	return res > 0, nil
}

// TrustChangeReason enumerates the admissible "why" strings on a
// worker_trust_change audit event. Keeping these constants prevents
// drift between emission sites and SIEM correlation rules.
const (
	TrustChangeReasonSessionIssued   = "session_issued"
	TrustChangeReasonSessionRenewed  = "session_renewed"
	TrustChangeReasonSessionRevoked  = "session_revoked"
	TrustChangeReasonSessionExpired  = "session_expired"
	TrustChangeReasonModeTransition  = "heartbeat_mode_transition"
	TrustChangeReasonResolverUnready = "trust_store_unready"
)

// EmitTrustChange records a worker_trust_change SIEMEvent on sink.
// Accepts (workerID, tenant, from, to, reason, jti). Keeps emission
// sites terse: call sites only carry state + reason, all canonical
// event shaping happens here.
//
// sink may be nil — the call becomes a no-op so production code
// doesn't need to nil-guard every call site (test deploys, non-
// critical scheduler instances, etc.).
func EmitTrustChange(ctx context.Context, sink audit.AuditSender, workerID, tenant, from, to, reason, jti string) {
	EmitTrustChangeWithActor(ctx, sink, workerID, tenant, from, to, reason, jti, "")
}

// EmitTrustChangeWithActor is the actor-enriched variant. Gateway
// revoke paths use this so the SIEM event carries the human operator
// who initiated the action (Identity + Extra.actor). The canonical
// shape (from/to/reason/jti + severity routing) is identical to
// EmitTrustChange.
func EmitTrustChangeWithActor(ctx context.Context, sink audit.AuditSender, workerID, tenant, from, to, reason, jti, actor string) {
	if sink == nil {
		return
	}
	extra := map[string]string{
		"worker_id": workerID,
		"tenant":    tenant,
		"from":      from,
		"to":        to,
		"reason":    reason,
		"jti":       jti,
	}
	if actor != "" {
		extra["actor"] = actor
	}
	ev := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventWorkerTrustChange,
		Severity:  severityForTrustChange(reason),
		TenantID:  tenant,
		AgentID:   workerID,
		Action:    "trust.change",
		Reason:    reason,
		Extra:     extra,
	}
	if actor != "" {
		ev.Identity = actor
	}
	sink.Send(ev)
	_ = ctx
}

// EmitTrustChangeViaSink is the AuditSink-shaped variant of
// EmitTrustChange. HandshakeService and Engine already hold an
// AuditSink, so this lets them emit without a second interface.
func EmitTrustChangeViaSink(ctx context.Context, sink interface {
	Emit(ctx context.Context, event audit.SIEMEvent)
}, workerID, tenant, from, to, reason, jti string) {
	if sink == nil {
		return
	}
	severity := severityForTrustChange(reason)
	sink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventWorkerTrustChange,
		Severity:  severity,
		TenantID:  tenant,
		AgentID:   workerID,
		Action:    "trust.change",
		Reason:    reason,
		Extra: map[string]string{
			"worker_id": workerID,
			"tenant":    tenant,
			"from":      from,
			"to":        to,
			"reason":    reason,
			"jti":       jti,
		},
	})
}

// EmitModeTransition records a scheduler-wide mode change. Emits with
// worker_id="*" because the event describes the whole fleet; dashboards
// can still correlate to per-worker trust changes via the reason string.
func EmitModeTransition(ctx context.Context, sink interface {
	Emit(ctx context.Context, event audit.SIEMEvent)
}, from, to HeartbeatMode, actor string) {
	if sink == nil {
		return
	}
	sink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventWorkerTrustChange,
		Severity:  audit.SeverityHigh,
		Action:    "trust.mode_transition",
		Reason:    TrustChangeReasonModeTransition,
		Extra: map[string]string{
			"worker_id": "*",
			"from":      from.String(),
			"to":        to.String(),
			"reason":    TrustChangeReasonModeTransition,
			"actor":     actor,
		},
	})
}

// severityForTrustChange maps a reason to a SIEM severity so alerting
// rules can route by severity without pattern-matching the reason.
func severityForTrustChange(reason string) string {
	switch reason {
	case TrustChangeReasonSessionRevoked, TrustChangeReasonModeTransition:
		return audit.SeverityHigh
	case TrustChangeReasonSessionExpired, TrustChangeReasonResolverUnready:
		return audit.SeverityMedium
	case TrustChangeReasonSessionIssued, TrustChangeReasonSessionRenewed:
		return audit.SeverityInfo
	default:
		return audit.SeverityMedium
	}
}
