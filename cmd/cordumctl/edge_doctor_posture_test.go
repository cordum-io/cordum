package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestEdgeCheckPolicyModePosture pins the effective fail-open/fail-closed
// posture reported for each policy mode.
//
// Context: enforce mode's core protection (deny risky/unknown PreToolUse
// actions, and deny any Gateway-degraded action) does NOT depend on
// CORDUM_AGENTD_FAIL_CLOSED — core/edge/agentd/fail_modes.go's ApplyFailMode
// never reads that env var, and core/edge/claude/runner.go's handleAgentdError
// denies PreToolUse via its own enforceMode(opts) check. CORDUM_AGENTD_FAIL_CLOSED
// only changes behavior for non-PreToolUse hook events when the local agentd
// process itself is unreachable. The doctor must therefore never claim an
// unconditional "an agentd error/timeout will ALLOW the action" for enforce,
// and must not warn (exit code 2) on a healthy enforce session just because
// that flag is unset or explicitly false.
func TestEdgeCheckPolicyModePosture(t *testing.T) {
	tests := []struct {
		name string
		mode string
		// failClosed mirrors the --fail-closed flag / CORDUM_AGENTD_FAIL_CLOSED
		// env value; empty means the operator gave no explicit value.
		failClosed string
		wantState  checkState
		// wantContains is matched case-insensitively against Detail.
		wantContains string
		// wantNotContains guards against overclaiming a blanket fail-open/closed
		// condition that the actual code doesn't back up.
		wantNotContains string
	}{
		{
			name:         "enforce with explicit fail-closed reports closed",
			mode:         "enforce",
			failClosed:   "true",
			wantState:    stateOK,
			wantContains: "degrades closed",
		},
		{
			name:            "enforce with explicit fail-open is still ok — core protection does not need the flag",
			mode:            "enforce",
			failClosed:      "false",
			wantState:       stateOK,
			wantContains:    "regardless of CORDUM_AGENTD_FAIL_CLOSED",
			wantNotContains: "degrades closed",
		},
		{
			name:            "enforce without explicit value must not claim closed",
			mode:            "enforce",
			failClosed:      "",
			wantState:       stateOK,
			wantContains:    "CORDUM_AGENTD_FAIL_CLOSED",
			wantNotContains: "degrades closed",
		},
		{
			name:         "enforce with unparseable fail-closed value warns",
			mode:         "enforce",
			failClosed:   "garbage",
			wantState:    stateWarn,
			wantContains: "not a recognized boolean",
		},
		{
			name:         "observe degrades open",
			mode:         "observe",
			failClosed:   "",
			wantState:    stateOK,
			wantContains: "degrades open",
		},
		{
			name:         "enterprise-strict still warns",
			mode:         "enterprise-strict",
			failClosed:   "",
			wantState:    stateWarn,
			wantContains: "fails closed",
		},
		{
			name:      "invalid mode fails",
			mode:      "bogus",
			wantState: stateFail,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &edgeDoctorEnv{policyMode: tc.mode, failClosed: tc.failClosed}
			got := edgeCheckPolicyMode(context.Background(), env)

			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q (detail=%q)", got.State, tc.wantState, got.Detail)
			}
			detail := strings.ToLower(got.Detail)
			if tc.wantContains != "" && !strings.Contains(detail, strings.ToLower(tc.wantContains)) {
				t.Fatalf("detail = %q, want it to contain %q", got.Detail, tc.wantContains)
			}
			if tc.wantNotContains != "" && strings.Contains(detail, strings.ToLower(tc.wantNotContains)) {
				t.Fatalf("detail = %q must NOT contain %q (false claim)", got.Detail, tc.wantNotContains)
			}
			if tc.wantState != stateOK && strings.TrimSpace(got.Fix) == "" {
				t.Fatalf("non-OK state %q must carry a Fix hint, got empty (detail=%q)", got.State, got.Detail)
			}
		})
	}
}

// TestEdgeCheckPolicyModeFailClosedParsing proves the explicit-value parser
// accepts the SAME spellings agentd itself honors (agentd.ParseBool: 1/0,
// true/false, yes/no, y/n, on/off), that an unset/whitespace-only value is
// treated as "no explicit value" (not a warning), and that a genuinely
// unparseable non-empty value warns rather than silently reporting OK.
func TestEdgeCheckPolicyModeFailClosedParsing(t *testing.T) {
	closed := []string{"true", "TRUE", "True", "1", "yes", "YES", "y", "Y", "on", "ON"}
	for _, v := range closed {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		got := edgeCheckPolicyMode(context.Background(), env)
		if got.State != stateOK || !strings.Contains(strings.ToLower(got.Detail), "degrades closed") {
			t.Fatalf("failClosed=%q -> state=%q detail=%q, want ok + degrades closed", v, got.State, got.Detail)
		}
	}

	open := []string{"false", "FALSE", "False", "0", "no", "NO", "n", "N", "off", "OFF"}
	for _, v := range open {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		got := edgeCheckPolicyMode(context.Background(), env)
		// Explicit fail-open no longer warns: enforce's core PreToolUse/
		// Gateway-degraded protection does not depend on this flag.
		if got.State != stateOK {
			t.Fatalf("failClosed=%q -> state=%q detail=%q, want ok (core enforce protection is unconditional)", v, got.State, got.Detail)
		}
		if strings.Contains(strings.ToLower(got.Detail), "degrades closed") {
			t.Fatalf("failClosed=%q must not claim degrades closed, got %q", v, got.Detail)
		}
	}

	// Whitespace-only trims to empty and must be treated as "unset", not as
	// unparseable garbage.
	env := &edgeDoctorEnv{policyMode: "enforce", failClosed: "  "}
	got := edgeCheckPolicyMode(context.Background(), env)
	if got.State != stateOK {
		t.Fatalf("failClosed=%q (whitespace) -> state=%q, want ok (treated as unset)", "  ", got.State)
	}

	// Genuine garbage (not a recognized spelling in either direction) must
	// warn — agentd would also fail to parse it and silently fall back to
	// its own default, so the doctor must not claim OK/undetermined as if
	// this were a plain unset value.
	garbage := []string{"maybe", "truthy", "2", "enabled"}
	for _, v := range garbage {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		got := edgeCheckPolicyMode(context.Background(), env)
		if got.State != stateWarn {
			t.Fatalf("failClosed=%q -> state=%q detail=%q, want warn (unparseable, not unset)", v, got.State, got.Detail)
		}
		if strings.Contains(strings.ToLower(got.Detail), "degrades closed") {
			t.Fatalf("failClosed=%q must not claim degrades closed, got %q", v, got.Detail)
		}
	}
}

