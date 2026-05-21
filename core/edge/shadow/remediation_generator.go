// EDGE-142 — Shadow remediation generator implementation.
//
// Pure, side-effect-free classification + step emission. Reads only
// from the input finding (or its scanner-shape predecessor) and the
// caller's GeneratorOptions; never touches the filesystem, network,
// Redis, Safety Kernel, or Cordum Jobs.
//
// All advisory text is bounded (≤ MaxRemediationPlanBytes) so a
// malicious / oversized finding cannot blow up dashboard payloads;
// inputs that exceed limits degrade to the manual-review fallback.
package shadow

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrRemediationValidation signals that the generator could not even
// shape a fallback plan (e.g. nil input). Wraps a typed sentinel so
// callers can errors.Is() detect.
var ErrRemediationValidation = errors.New("shadow remediation: validation")

// MaxRemediationPlanBytes bounds the serialised plan size. The 16 KiB
// cap is generous compared to any plan the generator emits today
// (~2 KiB), but matters when oversized inputs flow through — the
// generator never echoes raw evidence summaries longer than this.
const MaxRemediationPlanBytes = 16 * 1024

// Canonical signal names recognised by the classifier. Tests pin these
// strings, so the constants live in the implementation file alongside
// the lookup table. They're intentionally not exported — the wire
// contract is the signal-name strings themselves, which travel on the
// finding's SignalSet field.
const (
	signalUnmanagedClaudeSettings = "unmanaged_claude_settings"
	signalUnmanagedMCPServer      = "unmanaged_mcp_server"
	signalDirectProviderURL       = "direct_provider_url"
	signalK8sHeartbeatMissing     = "k8s_heartbeat_missing"
	signalUnmanagedProcess        = "unmanaged_process"
)

// GenerateForFinding produces a deterministic, redacted remediation
// plan for an EDGE-141 ShadowAgentFinding. Nil input returns
// ErrRemediationValidation; empty/unknown input returns the manual-
// review fallback plan, never an error.
func GenerateForFinding(f *ShadowAgentFinding, opts GeneratorOptions) (*RemediationPlan, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: finding is nil", ErrRemediationValidation)
	}
	features := normalizeShadowAgentFinding(f)
	return generateFromFeatures(features, opts), nil
}

// GenerateForScannerFinding produces a redacted remediation plan for an
// EDGE-140 scanner observation (used by `cordumctl shadow remediate`
// on JSONL output). Behavioural parity with GenerateForFinding;
// returned plan carries an empty FindingID because scanner findings
// have no persistent identity.
func GenerateForScannerFinding(f *Finding, opts GeneratorOptions) (*RemediationPlan, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: finding is nil", ErrRemediationValidation)
	}
	features := normalizeScannerFinding(f)
	return generateFromFeatures(features, opts), nil
}

// generateFromFeatures is the shape-agnostic core. Classifies, builds
// steps per audience, and finalises the plan. Never returns an error
// — empty/unknown classification falls through to manual_review.
func generateFromFeatures(features findingFeatures, opts GeneratorOptions) *RemediationPlan {
	resolved := opts.applyDefaults()
	kind := resolveActionKind(classify(features), resolved.Audience)
	severity := severityFromRisk(features.risk)

	plan := &RemediationPlan{
		FindingID:         features.findingID,
		TenantID:          features.tenantID,
		Audience:          resolved.Audience,
		Severity:          severity,
		ActionKind:        kind,
		Summary:           remediationSummary(kind, features),
		RiskExplanation:   riskExplanation(kind, features),
		RecommendedAction: recommendedAction(kind, resolved.Audience),
		SafetyNotes:       safetyNotes(kind),
		Steps:             buildSteps(kind, resolved, features),
		GeneratorVersion:  remediationGeneratorVersion,
		GeneratedAt:       resolved.Now().UTC(),
		AdvisoryOnly:      true,
	}

	// Bounded output: if the plan's user-facing string fields somehow
	// expand beyond the cap, swap in the manual-review fallback. This
	// is defence-in-depth — every emission path already uses bounded
	// constants and placeholders.
	if approximatePlanBytes(plan) > MaxRemediationPlanBytes {
		plan = manualReviewFallback(features, resolved)
	}
	if resolved.OmitCommands {
		stripCommandsFromSteps(plan.Steps)
	}
	return plan
}

