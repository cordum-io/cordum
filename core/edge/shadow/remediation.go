// EDGE-142 — Shadow remediation generator.
//
// This file defines the pure contract for shadow-agent remediation
// guidance and the deterministic generator that maps a
// ShadowAgentFinding (or scanner-only Finding) to advisory text plus
// machine-readable steps. The generator is side-effect free: it never
// touches the filesystem, the network, Redis, the Safety Kernel, or
// Cordum Jobs. All output is observe/warn ONLY — task rail #1 'advisory
// unless enforcement mode is explicitly implemented later'.
//
// Inputs are accepted in two shapes:
//
//   - shadow.ShadowAgentFinding — the EDGE-141 lifecycle record served
//     by /api/v1/edge/shadow-agents/{finding_id}. The Gateway API
//     remediation handler loads from the Store and calls
//     GenerateForFinding.
//   - shadow.Finding — the EDGE-140 scanner observation shape, used by
//     the offline `cordumctl shadow remediate --finding-file` path so
//     operators can preview guidance from scanner JSONL without first
//     posting findings to the Gateway.
//
// Both shapes feed normalizeFindingInput which folds them onto a
// shared `findingFeatures` struct; the rest of the generator is shape-
// agnostic. Adding a third detector shape later (e.g. K8s detector from
// EDGE-143.1) requires only an additional normalizer.
//
// All commands in generated steps use literal placeholders (never live
// values). Operators copy + parameterise before execution.
package shadow

import (
	"strings"
	"time"
)

// remediationGeneratorVersion bumps when the classification table or
// emitted step IDs change in a way that breaks deterministic JSON
// equality. Consumers that pin against test golden output use this to
// detect generator drift.
const remediationGeneratorVersion = "1.0.0"

// RemediationActionKind enumerates the kinds of advisory actions the
// generator can emit. Stable identifiers — UI, SIEM, and audit consumers
// pivot on these strings, so adding a new kind is a contract change.
type RemediationActionKind string

