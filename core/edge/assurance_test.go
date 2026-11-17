package edge

import (
	"strings"
	"testing"
	"time"
)

var allAssuranceStates = []AssuranceState{
	AssuranceStateCompatibility,
	AssuranceStateHookMediated,
	AssuranceStateExecutorEnforced,
	AssuranceStateUnsupported,
	AssuranceStateStale,
}

func isAssuranceState(state AssuranceState) bool {
	for _, known := range allAssuranceStates {
		if state == known {
			return true
		}
	}
	return false
}

// setLayer applies one of "absent", "false" or "true" to layers[name].
func setLayer(layers EnforcementLayers, name, value string) {
	if value != "absent" {
		layers[name] = value == "true"
	}
}

func TestDeriveAssuranceStateCombinations(t *testing.T) {
	started := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	ended := started.Add(time.Minute)
	modes := []PolicyMode{PolicyModeObserve, PolicyModeEnforce, PolicyModeEnterpriseStrict, "", "audit-only"}
	layerValues := []string{"absent", "false", "true"}
	statuses := []SessionStatus{SessionStatusRunning, SessionStatusDegraded, SessionStatusEnded}

	cases := 0
	for _, mode := range modes {
		for _, hook := range layerValues {
			for _, agentd := range layerValues {
				for _, extraLayer := range []bool{false, true} {
					for _, status := range statuses {
						for _, alive := range []bool{true, false} {
							cases++
							layers := EnforcementLayers{}
							setLayer(layers, "hook", hook)
							setLayer(layers, "agentd", agentd)
							if extraLayer {
								layers["mcp"] = true
							}
							if len(layers) == 0 {
								layers = nil
							}
							session := validEdgeSession(started)
							session.PolicyMode, session.Status, session.EnforcementLayers = mode, status, layers
							session.EndedAt = nil
							if status == SessionStatusEnded {
								session.EndedAt = &ended
							}
							got := DeriveAssuranceState(session, alive)
							name := strings.Join([]string{string(mode), "hook=" + hook, "agentd=" + agentd, string(status)}, "/")
							if extraLayer {
								name += "/extra-layer"
							}
							if !alive {
								name += "/heartbeat-lost"
							}

							if !isAssuranceState(got) {
								t.Fatalf("%s: %q is not one of the five states", name, got)
							}
							if got == AssuranceStateExecutorEnforced {
								t.Fatalf("%s: executor-enforced derived without an executor fact", name)
							}
							stale := !alive || status == SessionStatusEnded
							if stale != (got == AssuranceStateStale) {
								t.Fatalf("%s: got %q, stale=%v", name, got, stale)
							}
							enforcing := mode == PolicyModeEnforce || mode == PolicyModeEnterpriseStrict
							if enforcing && !(hook == "true" && agentd == "true") && got == AssuranceStateHookMediated {
								t.Fatalf("%s: hook-mediated without both hook and agentd true", name)
							}
							healthy := !stale && status == SessionStatusRunning && !extraLayer && hook == "true"
							switch {
							case healthy && mode == PolicyModeObserve:
								if got != AssuranceStateCompatibility {
									t.Fatalf("%s: got %q, want compatibility", name, got)
								}
							case healthy && enforcing && agentd == "true":
								if got != AssuranceStateHookMediated {
									t.Fatalf("%s: got %q, want hook-mediated", name, got)
								}
							default:
								// Missing, unknown or degraded facts: never stronger than unsupported.
								if got != AssuranceStateUnsupported && got != AssuranceStateStale {
									t.Fatalf("%s: got %q, want unsupported or stale", name, got)
								}
							}
						}
					}
				}
			}
		}
	}
	if cases != 540 {
		t.Fatalf("iterated %d combinations, want 540", cases)
	}

	// Boundaries outside the matrix: layer names match exactly, and EndedAt or
	// a failed status alone makes a session terminal.
	edges := []struct {
		name   string
		mutate func(*EdgeSession)
		want   AssuranceState
	}{
		{"case-variant layer", func(s *EdgeSession) { s.EnforcementLayers["Hook"] = true }, AssuranceStateUnsupported},
		{"padded layer", func(s *EdgeSession) { s.EnforcementLayers[" agentd"] = true }, AssuranceStateUnsupported},
		{"ended_at without terminal status", func(s *EdgeSession) { s.EndedAt = &ended }, AssuranceStateStale},
		{"failed status", func(s *EdgeSession) { s.Status = SessionStatusFailed }, AssuranceStateStale},
	}
	for _, tc := range edges {
		session := validEdgeSession(started)
		session.PolicyMode, session.Status, session.EndedAt = PolicyModeEnforce, SessionStatusRunning, nil
		session.EnforcementLayers = EnforcementLayers{"hook": true, "agentd": true}
		tc.mutate(&session)
		if got := DeriveAssuranceState(session, true); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEdgeSessionValidateRejectsUnknownAssuranceState(t *testing.T) {
	started := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	for _, state := range append([]AssuranceState{""}, allAssuranceStates...) {
		session := validEdgeSession(started)
		session.AssuranceState = state
		if err := session.Validate(); err != nil {
			t.Fatalf("assurance_state %q rejected: %v", state, err)
		}
	}
	for _, state := range []AssuranceState{"enforced", "Hook-Mediated", "stale "} {
		session := validEdgeSession(started)
		session.AssuranceState = state
		err := session.Validate()
		if err == nil || !strings.Contains(err.Error(), "assurance_state") {
			t.Fatalf("assurance_state %q: got %v, want an assurance_state error", state, err)
		}
	}
}
