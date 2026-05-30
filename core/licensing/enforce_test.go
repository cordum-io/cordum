package licensing

import (
	"encoding/json"
	"testing"
)

func TestNumericEnforcementChecksAcrossTiers(t *testing.T) {
	t.Parallel()

	type numericCheck struct {
		name      string
		limitName string
		check     func(int64, Entitlements) *TierLimitError
		configure func(*Entitlements, Plan) int64
	}

	customLimit := func(field string, community, team int64) func(*Entitlements, Plan) int64 {
		return func(entitlements *Entitlements, plan Plan) int64 {
			allowed := int64(Unlimited)
			switch plan {
			case PlanCommunity:
				allowed = community
			case PlanTeam:
				allowed = team
			case PlanEnterprise:
				allowed = Unlimited
			}
			setNamedIntField(entitlements, allowed, field)
			return allowed
		}
	}

	tests := []numericCheck{
		{
			name:      "worker limit",
			limitName: "max_workers",
			check:     CheckWorkerLimit,
			configure: func(entitlements *Entitlements, _ Plan) int64 {
				return readNamedIntField(*entitlements, "MaxWorkers")
			},
		},
		{
			name:      "job concurrency",
			limitName: "max_concurrent_jobs",
			check:     CheckJobConcurrency,
			configure: func(entitlements *Entitlements, _ Plan) int64 {
				return readNamedIntField(*entitlements, "MaxConcurrentJobs")
			},
		},
		{
			name:      "workflow steps",
			limitName: "max_workflow_steps",
			check:     CheckWorkflowSteps,
			configure: customLimit("MaxWorkflowSteps", 10, 100),
		},
		{
			name:      "active workflows",
			limitName: "max_active_workflows",
			check:     CheckActiveWorkflows,
			configure: customLimit("MaxActiveWorkflows", 2, 20),
		},
		{
			name:      "policy bundles",
			limitName: "max_policy_bundles",
			check:     CheckPolicyBundleLimit,
			configure: customLimit("MaxPolicyBundles", 5, 50),
		},
		{
			name:      "schema count",
			limitName: "max_schema_count",
			check:     CheckSchemaCount,
			configure: customLimit("MaxSchemaCount", 25, 250),
		},
		{
			name:      "rate limit rps",
			limitName: "requests_per_second",
			check:     CheckRateLimitRPS,
			configure: func(entitlements *Entitlements, _ Plan) int64 {
				return readNamedIntField(*entitlements, "RequestsPerSecond")
			},
		},
		{
			name:      "artifact size",
			limitName: "max_artifact_bytes",
			check:     CheckArtifactSize,
			configure: customLimit("MaxArtifactBytes", 1024, 10*1024),
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			for _, plan := range []Plan{PlanCommunity, PlanTeam, PlanEnterprise} {
				plan := plan
				t.Run(string(plan), func(t *testing.T) {
					t.Parallel()

					entitlements := DefaultEntitlements(plan)
					allowed := testCase.configure(&entitlements, plan)

					if allowed == Unlimited {
						if err := testCase.check(1_000_000, entitlements); err != nil {
							t.Fatalf("unlimited %s returned error: %v", testCase.limitName, err)
						}
						return
					}

					if under := maxInt64(0, allowed-1); testCase.check(under, entitlements) != nil {
						t.Fatalf("under limit should succeed for %s", testCase.limitName)
					}
					if err := testCase.check(allowed, entitlements); err != nil {
						t.Fatalf("at limit should succeed for %s: %v", testCase.limitName, err)
					}
					over := allowed + 1
					err := testCase.check(over, entitlements)
					if err == nil {
						t.Fatalf("over limit should fail for %s", testCase.limitName)
					}
					if err.Limit != testCase.limitName {
						t.Fatalf("Limit = %q, want %q", err.Limit, testCase.limitName)
					}
					if err.Current != over {
						t.Fatalf("Current = %d, want %d", err.Current, over)
					}
					if err.Allowed != allowed {
						t.Fatalf("Allowed = %d, want %d", err.Allowed, allowed)
					}
					if err.UpgradeURL != DefaultUpgradeURL {
						t.Fatalf("UpgradeURL = %q, want %q", err.UpgradeURL, DefaultUpgradeURL)
					}
				})
			}
		})
	}
}

