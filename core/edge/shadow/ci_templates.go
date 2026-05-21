// EDGE-143.7 — CI-scope remediation templates extending the EDGE-142
// generator per design doc §12.1.
//
// Each template emits operator-applicable text: workflow snippets,
// env-var configuration blocks, or proxy-routing guidance. Cordum
// NEVER opens PRs against CI repos, never mutates CI provider trust
// roots, never modifies workflow files. Q5 enforce-scope-out
// (governor ruling comment-a17f4f1c) is structural — template
// functions take only findingFeatures + audience, NEVER a GitHub
// client / GitLab client / Jenkins API client.
//
// Provider-specific output is keyed off findingFeatures.ciProvider
// which mirrors the §10.1 ci_provider enum (github_actions, gitlab_ci,
// jenkins, buildkite, circleci, other). Unknown providers fall through
// to a generic snippet so the output stays runnable.
package shadow

import (
	"fmt"
	"strings"
)

// EDGE-143.7 — CI signal constants. Mirrors the GitHub detector's
// emit names (core/edge/shadow/github/detector_test.go) and the
// future-detector signal `unmanaged_oidc`.
const (
	signalCIMissingCordumAttach = "missing_cordum_attach"
	signalCIUnmanagedOIDC       = "unmanaged_oidc"
	signalCIDirectProviderSDK   = "direct_provider_endpoint"
)

// ciProviderShortName trims the CIProvider enum string for use as an
// env-var suffix. Per Q6 the env vars are
// CORDUM_EDGE_SHADOW_OIDC_TRUST_<short> /
// CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<short>, where <short> is the
// provider with the conventional underscore-separator stripped.
func ciProviderShortName(p string) string {
	switch p {
	case CIProviderGitHubActions:
		return "github"
	case CIProviderGitLabCI:
		return "gitlab"
	case CIProviderJenkins:
		return "jenkins"
	case CIProviderBuildkite:
		return "buildkite"
	case CIProviderCircleCI:
		return "circleci"
	default:
		return "<provider>"
	}
}

// buildAddCordumEdgeAttachSteps emits the §12.1 "missing-Cordum-attach"
// remediation: a per-provider workflow snippet inserting the
// cordum-edge-attach step ahead of any agent step.
func buildAddCordumEdgeAttachSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	provider := strings.TrimSpace(f.ciProvider)
	snippet, file, lang := cordumEdgeAttachSnippet(provider)
	steps := []RemediationStep{
		{
			ID:    "add_cordum_edge_attach.snippet",
			Title: fmt.Sprintf("Add cordum-edge-attach to %s workflow", providerLabel(provider)),
			Kind:  kind,
			Command: fmt.Sprintf(
				"# In %s, insert this snippet BEFORE the agent step (%s):\n%s",
				file, lang, snippet,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"open a PR against the workflow file; do not commit directly to default branch",
				"verify CORDUM_EDGE_GATEWAY + CORDUM_EDGE_TENANT_ID secrets exist in the CI provider's secret store",
			},
		},
		{
			ID:    "add_cordum_edge_attach.verify_attach",
			Title: "After merge, verify the next run attaches under Cordum Edge",
			Kind:  kind,
			Command: "# Re-run the workflow and check the Cordum Edge dashboard for the new run id under " +
				"`/edge/sessions` (filter source_type=ci).",
			DocsURL: "docs/edge/shadow-remediation.md",
		},
	}
	return steps
}

// buildConfigureOIDCTrustSteps emits the §12.1 "unmanaged-OIDC"
// remediation: per-provider env-var config block per Q6.
func buildConfigureOIDCTrustSteps(kind RemediationActionKind, f findingFeatures) []RemediationStep {
	provider := strings.TrimSpace(f.ciProvider)
	short := ciProviderShortName(provider)
	trustVar := "CORDUM_EDGE_SHADOW_OIDC_TRUST_" + short
	audienceVar := "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_" + short
	defaultTrust := defaultOIDCIssuer(provider)
	return []RemediationStep{
		{
			ID:    "configure_oidc_trust.env_block",
			Title: fmt.Sprintf("Set OIDC trust root + audience for %s on the Cordum Edge service", providerLabel(provider)),
			Kind:  kind,
			Command: fmt.Sprintf(
				"# Add to the Cordum Edge service deployment env block:\n%s=%s\n%s=cordum-edge",
				trustVar, defaultTrust, audienceVar,
			),
			DocsURL: "docs/edge/managed-settings-deploy.md",
			Conditions: []string{
				"the issuer URL above is the provider's default OIDC root; replace only if your CI fleet runs a self-hosted OIDC token issuer",
				"audience MUST match what cordum-edge-attach@v1 requests at runtime (default: `cordum-edge`)",
			},
		},
		{
			ID:    "configure_oidc_trust.restart",
			Title: "Restart the Cordum Edge service so the new OIDC config is picked up",
			Kind:  kind,
			Conditions: []string{
				"rolling-restart the Edge service deployment (kubectl rollout restart, systemctl restart, etc.)",
				"verify with `cordumctl edge doctor --gateway <gateway-url>` that OIDC trust is loaded",
			},
		},
	}
}

