package edge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type edgePolicySimulationFixture struct {
	Cases []edgePolicySimulationCase `json:"cases"`
}

type edgePolicySimulationCase struct {
	Name                     string              `json:"name"`
	Event                    AgentActionEvent    `json:"event"`
	ExpectedPolicyInput      expectedPolicyInput `json:"expected_policy_input"`
	ExpectedDecision         string              `json:"expected_decision"`
	ExpectedRuleID           string              `json:"expected_rule_id"`
	ExpectedApprovalRequired bool                `json:"expected_approval_required"`
}

type expectedPolicyInput struct {
	Topic            string            `json:"topic"`
	Capability       string            `json:"capability"`
	RiskTags         []string          `json:"risk_tags"`
	Labels           map[string]string `json:"labels"`
	InputContentType string            `json:"input_content_type"`
}

func TestEdgePolicySimulationFixturesDeclareRequiredCases(t *testing.T) {
	fixture := loadEdgePolicySimulationFixture(t)

	required := map[string]struct{}{
		"bash_npm_test":      {},
		"bash_npm_run_build": {},
		"bash_rm_rf":         {},
		"read_dotenv":        {},
		"edit_source":        {},
		"git_push":           {},
		"curl_network":       {},
		"unknown_high_risk":  {},
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		if _, exists := seen[tc.Name]; exists {
			t.Fatalf("duplicate simulation case %q", tc.Name)
		}
		seen[tc.Name] = struct{}{}

		assertSyntheticEdgePolicyFixtureCase(t, tc)
	}
	for name := range required {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing required simulation case %q", name)
		}
	}
	if len(seen) != len(required) {
		t.Fatalf("fixture has %d cases, want exactly %d required cases", len(seen), len(required))
	}
}

func TestEdgeDemoPolicyFragmentParsesAndOrdersRules(t *testing.T) {
	policy := loadEdgeSafetyPolicyFragment(t, "policy.fragment.yaml")

	// EDGE-050 — wantOrder reflects the current shipped overlay yaml after
	// commits 06e34966 + db235832 + 1347bea7. The demo overlay grew from
	// 7 to 10 rules: the EDGE-049 fix added explicit allow-rules for
	// tool-less hooks (UserPromptSubmit, ConfigChange, FileChanged,
	// PolicyDecision, PermissionRequest); 1347bea7 ships only the demo
	// overlay (production fragment is a copyable template, not active);
	// db235832 restructured to default_decision=allow with explicit denies.
	wantOrder := []string{
		"claude-code.deny-secret-reads",
		"claude-code.deny-destructive-shell",
		"claude-code.require-approval-for-edits",
		"claude-code.require-approval-for-vcs-push",
		"claude-code.require-approval-for-network",
		"claude-code.allow-user-prompt-submit",
		"claude-code.allow-tool-less-hook-metadata",
		"claude-code.allow-safe-build-test",
		"claude-code.deny-unknown-high-risk",
		"claude-code.allow-edge-actions-default",
	}
	if len(policy.Rules) != len(wantOrder) {
		t.Fatalf("demo policy rule count = %d, want %d", len(policy.Rules), len(wantOrder))
	}
	for idx, wantID := range wantOrder {
		rule := policy.Rules[idx]
		if rule.ID != wantID {
			t.Fatalf("demo policy rule[%d] = %q, want %q", idx, rule.ID, wantID)
		}
		if !reflect.DeepEqual(rule.Match.Topics, []string{EdgePolicyTopic}) {
			t.Fatalf("demo policy rule %q topics = %#v, want [%q]", rule.ID, rule.Match.Topics, EdgePolicyTopic)
		}
		if strings.TrimSpace(rule.Reason) == "" {
			t.Fatalf("demo policy rule %q must have an operator-facing reason", rule.ID)
		}
	}
}