const (
	// RemediationAttachMCPGateway — route an unmanaged MCP server entry
	// through the Cordum MCP Gateway. Dev guidance: `cordumctl edge
	// claude` rewrites mcpServers in temporary settings; enterprise
	// guidance: deploy managed MCP config via managed-settings export.
	RemediationAttachMCPGateway RemediationActionKind = "attach_mcp_gateway"
	// RemediationUseCordumctlEdgeClaude — recommend launching Claude
	// Code via `cordumctl edge claude` so policy mode + hook command +
	// agentd routing are applied locally for the developer.
	RemediationUseCordumctlEdgeClaude RemediationActionKind = "use_cordumctl_edge_claude"
	// RemediationDeployManagedSettings — fleet-level remediation:
	// export and deploy managed-settings.json / managed-mcp.json via
	// MDM. Targets unmanaged Claude Code settings detected on a
	// managed-fleet endpoint.
	RemediationDeployManagedSettings RemediationActionKind = "deploy_managed_settings"
	// RemediationDisableUnmanagedConfig — remove or rename the
	// unmanaged config file so the managed deployment takes precedence.
	// Always marked preview_only=true and gated behind a backup step;
	// generator never emits an auto-executable destructive command.
	RemediationDisableUnmanagedConfig RemediationActionKind = "disable_unmanaged_config"
	// RemediationRouteThroughLLMProxy — direct provider URL detected;
	// recommend routing through the configured Cordum LLM proxy so
	// policy + audit + redaction apply to outbound traffic.
	RemediationRouteThroughLLMProxy RemediationActionKind = "route_through_llm_proxy"
	// RemediationRunEdgeDoctor — heartbeat missing or agentd state
	// unclear; recommend `cordumctl edge doctor` to surface the
	// failing managed-service preconditions and restart agentd.
	RemediationRunEdgeDoctor RemediationActionKind = "run_edge_doctor"
	// RemediationInvestigateProcess — unknown process / unclassifiable
	// finding; recommend manual inspection of the process tree + env
	// + open files; emit a safe checklist, never a kill command.
	RemediationInvestigateProcess RemediationActionKind = "investigate_process"
	// RemediationManualReview — unknown finding fallback. Operators
	// review evidence manually; the generator emits a stable step ID
	// so dashboards can group "needs human" findings consistently.
	RemediationManualReview RemediationActionKind = "manual_review"

	// EDGE-143.7 — §12.1 Kubernetes scope templates. Operator-executed
	// remediations: Cordum produces the diff / config snippet / kubectl
	// command, operator applies. NO Cordum-side cluster mutation per Q5
	// enforce-scope-out (binding governor ruling comment-a17f4f1c on
	// parent task-de50a293).

	// RemediationApplyTenantLabel — namespace or pod is missing the
	// Cordum tenant label so the K8s detector cannot resolve a tenant
	// id. Emits a `kubectl label namespace … cordum.io/tenant-id=…`
	// patch the operator runs. Maps to design doc §12.1 row
	// "tenant-label-missing".
	RemediationApplyTenantLabel RemediationActionKind = "apply_tenant_label"
	// RemediationAdoptUnmanagedWorkload — workload runs an agent image
	// but does not appear on the operator's workload allowlist AND has
	// no cordum-agentd sidecar. Emits two operator-applicable options:
	// add the workload name to the allowlist OR inject the agentd
	// sidecar via a Deployment patch. Maps to §12.1 row
	// "unmanaged-workload".
	RemediationAdoptUnmanagedWorkload RemediationActionKind = "adopt_unmanaged_workload"
	// RemediationRebaseAgentImage — agent image's registry prefix is
	// not on the operator's ImageRegistryAllowlist. Emits a rebase
	// recommendation: switch the workload manifest to an allowlisted
	// registry mirror. Maps to §12.1 row "untrusted-image".
	RemediationRebaseAgentImage RemediationActionKind = "rebase_agent_image"
	// RemediationExtendEgressPolicy — NetworkPolicy egress rule
	// permits traffic outside the operator-mandated LLM-proxy scope.
	// Emits a NetworkPolicy YAML patch extending `egress.to[]` to
	// include the LLM proxy CIDR / FQDN. Maps to §12.1 row
	// "egress-bypass".
	RemediationExtendEgressPolicy RemediationActionKind = "extend_egress_policy"

	// EDGE-143.7 — §12.1 CI scope templates. Operator-executed
	// workflow / config patches; Cordum NEVER mutates the CI repo or
	// the CI provider's settings per Q5 enforce-scope-out.

	// RemediationAddCordumEdgeAttach — CI workflow invokes an agent
	// action without first attaching cordum-edge. Emits a per-provider
	// workflow snippet (github_actions, gitlab_ci, jenkins, buildkite,
	// circleci) adding the `cordum-edge-attach@v1` step. Maps to
	// §12.1 row "missing-Cordum-attach".
	RemediationAddCordumEdgeAttach RemediationActionKind = "add_cordum_edge_attach"
	// RemediationConfigureOIDCTrust — CI provider OIDC trust root or
	// audience is not set in the operator's Cordum Edge configuration.
	// Emits the per-provider env-var configuration block
	// (CORDUM_EDGE_SHADOW_OIDC_TRUST_<provider> +
	// CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<provider>) per Q6. Maps to
	// §12.1 row "unmanaged-OIDC".
	RemediationConfigureOIDCTrust RemediationActionKind = "configure_oidc_trust"
	// RemediationRouteCISDKThroughProxy — CI job invokes a provider
	// SDK directly (not through the Cordum LLM proxy). Emits the
	// proxy-routing guidance plus the alternative "operator-acked
	// exception" snippet referencing EDGE-143.6's
	// `POST /api/v1/edge/shadow/exception`. Maps to §12.1 row
	// "direct-provider-SDK".
	RemediationRouteCISDKThroughProxy RemediationActionKind = "route_ci_sdk_through_proxy"
)

// RemediationAudience selects the wording + step shape. `dev` favours
// `cordumctl edge claude` local-wrapper guidance; `enterprise` favours
// managed-settings + MDM deployment guidance; `both` emits a layered
// plan with dev steps first followed by enterprise steps.
type RemediationAudience string

const (
	RemediationAudienceDev        RemediationAudience = "dev"
	RemediationAudienceEnterprise RemediationAudience = "enterprise"
	RemediationAudienceBoth       RemediationAudience = "both"
)

// RemediationSeverity is the operator-facing severity attached to the
// plan as a whole. Derived from the finding's risk, NOT from action
// kind — a deploy_managed_settings step on a critical finding stays
// critical even if the recommended action itself is preview-only.
type RemediationSeverity string

