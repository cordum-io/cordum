package main

import (
	"strconv"
	"strings"
)

// edgeEnforcePostureResult reports the EFFECTIVE fail posture of an enforce
// session rather than assuming enforce implies fail-closed.
//
// enforce only degrades closed when CORDUM_AGENTD_FAIL_CLOSED is enabled: the
// hook's failClosed() gate reads that env var (plus enterprise-strict), so an
// enforce session with it disabled ALLOWS the action when agentd errors or
// times out. When the operator gave no explicit value the doctor cannot see the
// session's real posture, so it reports the deciding input instead of claiming
// a fail-closed guarantee it has not verified.
func edgeEnforcePostureResult(env *edgeDoctorEnv) checkResult {
	failClosed, explicit := edgeFailClosedExplicit(env.failClosed)
	switch {
	case explicit && failClosed:
		return checkResult{
			State:  stateOK,
			Detail: "enforce degrades closed for risky/unknown actions (CORDUM_AGENTD_FAIL_CLOSED=true); fix failures before demos",
		}
	case explicit:
		return checkResult{
			State:  stateWarn,
			Detail: "enforce session is FAIL-OPEN: CORDUM_AGENTD_FAIL_CLOSED is disabled, so an agentd error or timeout will ALLOW the action",
			Fix:    "set CORDUM_AGENTD_FAIL_CLOSED=true for the session before relying on enforce",
		}
	default:
		return checkResult{
			State:  stateOK,
			Detail: "enforce posture is undetermined here: CORDUM_AGENTD_FAIL_CLOSED is not set in this shell, and it is what decides whether an agentd error denies or allows",
			Fix:    "run the doctor inside the session (or pass --fail-closed) to confirm the effective posture",
		}
	}
}

// edgeFailClosedExplicit parses an explicit CORDUM_AGENTD_FAIL_CLOSED value.
// ok is false for empty or unparseable input so callers fail safe and never
// report a fail-closed posture they cannot prove.
func edgeFailClosedExplicit(raw string) (failClosed bool, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, false
	}
	return parsed, true
}
