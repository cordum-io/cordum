package mcp

import "testing"

func TestParseRiskTier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want RiskTier
	}{
		{"", RiskTierUnknown},
		{"low", RiskTierLow},
		{"LOW", RiskTierLow},
		{"  medium  ", RiskTierMedium},
		{"Med", RiskTierMedium},
		{"High", RiskTierHigh},
		{"critical", RiskTierCritical},
		{"CRIT", RiskTierCritical},
		{"nonsense", RiskTierHigh}, // fail-closed default
	}
	for _, tc := range cases {
		if got := ParseRiskTier(tc.in); got != tc.want {
			t.Errorf("ParseRiskTier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRiskTierOrdering(t *testing.T) {
	t.Parallel()

	if RiskTierLow >= RiskTierMedium || RiskTierMedium >= RiskTierHigh || RiskTierHigh >= RiskTierCritical {
		t.Fatalf("risk tier ordering broken")
	}
}

func TestAllows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		actor RiskTier
		tool  RiskTier
		want  bool
	}{
		{"critical_actor_any_tool", RiskTierCritical, RiskTierLow, true},
		{"critical_actor_critical_tool", RiskTierCritical, RiskTierCritical, true},
		{"high_actor_critical_tool", RiskTierHigh, RiskTierCritical, false},
		{"high_actor_high_tool", RiskTierHigh, RiskTierHigh, true},
		{"medium_actor_high_tool", RiskTierMedium, RiskTierHigh, false},
		{"low_actor_low_tool", RiskTierLow, RiskTierLow, true},
		{"unknown_actor_any_tool", RiskTierUnknown, RiskTierLow, false},
		{"unknown_tool_defaults_high", RiskTierMedium, RiskTierUnknown, false},
		{"unknown_tool_defaults_high_allow", RiskTierHigh, RiskTierUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allows(tc.actor, tc.tool); got != tc.want {
				t.Fatalf("Allows(%v, %v) = %v, want %v", tc.actor, tc.tool, got, tc.want)
			}
		})
	}
}

func TestRiskTierString(t *testing.T) {
	t.Parallel()

	cases := map[RiskTier]string{
		RiskTierLow:      "low",
		RiskTierMedium:   "medium",
		RiskTierHigh:     "high",
		RiskTierCritical: "critical",
		RiskTierUnknown:  "unknown",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("RiskTier(%d).String() = %q, want %q", tier, got, want)
		}
	}
}
