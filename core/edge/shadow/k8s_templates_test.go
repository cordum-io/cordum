// EDGE-143.7 — Tests for the §12.1 Kubernetes-scope remediation
// templates extending the EDGE-142 generator.
//
// Each template is exercised via the public `GenerateForFinding`
// entrypoint with a synthetic ShadowAgentFinding shaped to match what
// the EDGE-143.1 K8s detector emits. Outputs are compared against
// `testdata/*.golden`. The clock seam (GeneratorOptions.Now) is fixed
// so JSON equality holds across runs.
//
// Goldens are regenerated with `go test ./core/edge/shadow/ -run
// 'TestK8sTemplate_|TestCITemplate_' -update`.
package shadow

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGoldens lets `go test … -update` rewrite testdata/*.golden in
// place. Off by default — CI runs the suite without -update so any
// drift is surfaced as a test failure.
var updateGoldens = flag.Bool("update", false, "rewrite testdata/*.golden with current output")

// assertGoldenPlan compares a generated plan against testdata/<name>.golden.
// Re-marshals to indented JSON so diffs are reviewable. With -update,
// writes the current output back to the golden file.
func assertGoldenPlan(t *testing.T, name string, plan *RemediationPlan) {
	t.Helper()
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan for %s: %v", name, err)
	}
	body = append(body, '\n')
	path := filepath.Join("testdata", name+".golden")
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", path, err)
	}
	if string(want) != string(body) {
		t.Errorf("golden mismatch for %s:\n--- want ---\n%s\n--- got ---\n%s", path, want, body)
	}
}

// newK8sFinding builds a baseline EDGE-141 lifecycle record shaped like
// what the EDGE-143.1 K8s detector emits. Tests override the signal +
// evidence type per template.
func newK8sFinding(id string) *ShadowAgentFinding {
	return &ShadowAgentFinding{
		FindingID:        "edge_shadow_" + id,
		TenantID:         "tenant-k8s",
		OwnerPrincipalID: "owner@cordum.test",
		PrincipalID:      "k8s-detector",
		AgentProduct:     "claude-code",
		Risk:             FindingRiskMedium,
		Status:           FindingStatusDetected,
		SourceType:       SourceTypeKubernetes,
		ClusterID:        "prod-east-1",
		Namespace:        "team-research",
		WorkloadKind:     "Deployment",
		WorkloadName:     "research-bot",
		PodUID:           "9a4f6e2c-1d80-4e8c-9f0a-2b3c4d5e6f70",
		DetectedAt:       fixedTime(),
	}
}

func TestK8sTemplate_TenantLabelMissing(t *testing.T) {
	f := newK8sFinding("k8s-tenant-1")
	f.EvidenceType = "k8s_namespace_untenanted"
	f.EvidenceSummary = "namespace missing tenant label; has agent-image pod"
	f.SignalSet = []string{"namespace_untenanted"}
	f.Risk = FindingRiskLow
	f.AgentProduct = "unknown"

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationApplyTenantLabel {
		t.Fatalf("ActionKind: want %q, got %q", RemediationApplyTenantLabel, plan.ActionKind)
	}
	// Must emit a kubectl label patch the operator runs verbatim.
	hasLabelCmd := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "kubectl") && strings.Contains(step.Command, "label namespace") &&
			strings.Contains(step.Command, "cordum.io/tenant-id=") {
			hasLabelCmd = true
		}
	}
	if !hasLabelCmd {
		t.Errorf("expected `kubectl … label namespace … cordum.io/tenant-id=` step; got %+v", stepKinds(plan.Steps))
	}
	assertGoldenPlan(t, "k8s_tenant_label_missing", plan)
}

func TestK8sTemplate_UnmanagedWorkload(t *testing.T) {
	f := newK8sFinding("k8s-workload-1")
	f.EvidenceType = "k8s_unmanaged_workload"
	f.EvidenceSummary = "workload owns agent-image pod outside allowlist"
	f.SignalSet = []string{"unmanaged_workload"}

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceBoth, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationAdoptUnmanagedWorkload {
		t.Fatalf("ActionKind: want %q, got %q", RemediationAdoptUnmanagedWorkload, plan.ActionKind)
	}
	hasAllowlist := false
	hasSidecar := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "WorkloadAllowlist") || strings.Contains(step.Command, "workload_allowlist") {
			hasAllowlist = true
		}
		if strings.Contains(step.Command, "cordum-agentd") {
			hasSidecar = true
		}
	}
	if !hasAllowlist {
		t.Errorf("expected workload-allowlist step; got %+v", stepKinds(plan.Steps))
	}
	if !hasSidecar {
		t.Errorf("expected sidecar-injection step (cordum-agentd); got %+v", stepKinds(plan.Steps))
	}
	assertGoldenPlan(t, "k8s_unmanaged_workload", plan)
}

func TestK8sTemplate_UntrustedImage(t *testing.T) {
	f := newK8sFinding("k8s-image-1")
	f.EvidenceType = "k8s_untrusted_agent_image"
	f.EvidenceSummary = "image registry not in operator allowlist; image=docker.io/anthropic/claude-code:v1.2"
	f.SignalSet = []string{"untrusted_agent_image"}
	f.Risk = FindingRiskLow
	f.Metadata = map[string]string{"container_image": "docker.io/anthropic/claude-code:v1.2"}

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationRebaseAgentImage {
		t.Fatalf("ActionKind: want %q, got %q", RemediationRebaseAgentImage, plan.ActionKind)
	}
	hasRebase := false
	for _, step := range plan.Steps {
		if strings.Contains(strings.ToLower(step.Title), "rebase") || strings.Contains(strings.ToLower(step.Command), "rebase") {
			hasRebase = true
		}
	}
	if !hasRebase {
		t.Errorf("expected rebase guidance; got %+v", stepKinds(plan.Steps))
	}
	assertGoldenPlan(t, "k8s_untrusted_image", plan)
}

func TestK8sTemplate_EgressBypass(t *testing.T) {
	f := newK8sFinding("k8s-egress-1")
	f.EvidenceType = "k8s_egress_bypass"
	f.EvidenceSummary = "NetworkPolicy permits broad egress outside LLM-proxy scope"
	f.SignalSet = []string{"egress_bypass"}
	f.Risk = FindingRiskHigh
	f.WorkloadKind = "NetworkPolicy"
	f.WorkloadName = "default-egress"

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationExtendEgressPolicy {
		t.Fatalf("ActionKind: want %q, got %q", RemediationExtendEgressPolicy, plan.ActionKind)
	}
	if plan.Severity != RemediationSeverityHigh {
		t.Errorf("Severity: want high, got %q", plan.Severity)
	}
	hasNetworkPolicyYAML := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "kind: NetworkPolicy") && strings.Contains(step.Command, "egress:") {
			hasNetworkPolicyYAML = true
		}
	}
	if !hasNetworkPolicyYAML {
		t.Errorf("expected NetworkPolicy YAML patch with egress block; got %+v", stepKinds(plan.Steps))
	}
	assertGoldenPlan(t, "k8s_egress_bypass", plan)
}

func TestK8sTemplate_NoMutation(t *testing.T) {
	// Each K8s template emits steps where every Command is a STRING
	// the operator runs locally; the plan never carries an APIRequest
	// that mutates Cordum-side cluster state (Q5 enforce-scope-out).
	cases := []struct {
		name      string
		signal    string
		ev        string
		kind      RemediationActionKind
		ciOnly    bool
		shadowMut func(*ShadowAgentFinding)
	}{
		{"tenant-label", "namespace_untenanted", "k8s_namespace_untenanted", RemediationApplyTenantLabel, false, nil},
		{"unmanaged-workload", "unmanaged_workload", "k8s_unmanaged_workload", RemediationAdoptUnmanagedWorkload, false, nil},
		{"untrusted-image", "untrusted_agent_image", "k8s_untrusted_agent_image", RemediationRebaseAgentImage, false, nil},
		{"egress-bypass", "egress_bypass", "k8s_egress_bypass", RemediationExtendEgressPolicy, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newK8sFinding(c.name)
			f.SignalSet = []string{c.signal}
			f.EvidenceType = c.ev
			plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			if plan.ActionKind != c.kind {
				t.Fatalf("ActionKind: want %q, got %q", c.kind, plan.ActionKind)
			}
			for _, step := range plan.Steps {
				if step.APIRequest != nil {
					// API requests are advisory operator GETs at most.
					// Any mutating verb on a Cordum-side resource path
					// would violate Q5 enforce-scope-out.
					mutating := step.APIRequest.Method == "POST" ||
						step.APIRequest.Method == "PUT" ||
						step.APIRequest.Method == "DELETE" ||
						step.APIRequest.Method == "PATCH"
					if mutating && strings.HasPrefix(step.APIRequest.Path, "/api/v1/edge/") &&
						!strings.HasSuffix(step.APIRequest.Path, "/exception") {
						// EDGE-143.6 exception API is the one allowed
						// mutating endpoint (operator-acked exception);
						// everything else must be operator-applied text.
						t.Errorf("step %q has Cordum-side mutating API request %s %s — violates Q5 enforce-scope-out",
							step.ID, step.APIRequest.Method, step.APIRequest.Path)
					}
				}
			}
		})
	}
}