// TestWorkflowChecksHonorLimitsMapOverride locks task-db35c079: CheckWorkflowSteps
// and CheckActiveWorkflows now route through effectiveLimit(), so an
// Entitlements.Limits override applies when the struct field is unset (0) — parity
// with the sibling checks (CheckPolicyBundleLimit, …). Mutation guard: with the
// pre-fix code (struct field read directly), a 0 field denies all positive usage
// (BUG-016), so the at-limit assertion below would fail — i.e. reverting the fix
// breaks this test.
func TestWorkflowChecksHonorLimitsMapOverride(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		check    func(int64, Entitlements) *TierLimitError
		limitKey string
		allowed  int64
	}{
		{name: "workflow steps", check: CheckWorkflowSteps, limitKey: "max_workflow_steps", allowed: 7},
		{name: "active workflows", check: CheckActiveWorkflows, limitKey: "max_active_workflows", allowed: 3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Struct field left 0 so effectiveLimit must consult the Limits map.
			ent := Entitlements{Limits: map[string]int64{tc.limitKey: tc.allowed}}

			if err := tc.check(tc.allowed, ent); err != nil {
				t.Fatalf("at limit (%d) should pass via Limits override for %s: %v", tc.allowed, tc.limitKey, err)
			}
			over := tc.allowed + 1
			err := tc.check(over, ent)
			if err == nil {
				t.Fatalf("over limit (%d) should fail via Limits override for %s", over, tc.limitKey)
			}
			if err.Limit != tc.limitKey {
				t.Fatalf("Limit = %q, want %q", err.Limit, tc.limitKey)
			}
			if err.Allowed != tc.allowed {
				t.Fatalf("Allowed = %d, want %d", err.Allowed, tc.allowed)
			}
			if err.Current != over {
				t.Fatalf("Current = %d, want %d", err.Current, over)
			}
		})
	}
}

// TestWorkflowChecksStructFieldTakesPriorityOverLimitsMap guards the ADDITIVE
// contract: a set struct field still wins over a Limits-map entry (effectiveLimit
// returns the field when non-zero), so the task-60dc3610 struct-field path is not
// replaced — only supplemented.
func TestWorkflowChecksStructFieldTakesPriorityOverLimitsMap(t *testing.T) {
	t.Parallel()

	ent := Entitlements{
		MaxWorkflowSteps:   5,
		MaxActiveWorkflows: 5,
		Limits:             map[string]int64{"max_workflow_steps": 99, "max_active_workflows": 99},
	}
	if err := CheckWorkflowSteps(6, ent); err == nil {
		t.Fatal("CheckWorkflowSteps(6) must fail at struct-field cap 5, not the Limits map's 99")
	}
	if err := CheckActiveWorkflows(6, ent); err == nil {
		t.Fatal("CheckActiveWorkflows(6) must fail at struct-field cap 5, not the Limits map's 99")
	}
}

func TestCheckApprovalMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowed   string
		requested string
		wantErr   bool
	}{
		{name: "community single allowed", allowed: string(ApprovalModeSingle), requested: string(ApprovalModeSingle)},
		{name: "community multi denied", allowed: string(ApprovalModeSingle), requested: string(ApprovalModeMulti), wantErr: true},
		{name: "team multi allowed", allowed: string(ApprovalModeMulti), requested: string(ApprovalModeMulti)},
		{name: "team custom denied", allowed: string(ApprovalModeMulti), requested: string(ApprovalModeCustom), wantErr: true},
		{name: "enterprise custom allowed", allowed: string(ApprovalModeCustom), requested: string(ApprovalModeCustom)},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := CheckApprovalMode(tc.requested, tc.allowed)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected TierLimitError, got nil")
				}
				if err.Limit != "approval_mode" {
					t.Fatalf("Limit = %q, want approval_mode", err.Limit)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckApprovalMode() error = %v, want nil", err)
			}
		})
	}
}