// buildRouteCISDKThroughProxySteps emits the §12.1 "direct-provider-SDK"
// remediation. Two operator-chosen options: re-route the CI job
// through the Cordum LLM proxy, OR file an operator-acked exception
// via EDGE-143.6's POST /api/v1/edge/shadow/exception API.
func buildRouteCISDKThroughProxySteps(kind RemediationActionKind, f findingFeatures, audience RemediationAudience) []RemediationStep {
	provider := strings.TrimSpace(f.ciProvider)
	envSnippet, file, lang := llmProxyEnvSnippet(provider)
	steps := []RemediationStep{
		{
			ID:    "route_ci_sdk_through_proxy.env_snippet",
			Title: fmt.Sprintf("Option A — Route the %s job through the Cordum LLM proxy", providerLabel(provider)),
			Kind:  kind,
			Command: fmt.Sprintf(
				"# In %s, set the provider SDK base URL to the Cordum LLM proxy (%s):\n%s",
				file, lang, envSnippet,
			),
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"obtain <llm-proxy-url> from your Cordum administrator",
				"do NOT inline the API key; reuse the existing provider-key secret and let Cordum redact / re-issue",
			},
		},
		{
			ID:    "route_ci_sdk_through_proxy.exception_request",
			Title: "Option B — File an operator-acked exception (only if proxy routing is infeasible)",
			Kind:  kind,
			APIRequest: &RemediationAPIRequest{
				Method: "POST",
				Path:   "/api/v1/edge/shadow/exception",
				Body: "{\n" +
					"  \"finding_id\": \"<finding-id>\",\n" +
					"  \"tenant_id\": \"<tenant-id>\",\n" +
					"  \"reason\": \"<operator-justification>\",\n" +
					"  \"expires_at\": \"<rfc3339-timestamp>\"\n" +
					"}",
			},
			DocsURL: "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"exception is an operator decision — Cordum emits an audit event at file time",
				"set a non-empty `expires_at` so the exception auto-lapses; never file open-ended exceptions",
			},
		},
	}
	if audience == RemediationAudienceEnterprise || audience == RemediationAudienceBoth {
		steps = append(steps, RemediationStep{
			ID:      "route_ci_sdk_through_proxy.enterprise.policy",
			Title:   "Roll the proxy-routing env block into your CI-template repo so new repos inherit it",
			Kind:    kind,
			DocsURL: "docs/edge/managed-settings-deploy.md",
			Conditions: []string{
				"central CI templates avoid per-repo drift; align with your platform team's workflow strategy",
			},
		})
	}
	return steps
}

// cordumEdgeAttachSnippet returns the (snippet, file, language) tuple
// for the provider's workflow file. Unknown providers fall through to
// a generic guidance string so the plan stays runnable.
func cordumEdgeAttachSnippet(provider string) (string, string, string) {
	switch provider {
	case CIProviderGitHubActions:
		return strings.Join([]string{
			"      - name: Attach Cordum Edge",
			"        uses: cordum/cordum-edge-attach@v1",
			"        with:",
			"          gateway-url: ${{ secrets.CORDUM_EDGE_GATEWAY }}",
			"          tenant-id: ${{ secrets.CORDUM_EDGE_TENANT_ID }}",
		}, "\n"), ".github/workflows/<workflow>.yml", "yaml"
	case CIProviderGitLabCI:
		return strings.Join([]string{
			"include:",
			"  - remote: 'https://raw.githubusercontent.com/cordum/cordum-edge-attach/v1/gitlab/attach.yml'",
			"",
			"variables:",
			"  CORDUM_EDGE_GATEWAY: $CORDUM_EDGE_GATEWAY",
			"  CORDUM_EDGE_TENANT_ID: $CORDUM_EDGE_TENANT_ID",
		}, "\n"), ".gitlab-ci.yml", "yaml"
	case CIProviderJenkins:
		return strings.Join([]string{
			"stage('Cordum Edge Attach') {",
			"  steps {",
			"    sh 'curl -sSL https://cordum.io/install/cordum-edge-attach@v1.sh | bash'",
			"    withCredentials([string(credentialsId: 'cordum-edge-gateway', variable: 'CORDUM_EDGE_GATEWAY'),",
			"                     string(credentialsId: 'cordum-edge-tenant', variable: 'CORDUM_EDGE_TENANT_ID')]) {",
			"      sh 'cordum-edge-attach'",
			"    }",
			"  }",
			"}",
		}, "\n"), "Jenkinsfile", "groovy"
	case CIProviderBuildkite:
		return strings.Join([]string{
			"steps:",
			"  - label: 'Attach Cordum Edge'",
			"    plugins:",
			"      - cordum/cordum-edge-attach#v1:",
			"          gateway-url: $$CORDUM_EDGE_GATEWAY",
			"          tenant-id: $$CORDUM_EDGE_TENANT_ID",
		}, "\n"), ".buildkite/pipeline.yml", "yaml"
	case CIProviderCircleCI:
		return strings.Join([]string{
			"orbs:",
			"  cordum-edge-attach: cordum/cordum-edge-attach@1.0",
			"",
			"jobs:",
			"  attach:",
			"    executor: cordum-edge-attach/default",
			"    steps:",
			"      - cordum-edge-attach/attach:",
			"          gateway-url: $CORDUM_EDGE_GATEWAY",
			"          tenant-id: $CORDUM_EDGE_TENANT_ID",
		}, "\n"), ".circleci/config.yml", "yaml"
	default:
		return "# Install and run cordum-edge-attach ahead of the agent step.\n" +
			"# See docs/edge/shadow-remediation.md for adapter scripts.", "<workflow-file>", "shell"
	}
}

