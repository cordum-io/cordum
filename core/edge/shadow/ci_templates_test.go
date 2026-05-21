// EDGE-143.7 — Tests for the §12.1 CI-scope remediation templates
// extending the EDGE-142 generator.
//
// Each template is exercised via the public `GenerateForFinding`
// entrypoint with a synthetic ShadowAgentFinding shaped to match what
// the EDGE-143.2 CI detector (and the EDGE-143.2/.3 follow-ups) emit.
// Outputs are compared against `testdata/*.golden`.
package shadow

import (
	"strings"
	"testing"
)

// newCIFinding builds a baseline CI-scope finding. Tests override
// SignalSet/EvidenceType + CIProvider per template.
func newCIFinding(id string) *ShadowAgentFinding {
	return &ShadowAgentFinding{
		FindingID:        "edge_shadow_" + id,
		TenantID:         "tenant-ci",
		OwnerPrincipalID: "owner@cordum.test",
		PrincipalID:      "ci-detector",
		AgentProduct:     "claude-code",
		Risk:             FindingRiskMedium,
		Status:           FindingStatusDetected,
		SourceType:       SourceTypeCI,
		Repo:             "acme/web",
		WorkflowID:       "200",
		Ref:              "refs/heads/main",
		DetectedAt:       fixedTime(),
	}
}

func TestCITemplate_MissingCordumAttach(t *testing.T) {
	providers := []struct {
		name           string
		provider       string
		wantSnippetTag string
	}{
		{"github_actions", CIProviderGitHubActions, "cordum/cordum-edge-attach@v1"},
		{"gitlab_ci", CIProviderGitLabCI, "cordum-edge-attach"},
		{"jenkins", CIProviderJenkins, "cordum-edge-attach"},
		{"buildkite", CIProviderBuildkite, "cordum-edge-attach"},
		{"circleci", CIProviderCircleCI, "cordum-edge-attach"},
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			f := newCIFinding("ci-attach-" + p.name)
			f.CIProvider = p.provider
			f.EvidenceType = "ci_missing_cordum_attach"
			f.EvidenceSummary = "workflow invokes agent action without cordum-edge attach"
			f.SignalSet = []string{"missing_cordum_attach"}

			plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			if plan.ActionKind != RemediationAddCordumEdgeAttach {
				t.Fatalf("ActionKind: want %q, got %q", RemediationAddCordumEdgeAttach, plan.ActionKind)
			}
			hasSnippet := false
			for _, step := range plan.Steps {
				if strings.Contains(step.Command, p.wantSnippetTag) {
					hasSnippet = true
				}
			}
			if !hasSnippet {
				t.Errorf("[%s] expected snippet containing %q; got %+v", p.name, p.wantSnippetTag, stepKinds(plan.Steps))
			}
			assertGoldenPlan(t, "ci_missing_cordum_attach_"+p.name, plan)
		})
	}
}

func TestCITemplate_UnmanagedOIDC(t *testing.T) {
	providers := []struct {
		name     string
		provider string
		wantEnv  string
	}{
		{"github_actions", CIProviderGitHubActions, "CORDUM_EDGE_SHADOW_OIDC_TRUST_github"},
		{"gitlab_ci", CIProviderGitLabCI, "CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab"},
		{"jenkins", CIProviderJenkins, "CORDUM_EDGE_SHADOW_OIDC_TRUST_jenkins"},
		{"buildkite", CIProviderBuildkite, "CORDUM_EDGE_SHADOW_OIDC_TRUST_buildkite"},
		{"circleci", CIProviderCircleCI, "CORDUM_EDGE_SHADOW_OIDC_TRUST_circleci"},
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			f := newCIFinding("ci-oidc-" + p.name)
			f.CIProvider = p.provider
			f.EvidenceType = "ci_unmanaged_oidc"
			f.EvidenceSummary = "OIDC trust root not configured for provider"
			f.SignalSet = []string{"unmanaged_oidc"}

			plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			if plan.ActionKind != RemediationConfigureOIDCTrust {
				t.Fatalf("ActionKind: want %q, got %q", RemediationConfigureOIDCTrust, plan.ActionKind)
			}
			hasTrust := false
			hasAudience := false
			for _, step := range plan.Steps {
				if strings.Contains(step.Command, p.wantEnv) {
					hasTrust = true
				}
				if strings.Contains(step.Command, strings.Replace(p.wantEnv, "_TRUST_", "_AUDIENCE_", 1)) {
					hasAudience = true
				}
			}
			if !hasTrust {
				t.Errorf("[%s] expected env-var %q in plan; got %+v", p.name, p.wantEnv, stepKinds(plan.Steps))
			}
			if !hasAudience {
				t.Errorf("[%s] expected matching _AUDIENCE_ env-var in plan", p.name)
			}
			assertGoldenPlan(t, "ci_unmanaged_oidc_"+p.name, plan)
		})
	}
}