// classify maps findingFeatures onto the most-specific known action
// kind. Order matters: signal_set is most specific, then evidence
// hints, then product/path heuristics, then manual-review fallback.
func classify(f findingFeatures) RemediationActionKind {
	signals := signalSet(f.signalSet)
	// EDGE-143.7 §12.1 Kubernetes-scope signals — highest specificity
	// for K8s-source findings; checked before EDGE-142's generic
	// signals so a k8s finding never decays to the local-wrapper path.
	switch {
	case signals[signalK8sNamespaceUntenanted]:
		return RemediationApplyTenantLabel
	case signals[signalK8sUnmanagedWorkload]:
		return RemediationAdoptUnmanagedWorkload
	case signals[signalK8sUntrustedAgentImage]:
		return RemediationRebaseAgentImage
	case signals[signalK8sEgressBypass]:
		return RemediationExtendEgressPolicy
	}
	// EDGE-143.7 §12.1 CI-scope signals.
	switch {
	case signals[signalCIMissingCordumAttach]:
		return RemediationAddCordumEdgeAttach
	case signals[signalCIUnmanagedOIDC]:
		return RemediationConfigureOIDCTrust
	case signals[signalCIDirectProviderSDK] && f.sourceType == SourceTypeCI:
		return RemediationRouteCISDKThroughProxy
	}
	switch {
	case signals[signalDirectProviderURL]:
		return RemediationRouteThroughLLMProxy
	case signals[signalK8sHeartbeatMissing]:
		return RemediationRunEdgeDoctor
	case signals[signalUnmanagedMCPServer]:
		return RemediationAttachMCPGateway
	case signals[signalUnmanagedClaudeSettings]:
		return RemediationUseCordumctlEdgeClaude
	case signals[signalUnmanagedProcess]:
		return RemediationInvestigateProcess
	}

	// Heuristic fallback: evidence_type + path hints.
	switch f.evidenceType {
	case "heartbeat":
		return RemediationRunEdgeDoctor
	case EvidenceProcessName:
		return RemediationInvestigateProcess
	case EvidenceEnvironmentVar:
		// Env-var detected outside managed deployment; advise routing
		// through the proxy + managed settings.
		return RemediationRouteThroughLLMProxy
	case EvidenceConfigFile:
		// Path-aware: claude settings → cordumctl edge claude; mcp
		// config → attach mcp gateway; anything else under a known
		// agent dir → manual review.
		lp := strings.ToLower(f.redactedPath)
		switch {
		case strings.Contains(lp, "mcp.json") || strings.Contains(lp, "/mcp/"):
			return RemediationAttachMCPGateway
		case strings.Contains(lp, ".claude/settings"):
			return RemediationUseCordumctlEdgeClaude
		case strings.HasPrefix(lp, "~/.claude/") || strings.HasPrefix(lp, "~/.cursor/") || strings.HasPrefix(lp, "~/.codex/"):
			return RemediationUseCordumctlEdgeClaude
		}
	}

	return RemediationManualReview
}

// resolveActionKind applies audience-specific overrides to the
// classifier's natural choice. For pure-enterprise audiences,
// unmanaged-Claude-settings remediation is best expressed as a
// managed-settings deployment (MDM-driven) rather than the local
// `cordumctl edge claude` wrapper. For audience=both we keep the
// natural kind and let buildSteps layer dev+enterprise steps.
func resolveActionKind(kind RemediationActionKind, audience RemediationAudience) RemediationActionKind {
	if audience != RemediationAudienceEnterprise {
		return kind
	}
	switch kind {
	case RemediationUseCordumctlEdgeClaude:
		return RemediationDeployManagedSettings
	default:
		return kind
	}
}

func signalSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[strings.ToLower(strings.TrimSpace(s))] = true
	}
	return out
}

// severityFromRisk maps the finding's risk onto the plan severity.
// Critical collapses into high because the plan severity is a coarser
// scale; consumers that need critical surface it from the source
// finding directly.
func severityFromRisk(risk string) RemediationSeverity {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case string(FindingRiskLow):
		return RemediationSeverityLow
	case string(FindingRiskMedium):
		return RemediationSeverityMedium
	case string(FindingRiskHigh), string(FindingRiskCritical):
		return RemediationSeverityHigh
	default:
		return RemediationSeverityMedium
	}
}