// TestEdgeDoctorFailClosedFlagWiring proves --fail-closed reaches the check
// through the real command path. Only a genuinely unparseable value now
// warns (exit 2); explicit true/false and the unset default are all healthy
// enforce postures (exit 0), since enforce's core protection does not depend
// on this flag.
func TestEdgeDoctorFailClosedFlagWiring(t *testing.T) {
	t.Run("explicit fail-open stays ok and exits 0", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json", "--fail-closed=false")...)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateOK)
	})

	t.Run("explicit fail-closed stays ok and exits 0", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json", "--fail-closed=true")...)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateOK)
	})

	t.Run("unparseable value warns and exits 2", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json", "--fail-closed=garbage")...)
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateWarn)
	})

	t.Run("env var supplies the default", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_API_KEY", "")
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "false")
		// Invoked directly rather than through runEdgeDoctorForTest: that helper
		// clears CORDUM_AGENTD_FAIL_CLOSED for determinism, which is exactly the
		// input under test here.
		var stdout, stderr bytes.Buffer
		code := runEdgeDoctorCmd(fx.args("--json"), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit=%d, want 0 (explicit fail-open is still a healthy enforce posture); stdout=%s", code, stdout.String())
		}
		payload := decodeEdgeDoctorJSON(t, stdout.String())
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateOK)
	})

	t.Run("no explicit value preserves exit 0", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json")...)
		if code != 0 {
			t.Fatalf("exit=%d, want 0; stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateOK)
	})
}

// TestEdgeModeImplicationWording pins the string edgeCheckAgentdStatus embeds
// in its "local agentd not reachable" detail for every policy mode,
// including an INVALID one. edgePolicyModeOrDefault only normalizes an EMPTY
// mode to "enforce" — a real invalid --policy-mode value (which
// edgeCheckPolicyMode elsewhere correctly flags as invalid) must not fall
// through to enforce wording here; it must say the mode is unknown instead.
func TestEdgeModeImplicationWording(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		failClosed      string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "observe",
			mode:         "observe",
			wantContains: []string{"observe mode degrades open"},
		},
		{
			name:         "enforce explicit true",
			mode:         "enforce",
			failClosed:   "true",
			wantContains: []string{"enforce mode denies every hook event", "CORDUM_AGENTD_FAIL_CLOSED=true"},
		},
		{
			name:            "enforce explicit false does not overclaim fail-open",
			mode:            "enforce",
			failClosed:      "false",
			wantContains:    []string{"enforce mode still denies risky/unknown PreToolUse actions"},
			wantNotContains: []string{"fail-open", "degraded actions are allowed"},
		},
		{
			name:         "enforce unset",
			mode:         "enforce",
			failClosed:   "",
			wantContains: []string{"enforce mode denies risky/unknown pretooluse actions"},
		},
		{
			name:         "enforce unparseable",
			mode:         "enforce",
			failClosed:   "garbage",
			wantContains: []string{"not a recognized boolean"},
		},
		{
			name:         "enterprise-strict",
			mode:         "enterprise-strict",
			wantContains: []string{"enterprise-strict fails closed"},
		},
		{
			name:            "invalid mode is NOT rendered as enforce",
			mode:            "bogus-mode",
			wantContains:    []string{"unknown policy mode", "bogus-mode"},
			wantNotContains: []string{"enforce mode"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &edgeDoctorEnv{policyMode: tc.mode, failClosed: tc.failClosed}
			got := strings.ToLower(edgeModeImplication(env))
			for _, want := range tc.wantContains {
				if !strings.Contains(got, strings.ToLower(want)) {
					t.Fatalf("edgeModeImplication(mode=%q, failClosed=%q) = %q, want it to contain %q", tc.mode, tc.failClosed, got, want)
				}
			}
			for _, notWant := range tc.wantNotContains {
				if strings.Contains(got, strings.ToLower(notWant)) {
					t.Fatalf("edgeModeImplication(mode=%q, failClosed=%q) = %q, must NOT contain %q", tc.mode, tc.failClosed, got, notWant)
				}
			}
		})
	}
}
