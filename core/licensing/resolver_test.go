package licensing

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

var _ LicenseInfoProvider = (*EntitlementResolver)(nil)

func TestEntitlementResolverNoLicenseFallsBackToCommunity(t *testing.T) {
	t.Parallel()

	resolver := NewEntitlementResolver()
	resolver.loadFromEnv = func() (*License, error) { return nil, nil }
	resolver.publicKeyFromEnv = func() (ed25519.PublicKey, error) {
		t.Fatal("public key loader should not run when no license is present")
		return nil, nil
	}
	resolver.verify = func(*License, ed25519.PublicKey, time.Time) error {
		t.Fatal("verifier should not run when no license is present")
		return nil
	}

	resolver.Init()

	if plan := resolver.ResolvedPlan(); plan != PlanCommunity {
		t.Fatalf("ResolvedPlan = %q, want %q", plan, PlanCommunity)
	}
	info := resolver.LicenseInfo()
	if info == nil {
		t.Fatal("LicenseInfo returned nil")
	}
	if info.Mode != string(PlanCommunity) {
		t.Fatalf("Mode = %q, want %q", info.Mode, PlanCommunity)
	}
	if info.Status != "active" {
		t.Fatalf("Status = %q, want active", info.Status)
	}
	if info.Plan != "Community" {
		t.Fatalf("Plan = %q, want Community", info.Plan)
	}
	if got := limitValue(info.Limits, "max_workers"); got != 3 {
		t.Fatalf("max_workers = %d, want 3", got)
	}
	if got := limitValue(info.Limits, "requests_per_second", "rate_limit_rps", "max_requests_per_second", "rps"); got != 500 {
		t.Fatalf("requests_per_second = %d, want 500", got)
	}
	if got := limitValue(info.Limits, "audit_retention_days"); got != 7 {
		t.Fatalf("audit_retention_days = %d, want 7", got)
	}
}

// TestMergeEntitlements_PresenceAware locks the BUG-016-sibling fix: a verified
// claim's EXPLICITLY-set scalar limit is authoritative over the tier default
// (may restrict below it — security-safe), while UNSET fields keep the tier
// default (no sparse-license DENY). Claims are built via json.Unmarshal so the
// Entitlements.present set is populated exactly as on the real verified-claim path.
func TestMergeEntitlements_PresenceAware(t *testing.T) {
	t.Parallel()
	claim := func(t *testing.T, js string) Entitlements {
		t.Helper()
		var e Entitlements
		if err := json.Unmarshal([]byte(js), &e); err != nil {
			t.Fatalf("unmarshal claim: %v", err)
		}
		return e
	}

	t.Run("explicit_lower_honored", func(t *testing.T) {
		m := mergeEntitlements(DefaultEntitlements(PlanEnterprise), claim(t, `{"max_workflow_steps":1}`))
		if m.MaxWorkflowSteps != 1 {
			t.Fatalf("MaxWorkflowSteps = %d, want 1 (explicit lower must win over Unlimited default)", m.MaxWorkflowSteps)
		}
	})

	t.Run("unset_keeps_tier_default_not_zero", func(t *testing.T) {
		def := DefaultEntitlements(PlanEnterprise)
		m := mergeEntitlements(def, claim(t, `{"max_workflow_steps":1}`))
		if m.MaxWorkers != def.MaxWorkers {
			t.Fatalf("MaxWorkers = %d, want tier default %d (unset field must NOT be zeroed — sparse-DENY hazard)", m.MaxWorkers, def.MaxWorkers)
		}
		if m.MaxActiveWorkflows != def.MaxActiveWorkflows {
			t.Fatalf("MaxActiveWorkflows = %d, want tier default %d", m.MaxActiveWorkflows, def.MaxActiveWorkflows)
		}
	})

	t.Run("explicit_zero_is_deny", func(t *testing.T) {
		m := mergeEntitlements(DefaultEntitlements(PlanEnterprise), claim(t, `{"max_workflow_steps":0}`))
		if m.MaxWorkflowSteps != 0 {
			t.Fatalf("MaxWorkflowSteps = %d, want 0 (explicit 0 = deny, must be honored)", m.MaxWorkflowSteps)
		}
	})

	t.Run("explicit_unlimited_honored", func(t *testing.T) {
		m := mergeEntitlements(DefaultEntitlements(PlanCommunity), claim(t, `{"requests_per_second":-1}`))
		if m.RequestsPerSecond != Unlimited {
			t.Fatalf("RequestsPerSecond = %d, want Unlimited (%d)", m.RequestsPerSecond, Unlimited)
		}
	})

	t.Run("claim_can_still_raise", func(t *testing.T) {
		m := mergeEntitlements(DefaultEntitlements(PlanCommunity), claim(t, `{"max_workers":100}`))
		if m.MaxWorkers != 100 {
			t.Fatalf("MaxWorkers = %d, want 100 (raising above the tier default is honored)", m.MaxWorkers)
		}
	})

	t.Run("community_policy_bundles_default_preserved_when_unset", func(t *testing.T) {
		// Guards against re-fighting task-c850d8ac: Community MaxPolicyBundles=0
		// must survive a claim that does not mention it.
		m := mergeEntitlements(DefaultEntitlements(PlanCommunity), claim(t, `{"max_workers":5}`))
		if m.MaxPolicyBundles != 0 {
			t.Fatalf("MaxPolicyBundles = %d, want 0 (Community free-tier gate, unset by claim)", m.MaxPolicyBundles)
		}
	})

	for _, plan := range []Plan{PlanCommunity, PlanTeam, PlanEnterprise} {
		t.Run("explicit_lower_across_tiers/"+string(plan), func(t *testing.T) {
			m := mergeEntitlements(DefaultEntitlements(plan), claim(t, `{"max_active_workflows":2}`))
			if m.MaxActiveWorkflows != 2 {
				t.Fatalf("plan=%s MaxActiveWorkflows = %d, want 2", plan, m.MaxActiveWorkflows)
			}
		})
	}

	t.Run("in_code_override_keeps_legacy_max_merge", func(t *testing.T) {
		// An override built in-code (no JSON) has present==nil → applyPresentLimits
		// is a no-op → legacy MAX-merge preserved (1 < Community's 3 is ignored).
		m := mergeEntitlements(DefaultEntitlements(PlanCommunity), Entitlements{MaxWorkers: 1})
		if m.MaxWorkers != 3 {
			t.Fatalf("MaxWorkers = %d, want 3 (in-code override has no presence → legacy max-merge, lower value ignored)", m.MaxWorkers)
		}
	})
}