// remediationSummary returns the short operator-facing tagline. Pure
// fn of (kind, features); never echoes raw evidence text.
func remediationSummary(kind RemediationActionKind, f findingFeatures) string {
	product := safeProductName(f.agentProduct)
	switch kind {
	case RemediationUseCordumctlEdgeClaude:
		return fmt.Sprintf("Bring %s configuration under Cordum Edge management.", product)
	case RemediationAttachMCPGateway:
		return fmt.Sprintf("Route the %s MCP server through the Cordum MCP Gateway.", product)
	case RemediationRouteThroughLLMProxy:
		return fmt.Sprintf("Route %s LLM traffic through the configured Cordum proxy.", product)
	case RemediationRunEdgeDoctor:
		return "Restore the Cordum Edge heartbeat for this host."
	case RemediationDeployManagedSettings:
		return fmt.Sprintf("Deploy Cordum managed settings to fleet endpoints running %s.", product)
	case RemediationDisableUnmanagedConfig:
		return fmt.Sprintf("Disable unmanaged %s configuration so managed settings take precedence.", product)
	case RemediationInvestigateProcess:
		return fmt.Sprintf("Investigate the observed %s process.", product)
	case RemediationApplyTenantLabel:
		return "Label the Kubernetes namespace with the Cordum tenant id so the detector can resolve a tenant."
	case RemediationAdoptUnmanagedWorkload:
		return fmt.Sprintf("Adopt the unmanaged %s workload: allowlist it or attach the cordum-agentd sidecar.", product)
	case RemediationRebaseAgentImage:
		return fmt.Sprintf("Rebase the %s workload onto an allowlisted container image registry.", product)
	case RemediationExtendEgressPolicy:
		return "Extend the NetworkPolicy egress allowlist to include the operator's LLM proxy."
	case RemediationAddCordumEdgeAttach:
		return "Add the cordum-edge-attach step to the CI workflow so the job runs under Cordum Edge governance."
	case RemediationConfigureOIDCTrust:
		return "Configure the CI provider's OIDC trust root and audience in Cordum Edge."
	case RemediationRouteCISDKThroughProxy:
		return "Route CI provider-SDK calls through the Cordum LLM proxy or file an operator-acked exception."
	case RemediationManualReview:
		fallthrough
	default:
		return "Manual review required: finding does not match a known remediation pattern."
	}
}

// riskExplanation returns the operator-facing rationale for the action
// kind. Avoids re-quoting the raw evidence summary; references the
// signal/evidence type instead so we never carry user input forward.
func riskExplanation(kind RemediationActionKind, f findingFeatures) string {
	signalsLabel := strings.Join(sortedSignals(f.signalSet), ", ")
	if signalsLabel == "" {
		signalsLabel = "<no-signals>"
	}
	switch kind {
	case RemediationUseCordumctlEdgeClaude:
		return "Unmanaged agent configuration was detected outside Cordum Edge. " +
			"Operators on unmanaged settings can bypass Cordum policy enforcement, hook routing, and audit. " +
			"Signals: " + signalsLabel + "."
	case RemediationAttachMCPGateway:
		return "An MCP server entry was found that does not route through the Cordum MCP Gateway. " +
			"Direct MCP routing bypasses tool-policy enforcement, approvals, and tenant-scoped tool catalogs. " +
			"Signals: " + signalsLabel + "."
	case RemediationRouteThroughLLMProxy:
		return "An agent endpoint was observed pointing at a provider URL directly. " +
			"Direct provider traffic bypasses the Cordum LLM proxy and Safety Kernel output policy. " +
			"Signals: " + signalsLabel + "."
	case RemediationRunEdgeDoctor:
		return "The Cordum Edge heartbeat has stopped reporting for this host. " +
			"Sessions launched while agentd is offline run without policy enforcement. " +
			"Signals: " + signalsLabel + "."
	case RemediationDeployManagedSettings:
		return "Unmanaged settings detected on a fleet endpoint. " +
			"Until managed settings are deployed, developers retain control over policy mode and hook command. " +
			"Signals: " + signalsLabel + "."
	case RemediationDisableUnmanagedConfig:
		return "An unmanaged configuration file was observed alongside the managed deployment. " +
			"Both files coexisting can cause policy to silently fall back to the developer's settings."
	case RemediationInvestigateProcess:
		return "An agent process was observed that does not match any managed Cordum binary. " +
			"Investigate before deciding whether to disable or accept. Signals: " + signalsLabel + "."
	case RemediationApplyTenantLabel:
		return "A Kubernetes namespace that contains agent-image pods is missing the Cordum tenant label. " +
			"Without the label, findings from this namespace cannot be tenant-scoped and default to quarantine. " +
			"Signals: " + signalsLabel + "."
	case RemediationAdoptUnmanagedWorkload:
		return "A Kubernetes workload runs an agent image but is not on the operator's WorkloadAllowlist AND " +
			"has no cordum-agentd sidecar. Until adopted, agent sessions launched from this workload run without " +
			"Cordum policy / hook routing. Signals: " + signalsLabel + "."
	case RemediationRebaseAgentImage:
		return "A workload's container image is pulled from a registry that is not on the operator's " +
			"ImageRegistryAllowlist. Untrusted registries can ship modified agent binaries or runtime payloads. " +
			"Signals: " + signalsLabel + "."
	case RemediationExtendEgressPolicy:
		return "A Kubernetes NetworkPolicy permits broader egress than the operator's LLM-proxy scope. " +
			"Pods covered by this policy can reach provider APIs directly, bypassing the Cordum LLM proxy and " +
			"its policy/redaction layer. Signals: " + signalsLabel + "."
	case RemediationAddCordumEdgeAttach:
		return "A CI workflow invokes an agent action without first attaching cordum-edge. " +
			"The job runs without Cordum policy, audit, or redaction. Signals: " + signalsLabel + "."
	case RemediationConfigureOIDCTrust:
		return "The CI provider's OIDC trust root or audience is not configured in Cordum Edge. " +
			"Without trust + audience, cordum-edge cannot verify CI job identity at attach time. " +
			"Signals: " + signalsLabel + "."
	case RemediationRouteCISDKThroughProxy:
		return "A CI job invoked a provider SDK directly, bypassing the Cordum LLM proxy. " +
			"Direct provider calls evade Safety Kernel output policy and Cordum audit. " +
			"Signals: " + signalsLabel + "."
	case RemediationManualReview:
		fallthrough
	default:
		return "This finding does not match any predefined classification. " +
			"Review evidence_type and signal_set against your Cordum Edge runbook."
	}
}