func TestTierLimitErrorToHTTPError(t *testing.T) {
	t.Parallel()

	httpErr := CheckWorkerLimit(4, DefaultEntitlements(PlanCommunity)).ToHTTPError()
	if httpErr.Code != "tier_limit_exceeded" {
		t.Fatalf("Code = %q, want tier_limit_exceeded", httpErr.Code)
	}
	if httpErr.UpgradeURL != DefaultUpgradeURL {
		t.Fatalf("UpgradeURL = %q, want %q", httpErr.UpgradeURL, DefaultUpgradeURL)
	}
	if httpErr.Limit != "max_workers" || httpErr.Current != 4 || httpErr.Allowed != 3 {
		t.Fatalf("unexpected HTTP error payload: %#v", httpErr)
	}

	payload, err := json.Marshal(httpErr)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(payload) == "" {
		t.Fatal("json.Marshal() returned empty payload")
	}
	if !json.Valid(payload) {
		t.Fatalf("json payload is invalid: %s", payload)
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// BUG-016 — allowed=0 must DENY any positive usage; Unlimited is the only
// "no cap" sentinel. Zero usage against zero cap still passes (current<=allowed).
func TestCheckNumericLimit_ZeroAllowedDeniesPositive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		current     int64
		allowed     int64
		shouldAllow bool
	}{
		{"zero_cap_denies_positive", 1, 0, false},
		{"zero_cap_allows_zero_usage", 0, 0, true},
		{"unlimited_allows_anything", 5, Unlimited, true},
		{"under_cap_allowed", 5, 10, true},
		{"at_cap_allowed", 10, 10, true},
		{"over_cap_denied", 11, 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkNumericLimit("test", tc.current, tc.allowed)
			if tc.shouldAllow {
				if err != nil {
					t.Fatalf("want allow (nil), got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want deny, got nil")
			}
		})
	}
}

// BUG-016 regression lock: every Plan in DefaultEntitlements must populate
// every numeric limit field to Unlimited (or a real cap). An accidental 0
// would now deny instead of "implicitly unlimited" — this test fires before
// that misconfiguration ships.
func TestDefaultEntitlements_EveryNumericFieldAllowsPositiveUsage(t *testing.T) {
	t.Parallel()
	const probe int64 = 1
	checks := []struct {
		name string
		fn   func(int64, Entitlements) *TierLimitError
	}{
		{"CheckWorkerLimit", CheckWorkerLimit},
		{"CheckJobConcurrency", CheckJobConcurrency},
		{"CheckWorkflowSteps", CheckWorkflowSteps},
		{"CheckActiveWorkflows", CheckActiveWorkflows},
		{"CheckPolicyBundleLimit", CheckPolicyBundleLimit},
		{"CheckSchemaCount", CheckSchemaCount},
		{"CheckRateLimitRPS", CheckRateLimitRPS},
		{"CheckArtifactSize", CheckArtifactSize},
	}
	for _, plan := range []Plan{PlanCommunity, PlanTeam, PlanEnterprise} {
		ent := DefaultEntitlements(plan)
		for _, c := range checks {
			t.Run(string(plan)+"/"+c.name, func(t *testing.T) {
				err := c.fn(probe, ent)
				// MaxPolicyBundles is a GATED feature, not a resource limit: the
				// Community (free) tier is intentionally capped at 0 custom policy
				// bundles (monetization gate). 0 here is DELIBERATE — enforced by the
				// loader's policyBundleLimit() Community branch AND CheckPolicyBundleLimit
				// — not the "accidental unset = deny" misconfiguration this invariant
				// guards against. So for this one combo positive usage MUST be denied.
				if plan == PlanCommunity && c.name == "CheckPolicyBundleLimit" {
					if err == nil {
						t.Fatalf("plan=community check=CheckPolicyBundleLimit probe=1: expected DENY (free tier is gated to 0 custom policy bundles), got nil")
					}
					return
				}
				if err != nil {
					t.Fatalf("plan=%s check=%s probe=1 returned %v — limit field is unset (0 = deny); set Unlimited or a positive cap in TierDefaults", plan, c.name, err)
				}
			})
		}
	}
}