func TestEdgeProductionPolicyFragmentParsesAndDocumentsEnterpriseBoundary(t *testing.T) {
	policy := loadEdgeSafetyPolicyFragment(t, "policy.production.fragment.yaml")
	if policy.DefaultDecision != "deny" {
		t.Fatalf("production policy default_decision = %q, want deny", policy.DefaultDecision)
	}

	for _, ruleID := range []string{
		"claude-code.deny-secret-reads",
		"claude-code.deny-destructive-shell",
		"claude-code.deny-unknown-high-risk",
		"claude-code.require-approval-for-edits",
		"claude-code.require-approval-for-vcs-push",
		"claude-code.require-approval-for-network",
		"claude-code.allow-safe-build-test",
	} {
		rule := findPolicyRuleByID(t, policy, ruleID)
		if !reflect.DeepEqual(rule.Match.Topics, []string{EdgePolicyTopic}) {
			t.Fatalf("production policy rule %q topics = %#v, want [%q]", rule.ID, rule.Match.Topics, EdgePolicyTopic)
		}
	}

	allowRule := findPolicyRuleByID(t, policy, "claude-code.allow-safe-build-test")
	if allowRule.Decision != "allow" {
		t.Fatalf("production safe build/test decision = %q, want allow", allowRule.Decision)
	}
	if !allowRule.Constraints.Sandbox.Isolated {
		t.Fatalf("production safe build/test rule must include an isolated sandbox constraint")
	}
	if len(allowRule.Constraints.Toolchain.AllowedCommands) == 0 {
		t.Fatalf("production safe build/test rule must constrain allowed commands")
	}
	if allowRule.Constraints.RedactionLevel != "strict" {
		t.Fatalf("production safe build/test redaction_level = %q, want strict", allowRule.Constraints.RedactionLevel)
	}

	readme := readEdgePackREADME(t)
	for _, phrase := range []string{
		"managed Claude settings",
		"cordum-agentd",
		"short-lived tokens",
		"OS/tenant controls",
		"not a complete enterprise enforcement boundary",
	} {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("README must document production boundary phrase %q", phrase)
		}
	}
}

func TestEdgePackManifestIsPolicyOnly(t *testing.T) {
	manifest := loadEdgePackManifest(t)
	if err := packs.ValidatePackManifest(manifest); err != nil {
		t.Fatalf("validate edge pack manifest: %v", err)
	}
	if len(manifest.Topics) != 0 {
		t.Fatalf("edge pack manifest must not declare dispatchable topics; got %#v", manifest.Topics)
	}
	if len(manifest.Resources.Schemas) != 0 || len(manifest.Resources.Workflows) != 0 {
		t.Fatalf("edge pack manifest must not declare schemas/workflows; got schemas=%#v workflows=%#v", manifest.Resources.Schemas, manifest.Resources.Workflows)
	}
	if len(manifest.Overlays.Config) != 0 {
		t.Fatalf("edge pack manifest must not declare config/pool/timeouts overlays; got %#v", manifest.Overlays.Config)
	}
	// EDGE-050 — manifest collapsed to demo-only after 1347bea7. The
	// production fragment still ships in overlays/ as a copyable
	// template (covered by TestEdgeProductionPolicyFragmentParses-
	// AndDocumentsEnterpriseBoundary which loads it by direct path),
	// but it is NOT registered in pack.yaml because loading both
	// fragments simultaneously caused last-seen-wins rule merging
	// where production's default_decision=deny overrode demo's
	// default_decision=allow, breaking demo flow.
	wantPolicyOverlays := map[string]string{
		"demo-edge-policy": "overlays/policy.fragment.yaml",
	}
	if len(manifest.Overlays.Policy) != len(wantPolicyOverlays) {
		t.Fatalf("policy overlay count = %d, want %d", len(manifest.Overlays.Policy), len(wantPolicyOverlays))
	}
	for _, overlay := range manifest.Overlays.Policy {
		wantPath, ok := wantPolicyOverlays[overlay.Name]
		if !ok {
			t.Fatalf("unexpected policy overlay %q", overlay.Name)
		}
		if overlay.Path != wantPath {
			t.Fatalf("policy overlay %q path = %q, want %q", overlay.Name, overlay.Path, wantPath)
		}
		if overlay.Strategy != "bundle_fragment" {
			t.Fatalf("policy overlay %q strategy = %q, want bundle_fragment", overlay.Name, overlay.Strategy)
		}
	}
	if len(manifest.Tests.PolicySimulations) != 0 {
		t.Fatalf("edge pack manifest must not use label-less pack policySimulations; got %#v", manifest.Tests.PolicySimulations)
	}

	readme := readEdgePackREADME(t)
	for _, phrase := range []string{
		"policy-only",
		"does not define pools",
		"does not dispatch Claude tool calls",
		"fixtures/policy-simulations.json",
	} {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("README must document policy-only manifest phrase %q", phrase)
		}
	}
}

