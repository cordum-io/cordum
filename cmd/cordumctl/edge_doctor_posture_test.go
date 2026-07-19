package main

import (
	"context"
	"strings"
	"testing"
)

// TestEdgeCheckPolicyModePosture pins the effective fail-open/fail-closed
// posture reported for each policy mode.
//
// Context: writeLaunchSettings currently omits DevSettingsOptions.FailClosed,
// so every generated session carries CORDUM_AGENTD_FAIL_CLOSED=false and a real
// enforce session is FAIL-OPEN. The doctor must therefore never assert
// "degrades closed" unless fail-closed is actually enabled.
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
		// wantNotContains guards DoD3: no false "degrades closed" claim.
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
			name:            "enforce with explicit fail-open warns",
			mode:            "enforce",
			failClosed:      "false",
			wantState:       stateWarn,
			wantContains:    "fail-open",
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
				t.Fatalf("detail = %q must NOT contain %q (false fail-closed claim)", got.Detail, tc.wantNotContains)
			}
			if tc.wantState != stateOK && strings.TrimSpace(got.Fix) == "" {
				t.Fatalf("non-OK state %q must carry a Fix hint, got empty (detail=%q)", got.State, got.Detail)
			}
		})
	}
}

// TestEdgeCheckPolicyModeFailClosedParsing proves the explicit-value parser
// accepts the spellings settings.json and operators actually produce, and that
// an unparseable value is treated as "not explicitly closed" rather than
// silently claiming a closed posture.
func TestEdgeCheckPolicyModeFailClosedParsing(t *testing.T) {
	closed := []string{"true", "TRUE", "True", "1"}
	for _, v := range closed {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		if got := edgeCheckPolicyMode(context.Background(), env); got.State != stateOK ||
			!strings.Contains(strings.ToLower(got.Detail), "degrades closed") {
			t.Fatalf("failClosed=%q -> state=%q detail=%q, want ok + degrades closed", v, got.State, got.Detail)
		}
	}

	open := []string{"false", "FALSE", "False", "0"}
	for _, v := range open {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		if got := edgeCheckPolicyMode(context.Background(), env); got.State != stateWarn {
			t.Fatalf("failClosed=%q -> state=%q detail=%q, want warn", v, got.State, got.Detail)
		}
	}

	// Garbage must not be read as fail-closed.
	for _, v := range []string{"yes", "maybe", "  "} {
		env := &edgeDoctorEnv{policyMode: "enforce", failClosed: v}
		got := edgeCheckPolicyMode(context.Background(), env)
		if strings.Contains(strings.ToLower(got.Detail), "degrades closed") {
			t.Fatalf("failClosed=%q must not claim degrades closed, got %q", v, got.Detail)
		}
	}
}

// TestEdgeDoctorFailClosedFlagWiring proves --fail-closed reaches the check
// through the real command path, and pins the exit-code contract: only an
// explicit fail-open warns (exit 2); the default enforce run stays exit 0.
func TestEdgeDoctorFailClosedFlagWiring(t *testing.T) {
	t.Run("explicit fail-open warns and exits 2", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json", "--fail-closed=false")...)
		if code != 2 {
			t.Fatalf("exit=%d, want 2; stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateWarn)
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

	t.Run("env var supplies the default", func(t *testing.T) {
		fx := newEdgeDoctorFixture(t)
		t.Setenv("CORDUM_AGENTD_FAIL_CLOSED", "false")
		code, stdout, _ := runEdgeDoctorForTest(t, fx.args("--json")...)
		if code != 2 {
			t.Fatalf("exit=%d, want 2 (env fail-open must warn); stdout=%s", code, stdout)
		}
		payload := decodeEdgeDoctorJSON(t, stdout)
		assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateWarn)
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
