package main

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

func TestGenerateCILicenseVerifiesAsEnterprise(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	env, err := generateCILicense(now)
	if err != nil {
		t.Fatalf("generate CI license: %v", err)
	}
	if env.Token == "" || env.PublicKey == "" {
		t.Fatal("generated CI license environment contains an empty value")
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", env.Token)
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", env.PublicKey)
	license, err := licensing.LoadFromEnv()
	if err != nil {
		t.Fatalf("load generated license: %v", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(env.PublicKey)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	if err := license.Verify(publicKey, now); err != nil {
		t.Fatalf("verify generated license: %v", err)
	}
	if license.Payload.Plan != string(licensing.PlanEnterprise) {
		t.Fatalf("plan = %q, want enterprise", license.Payload.Plan)
	}
	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	entitlements := resolver.Entitlements()
	if resolver.ResolvedPlan() != licensing.PlanEnterprise {
		t.Fatalf("resolved plan = %q, want enterprise", resolver.ResolvedPlan())
	}
	if !entitlements.AgentIdentity {
		t.Fatal("enterprise CI license does not enable agent identity")
	}
	workersTooSmall := entitlements.MaxWorkers != licensing.Unlimited && entitlements.MaxWorkers < 20
	jobsTooSmall := entitlements.MaxConcurrentJobs != licensing.Unlimited && entitlements.MaxConcurrentJobs < 20
	if workersTooSmall || jobsTooSmall {
		t.Fatalf("CI limits too small: workers=%d concurrent=%d", entitlements.MaxWorkers, entitlements.MaxConcurrentJobs)
	}
}