func TestEdgePolicySimulationFixturesEvaluateAgainstDemoPolicy(t *testing.T) {
	policy := loadEdgeSafetyPolicyFragment(t, "policy.fragment.yaml")
	fixture := loadEdgePolicySimulationFixture(t)

	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			classification, err := ClassifyEvent(tc.Event)
			if err != nil {
				t.Fatalf("classify fixture event: %v", err)
			}
			req, err := MapEventToPolicyCheckRequest(tc.Event, classification, PolicyMappingOptions{ActorType: pb.ActorType_ACTOR_TYPE_SERVICE})
			if err != nil {
				t.Fatalf("map fixture policy request: %v", err)
			}

			resp := policybundles.EvaluatePolicyCheck(policy, "edge-demo-policy-fixture-test", req)
			if got := policyDecisionName(resp.GetDecision()); got != tc.ExpectedDecision {
				t.Fatalf("decision = %q, want %q (reason=%q rule_id=%q)", got, tc.ExpectedDecision, resp.GetReason(), resp.GetRuleId())
			}
			if resp.GetRuleId() != tc.ExpectedRuleID {
				t.Fatalf("rule_id = %q, want %q", resp.GetRuleId(), tc.ExpectedRuleID)
			}
			if resp.GetApprovalRequired() != tc.ExpectedApprovalRequired {
				t.Fatalf("approval_required = %v, want %v", resp.GetApprovalRequired(), tc.ExpectedApprovalRequired)
			}
			if (tc.ExpectedDecision == "DENY" || tc.ExpectedApprovalRequired) && strings.TrimSpace(resp.GetReason()) == "" {
				t.Fatalf("deny/approval decision must include a non-empty reason")
			}
			if tc.ExpectedDecision == "DENY" && strings.TrimSpace(resp.GetRuleId()) == "" {
				t.Fatalf("deny decision fell through to default deny without an explicit rule_id")
			}
			if resp.GetPolicySnapshot() != "edge-demo-policy-fixture-test" {
				t.Fatalf("policy_snapshot = %q, want edge-demo-policy-fixture-test", resp.GetPolicySnapshot())
			}
		})
	}
}

