package edge

// AssuranceState names the strongest assurance level an Edge session's
// server-side facts support. It describes what the session reported, not a
// guarantee that any action was blocked.
type AssuranceState string

const (
	// AssuranceStateCompatibility: observe mode with the hook layer reported.
	// Observe mode records decisions and does not block actions.
	AssuranceStateCompatibility AssuranceState = "compatibility"
	// AssuranceStateHookMediated: an enforcing policy mode with both the hook
	// and agentd layers reported. Mediation depends on the agent calling its
	// own hook; nothing outside the agent host confirms that it does.
	AssuranceStateHookMediated AssuranceState = "hook-mediated"
	// AssuranceStateExecutorEnforced is reserved. No executor-presence fact
	// exists yet (roadmap M3), so DeriveAssuranceState never returns it.
	AssuranceStateExecutorEnforced AssuranceState = "executor-enforced"
	// AssuranceStateUnsupported: the facts are missing, unknown, degraded or
	// match no supported combination.
	AssuranceStateUnsupported AssuranceState = "unsupported"
	// AssuranceStateStale: the session is terminal or its agent heartbeat is
	// not alive, so no current assurance can be stated.
	AssuranceStateStale AssuranceState = "stale"
)

// Enforcement layer keys DeriveAssuranceState recognizes, matched exactly.
const (
	assuranceLayerHook   = "hook"
	assuranceLayerAgentd = "agentd"
)

// DeriveAssuranceState decides a session's assurance state from facts the
// server holds. It never reports a state stronger than those facts support.
//
// Of the six facts that bear on assurance, it reads four: the policy mode, the
// enforcement layers, the session status, and whether the agent heartbeat is
// alive. Managed-settings status and binary integrity are not collected per
// session, so no state relies on them. Executor presence is not modelled yet,
// so executor-enforced is reserved and never derived.
//
// The first matching rule wins:
//  1. a terminal session (EndedAt set, or status ended or failed), or a
//     heartbeat that is not alive: stale;
//  2. status degraded, or any enforcement layer other than hook and agentd:
//     unsupported;
//  3. policy mode observe with hook true: compatibility;
//  4. policy mode enforce or enterprise-strict with hook and agentd both true:
//     hook-mediated;
//  5. anything else (empty or unknown mode, a missing or false layer):
//     unsupported.
func DeriveAssuranceState(session EdgeSession, heartbeatAlive bool) AssuranceState {
	terminal := session.EndedAt != nil ||
		session.Status == SessionStatusEnded || session.Status == SessionStatusFailed
	if terminal || !heartbeatAlive {
		return AssuranceStateStale
	}
	if session.Status == SessionStatusDegraded || hasUnrecognizedAssuranceLayer(session.EnforcementLayers) {
		return AssuranceStateUnsupported
	}
	hook := session.EnforcementLayers[assuranceLayerHook]
	agentd := session.EnforcementLayers[assuranceLayerAgentd]
	switch session.PolicyMode {
	case PolicyModeObserve:
		if hook {
			return AssuranceStateCompatibility
		}
	case PolicyModeEnforce, PolicyModeEnterpriseStrict:
		if hook && agentd {
			return AssuranceStateHookMediated
		}
	}
	return AssuranceStateUnsupported
}

func hasUnrecognizedAssuranceLayer(layers EnforcementLayers) bool {
	for name := range layers {
		if name != assuranceLayerHook && name != assuranceLayerAgentd {
			return true
		}
	}
	return false
}