// recommendedAction is the one-paragraph operator instruction. Audience
// drives wording: dev steers toward `cordumctl edge claude`,
// enterprise steers toward MDM-distributed managed settings.
func recommendedAction(kind RemediationActionKind, audience RemediationAudience) string {
	switch kind {
	case RemediationUseCordumctlEdgeClaude:
		switch audience {
		case RemediationAudienceEnterprise:
			return "Roll the host into the managed-settings deployment so policy mode and hook command are MDM-enforced."
		case RemediationAudienceDev:
			return "Launch Claude Code via `cordumctl edge claude` on this host so Cordum Edge controls policy mode and hook routing."
		default:
			return "Developer: launch via `cordumctl edge claude`. Fleet: deploy Cordum managed settings via MDM."
		}
	case RemediationAttachMCPGateway:
		switch audience {
		case RemediationAudienceEnterprise:
			return "Add the MCP server entries to the managed MCP payload and redeploy via `cordumctl edge managed-settings export`."
		case RemediationAudienceDev:
			return "Re-route the MCP server through the Cordum MCP Gateway with `cordumctl edge claude`."
		default:
			return "Developer: re-launch via `cordumctl edge claude`. Fleet: redeploy managed MCP payload via MDM."
		}
	case RemediationRouteThroughLLMProxy:
		return "Update the agent endpoint to point at the configured Cordum LLM proxy so Safety Kernel output policy applies."
	case RemediationRunEdgeDoctor:
		return "Run `cordumctl edge doctor` to surface the failing preconditions, then repair agentd or the managed-service bootstrap."
	case RemediationDeployManagedSettings:
		return "Export and deploy Cordum managed settings via your MDM workflow; verify with `cordumctl edge doctor`."
	case RemediationDisableUnmanagedConfig:
		return "Back up the unmanaged config, preview removal, then remove only after the managed deployment is in place."
	case RemediationInvestigateProcess:
		return "Identify the parent process, command line, and owner; do not terminate without operator confirmation."
	case RemediationApplyTenantLabel:
		return "Run `kubectl label namespace <ns> cordum.io/tenant-id=<id>` under operator credentials; verify by re-running the detector."
	case RemediationAdoptUnmanagedWorkload:
		return "Choose ONE: (A) add the workload to WorkloadAllowlist in the Cordum operator config, OR (B) patch the workload to inject the cordum-agentd sidecar."
	case RemediationRebaseAgentImage:
		return "Repoint the workload's container image to your Cordum-allowlisted registry mirror; verify the digest before rolling out."
	case RemediationExtendEgressPolicy:
		return "Apply a NetworkPolicy patch adding the operator's LLM-proxy CIDR / namespace selector to the offending policy's egress allowlist."
	case RemediationAddCordumEdgeAttach:
		return "Open a PR adding the `cordum-edge-attach@v1` step (or provider equivalent) ahead of any agent step in the workflow."
	case RemediationConfigureOIDCTrust:
		return "Set the CORDUM_EDGE_SHADOW_OIDC_TRUST_<provider> + CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<provider> env vars on the Cordum Edge service, then redeploy."
	case RemediationRouteCISDKThroughProxy:
		return "Update the CI job env to route SDK calls through the Cordum LLM proxy, OR file an operator-acked exception via POST /api/v1/edge/shadow/exception (EDGE-143.6)."
	case RemediationManualReview:
		fallthrough
	default:
		return "Open the finding's evidence in the dashboard; cross-reference against the Cordum Edge runbook."
	}
}