func assertSyntheticEdgePolicyFixtureCase(t *testing.T, tc edgePolicySimulationCase) {
	t.Helper()

	for field, value := range map[string]string{
		"event_id":          tc.Event.EventID,
		"session_id":        tc.Event.SessionID,
		"execution_id":      tc.Event.ExecutionID,
		"tenant_id":         tc.Event.TenantID,
		"principal_id":      tc.Event.PrincipalID,
		"agent_product":     tc.Event.AgentProduct,
		"tool_name":         tc.Event.ToolName,
		"expected_rule":     tc.ExpectedRuleID,
		"expected_decision": tc.ExpectedDecision,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s: %s is required", tc.Name, field)
		}
	}
	if !strings.HasPrefix(tc.Event.EventID, "evt-edge-sim-") ||
		!strings.HasPrefix(tc.Event.SessionID, "sess-edge-sim-") ||
		!strings.HasPrefix(tc.Event.ExecutionID, "exec-edge-sim-") {
		t.Fatalf("%s: event/session/execution IDs must be synthetic edge simulation IDs", tc.Name)
	}
	if tc.Event.InputRedacted == nil {
		t.Fatalf("%s: input_redacted is required", tc.Name)
	}
	assertNoRawPayloadKeys(t, tc.Name, tc.Event.InputRedacted)

	classification, err := ClassifyEvent(tc.Event)
	if err != nil {
		t.Fatalf("%s: classify fixture event: %v", tc.Name, err)
	}
	req, err := MapEventToPolicyCheckRequest(tc.Event, classification, PolicyMappingOptions{ActorType: pb.ActorType_ACTOR_TYPE_SERVICE})
	if err != nil {
		t.Fatalf("%s: map policy input: %v", tc.Name, err)
	}

	if req.GetTopic() != tc.ExpectedPolicyInput.Topic {
		t.Fatalf("%s: topic = %q, want %q", tc.Name, req.GetTopic(), tc.ExpectedPolicyInput.Topic)
	}
	if got := req.GetMeta().GetCapability(); got != tc.ExpectedPolicyInput.Capability {
		t.Fatalf("%s: capability = %q, want %q", tc.Name, got, tc.ExpectedPolicyInput.Capability)
	}
	if got := req.GetMeta().GetRiskTags(); !reflect.DeepEqual(got, tc.ExpectedPolicyInput.RiskTags) {
		t.Fatalf("%s: risk_tags = %#v, want %#v", tc.Name, got, tc.ExpectedPolicyInput.RiskTags)
	}
	if got := req.GetInputContentType(); got != tc.ExpectedPolicyInput.InputContentType {
		t.Fatalf("%s: input_content_type = %q, want %q", tc.Name, got, tc.ExpectedPolicyInput.InputContentType)
	}
	for key, want := range tc.ExpectedPolicyInput.Labels {
		if got := req.GetLabels()[key]; got != want {
			t.Fatalf("%s: label %q = %q, want %q", tc.Name, key, got, want)
		}
	}
}

func assertNoRawPayloadKeys(t *testing.T, name string, input map[string]any) {
	t.Helper()
	for _, forbidden := range []string{"tool_input", "tool_result", "raw_input", "raw_output", "transcript"} {
		if _, ok := input[forbidden]; ok {
			t.Fatalf("%s: input_redacted contains forbidden raw payload key %q", name, forbidden)
		}
	}
}

func loadEdgePolicySimulationFixture(t *testing.T) edgePolicySimulationFixture {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "cordum-edge-pack", "fixtures", "policy-simulations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edge policy simulation fixture %s: %v", path, err)
	}
	var fixture edgePolicySimulationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse edge policy simulation fixture %s: %v", path, err)
	}
	return fixture
}

func loadEdgeSafetyPolicyFragment(t *testing.T, name string) *config.SafetyPolicy {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "cordum-edge-pack", "overlays", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edge policy fragment %s: %v", path, err)
	}
	policy, err := config.ParseSafetyPolicy(data)
	if err != nil {
		t.Fatalf("parse edge policy fragment %s: %v", path, err)
	}
	if policy == nil {
		t.Fatalf("parse edge policy fragment %s returned nil", path)
	}
	return policy
}

func findPolicyRuleByID(t *testing.T, policy *config.SafetyPolicy, id string) config.PolicyRule {
	t.Helper()
	count := 0
	var found config.PolicyRule
	for _, rule := range policy.Rules {
		if rule.ID == id {
			count++
			found = rule
		}
	}
	if count != 1 {
		t.Fatalf("policy rule %q count = %d, want exactly 1", id, count)
	}
	return found
}

func readEdgePackREADME(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "cordum-edge-pack", "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edge pack README %s: %v", path, err)
	}
	return string(data)
}

func loadEdgePackManifest(t *testing.T) *packs.PackManifest {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "cordum-edge-pack")
	manifest, err := packs.LoadPackManifest(path)
	if err != nil {
		t.Fatalf("load edge pack manifest %s: %v", path, err)
	}
	return manifest
}

func policyDecisionName(decision pb.DecisionType) string {
	switch decision {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return "ALLOW"
	case pb.DecisionType_DECISION_TYPE_DENY:
		return "DENY"
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return "REQUIRE_HUMAN"
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return "THROTTLE"
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return "ALLOW_WITH_CONSTRAINTS"
	default:
		return decision.String()
	}
}