const (
	RemediationSeverityInfo   RemediationSeverity = "info"
	RemediationSeverityLow    RemediationSeverity = "low"
	RemediationSeverityMedium RemediationSeverity = "medium"
	RemediationSeverityHigh   RemediationSeverity = "high"
)

// RemediationAPIRequest describes a Cordum API call advisor operators
// can issue manually or via UI. The generator never executes; the
// shape exists so a future enforcement mode (out of scope) can wire
// `Method` + `Path` + `Body` into an action runner without re-parsing
// human-readable command text. All path/body values use placeholders
// (`<tenant-id>`, `<finding-id>`) — never live identifiers.
type RemediationAPIRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body,omitempty"`
}

// RemediationStep is one ordered advisory action in a plan. Steps are
// emitted in a deterministic order so JSON equality holds between
// repeated calls with the same input.
type RemediationStep struct {
	// ID is a stable identifier for this step within the plan. Used by
	// dashboards to attach per-step UI state; format: lowercase, dot-
	// separated, plan-action-derived (e.g. "attach_mcp_gateway.dev.1").
	ID string `json:"id"`
	// Title is the short operator-facing label.
	Title string `json:"title"`
	// Kind classifies the step for filtering / grouping. Mirrors the
	// plan's primary action_kind in most cases but a single plan may
	// emit multiple kinds (backup → disable → re-attach).
	Kind RemediationActionKind `json:"kind"`
	// Command is the shell-runnable suggestion. Uses literal
	// placeholders enclosed in `<…>`; never contains live secrets or
	// tenant-specific identifiers. Empty when the step is API-only.
	Command string `json:"command,omitempty"`
	// APIRequest is the structured Cordum API alternative when one
	// applies. Empty for shell-only steps.
	APIRequest *RemediationAPIRequest `json:"api_request,omitempty"`
	// RequiresBackup signals that the operator should snapshot the
	// target (config file, settings payload) before the step runs.
	// True for any disable / destructive kind even if the step itself
	// is preview_only.
	RequiresBackup bool `json:"requires_backup,omitempty"`
	// PreviewOnly signals that the operator should run the step in
	// dry-run mode first. Generator emits PreviewOnly=true for every
	// destructive kind; subsequent steps gated on operator review.
	PreviewOnly bool `json:"preview_only,omitempty"`
	// Destructive marks irreversible operations. Combined with
	// RequiresBackup + PreviewOnly, this gates UI confirmation.
	Destructive bool `json:"destructive,omitempty"`
	// DocsURL points at the canonical Cordum doc for this action.
	// Relative paths so the field works across deployments without
	// hard-coded hostnames.
	DocsURL string `json:"docs_url,omitempty"`
	// Conditions documents preconditions ("requires admin on host",
	// "tenant must have managed-settings enabled"). Short
	// human-readable strings; not machine-parsed.
	Conditions []string `json:"conditions,omitempty"`
}

// RemediationPlan is the top-level output of GenerateForFinding /
// GenerateForScannerFinding. JSON-stable: re-generating with the same
// inputs produces byte-equal output (modulo GeneratedAt, which is
// injected so tests can pin it).
type RemediationPlan struct {
	FindingID         string                `json:"finding_id,omitempty"`
	TenantID          string                `json:"tenant_id,omitempty"`
	Audience          RemediationAudience   `json:"audience"`
	Severity          RemediationSeverity   `json:"severity"`
	ActionKind        RemediationActionKind `json:"action_kind"`
	Summary           string                `json:"summary"`
	RiskExplanation   string                `json:"risk_explanation"`
	RecommendedAction string                `json:"recommended_action"`
	SafetyNotes       []string              `json:"safety_notes,omitempty"`
	Steps             []RemediationStep     `json:"steps"`
	GeneratorVersion  string                `json:"generator_version"`
	GeneratedAt       time.Time             `json:"generated_at"`
	// AdvisoryOnly is always true in this generator. Reserved field so
	// a future enforcement mode (out of scope per task rail #1) can
	// flip it without changing the type signature.
	AdvisoryOnly bool `json:"advisory_only"`
}

