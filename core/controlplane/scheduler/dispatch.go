package scheduler

// Dispatch-time worker eligibility under the heartbeat-demotion rollout.
//
// Before demotion, dispatch consulted heartbeat recency (registry TTL
// gates) as the authoritative signal for "is this worker alive enough
// to receive a job". That signal is fragile: a clock skew, a lost
// NATS packet, or a 31-second GC pause on the worker side all make a
// perfectly healthy agent look dead.
//
// DispatchGate is the thin layer that replaces the TTL gate on the
// dispatch path. It reads WorkerTrustState (session-token state +
// revocation) via the concrete TrustResolver and returns the filtered
// set the strategy should pick from. In HeartbeatModeAuthority
// (legacy) the gate degenerates to pass-through so existing deploys
// keep the old semantics until the operator flips the
// CORDUM_HEARTBEAT_MODE flag.
//
// The gate never writes, never mints tokens, never consults bus or
// cap/sdk/go directly. All state is derived — one Redis GET + one
// EXISTS per worker — so callers can invoke it per dispatch attempt.

import (
	"context"
	"log/slog"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// snapshotAllCapable is the optional interface DispatchGate looks for
// on its registry argument. Registries that implement SnapshotAll
// return every tracked worker (the heartbeat TTL filter does not
// apply), which lets session authority admit a worker whose heartbeat
// has lapsed. MemoryRegistry implements this.
type snapshotAllCapable interface {
	SnapshotAll() map[string]*pb.Heartbeat
}

// DispatchRegistry is the minimum contract DispatchGate needs from a
// registry — the same Snapshot method WorkerRegistry already exposes.
type DispatchRegistry interface {
	Snapshot() map[string]*pb.Heartbeat
}

// DispatchGate filters a registry snapshot by WorkerTrustState when
// HeartbeatMode enforces session authority. See heartbeat_mode.go for
// the flag semantics.
type DispatchGate struct {
	resolver *TrustResolver
	mode     HeartbeatMode
	logger   *slog.Logger
}

// NewDispatchGate constructs a gate for the given resolver and mode.
// A nil resolver is allowed and results in pass-through behaviour for
// the session-enforcing modes (so the gate degrades to the legacy
// heartbeat-TTL path if the operator forgets to wire a resolver).
func NewDispatchGate(resolver *TrustResolver, mode HeartbeatMode) *DispatchGate {
	return &DispatchGate{resolver: resolver, mode: mode, logger: slog.Default()}
}

// WithLogger lets callers (tests, boot wiring) override the embedded
// logger. nil is ignored.
func (g *DispatchGate) WithLogger(l *slog.Logger) *DispatchGate {
	if g == nil || l == nil {
		return g
	}
	g.logger = l
	return g
}

// Mode returns the active mode. Useful for engine boot logs and for
// tests to assert wiring.
func (g *DispatchGate) Mode() HeartbeatMode {
	if g == nil {
		return HeartbeatModeAuthority
	}
	return g.mode
}

// EnforcesSession is a shorthand that returns whether this gate will
// actually filter (true in warn + telemetry modes, false otherwise).
func (g *DispatchGate) EnforcesSession() bool {
	if g == nil {
		return false
	}
	return g.mode.EnforcesSession() && g.resolver != nil
}

// EligibleWorkers returns the dispatch-eligible snapshot for reg.
//
//   - authority mode, nil gate, or no resolver: returns reg.Snapshot()
//     untouched and zero disagreements.
//   - warn + telemetry: starts from reg.SnapshotAll() when the
//     registry supports it (heartbeat staleness ignored), falls back
//     to reg.Snapshot() otherwise. Each worker is checked against
//     WorkerTrustState and kept only if IsAlive(). In warn mode, a
//     HeartbeatDisagreement payload is appended per worker whose two
//     signals diverge.
//
// A per-worker resolver error is logged at WARN and causes the worker
// to be dropped (fail closed) — a revoked or unknown-state worker
// should not slip through on a transient Redis hiccup.
func (g *DispatchGate) EligibleWorkers(ctx context.Context, reg DispatchRegistry) (map[string]*pb.Heartbeat, []HeartbeatDisagreement) {
	if reg == nil {
		return map[string]*pb.Heartbeat{}, nil
	}
	if !g.EnforcesSession() {
		return reg.Snapshot(), nil
	}
	source := reg.Snapshot()
	if full, ok := reg.(snapshotAllCapable); ok {
		source = full.SnapshotAll()
	}
	filtered := make(map[string]*pb.Heartbeat, len(source))
	var disagreements []HeartbeatDisagreement
	computeDisagreement := g.mode.EmitsDisagreement()
	legacyAlive := g.heartbeatAliveLookup(reg)
	for id, hb := range source {
		state, err := g.resolver.ResolveTrust(ctx, id)
		if err != nil {
			g.logger.Warn("dispatch gate: trust resolve failed; worker dropped",
				"worker_id", id,
				"mode", g.mode.String(),
				"error", err,
			)
			continue
		}
		alive := state.IsAlive()
		if alive {
			filtered[id] = hb
		}
		if computeDisagreement && legacyAlive != nil {
			hbAlive := legacyAlive(id)
			if d := ClassifyDisagreement(id, state.Tenant, state.JTI, alive, hbAlive); d != nil {
				disagreements = append(disagreements, *d)
			}
		}
	}
	return filtered, disagreements
}

// IsWorkerEligible reports whether the named worker is dispatch-
// eligible. This replaces the post-pick registry.IsAlive check.
//
// legacyIsAlive is the heartbeat-TTL function that used to gate
// dispatch; it is used in two ways: (a) as the fallback when the gate
// is nil / in authority mode; (b) as the comparison signal for warn-
// mode disagreement detection. It may be nil — in which case authority
// mode defaults to true (never blocks) and warn mode doesn't emit a
// disagreement.
func (g *DispatchGate) IsWorkerEligible(ctx context.Context, workerID string, legacyIsAlive func(string) bool) (bool, *HeartbeatDisagreement) {
	if !g.EnforcesSession() {
		if legacyIsAlive == nil {
			return true, nil
		}
		return legacyIsAlive(workerID), nil
	}
	state, err := g.resolver.ResolveTrust(ctx, workerID)
	if err != nil {
		g.logger.Warn("dispatch gate: trust resolve failed; worker rejected",
			"worker_id", workerID,
			"mode", g.mode.String(),
			"error", err,
		)
		return false, nil
	}
	alive := state.IsAlive()
	if g.mode.EmitsDisagreement() && legacyIsAlive != nil {
		hb := legacyIsAlive(workerID)
		if d := ClassifyDisagreement(workerID, state.Tenant, state.JTI, alive, hb); d != nil {
			return alive, d
		}
	}
	return alive, nil
}

// heartbeatAliveLookup returns a per-worker heartbeat-alive
// predicate derived from the registry. A worker is heartbeat-alive
// iff it is present in the TTL-filtered Snapshot. We prefer a direct
// IsAlive method when the registry exposes one (atomic per-worker
// check); otherwise we fall back to a captured snapshot map.
func (g *DispatchGate) heartbeatAliveLookup(reg DispatchRegistry) func(string) bool {
	if reg == nil {
		return nil
	}
	if checker, ok := reg.(interface{ IsAlive(string) bool }); ok {
		return checker.IsAlive
	}
	snapshot := reg.Snapshot()
	return func(id string) bool {
		_, ok := snapshot[id]
		return ok
	}
}