// llmProxyEnvSnippet returns the (snippet, file, language) tuple for
// setting the provider-SDK base URL to the Cordum LLM proxy.
func llmProxyEnvSnippet(provider string) (string, string, string) {
	switch provider {
	case CIProviderGitHubActions:
		return strings.Join([]string{
			"env:",
			"  ANTHROPIC_BASE_URL: ${{ secrets.CORDUM_LLM_PROXY_URL }}",
			"  OPENAI_BASE_URL: ${{ secrets.CORDUM_LLM_PROXY_URL }}",
		}, "\n"), ".github/workflows/<workflow>.yml", "yaml"
	case CIProviderGitLabCI:
		return strings.Join([]string{
			"variables:",
			"  ANTHROPIC_BASE_URL: $CORDUM_LLM_PROXY_URL",
			"  OPENAI_BASE_URL: $CORDUM_LLM_PROXY_URL",
		}, "\n"), ".gitlab-ci.yml", "yaml"
	case CIProviderJenkins:
		return strings.Join([]string{
			"environment {",
			"  ANTHROPIC_BASE_URL = credentials('cordum-llm-proxy-url')",
			"  OPENAI_BASE_URL = credentials('cordum-llm-proxy-url')",
			"}",
		}, "\n"), "Jenkinsfile", "groovy"
	case CIProviderBuildkite:
		return strings.Join([]string{
			"env:",
			"  ANTHROPIC_BASE_URL: $$CORDUM_LLM_PROXY_URL",
			"  OPENAI_BASE_URL: $$CORDUM_LLM_PROXY_URL",
		}, "\n"), ".buildkite/pipeline.yml", "yaml"
	case CIProviderCircleCI:
		return strings.Join([]string{
			"jobs:",
			"  agent:",
			"    environment:",
			"      ANTHROPIC_BASE_URL: <llm-proxy-url>",
			"      OPENAI_BASE_URL: <llm-proxy-url>",
		}, "\n"), ".circleci/config.yml", "yaml"
	default:
		return "# Set ANTHROPIC_BASE_URL / OPENAI_BASE_URL to <llm-proxy-url> in the job env.", "<workflow-file>", "shell"
	}
}

// providerLabel returns the human-friendly provider name for use in
// step titles / summary text.
func providerLabel(p string) string {
	switch p {
	case CIProviderGitHubActions:
		return "GitHub Actions"
	case CIProviderGitLabCI:
		return "GitLab CI"
	case CIProviderJenkins:
		return "Jenkins"
	case CIProviderBuildkite:
		return "Buildkite"
	case CIProviderCircleCI:
		return "CircleCI"
	default:
		return "the CI provider"
	}
}

// defaultOIDCIssuer returns the well-known OIDC issuer URL per
// provider. Mirrors EDGE-143.2 / Q6 defaults so operators don't have
// to hunt for the right URL — they only override when self-hosting
// the OIDC token issuer.
func defaultOIDCIssuer(provider string) string {
	switch provider {
	case CIProviderGitHubActions:
		return "https://token.actions.githubusercontent.com"
	case CIProviderGitLabCI:
		return "https://gitlab.com"
	case CIProviderJenkins:
		return "<jenkins-oidc-issuer-url>"
	case CIProviderBuildkite:
		return "https://agent.buildkite.com"
	case CIProviderCircleCI:
		return "https://oidc.circleci.com/org/<org-id>"
	default:
		return "<provider-oidc-issuer-url>"
	}
}