// safetyNotes returns the bounded list of caveats attached to every
// plan. These are common reminders, so the generator emits them
// uniformly across audiences.
func safetyNotes(kind RemediationActionKind) []string {
	common := []string{
		"All steps are advisory — Cordum Edge does not auto-execute remediation.",
		"Never substitute live secrets into the placeholders shown below; use your secret-manager / api-key-helper.",
	}
	switch kind {
	case RemediationDisableUnmanagedConfig, RemediationInvestigateProcess:
		return append(common,
			"Confirm a managed deployment is already in place before disabling the unmanaged path.",
			"Snapshot the target before any destructive step.",
		)
	case RemediationRunEdgeDoctor:
		return append(common, "If the heartbeat outage is fleet-wide, page the platform on-call before restarting agentd.")
	case RemediationApplyTenantLabel, RemediationAdoptUnmanagedWorkload,
		RemediationRebaseAgentImage, RemediationExtendEgressPolicy:
		return append(common,
			"Cordum NEVER mutates Kubernetes cluster state; every kubectl command must be applied by the operator.",
			"Only target namespaces you own; cross-tenant patches are explicitly out of scope (Q5 enforce-scope-out).",
		)
	case RemediationAddCordumEdgeAttach, RemediationConfigureOIDCTrust,
		RemediationRouteCISDKThroughProxy:
		return append(common,
			"Cordum NEVER mutates CI provider repos, workflows, or trust roots; operator opens the PR / sets the env var.",
			"For exception flows, route through POST /api/v1/edge/shadow/exception (EDGE-143.6) so the operator-acked decision is audited.",
		)
	default:
		return common
	}
}

// buildSteps emits the ordered RemediationStep slice per (kind, audience).
// Builds dev-first then enterprise for audience=both so consumers can
// trim the suffix when audience changes.
func buildSteps(kind RemediationActionKind, opts GeneratorOptions, f findingFeatures) []RemediationStep {
	switch kind {
	case RemediationUseCordumctlEdgeClaude:
		return buildClaudeWrapperSteps(kind, opts.Audience)
	case RemediationAttachMCPGateway:
		return buildMCPGatewaySteps(kind, opts.Audience)
	case RemediationRouteThroughLLMProxy:
		return buildLLMProxySteps(kind, opts.Audience)
	case RemediationRunEdgeDoctor:
		return buildEdgeDoctorSteps(kind)
	case RemediationDeployManagedSettings:
		return buildManagedSettingsSteps(kind)
	case RemediationDisableUnmanagedConfig:
		return buildDisableConfigSteps(kind)
	case RemediationInvestigateProcess:
		return buildInvestigateProcessSteps(kind)
	case RemediationApplyTenantLabel:
		return buildTenantLabelMissingSteps(kind, f)
	case RemediationAdoptUnmanagedWorkload:
		return buildUnmanagedWorkloadSteps(kind, f)
	case RemediationRebaseAgentImage:
		return buildRebaseAgentImageSteps(kind, f)
	case RemediationExtendEgressPolicy:
		return buildExtendEgressPolicySteps(kind, f)
	case RemediationAddCordumEdgeAttach:
		return buildAddCordumEdgeAttachSteps(kind, f)
	case RemediationConfigureOIDCTrust:
		return buildConfigureOIDCTrustSteps(kind, f)
	case RemediationRouteCISDKThroughProxy:
		return buildRouteCISDKThroughProxySteps(kind, f, opts.Audience)
	case RemediationManualReview:
		fallthrough
	default:
		return buildManualReviewSteps(kind, f)
	}
}

