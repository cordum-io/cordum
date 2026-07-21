package scheduler

// CAP-PRODUCTION scheduler-side primitives (task-a13f83fa). Freezes the
// threat-model contract in production_profile_red_test.go: authoritative
// identity binding (step-9), atomic dispatch/attempt fencing (step-10), and
// compensation fail-closed-on-safety-unavailable (step-11).

import (
	"errors"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/capprofile"
)

var (
	ErrProductionIdentityMismatch    = errors.New("scheduler: authoritative identity mismatch")
	ErrStaleDispatchEvent            = errors.New("scheduler: stale or unauthorized dispatch event")
	ErrSafetyUnavailable             = errors.New("scheduler: safety decision unavailable")
	ErrProductionMissingSafety       = errors.New("scheduler: CAP-PRODUCTION requires a configured Safety Kernel")
	ErrProductionFailOpenConfigured  = errors.New("scheduler: CAP-PRODUCTION forbids fail-open input/output safety modes")
	ErrProductionIdentityDisabled    = errors.New("scheduler: CAP-PRODUCTION identity enforcement is disabled")
	ErrProductionHandshakeDisabled   = errors.New("scheduler: CAP-PRODUCTION requires handshake enforce mode")
	ErrProductionMissingOutputSafety = errors.New("scheduler: CAP-PRODUCTION requires output safety")
)

// ValidateProductionStartup rejects an Engine configuration that would
// violate CAP-PRODUCTION's owned invariants. Tenant fail-open overrides are
// unreachable while production identity enforcement is active; the engine
// does not rely on enumerating a dynamic tenant configuration store.
func (e *Engine) ValidateProductionStartup(readiness ...capprofile.Readiness) error {
	if e == nil || e.safety == nil {
		return ErrProductionMissingSafety
	}
	if !e.productionIdentity.Load() {
		return ErrProductionIdentityDisabled
	}
	if e.sessionMiddleware == nil || e.sessionMiddleware.Mode() != HandshakeModeEnforce {
		return ErrProductionHandshakeDisabled
	}
	if e.outputSafety == nil || !e.outputSafetyEnabled.Load() {
		return ErrProductionMissingOutputSafety
	}
	if e.inputFailOpen.Load() || e.asyncFailOpen.Load() {
		return ErrProductionFailOpenConfigured
	}
	for _, state := range readiness {
		if err := state.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ProductionSession is the authenticated transport/session identity a raw
// packet must be checked against. It is derived from the actual NATS
// session/subject binding, never from packet payload.
type ProductionSession struct {
	TenantID string
	Subject  string
}

// ValidateProductionIdentity rejects a packet whose payload identity
// disagrees with the authenticated session. The session is authoritative;
// a mismatch anywhere (job request tenant, sender-vs-subject) fails closed.
func ValidateProductionIdentity(packet *agentv1.BusPacket, session ProductionSession) error {
	if packet == nil || session.TenantID == "" || session.Subject == "" {
		return ErrProductionIdentityMismatch
	}
	if packet.GetSenderId() != "" && packet.GetSenderId() != session.Subject {
		return ErrProductionIdentityMismatch
	}
	if req := packet.GetJobRequest(); req != nil {
		if req.GetTenantId() != "" && req.GetTenantId() != session.TenantID {
			return ErrProductionIdentityMismatch
		}
		if identity := req.GetIdentity(); identity != nil && identity.GetTenantId() != "" &&
			identity.GetTenantId() != session.TenantID {
			return ErrProductionIdentityMismatch
		}
	}
	return nil
}

// DispatchFence identifies one physical dispatch attempt. An event (Result/
// Progress/Cancel) is only honored when it matches the CURRENT fence exactly
// — stale attempts, future attempts, and wrong-worker events are all
// rejected before any side effect (DoD #4).
type DispatchFence struct {
	DispatchID string
	Attempt    int
	WorkerID   string
}

func ValidateDispatchEvent(current, event DispatchFence) error {
	if current.DispatchID == "" || current.DispatchID != event.DispatchID ||
		current.Attempt != event.Attempt || current.WorkerID != event.WorkerID {
		return ErrStaleDispatchEvent
	}
	return nil
}

// ValidateCompensationSafety fails closed whenever the safety decision could
// not be obtained (checkErr non-nil, or unavailable explicitly signaled) or
// denies. It never proceeds on error (DoD #5 / the saga.go
// "proceeding" bug this step exists to close).
func ValidateCompensationSafety(decision *SafetyDecisionRecord, checkErr error, unavailable bool) error {
	if checkErr != nil || unavailable {
		return ErrSafetyUnavailable
	}
	if decision == nil {
		return ErrSafetyUnavailable
	}
	if decision.Decision == SafetyDeny {
		return errors.New("scheduler: compensation denied by policy")
	}
	return nil
}
