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