func TestEntitlementResolverInvalidLicenseFallsBackToCommunity(t *testing.T) {
	t.Parallel()

	resolver := NewEntitlementResolver()
	resolver.loadFromEnv = func() (*License, error) { return buildTestLicense(t, PlanTeam, nil), nil }
	resolver.publicKeyFromEnv = func() (ed25519.PublicKey, error) {
		return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), nil
	}
	resolver.verify = func(*License, ed25519.PublicKey, time.Time) error {
		return errors.New("bad signature")
	}

	resolver.Init()

	if plan := resolver.ResolvedPlan(); plan != PlanCommunity {
		t.Fatalf("ResolvedPlan = %q, want %q", plan, PlanCommunity)
	}
	info := resolver.LicenseInfo()
	if info.Status != "fallback" {
		t.Fatalf("Status = %q, want fallback", info.Status)
	}
}

func TestEntitlementResolverInitMergesOverridesAndBuildsLicenseInfo(t *testing.T) {
	t.Parallel()

	license := buildTestLicense(t, PlanTeam, func(entitlements *Entitlements) {
		setNamedIntField(entitlements, 50, "MaxWorkers")
		setNamedIntField(entitlements, 5000, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS")
		setNamedIntField(entitlements, 180, "AuditRetentionDays")
		setNamedIntField(entitlements, 5, "MaxConcurrentJobs")
		setNamedStringField(entitlements, string(ApprovalModeCustom), "ApprovalMode")
		setNamedBoolField(entitlements, true, "RBAC", "AdvancedRBAC")
		setNamedBoolField(entitlements, true, "SCIM")
	})
	payload := writableLicensePayload(t, license)
	setNamedStringField(payload, "org-acme", "OrgID")
	setNamedStringField(payload, "lic-123", "LicenseID")
	setNamedStringField(payload, "on_prem", "DeploymentType")
	setNamedStringField(payload, "2026-04-01T00:00:00Z", "IssuedAt")
	setNamedStringField(payload, "2026-04-01T00:00:00Z", "NotBefore")
	setNamedStringField(payload, "2027-04-01T00:00:00Z", "ExpiresAt")

	resolver := NewEntitlementResolver()
	resolver.loadFromEnv = func() (*License, error) { return license, nil }
	resolver.publicKeyFromEnv = func() (ed25519.PublicKey, error) {
		return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), nil
	}
	resolver.verify = func(*License, ed25519.PublicKey, time.Time) error { return nil }

	resolver.Init()

	if plan := resolver.ResolvedPlan(); plan != PlanTeam {
		t.Fatalf("ResolvedPlan = %q, want %q", plan, PlanTeam)
	}
	entitlements := resolver.Entitlements()
	if workers := readNamedIntField(entitlements, "MaxWorkers"); workers != 50 {
		t.Fatalf("MaxWorkers = %d, want 50", workers)
	}
	if rps := readNamedIntField(entitlements, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS"); rps != 5000 {
		t.Fatalf("RequestsPerSecond = %d, want 5000", rps)
	}
	if audit := readNamedIntField(entitlements, "AuditRetentionDays"); audit != 180 {
		t.Fatalf("AuditRetentionDays = %d, want 180", audit)
	}
	if jobs := readNamedIntField(entitlements, "MaxConcurrentJobs"); jobs != 25 {
		t.Fatalf("MaxConcurrentJobs = %d, want team floor 25", jobs)
	}
	if mode := readNamedStringField(entitlements, "ApprovalMode"); mode != string(ApprovalModeCustom) {
		t.Fatalf("ApprovalMode = %q, want %q", mode, ApprovalModeCustom)
	}
	if !readNamedBoolField(entitlements, "RBAC", "AdvancedRBAC") {
		t.Fatal("RBAC override was not preserved")
	}
	if !readNamedBoolField(entitlements, "SCIM") {
		t.Fatal("SCIM override was not preserved")
	}

	info := resolver.LicenseInfo()
	if info == nil {
		t.Fatal("LicenseInfo returned nil")
	}
	if info.Mode != string(PlanTeam) {
		t.Fatalf("Mode = %q, want %q", info.Mode, PlanTeam)
	}
	if info.Plan != "Team" {
		t.Fatalf("Plan = %q, want Team", info.Plan)
	}
	if info.OrgID != "org-acme" || info.LicenseID != "lic-123" {
		t.Fatalf("unexpected license identifiers: %#v", info)
	}
	if got := limitValue(info.Limits, "max_workers"); got != 50 {
		t.Fatalf("license info max_workers = %d, want 50", got)
	}
	if got := limitValue(info.Limits, "requests_per_second", "rate_limit_rps", "max_requests_per_second", "rps"); got != 5000 {
		t.Fatalf("license info requests_per_second = %d, want 5000", got)
	}
	if !slices.Contains(info.Features, "rbac") {
		t.Fatalf("expected rbac in features, got %#v", info.Features)
	}
	if !slices.Contains(info.Features, "scim") {
		t.Fatalf("expected scim in features, got %#v", info.Features)
	}
}

