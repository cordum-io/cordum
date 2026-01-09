package safetykernel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestCheckMCPPolicyDenies(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					DenyServers: []string{"blocked.example.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.server": "blocked.example.com",
			"mcp.tool":   "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
}

func TestCheckMCPPolicyRequiresFieldWhenAllowlistSet(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					AllowServers: []string{"github.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-2",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.tool": "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny when mcp.server missing, got %v", resp.GetDecision())
	}
}

func TestCheckAppliesEffectiveConfigDeny(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:            "job-3",
		Topic:            "job.deny",
		Tenant:           "default",
		EffectiveConfig:  []byte(`{"safety":{"denied_topics":["job.deny"]}}`),
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
	if !strings.Contains(resp.GetReason(), "denied") {
		t.Fatalf("expected denial reason, got %q", resp.GetReason())
	}
}

func TestExtractPolicyFragmentHonorsEnabled(t *testing.T) {
	if content, ok := extractPolicyFragment(map[string]any{"content": "foo", "enabled": true}); !ok || content != "foo" {
		t.Fatalf("expected enabled content")
	}
	if content, ok := extractPolicyFragment(map[string]any{"content": "bar", "enabled": false}); ok || content != "" {
		t.Fatalf("expected disabled fragment")
	}
}

func TestPolicyLoaderLoadsFragments(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	defer svc.Close()

	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"alpha": `
default_tenant: default
tenants:
  default:
    allow_topics:
      - job.*
`,
				"beta": map[string]any{
					"content": `
rules:
  - id: require-prod
    match:
      topics:
        - job.prod.*
    decision: require_approval
    reason: prod writes
`,
				},
				"disabled": map[string]any{
					"content": "tenants:\n  default:\n    deny_topics:\n      - job.disabled\n",
					"enabled": false,
				},
			},
		},
	}
	if err := svc.Set(context.Background(), doc); err != nil {
		t.Fatalf("set config doc: %v", err)
	}

	loader := &policyLoader{
		configSvc:   svc,
		configScope: configsvc.ScopeSystem,
		configID:    "policy",
		configKey:   "bundles",
	}
	policy, snapshot, err := loader.loadFragments(context.Background())
	if err != nil {
		t.Fatalf("load fragments: %v", err)
	}
	if policy == nil {
		t.Fatalf("expected policy")
	}
	if snapshot == "" {
		t.Fatalf("expected snapshot hash")
	}
	resp := policy.Evaluate(config.PolicyInput{Tenant: "default", Topic: "job.prod.test"})
	if resp.Decision != "require_approval" {
		t.Fatalf("expected require_approval got %q", resp.Decision)
	}
}

func TestEvaluateExplainSimulate(t *testing.T) {
	srv := &server{}
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}
	srv.setPolicy(policy, "snap-1")

	req := &pb.PolicyCheckRequest{
		JobId:  "job-9",
		Topic:  "job.test",
		Tenant: "default",
	}

	if resp, err := srv.Evaluate(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("evaluate expected allow: resp=%v err=%v", resp, err)
	}
	if resp, err := srv.Explain(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("explain expected allow: resp=%v err=%v", resp, err)
	}
	if resp, err := srv.Simulate(context.Background(), req); err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("simulate expected allow: resp=%v err=%v", resp, err)
	}
}

func TestListSnapshotsTracksHistory(t *testing.T) {
	srv := &server{}
	for i := 0; i < 12; i++ {
		srv.setPolicy(nil, fmt.Sprintf("snap-%d", i))
	}

	resp, err := srv.ListSnapshots(context.Background(), &pb.ListSnapshotsRequest{})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(resp.Snapshots) != 10 {
		t.Fatalf("expected 10 snapshots, got %d", len(resp.Snapshots))
	}
	if resp.Snapshots[0] != "snap-11" {
		t.Fatalf("expected latest snapshot first, got %s", resp.Snapshots[0])
	}
}

func TestEvaluateMissingTopic(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{DefaultTenant: "default"}}
	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{})
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny on missing topic")
	}
	if resp.GetReason() == "" {
		t.Fatalf("expected reason on missing topic")
	}
}

func TestPolicyLoaderFromSource(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/policy.yaml"
	content := []byte("default_tenant: default\ntenants:\n  default:\n    allow_topics:\n      - job.*\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	loader := &policyLoader{source: path}
	policy, snapshot, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if policy == nil || snapshot == "" {
		t.Fatalf("expected policy and snapshot")
	}
}

func TestNewPolicyLoaderDefaults(t *testing.T) {
	t.Setenv("SAFETY_POLICY_CONFIG_DISABLE", "1")
	loader := newPolicyLoader(nil, "")
	if loader.configSvc != nil {
		t.Fatalf("expected config service disabled")
	}
	if loader.ShouldWatch() {
		t.Fatalf("expected ShouldWatch false for empty loader")
	}
	loader.Close()

	loader = newPolicyLoader(nil, "/tmp/policy.yaml")
	if !loader.ShouldWatch() {
		t.Fatalf("expected ShouldWatch true when source set")
	}
}
