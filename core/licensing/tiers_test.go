package licensing

import "testing"

func TestDefaultEntitlementsByTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		plan         Plan
		maxWorkers   int64
		rps          int64
		auditDays    int64
		approvalMode string
		velocity     bool
		agentID      bool
	}{
		{
			name:         "community",
			plan:         PlanCommunity,
			maxWorkers:   3,
			rps:          500,
			auditDays:    7,
			approvalMode: string(ApprovalModeSingle),
			velocity:     false,
			agentID:      false,
		},
		{
			name:         "team",
			plan:         PlanTeam,
			maxWorkers:   25,
			rps:          2000,
			auditDays:    90,
			approvalMode: string(ApprovalModeMulti),
			velocity:     false,
			agentID:      false,
		},
		{
			name:         "enterprise",
			plan:         PlanEnterprise,
			maxWorkers:   Unlimited,
			rps:          10000,
			auditDays:    Unlimited,
			approvalMode: string(ApprovalModeCustom),
			velocity:     true,
			agentID:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entitlements := DefaultEntitlements(tc.plan)
			if got := readNamedIntField(entitlements, "MaxWorkers"); got != tc.maxWorkers {
				t.Fatalf("MaxWorkers = %d, want %d", got, tc.maxWorkers)
			}
			if got := readNamedIntField(entitlements, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS"); got != tc.rps {
				t.Fatalf("RequestsPerSecond = %d, want %d", got, tc.rps)
			}
			if got := readNamedIntField(entitlements, "AuditRetentionDays"); got != tc.auditDays {
				t.Fatalf("AuditRetentionDays = %d, want %d", got, tc.auditDays)
			}
			if got := readNamedStringField(entitlements, "ApprovalMode"); got != tc.approvalMode {
				t.Fatalf("ApprovalMode = %q, want %q", got, tc.approvalMode)
			}
			if got := readNamedBoolField(entitlements, "VelocityRules"); got != tc.velocity {
				t.Fatalf("VelocityRules = %v, want %v", got, tc.velocity)
			}
			if got := readNamedBoolField(entitlements, "AgentIdentity"); got != tc.agentID {
				t.Fatalf("AgentIdentity = %v, want %v", got, tc.agentID)
			}
		})
	}
}

func TestMergeEntitlementsUpwardOnly(t *testing.T) {
	t.Parallel()

	base := DefaultEntitlements(PlanTeam)

	var upgrade Entitlements
	setNamedIntField(&upgrade, 50, "MaxWorkers")
	setNamedIntField(&upgrade, 5000, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS")
	setNamedIntField(&upgrade, 180, "AuditRetentionDays")
	setNamedStringField(&upgrade, string(ApprovalModeCustom), "ApprovalMode")
	setNamedBoolField(&upgrade, true, "RBAC", "AdvancedRBAC")
	setNamedBoolField(&upgrade, true, "SCIM")

	got := mergeEntitlements(base, upgrade)
	if workers := readNamedIntField(got, "MaxWorkers"); workers != 50 {
		t.Fatalf("upgraded MaxWorkers = %d, want 50", workers)
	}
	if rps := readNamedIntField(got, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS"); rps != 5000 {
		t.Fatalf("upgraded RequestsPerSecond = %d, want 5000", rps)
	}
	if audit := readNamedIntField(got, "AuditRetentionDays"); audit != 180 {
		t.Fatalf("upgraded AuditRetentionDays = %d, want 180", audit)
	}
	if mode := readNamedStringField(got, "ApprovalMode"); mode != string(ApprovalModeCustom) {
		t.Fatalf("upgraded ApprovalMode = %q, want %q", mode, ApprovalModeCustom)
	}
	if !readNamedBoolField(got, "RBAC", "AdvancedRBAC") {
		t.Fatal("RBAC override was not applied")
	}
	if !readNamedBoolField(got, "SCIM") {
		t.Fatal("SCIM override was not applied")
	}

	var downgrade Entitlements
	setNamedIntField(&downgrade, 1, "MaxWorkers")
	setNamedIntField(&downgrade, 100, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS")
	setNamedIntField(&downgrade, 7, "AuditRetentionDays")
	setNamedStringField(&downgrade, string(ApprovalModeSingle), "ApprovalMode")

	got = mergeEntitlements(base, downgrade)
	if workers := readNamedIntField(got, "MaxWorkers"); workers != 25 {
		t.Fatalf("downgraded MaxWorkers = %d, want 25", workers)
	}
	if rps := readNamedIntField(got, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS"); rps != 2000 {
		t.Fatalf("downgraded RequestsPerSecond = %d, want 2000", rps)
	}
	if audit := readNamedIntField(got, "AuditRetentionDays"); audit != 90 {
		t.Fatalf("downgraded AuditRetentionDays = %d, want 90", audit)
	}
	if mode := readNamedStringField(got, "ApprovalMode"); mode != string(ApprovalModeMulti) {
		t.Fatalf("downgraded ApprovalMode = %q, want %q", mode, ApprovalModeMulti)
	}

	var unlimited Entitlements
	setNamedIntField(&unlimited, Unlimited, "MaxWorkers")
	setNamedIntField(&unlimited, Unlimited, "AuditRetentionDays")

	got = mergeEntitlements(base, unlimited)
	if workers := readNamedIntField(got, "MaxWorkers"); workers != Unlimited {
		t.Fatalf("MaxWorkers = %d, want unlimited", workers)
	}
	if audit := readNamedIntField(got, "AuditRetentionDays"); audit != Unlimited {
		t.Fatalf("AuditRetentionDays = %d, want unlimited", audit)
	}
}
