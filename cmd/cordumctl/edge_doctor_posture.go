package main

import (
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/edge/agentd"
)

// edgeFailClosedState classifies a raw CORDUM_AGENTD_FAIL_CLOSED value (or
// --fail-closed flag value) the doctor was given.
type edgeFailClosedState int

const (
	// edgeFailClosedUnset means the operator gave no value at all (empty
	// after trimming). Defaults apply; the doctor cannot see the session's
	// real posture from here.
	edgeFailClosedUnset edgeFailClosedState = iota
	// edgeFailClosedTrue means the value parsed to true.
	edgeFailClosedTrue
	// edgeFailClosedFalse means the value parsed to false.
	edgeFailClosedFalse
	// edgeFailClosedUnparseable means the value is non-empty but did not
	// match any spelling agentd itself recognizes — a likely typo/misconfig
	// that agentd would silently treat as unset/false rather than error on.
	edgeFailClosedUnparseable
)

// edgeFailClosedStateOf classifies raw using the SAME boolean spellings
// agentd honors (agentd.ParseBool: 1/0, true/false, yes/no, y/n, on/off) so
// the doctor never reports a posture agentd would not actually apply. Reusing
// agentd's parser (rather than a narrower reimplementation) is what makes
// this doctor check trustworthy: "unparseable by the doctor" now means
// "unparseable by agentd," not just "not recognized by a narrower check."
func edgeFailClosedStateOf(raw string) edgeFailClosedState {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return edgeFailClosedUnset
	}
	parsed, ok := agentd.ParseBool(trimmed)
	if !ok {
		return edgeFailClosedUnparseable
	}
	if parsed {
		return edgeFailClosedTrue
	}
	return edgeFailClosedFalse
}

// edgeEnforcePostureResult reports the effective CORDUM_AGENTD_FAIL_CLOSED
// posture of an enforce session, and whether that value is even something
// enforce mode's core protection needs.
//
// enforce mode already denies regardless of CORDUM_AGENTD_FAIL_CLOSED in the
// two paths that matter most:
//   - agentd reachable but Gateway/governance degraded: ApplyFailMode's
//     enforce branch (core/edge/agentd/fail_modes.go) denies every
//     non-known-safe action for every hook event kind, and never even reads
//     that env var.
//   - agentd unreachable from the hook for a PreToolUse event:
//     handleAgentdError (core/edge/claude/runner.go) checks enforceMode(opts)
//     directly and denies risky/unclassified actions, independent of
//     FAIL_CLOSED.
//
// CORDUM_AGENTD_FAIL_CLOSED only changes the outcome for the narrower gap
// where the local agentd PROCESS itself is unreachable AND the hook event is
// NOT PreToolUse (UserPromptSubmit/PostToolUse/PostToolUseFailure/
// ConfigChange): those are allowed through when the flag is unset/false, and
// denied when the flag is true (or CORDUM_EDGE_MODE=enterprise-strict).
//
// The doctor must not claim a broader "an agentd error or timeout will ALLOW
// the action" condition than that narrow gap, and must not send operators to
// flip a flag that enforce's core protection does not need — doing so
// previously turned a healthy enforce session into a false warning (doctor
// exit code 2).
//
// A non-empty value that fails to parse (garbage) is reported as a WARNING,
// not folded into the "unset" case: agentd would also fail to recognize it
// and silently fall back to its own default, so the operator's intent is
// unknown and the doctor should say so rather than imply "OK, defaults
// apply" for what is actually a misconfiguration.
func edgeEnforcePostureResult(env *edgeDoctorEnv) checkResult {
	switch edgeFailClosedStateOf(env.failClosed) {
	case edgeFailClosedTrue:
		return checkResult{
			State:  stateOK,
			Detail: "enforce degrades closed for every hook event, including if the local agentd process itself becomes unreachable (CORDUM_AGENTD_FAIL_CLOSED=true)",
		}
	case edgeFailClosedFalse:
		return checkResult{
			State:  stateOK,
			Detail: "enforce already denies risky/unknown PreToolUse actions and any Gateway-degraded action regardless of CORDUM_AGENTD_FAIL_CLOSED; with it disabled, only non-PreToolUse hook events are allowed through if the local agentd process itself becomes unreachable",
		}
	case edgeFailClosedUnparseable:
		return checkResult{
			State:  stateWarn,
			Detail: fmt.Sprintf("CORDUM_AGENTD_FAIL_CLOSED=%q is not a recognized boolean (agentd accepts 1/0, true/false, yes/no, y/n, on/off); agentd would also fail to parse it and silently fall back to its default", strings.TrimSpace(env.failClosed)),
			Fix:    "set CORDUM_AGENTD_FAIL_CLOSED to true or false (also accepts yes/no, y/n, on/off, 1/0), or unset it to use the default",
		}
	default: // edgeFailClosedUnset
		return checkResult{
			State:  stateOK,
			Detail: "enforce already denies risky/unknown PreToolUse actions and any Gateway-degraded action regardless of CORDUM_AGENTD_FAIL_CLOSED; that flag is not set in this shell and only changes behavior for non-PreToolUse hook events if the local agentd process itself becomes unreachable",
			Fix:    "run the doctor inside the session (or pass --fail-closed) to confirm the effective posture",
		}
	}
}