func TestCITemplate_DirectProviderSDK(t *testing.T) {
	f := newCIFinding("ci-sdk-1")
	f.CIProvider = CIProviderGitHubActions
	f.EvidenceType = "ci_direct_provider_endpoint"
	f.EvidenceSummary = "CI job invoked provider SDK directly (host=api.anthropic.com)"
	f.SignalSet = []string{"direct_provider_endpoint"}
	f.Risk = FindingRiskHigh

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationRouteCISDKThroughProxy {
		t.Fatalf("ActionKind: want %q, got %q", RemediationRouteCISDKThroughProxy, plan.ActionKind)
	}
	if plan.Severity != RemediationSeverityHigh {
		t.Errorf("Severity: want high, got %q", plan.Severity)
	}
	hasProxyRoute := false
	hasExceptionAlt := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "<llm-proxy-url>") || strings.Contains(step.Title, "LLM proxy") {
			hasProxyRoute = true
		}
		if step.APIRequest != nil &&
			step.APIRequest.Method == "POST" &&
			strings.HasSuffix(step.APIRequest.Path, "/exception") {
			hasExceptionAlt = true
		}
	}
	if !hasProxyRoute {
		t.Errorf("expected proxy-routing step; got %+v", stepKinds(plan.Steps))
	}
	if !hasExceptionAlt {
		t.Errorf("expected EDGE-143.6 exception API alternative (POST …/exception); got %+v", stepKinds(plan.Steps))
	}
	assertGoldenPlan(t, "ci_direct_provider_sdk", plan)
}

func TestCITemplate_NoMutation(t *testing.T) {
	// Mirror of TestK8sTemplate_NoMutation for the 3 CI templates.
	// Note: DirectProviderSDK explicitly allows the EDGE-143.6
	// exception API as an alternative path — that POST is OK because
	// the operator initiates it via dashboard / CLI, not via the
	// template auto-applying it.
	cases := []struct {
		name string
		mut  func(*ShadowAgentFinding)
		kind RemediationActionKind
	}{
		{"missing-attach", func(f *ShadowAgentFinding) {
			f.SignalSet = []string{"missing_cordum_attach"}
			f.CIProvider = CIProviderGitHubActions
			f.EvidenceType = "ci_missing_cordum_attach"
		}, RemediationAddCordumEdgeAttach},
		{"unmanaged-oidc", func(f *ShadowAgentFinding) {
			f.SignalSet = []string{"unmanaged_oidc"}
			f.CIProvider = CIProviderGitHubActions
			f.EvidenceType = "ci_unmanaged_oidc"
		}, RemediationConfigureOIDCTrust},
		{"direct-sdk", func(f *ShadowAgentFinding) {
			f.SignalSet = []string{"direct_provider_endpoint"}
			f.CIProvider = CIProviderGitHubActions
			f.EvidenceType = "ci_direct_provider_endpoint"
		}, RemediationRouteCISDKThroughProxy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCIFinding(c.name)
			c.mut(f)
			plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			if plan.ActionKind != c.kind {
				t.Fatalf("ActionKind: want %q, got %q", c.kind, plan.ActionKind)
			}
			for _, step := range plan.Steps {
				if step.APIRequest == nil {
					continue
				}
				if step.APIRequest.Method == "POST" || step.APIRequest.Method == "PUT" ||
					step.APIRequest.Method == "DELETE" || step.APIRequest.Method == "PATCH" {
					if !strings.HasSuffix(step.APIRequest.Path, "/exception") {
						t.Errorf("step %q mutating API path %s %s violates Q5 enforce-scope-out (only /exception allowed)",
							step.ID, step.APIRequest.Method, step.APIRequest.Path)
					}
				}
			}
		})
	}
}