func TestEntitlementResolverConcurrentReads(t *testing.T) {
	t.Parallel()

	license := buildTestLicense(t, PlanEnterprise, func(entitlements *Entitlements) {
		setNamedBoolField(entitlements, true, "RBAC", "AdvancedRBAC")
		setNamedBoolField(entitlements, true, "SCIM")
	})

	resolver := NewEntitlementResolver()
	resolver.loadFromEnv = func() (*License, error) { return license, nil }
	resolver.publicKeyFromEnv = func() (ed25519.PublicKey, error) {
		return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), nil
	}
	resolver.verify = func(*License, ed25519.PublicKey, time.Time) error { return nil }
	resolver.Init()

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if plan := resolver.ResolvedPlan(); plan != PlanEnterprise {
					t.Errorf("ResolvedPlan = %q, want %q", plan, PlanEnterprise)
				}
				info := resolver.LicenseInfo()
				if info == nil {
					t.Error("LicenseInfo returned nil")
					return
				}
				if info.Mode != string(PlanEnterprise) {
					t.Errorf("Mode = %q, want %q", info.Mode, PlanEnterprise)
				}
				if workers := readNamedIntField(resolver.Entitlements(), "MaxWorkers"); workers != Unlimited {
					t.Errorf("MaxWorkers = %d, want unlimited", workers)
				}
			}
		}()
	}
	wg.Wait()
}

func buildTestLicense(t *testing.T, plan Plan, mutate func(*Entitlements)) *License {
	t.Helper()

	license := &License{}
	payload := writableLicensePayload(t, license)
	setNamedStringField(payload, string(plan), "Tier", "Plan")

	entitlements := DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	field := namedField(payload, "Entitlements")
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("license payload is missing writable Entitlements field")
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}
	value := reflect.ValueOf(entitlements)
	if !value.Type().AssignableTo(field.Type()) {
		t.Fatalf("Entitlements type %s is not assignable to payload field %s", value.Type(), field.Type())
	}
	field.Set(value)
	return license
}

func writableLicensePayload(t *testing.T, license *License) any {
	t.Helper()

	root := reflect.ValueOf(license)
	if !root.IsValid() || root.Kind() != reflect.Pointer || root.IsNil() {
		t.Fatal("license must be a non-nil pointer")
	}
	root = root.Elem()
	for _, name := range []string{"Payload", "Claims"} {
		field := root.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				field.Set(reflect.New(field.Type().Elem()))
			}
			return field.Interface()
		}
		if field.CanAddr() {
			return field.Addr().Interface()
		}
	}
	return license
}

func readNamedIntField(target any, names ...string) int64 {
	for _, name := range names {
		field := readableNamedField(target, name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return field.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return int64(field.Uint())
		}
	}
	return 0
}

func readNamedStringField(target any, names ...string) string {
	for _, name := range names {
		field := readableNamedField(target, name)
		if field.IsValid() && field.Kind() == reflect.String {
			return field.String()
		}
	}
	return ""
}

func readNamedBoolField(target any, names ...string) bool {
	for _, name := range names {
		field := readableNamedField(target, name)
		if field.IsValid() && field.Kind() == reflect.Bool {
			return field.Bool()
		}
	}
	return false
}

func readableNamedField(target any, name string) reflect.Value {
	value := indirectValue(reflect.ValueOf(target))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return indirectValue(value.FieldByName(name))
}

func limitValue(limits map[string]int64, names ...string) int64 {
	for _, name := range names {
		if value, ok := limits[name]; ok {
			return value
		}
	}
	return 0
}
