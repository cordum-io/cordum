package claude

import (
	"testing"
	"time"
)

// TestWriteLaunchSettingsSetsFailClosedFromPolicyMode locks in the contract
// that the generated Claude settings.json carries CORDUM_AGENTD_FAIL_CLOSED
// derived from the session policy mode: every mode EXCEPT observe must fail
// closed (enforce + enterprise-strict + any unknown/future enforce-like mode),
// while observe stays fail-open by design.
//
// Regression guard for epic-8c29308d: writeLaunchSettings previously omitted
// FailClosed from the DevSettingsOptions literal, so the flag defaulted false
// for every session and enforce silently failed OPEN when agentd was
// unreachable. Assertions use exact-string equality (not truthiness) so a
// single-character regression in the production derivation is caught.
func TestWriteLaunchSettingsSetsFailClosedFromPolicyMode(t *testing.T) {
	cases := []struct {
		name           string
		policyMode     string
		wantFailClosed string
	}{
		{name: "observe stays fail-open", policyMode: "observe", wantFailClosed: "false"},
		{name: "enforce fails closed", policyMode: "enforce", wantFailClosed: "true"},
		{name: "enterprise-strict fails closed", policyMode: "enterprise-strict", wantFailClosed: "true"},
		{name: "mixed-case Enforce fails closed (EqualFold)", policyMode: "Enforce", wantFailClosed: "true"},
		{name: "local-dev-enforce fails closed (non-observe contract)", policyMode: "local-dev-enforce", wantFailClosed: "true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := launchConfig{
				PolicyMode:          tc.policyMode,
				AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
				ApprovalWaitTimeout: 30 * time.Second,
				TenantID:            "tenant-test",
				HookCommand:         "cordum-hook",
			}
			meta := LaunchMetadata{PrincipalID: "user-1"}
			state := launchSessionState{SessionID: "sess-1", ExecutionID: "exec-1"}

			_, settings, err := writeLaunchSettings(t.TempDir(), cfg, meta, state)
			if err != nil {
				t.Fatalf("writeLaunchSettings(policyMode=%q) returned error: %v", tc.policyMode, err)
			}

			env := jsonObject(t, decodeJSONMap(t, settings)["env"])
			if got := env["CORDUM_AGENTD_FAIL_CLOSED"]; got != tc.wantFailClosed {
				t.Fatalf("policyMode=%q: env[CORDUM_AGENTD_FAIL_CLOSED] = %v, want %q", tc.policyMode, got, tc.wantFailClosed)
			}
		})
	}
}