func buildClaudeWrapperSteps(kind RemediationActionKind, audience RemediationAudience) []RemediationStep {
	steps := make([]RemediationStep, 0, 4)
	if audience == RemediationAudienceDev || audience == RemediationAudienceBoth {
		steps = append(steps,
			RemediationStep{
				ID:      "use_cordumctl_edge_claude.dev.launch",
				Title:   "Launch Claude Code via cordumctl edge claude",
				Kind:    kind,
				Command: "cordumctl edge claude --gateway <gateway-url> --tenant <tenant-id> --principal <principal-id>",
				DocsURL: "docs/edge/cordumctl-edge-claude.md",
				Conditions: []string{
					"requires cordum-agentd in PATH on the host",
				},
			},
			RemediationStep{
				ID:      "use_cordumctl_edge_claude.dev.verify",
				Title:   "Verify policy mode and hook routing",
				Kind:    kind,
				Command: "cordumctl edge doctor --gateway <gateway-url> --tenant <tenant-id>",
				DocsURL: "docs/edge/cordumctl-edge-doctor.md",
			},
		)
	}
	if audience == RemediationAudienceEnterprise || audience == RemediationAudienceBoth {
		steps = append(steps,
			RemediationStep{
				ID:    "use_cordumctl_edge_claude.enterprise.backup",
				Title: "Backup existing user settings before deploying managed payload",
				Kind:  kind,
				Command: "cordumctl edge managed-settings export --output <output-dir>" +
					" --mcp-gateway-url <gateway-url> --llm-proxy-base-url <llm-proxy-url> --api-key-helper-command <api-key-helper-command>",
				RequiresBackup: true,
				DocsURL:        "docs/edge/managed-settings-deploy.md",
			},
			RemediationStep{
				ID:    "use_cordumctl_edge_claude.enterprise.deploy",
				Title: "Deploy managed settings via MDM and verify",
				Kind:  kind,
				Command: "cordumctl edge managed-settings verify --managed-settings-path <path-to-managed-settings.json>" +
					" --managed-mcp-path <path-to-managed-mcp.json>",
				DocsURL: "docs/edge/managed-settings-deploy.md",
				Conditions: []string{
					"requires MDM access; do NOT push managed settings without sign-off",
				},
			},
		)
	}
	return steps
}

func buildMCPGatewaySteps(kind RemediationActionKind, audience RemediationAudience) []RemediationStep {
	steps := make([]RemediationStep, 0, 3)
	if audience == RemediationAudienceDev || audience == RemediationAudienceBoth {
		steps = append(steps, RemediationStep{
			ID:      "attach_mcp_gateway.dev.relaunch",
			Title:   "Re-launch the agent via cordumctl edge claude to rewrite MCP routing",
			Kind:    kind,
			Command: "cordumctl edge claude --gateway <gateway-url> --tenant <tenant-id>",
			DocsURL: "docs/edge/cordumctl-edge-claude.md",
		})
	}
	if audience == RemediationAudienceEnterprise || audience == RemediationAudienceBoth {
		steps = append(steps, RemediationStep{
			ID:    "attach_mcp_gateway.enterprise.export",
			Title: "Export managed MCP payload and redeploy via MDM",
			Kind:  kind,
			Command: "cordumctl edge managed-settings export --output <output-dir>" +
				" --mcp-gateway-url <gateway-url> --llm-proxy-base-url <llm-proxy-url> --api-key-helper-command <api-key-helper-command>",
			DocsURL: "docs/edge/managed-settings-deploy.md",
			Conditions: []string{
				"requires MDM access",
			},
		})
	}
	return steps
}

