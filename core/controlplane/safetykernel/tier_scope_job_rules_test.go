package safetykernel

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// A job's labels come from the client, so a label that declares an MCP call
// (mcp.server, mcp_tool, mcpAction, ...) must not change which rules the job
// is checked against.
func TestJobMCPLabelsDoNotSkipJobRules(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant: "default", DefaultDecision: "deny",
		Tenants: map[string]config.TenantPolicy{"default": {AllowTopics: []string{"job.*"}}},
		Rules: []config.PolicyRule{
			{ID: "deny-payments", Decision: "deny", Match: config.PolicyMatch{Topics: []string{"job.payments.*"}}},
			{ID: "approve-deploys", Decision: "require_approval", Match: config.PolicyMatch{Topics: []string{"job.deploy.*"}}},
			{ID: "allow-reports", Decision: "allow", Match: config.PolicyMatch{Topics: []string{"job.reports.*"}}},
		},
	}
	srv := &server{cacheTTL: 5 * time.Minute, cache: map[string]cacheEntry{}, cacheMaxSize: 100}
	if err := srv.setPolicy(context.Background(), policy, "snap-job-rules"); err != nil {
		t.Fatalf("setPolicy: %v", err)
	}
	baseline := map[string]pb.DecisionType{
		"job.payments.transfer": pb.DecisionType_DECISION_TYPE_DENY,
		"job.deploy.prod":       pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		"job.reports.daily":     pb.DecisionType_DECISION_TYPE_ALLOW,
		"job.unlisted.topic":    pb.DecisionType_DECISION_TYPE_DENY, // default_decision
	}
	mcpLabels := []string{
		"", // no MCP label: the baseline itself
		"mcp.server", "mcp_server", "mcpServer",
		"mcp.tool", "mcp_tool", "mcpTool",
		"mcp.resource", "mcp_resource", "mcpResource",
		"mcp.action", "mcp_action", "mcpAction",
	}
	n := 0
	for topic, want := range baseline {
		for _, label := range mcpLabels {
			n++
			labels := map[string]string{"_source": "api"}
			if label != "" {
				labels[label] = "anything"
			}
			resp, err := srv.evaluate(context.Background(), &pb.PolicyCheckRequest{
				JobId: "job-" + string(rune('a'+n%26)) + string(rune('a'+n/26)), Topic: topic, Tenant: "default", PrincipalId: "user-1",
				Labels: labels, Meta: &pb.JobMetadata{TenantId: "default", ActorId: "user-1"},
			}, "check")
			if err != nil {
				t.Fatalf("%s label %q: evaluate: %v", topic, label, err)
			}
			if resp.GetDecision() != want {
				t.Errorf("%s with label %q = %v (rule %q), want %v", topic, label, resp.GetDecision(), resp.GetRuleId(), want)
			}
		}
	}
}

// Rules with match.mcp lists still apply to a job that declares an MCP call,
// in the order the policy lists them with every other rule. A plain job does
// not see them: on this release line a match.mcp rule selects the calls its
// lists permit, and it would also match a request with no MCP labels.
func TestJobMCPRulesKeepAuthoredOrder(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant: "default", DefaultDecision: "deny",
		Rules: []config.PolicyRule{
			{ID: "mcp-deny-delete", Decision: "deny", Match: config.PolicyMatch{MCP: config.MCPPolicy{AllowTools: []string{"delete_repo"}}}},
			{ID: "approve-admin", Decision: "require_approval", Match: config.PolicyMatch{Topics: []string{"job.github.admin"}}},
			{ID: "mcp-allow-github", Decision: "allow", Match: config.PolicyMatch{MCP: config.MCPPolicy{AllowServers: []string{"github"}}}},
			{ID: "allow-github-sync", Decision: "allow", Match: config.PolicyMatch{Topics: []string{"job.github.sync"}}},
		},
	}
	srv := &server{cacheTTL: 5 * time.Minute, cache: map[string]cacheEntry{}, cacheMaxSize: 100}
	if err := srv.setPolicy(context.Background(), policy, "snap-job-rules-order"); err != nil {
		t.Fatalf("setPolicy: %v", err)
	}
	for i, tc := range []struct {
		name     string
		topic    string
		labels   map[string]string
		want     pb.DecisionType
		wantRule string
	}{
		{"plain job", "job.github.sync", nil, pb.DecisionType_DECISION_TYPE_ALLOW, "allow-github-sync"},
		{"forbidden tool", "job.github.sync", map[string]string{"mcp.server": "github", "mcp.tool": "delete_repo"}, pb.DecisionType_DECISION_TYPE_DENY, "mcp-deny-delete"},
		{"permitted call", "job.github.other", map[string]string{"mcp.server": "github", "mcp.tool": "list"}, pb.DecisionType_DECISION_TYPE_ALLOW, "mcp-allow-github"},
		{"earlier rule wins over a later MCP allow", "job.github.admin", map[string]string{"mcp.server": "github", "mcp.tool": "list"}, pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, "approve-admin"},
		{"MCP allow does not match a plain job", "job.github.other", nil, pb.DecisionType_DECISION_TYPE_DENY, ""},
	} {
		resp, err := srv.evaluate(context.Background(), &pb.PolicyCheckRequest{
			JobId: "job-order-" + string(rune('a'+i)), Topic: tc.topic, Tenant: "default", PrincipalId: "user-1",
			Labels: tc.labels, Meta: &pb.JobMetadata{TenantId: "default", ActorId: "user-1"},
		}, "check")
		if err != nil {
			t.Fatalf("%s: evaluate: %v", tc.name, err)
		}
		if resp.GetDecision() != tc.want || resp.GetRuleId() != tc.wantRule {
			t.Errorf("%s: got %v by rule %q, want %v by rule %q", tc.name, resp.GetDecision(), resp.GetRuleId(), tc.want, tc.wantRule)
		}
	}
}
