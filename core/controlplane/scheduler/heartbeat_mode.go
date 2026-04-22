package scheduler

// Heartbeat-demotion mode flag — gates the rollout of WorkerTrustState
// authority across the scheduler. See
// docs/internal/heartbeat-demotion-audit.md for the full migration
// story.
//
// Three modes, walked in this exact order during rollout:
//
//   1. authority — legacy. Every TTL gate enforces heartbeat recency
//      as the authority for visibility/dispatch. WorkerTrustState is
//      computed only by callers that explicitly opt in. This is the
//      default for backward compatibility on the first release that
//      includes this flag.
//   2. warn — session-token authority is enforced; heartbeat
//      recency is computed in parallel and a structured ERROR
//      log + heartbeat_disagreement SIEMEvent fires whenever the
//      two signals would have produced different decisions. Becomes
//      the default after one release.
//   3. telemetry — session-token authority is enforced; heartbeat
//      recency is not consulted on the decision path at all.
//
// Operators upgrade by leaving the default until they have visibility
// of disagreement events, flip to warn, watch the disagreement
// counter, then flip to telemetry once they're comfortable.

import (
	"log/slog"
	"strings"
)

// HeartbeatMode enumerates the rollout modes for the heartbeat
// demotion. String() returns the canonical lowercase form so the
// scheduler can log the active mode at boot without conditional
// formatting.
type HeartbeatMode string

const (
	HeartbeatModeAuthority HeartbeatMode = "authority"
	HeartbeatModeWarn      HeartbeatMode = "warn"
	HeartbeatModeTelemetry HeartbeatMode = "telemetry"

	// EnvHeartbeatMode is the environment variable operators set to
	// pick the active mode. Documented in the operator runbook.
	EnvHeartbeatMode = "CORDUM_HEARTBEAT_MODE"
)

// ParseHeartbeatMode normalises a raw env-var value into a canonical
// HeartbeatMode. Unknown values fall back to the default (authority)
// and emit a one-shot WARN log so operators notice the misconfig
// without seeing a flood of warnings on every dispatch.
func ParseHeartbeatMode(raw string) HeartbeatMode {
	switch HeartbeatMode(strings.ToLower(strings.TrimSpace(raw))) {
	case HeartbeatModeAuthority:
		return HeartbeatModeAuthority
	case HeartbeatModeWarn:
		return HeartbeatModeWarn
	case HeartbeatModeTelemetry:
		return HeartbeatModeTelemetry
	}
	return HeartbeatModeAuthority
}

// EnforcesSession reports whether session-token state is the authority
// for visibility/dispatch decisions under this mode. True for warn +
// telemetry; false for authority (the legacy heartbeat-gates path).
func (m HeartbeatMode) EnforcesSession() bool {
	switch m {
	case HeartbeatModeWarn, HeartbeatModeTelemetry:
		return true
	}
	return false
}

// ConsultsHeartbeat reports whether the mode still computes heartbeat
// recency on the decision path — true for authority + warn (warn
// computes both signals to compare them); false for telemetry.
func (m HeartbeatMode) ConsultsHeartbeat() bool {
	switch m {
	case HeartbeatModeAuthority, HeartbeatModeWarn:
		return true
	}
	return false
}

// EmitsDisagreement reports whether the mode emits structured
// disagreement events when the two signals diverge. Only warn does;
// authority doesn't compute the alternate signal, telemetry doesn't
// care about it.
func (m HeartbeatMode) EmitsDisagreement() bool {
	return m == HeartbeatModeWarn
}

// String returns the canonical lowercase form so callers can log the
// active mode without conditional formatting.
func (m HeartbeatMode) String() string {
	if m == "" {
		return string(HeartbeatModeAuthority)
	}
	return string(m)
}

// LogActiveMode emits a single INFO log at scheduler boot describing
// the active mode and what it means for dispatch. Helpful so anyone
// inspecting a deploy can grep the scheduler logs and confirm
// heartbeat is no longer authority.
func (m HeartbeatMode) LogActiveMode(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	switch m {
	case HeartbeatModeAuthority:
		logger.Info("heartbeat mode active",
			"mode", string(m),
			"semantics", "legacy: heartbeat recency gates visibility + dispatch",
		)
	case HeartbeatModeWarn:
		logger.Info("heartbeat mode active",
			"mode", string(m),
			"semantics", "session-token authority; disagreement events emitted when heartbeat would have produced a different decision",
		)
	case HeartbeatModeTelemetry:
		logger.Info("heartbeat mode active",
			"mode", string(m),
			"semantics", "session-token authority; heartbeat is informational only",
		)
	}
}

// HeartbeatDisagreement is the payload emitted by warn-mode dispatch
// attempts when the two signals would have produced different
// outcomes. Suitable for direct SIEMEvent.Extra serialisation.
type HeartbeatDisagreement struct {
	WorkerID         string
	Tenant           string
	JTI              string
	SessionAuthAlive bool   // what session-token authority decided
	HeartbeatAlive   bool   // what legacy heartbeat-staleness check would have decided
	Direction        string // "session_allows_heartbeat_blocks" | "session_blocks_heartbeat_allows"
}

// ClassifyDisagreement returns a HeartbeatDisagreement when the two
// signals diverge, or nil when they agree. Callers in dispatch should
// invoke this only when mode.EmitsDisagreement() is true; the
// resulting payload feeds both a structured log and a
// heartbeat_disagreement SIEMEvent.
func ClassifyDisagreement(workerID, tenant, jti string, sessionAuthAlive, heartbeatAlive bool) *HeartbeatDisagreement {
	if sessionAuthAlive == heartbeatAlive {
		return nil
	}
	dir := "session_blocks_heartbeat_allows"
	if sessionAuthAlive {
		dir = "session_allows_heartbeat_blocks"
	}
	return &HeartbeatDisagreement{
		WorkerID:         workerID,
		Tenant:           tenant,
		JTI:              jti,
		SessionAuthAlive: sessionAuthAlive,
		HeartbeatAlive:   heartbeatAlive,
		Direction:        dir,
	}
}