func buildLLMProxySteps(kind RemediationActionKind, audience RemediationAudience) []RemediationStep {
	steps := []RemediationStep{
		{
			ID:    "route_through_llm_proxy.update_endpoint",
			Title: "Point the agent at the Cordum LLM proxy",
			Kind:  kind,
			Conditions: []string{
				"obtain the proxy base URL from your Cordum administrator",
				"do not paste live API keys; use the api-key-helper-command",
			},
		},
	}
	if audience == RemediationAudienceEnterprise || audience == RemediationAudienceBoth {
		steps = append(steps, RemediationStep{
			ID:    "route_through_llm_proxy.enterprise.managed",
			Title: "Bind the LLM proxy URL into managed settings",
			Kind:  kind,
			Command: "cordumctl edge managed-settings export --output <output-dir>" +
				" --mcp-gateway-url <gateway-url> --llm-proxy-base-url <llm-proxy-url> --api-key-helper-command <api-key-helper-command>",
			DocsURL: "docs/edge/managed-settings-deploy.md",
		})
	}
	return steps
}

func buildEdgeDoctorSteps(kind RemediationActionKind) []RemediationStep {
	return []RemediationStep{
		{
			ID:      "run_edge_doctor.diagnose",
			Title:   "Surface failing Edge preconditions",
			Kind:    kind,
			Command: "cordumctl edge doctor --gateway <gateway-url> --tenant <tenant-id>",
			DocsURL: "docs/edge/cordumctl-edge-doctor.md",
		},
		{
			ID:    "run_edge_doctor.restart_agentd",
			Title: "Restart agentd / managed-service",
			Kind:  kind,
			Conditions: []string{
				"on macOS: relaunch via launchd",
				"on Linux: systemctl restart cordum-agentd",
				"on Windows: restart the Cordum agentd Windows service",
			},
		},
	}
}

func buildManagedSettingsSteps(kind RemediationActionKind) []RemediationStep {
	return []RemediationStep{
		{
			ID:    "deploy_managed_settings.export",
			Title: "Export managed settings + managed MCP payload",
			Kind:  kind,
			Command: "cordumctl edge managed-settings export --output <output-dir>" +
				" --mcp-gateway-url <gateway-url> --llm-proxy-base-url <llm-proxy-url> --api-key-helper-command <api-key-helper-command>",
			DocsURL: "docs/edge/managed-settings-deploy.md",
		},
		{
			ID:    "deploy_managed_settings.verify",
			Title: "Verify deployment with the doctor",
			Kind:  kind,
			Command: "cordumctl edge managed-settings verify --managed-settings-path <path-to-managed-settings.json>" +
				" --managed-mcp-path <path-to-managed-mcp.json>",
			DocsURL: "docs/edge/managed-settings-deploy.md",
		},
	}
}

func buildDisableConfigSteps(kind RemediationActionKind) []RemediationStep {
	return []RemediationStep{
		{
			ID:             "disable_unmanaged_config.backup",
			Title:          "Backup the unmanaged config file",
			Kind:           kind,
			Command:        "cp <unmanaged-config-path> <unmanaged-config-path>.bak.$(date +%s)",
			RequiresBackup: true,
			DocsURL:        "docs/edge/shadow-remediation.md",
		},
		{
			ID:             "disable_unmanaged_config.preview",
			Title:          "Preview the disable operation",
			Kind:           kind,
			Command:        "ls -la <unmanaged-config-path>",
			PreviewOnly:    true,
			RequiresBackup: true,
			Destructive:    true,
			DocsURL:        "docs/edge/shadow-remediation.md",
		},
		{
			ID:             "disable_unmanaged_config.rename",
			Title:          "Rename the unmanaged config so managed settings take precedence",
			Kind:           kind,
			Command:        "mv <unmanaged-config-path> <unmanaged-config-path>.disabled-by-cordum",
			RequiresBackup: true,
			PreviewOnly:    true,
			Destructive:    true,
			DocsURL:        "docs/edge/shadow-remediation.md",
			Conditions: []string{
				"only run after the managed deployment is confirmed via cordumctl edge doctor",
			},
		},
	}
}

func buildInvestigateProcessSteps(kind RemediationActionKind) []RemediationStep {
	return []RemediationStep{
		{
			ID:    "investigate_process.inspect",
			Title: "Inspect the parent process and command line",
			Kind:  kind,
			Conditions: []string{
				"do NOT terminate the process without operator confirmation",
				"capture parent PID, working directory, and open files for the audit record",
			},
		},
		{
			ID:      "investigate_process.cross_reference",
			Title:   "Cross-reference against the Cordum Edge runbook",
			Kind:    kind,
			DocsURL: "docs/edge/shadow-remediation.md",
		},
	}
}

