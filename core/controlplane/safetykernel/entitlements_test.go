package safetykernel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func newSafetyEntitlementResolver(t *testing.T, plan licensing.Plan, mutate func(*licensing.Entitlements)) *licensing.EntitlementResolver {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	payloadBytes, err := json.Marshal(licensing.Claims{
		Plan:         string(plan),
		Entitlements: &entitlements,
	})
	if err != nil {
		t.Fatalf("marshal license payload: %v", err)
	}

	licenseBytes, err := json.Marshal(map[string]any{
		"payload":   json.RawMessage(payloadBytes),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(licenseBytes))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func TestPolicyLoaderSkipsCustomBundlesWhenTierLimitExceeded(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer func() { _ = svc.Close() }()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"alpha": `
default_tenant: default
default_decision: allow
`,
				"secops/custom-deny": `
rules:
  - id: deny-custom
    match:
      topics:
        - job.secops.*
    decision: deny
    reason: custom policy active
`,
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config doc: %v", err)
	}

	loader := &policyLoader{
		configSvc:    svc,
		configScope:  configsvc.ScopeSystem,
		configID:     "policy",
		configKey:    "bundles",
		entitlements: newSafetyEntitlementResolver(t, licensing.PlanCommunity, nil),
	}
	policy, snapshot, customCount, err := loader.loadFragments(context.Background())
	if err != nil {
		t.Fatalf("load fragments: %v", err)
	}
	if policy == nil {
		t.Fatal("expected base policy to load")
	}
	if snapshot == "" {
		t.Fatal("expected snapshot hash")
	}
	if customCount != 0 {
		t.Fatalf("custom bundle count = %d, want 0", customCount)
	}

	resp := policy.Evaluate(config.PolicyInput{Tenant: "default", Topic: "job.secops.test"})
	if resp.Decision != "allow" {
		t.Fatalf("expected skipped custom bundle to fall back to allow, got %q", resp.Decision)
	}
}

func TestVelocityRulesBeyondTierLimitAreSkipped(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{
				ID: "velocity-ignored",
				Match: config.PolicyMatch{
					Topics: []string{"job.other.*"},
				},
				Velocity: &config.VelocityConfig{
					MaxRequests:   1,
					WindowSeconds: 60,
					Key:           "labels.session_id",
				},
				Decision: "deny",
				Reason:   "first velocity rule",
			},
			{
				ID: "velocity-capped",
				Match: config.PolicyMatch{
					Topics: []string{"job.target"},
				},
				Velocity: &config.VelocityConfig{
					MaxRequests:   1,
					WindowSeconds: 60,
					Key:           "labels.session_id",
				},
				Decision: "deny",
				Reason:   "second velocity rule should be skipped",
			},
			{
				ID: "allow-fallback",
				Match: config.PolicyMatch{
					Topics: []string{"job.target"},
				},
				Decision: "allow",
				Reason:   "fallback allow",
			},
		},
	}

	srv, _ := newTestServerWithVelocity(t, policy, "snap-tier-velocity")
	srv.entitlements = newSafetyEntitlementResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.Limits = map[string]int64{"velocity_rule_count": 1}
	})

	resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-tier-velocity",
		Topic:  "job.target",
		Tenant: "default",
		Labels: map[string]string{"session_id": "session-1"},
	})
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected allow when capped velocity rule is skipped, got %v", resp.GetDecision())
	}
	if resp.GetRuleId() != "allow-fallback" {
		t.Fatalf("expected fallback rule to fire, got %q", resp.GetRuleId())
	}
}
