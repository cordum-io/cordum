package actiongates

import "testing"

// TestResolveFailClosedDestructiveOnTaintLookupError pins the profile-aware
// default + explicit-override precedence for CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE.
// Uses t.Setenv (so the subtests must stay sequential, not parallel). An empty
// value is treated as "unset" (falls back to the profile default).
func TestResolveFailClosedDestructiveOnTaintLookupError(t *testing.T) {
	const (
		envMode = "CORDUM_ENV"
		envProd = "CORDUM_PRODUCTION"
		envFlag = "CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE"
	)
	cases := []struct {
		name string
		mode string
		prod string
		flag string
		want bool
	}{
		{"prod_profile_flag_unset_defaults_on", "production", "", "", true},
		{"prod_via_production_bool_flag_unset_on", "", "true", "", true},
		{"dev_profile_flag_unset_defaults_off", "development", "", "", false},
		{"no_profile_flag_unset_defaults_off", "", "", "", false},
		{"explicit_false_in_prod_overrides_off", "production", "", "false", false},
		{"explicit_0_in_prod_overrides_off", "production", "", "0", false},
		{"explicit_off_in_prod_overrides_off", "production", "", "off", false},
		{"explicit_true_in_dev_overrides_on", "development", "", "true", true},
		{"explicit_on_in_dev_overrides_on", "development", "", "on", true},
		{"unrecognized_in_prod_falls_back_to_profile_on", "production", "", "maybe", true},
		{"unrecognized_in_dev_falls_back_to_profile_off", "development", "", "maybe", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envMode, tc.mode)
			t.Setenv(envProd, tc.prod)
			t.Setenv(envFlag, tc.flag)
			if got := ResolveFailClosedDestructiveOnTaintLookupError(); got != tc.want {
				t.Fatalf("ResolveFailClosedDestructiveOnTaintLookupError() = %v, want %v (mode=%q prod=%q flag=%q)",
					got, tc.want, tc.mode, tc.prod, tc.flag)
			}
		})
	}
}

// TestApproverConfiguredForTaintFailClosed pins the approver-config signal that
// switches the fail-closed no-clean-confirmation outcome between REQUIRE_HUMAN
// (approver configured) and DENY (none). Default/unset is the secure false.
func TestApproverConfiguredForTaintFailClosed(t *testing.T) {
	const envApprover = "CORDUM_MCP_TAINT_FAILCLOSED_APPROVER"
	cases := []struct {
		name string
		val  string
		want bool
	}{
		{"unset_defaults_false", "", false},
		{"true", "true", true},
		{"one", "1", true},
		{"on", "on", true},
		{"yes", "yes", true},
		{"false", "false", false},
		{"zero", "0", false},
		{"garbage", "maybe", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envApprover, tc.val)
			if got := ApproverConfiguredForTaintFailClosed(); got != tc.want {
				t.Fatalf("ApproverConfiguredForTaintFailClosed() = %v, want %v (val=%q)", got, tc.want, tc.val)
			}
		})
	}
}