func buildManualReviewSteps(kind RemediationActionKind, _ findingFeatures) []RemediationStep {
	return []RemediationStep{
		{
			ID:    "manual_review.open_finding",
			Title: "Open the finding in the Cordum dashboard",
			Kind:  kind,
			APIRequest: &RemediationAPIRequest{
				Method: "GET",
				Path:   "/api/v1/edge/shadow-agents/<finding-id>",
			},
			DocsURL: "docs/edge/shadow-remediation.md",
		},
		{
			ID:      "manual_review.consult_runbook",
			Title:   "Consult the Cordum Edge runbook",
			Kind:    kind,
			DocsURL: "docs/edge/shadow-remediation.md",
		},
	}
}

func manualReviewFallback(features findingFeatures, opts GeneratorOptions) *RemediationPlan {
	return &RemediationPlan{
		FindingID:         features.findingID,
		TenantID:          features.tenantID,
		Audience:          opts.Audience,
		Severity:          severityFromRisk(features.risk),
		ActionKind:        RemediationManualReview,
		Summary:           remediationSummary(RemediationManualReview, features),
		RiskExplanation:   riskExplanation(RemediationManualReview, features),
		RecommendedAction: recommendedAction(RemediationManualReview, opts.Audience),
		SafetyNotes:       safetyNotes(RemediationManualReview),
		Steps:             buildManualReviewSteps(RemediationManualReview, features),
		GeneratorVersion:  remediationGeneratorVersion,
		GeneratedAt:       opts.Now().UTC(),
		AdvisoryOnly:      true,
	}
}

// stripCommandsFromSteps zeroes out Command + APIRequest.Body when the
// caller wants step shape without execution-bound strings. Kind/title/
// conditions stay so dashboards can still render a meaningful card.
func stripCommandsFromSteps(steps []RemediationStep) {
	for i := range steps {
		steps[i].Command = ""
		if steps[i].APIRequest != nil {
			steps[i].APIRequest.Body = ""
		}
	}
}

// approximatePlanBytes returns a fast, allocation-cheap estimate of
// the JSON size. Always over-estimates (string length + per-field
// scaffolding), so the cap behaves as a floor not a ceiling.
func approximatePlanBytes(p *RemediationPlan) int {
	if p == nil {
		return 0
	}
	total := len(p.Summary) + len(p.RiskExplanation) + len(p.RecommendedAction)
	for _, n := range p.SafetyNotes {
		total += len(n) + 8
	}
	for _, step := range p.Steps {
		total += len(step.ID) + len(step.Title) + len(step.Command) + len(step.DocsURL) + 64
		if step.APIRequest != nil {
			total += len(step.APIRequest.Method) + len(step.APIRequest.Path) + len(step.APIRequest.Body) + 16
		}
		for _, c := range step.Conditions {
			total += len(c) + 8
		}
	}
	return total
}

// safeProductName trims the agent product to a short, safe label.
// Empty or unknown product falls back to "agent" so summaries stay
// grammatical. Routes through stripSecretMarkers so a misbehaving
// uploader cannot smuggle a secret into the operator-facing summary
// by setting `agent_product = "claude-code sk-ant-…"`. After
// redaction the value is bounded at 32 bytes.
func safeProductName(product string) string {
	p := strings.TrimSpace(product)
	if p == "" {
		return "agent"
	}
	p = stripSecretMarkers(p)
	// Drop non-printable / control runes so a deliberately crafted
	// product label cannot inject newlines or terminal escapes into
	// the operator-facing summary.
	p = stripUnsafeRunes(p)
	const maxProductLen = 32
	if len(p) > maxProductLen {
		p = p[:maxProductLen]
	}
	if p == "" {
		return "agent"
	}
	return p
}

// stripUnsafeRunes drops bytes outside the printable-ASCII range
// (control chars, newlines, escape sequences). User-facing summary
// strings flow through this so an upstream component cannot inject
// terminal escapes via finding fields.
func stripUnsafeRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sortedSignals returns the deduplicated, sorted, redacted signal
// list used by risk_explanation. Empty input returns nil. Each entry
// flows through stripSecretMarkers + stripUnsafeRunes so a malicious
// uploader cannot smuggle a secret into the explanation via a
// crafted SignalSet[].
func sortedSignals(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		s = stripSecretMarkers(s)
		s = stripUnsafeRunes(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