// GeneratorOptions tunes audience + side-channel controls. Zero value
// defaults to Audience=both, OmitCommands=false (commands included),
// Now=time.Now.
type GeneratorOptions struct {
	// Audience selects wording + step layering. Empty defaults to
	// RemediationAudienceBoth.
	Audience RemediationAudience
	// OmitCommands suppresses the Command and APIRequest.Body fields
	// when true. Defaults to false so operators get full commands;
	// dashboards that want lean summary cards pass true explicitly.
	// The semantic is inverted from a naive IncludeCommands flag so
	// the Go zero value matches the common case (full output).
	OmitCommands bool
	// Now is the clock seam. Tests inject a fixed time; production
	// leaves this nil so the generator falls back to time.Now.
	Now func() time.Time
}

// applyDefaults populates zero-value fields with the documented
// defaults. Returns a copy so the caller's GeneratorOptions is not
// mutated.
func (o GeneratorOptions) applyDefaults() GeneratorOptions {
	out := o
	switch out.Audience {
	case RemediationAudienceDev, RemediationAudienceEnterprise, RemediationAudienceBoth:
		// already valid
	default:
		out.Audience = RemediationAudienceBoth
	}
	if out.Now == nil {
		out.Now = time.Now
	}
	return out
}

// findingFeatures is the shape-agnostic projection of an input
// finding. Both ShadowAgentFinding and Finding normalize onto this
// struct; the generator's classification + step emission read only
// from here.
type findingFeatures struct {
	findingID       string
	tenantID        string
	agentProduct    string
	hostname        string
	risk            string
	status          string
	evidenceType    string
	evidenceSummary string
	redactedPath    string
	sourceType      string
	ciProvider      string
	signalSet       []string
	metadata        map[string]string

	// EDGE-143.7 — §10.1 fields surfaced for K8s + CI scope templates.
	// Omit-empty on the source struct; defaults to empty string here.
	clusterID    string
	namespace    string
	workloadKind string
	workloadName string
	podUID       string
	repo         string
	workflowID   string
}

// normalizeShadowAgentFinding projects an EDGE-141 lifecycle record
// onto findingFeatures. Defensive against nil.
func normalizeShadowAgentFinding(f *ShadowAgentFinding) findingFeatures {
	if f == nil {
		return findingFeatures{}
	}
	signals := make([]string, len(f.SignalSet))
	copy(signals, f.SignalSet)
	return findingFeatures{
		findingID:       f.FindingID,
		tenantID:        f.TenantID,
		agentProduct:    strings.ToLower(strings.TrimSpace(f.AgentProduct)),
		hostname:        f.Hostname,
		risk:            strings.ToLower(strings.TrimSpace(string(f.Risk))),
		status:          string(f.Status),
		evidenceType:    strings.ToLower(strings.TrimSpace(f.EvidenceType)),
		evidenceSummary: f.EvidenceSummary,
		redactedPath:    f.RedactedPath,
		sourceType:      strings.ToLower(strings.TrimSpace(f.SourceType)),
		ciProvider:      strings.ToLower(strings.TrimSpace(f.CIProvider)),
		signalSet:       signals,
		metadata:        copyShallowMetadata(f.Metadata),
		clusterID:       strings.TrimSpace(f.ClusterID),
		namespace:       strings.TrimSpace(f.Namespace),
		workloadKind:    strings.TrimSpace(f.WorkloadKind),
		workloadName:    strings.TrimSpace(f.WorkloadName),
		podUID:          strings.TrimSpace(f.PodUID),
		repo:            strings.TrimSpace(f.Repo),
		workflowID:      strings.TrimSpace(f.WorkflowID),
	}
}

// normalizeScannerFinding projects an EDGE-140 scanner observation
// onto findingFeatures. Status/risk strings are already lowercase per
// scanner contract; we normalise defensively anyway.
func normalizeScannerFinding(f *Finding) findingFeatures {
	if f == nil {
		return findingFeatures{}
	}
	return findingFeatures{
		findingID:       "", // scanner findings have no persistent ID
		tenantID:        f.TenantID,
		agentProduct:    strings.ToLower(strings.TrimSpace(f.Product)),
		hostname:        f.Hostname,
		risk:            strings.ToLower(strings.TrimSpace(f.Risk)),
		status:          f.Status,
		evidenceType:    strings.ToLower(strings.TrimSpace(f.EvidenceType)),
		evidenceSummary: f.RedactedConfigSummary,
		redactedPath:    f.RedactedPath,
		sourceType:      SourceTypeLocal,
	}
}

func copyShallowMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
