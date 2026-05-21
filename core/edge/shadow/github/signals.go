package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	gogithub "github.com/google/go-github/v74/github"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// signal IDs (mirror design doc §8.1). Kept as bare constants so the
// strings stay searchable by SIEM and dashboard filters; values match
// the validShadowSignal regex `^[a-z0-9_]{1,32}$` so the shadow store
// accepts them verbatim.
const (
	signalSelfHostedRunner    = "self_hosted_runner_unlabeled"
	signalMissingCordumAttach = "missing_cordum_attach"
	signalAgentConfigPresent  = "agent_config_present"
	signalEnvVarIndicator     = "env_var_name_indicator"
	signalDirectProvider      = "direct_provider_endpoint"
	signalAgentActionUsed     = "agent_action_used"
)

// §14 false-positive reason vocabulary. Surface to operators verbatim
// in dashboard + SIEM; do NOT translate at emit time.
const (
	fpReasonForkPR        = "fork_pr_ephemeral"
	fpReasonScheduled     = "scheduled"
	fpReasonAutomationBot = "automation_bot"
	fpReasonTestFixture   = "test_fixture"
	fpReasonDevSandbox    = "dev_sandbox"
)

// scanContext is the per-run derived state that every signal extractor
// reads from. Built once per run by scanRun so extractors stay pure
// functions of (run, jobs, workflowYAML, hostHits, cfg) without
// re-fetching the same artifacts.
type scanContext struct {
	org             string
	repo            string
	run             *gogithub.WorkflowRun
	repoFull        string
	jobs            []*gogithub.WorkflowJob
	workflow        *workflowSpec
	workflowPath    string
	edgeHeartbeat   bool
	configPaths     []string
	configSummaries []string
	hostHits        []string
}

// scanRun extracts every signal for one workflow run, applies §14
// false-positive controls, and emits one finding when the signal set
// is non-empty. Returns errSkipRun when nothing of interest was found
// (so the caller doesn't audit-spam for clean runs).
func (d *Detector) scanRun(ctx context.Context, org, repoName string, run *gogithub.WorkflowRun) error {
	jobs, err := d.listWorkflowJobs(ctx, org, repoName, run.GetID())
	if err != nil {
		return err
	}

	// run.Path is the canonical workflow file path; production runs
	// always carry it. Fall back to the conventional default so test
	// fixtures (and the rare API edge-case) still surface a workflow.
	workflowPath := run.GetPath()
	if workflowPath == "" {
		workflowPath = ".github/workflows/ci.yml"
	}
	workflowYAML, yamlErr := d.fetchWorkflowYAML(ctx, org, repoName, workflowPath, run.GetHeadSHA())
	if yamlErr != nil {
		d.emitWorkflowFetchDegraded(workflowPath, yamlErr, run)
	}
	parsed := parseWorkflowYAML(workflowYAML)

	repoFull := fmt.Sprintf("%s/%s", org, repoName)
	scan := &scanContext{
		org:           org,
		repo:          repoName,
		run:           run,
		repoFull:      repoFull,
		jobs:          jobs,
		workflow:      parsed,
		workflowPath:  workflowPath,
		edgeHeartbeat: d.edgeSessionHeartbeat(ctx, run),
	}

	if d.cfg.JobLogHostHits != nil {
		hits, hitErr := d.cfg.JobLogHostHits(ctx, org, repoName, run.GetID())
		if hitErr == nil {
			scan.hostHits = sanitizeProviderHosts(hits, d.cfg.ProviderEndpointHosts)
		}
	}

	hardSuppress, fpReason := d.evaluateFalsePositives(scan)
	signals := d.collectSignals(ctx, scan, hardSuppress)
	if len(signals) == 0 {
		return errSkipRun
	}

	claims := d.verifiedOIDCClaims(ctx, run)
	tenant, tenantSource := d.resolver.ResolveTenant(ctx, claims, run, run.GetRepository())
	// Fork PRs intentionally route to quarantine regardless of map
	// hit because the run's identity comes from an untrusted fork.
	if fpReason == fpReasonForkPR {
		tenant = d.cfg.QuarantineTenantID
		tenantSource = TenantSourceQuarantine
	}
	principal, principalSource := d.resolver.ResolvePrincipal(ctx, claims, run)
	if tenantSource == TenantSourceQuarantine {
		principal = "unknown"
		principalSource = PrincipalSourceQuarantine
	}

	risk := signalsToRisk(signals)
	signalIDs := signalIDsOf(signals)
	for _, s := range signals {
		d.observer.RecordFindingEmit(s.id, string(risk))
	}

	job := primaryJob(scan.jobs)
	jobID := ""
	runnerID := ""
	if job != nil {
		jobID = strconv.FormatInt(job.GetID(), 10)
		runnerID = strconv.FormatInt(job.GetRunnerID(), 10)
	}

	now := d.now()
	req := shadow.CreateFindingRequest{
		TenantID:            tenant,
		OwnerPrincipalID:    principal,
		PrincipalID:         principal,
		AgentProduct:        agentProductForSignals(scan),
		Risk:                risk,
		EvidenceType:        shadow.EvidenceProcessName,
		EvidenceSummary:     buildEvidenceSummary(scan, signals),
		RedactedPath:        redactCIPath(repoFull, workflowPath),
		DetectedAt:          now,
		SourceType:          shadow.SourceTypeCI,
		SourceID:            "github_actions:" + repoFull,
		CIProvider:          shadow.CIProviderGitHubActions,
		Repo:                repoFull,
		Ref:                 strings.TrimSpace(run.GetHeadBranch()),
		WorkflowID:          strconv.FormatInt(run.GetWorkflowID(), 10),
		JobID:               jobID,
		RunID:               strconv.FormatInt(run.GetID(), 10),
		RunnerID:            runnerID,
		TenantSource:        tenantSource,
		PrincipalSource:     principalSource,
		SignalSet:           signalIDs,
		Confidence:          confidenceForSignals(signals),
		FirstSeen:           &now,
		LastSeen:            &now,
		FalsePositiveReason: fpReason,
		RetentionClass:      shadow.ShadowRetentionDefault,
	}

	if _, createErr := d.store.CreateFinding(ctx, req); createErr != nil {
		return fmt.Errorf("CreateFinding: %w", createErr)
	}

	slog.Info("shadow_detector finding_emit",
		"source_type", githubActionsSourceType,
		"ci_provider", shadow.CIProviderGitHubActions,
		"repo", repoFull,
		"run_id", req.RunID,
		"signal_count", len(signalIDs),
		"risk", string(risk))

	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp:     now,
		EventType:     "edge.shadow_finding_created",
		Severity:      severityForRisk(risk),
		TenantID:      tenant,
		AgentID:       repoFull,
		AgentName:     "github_actions",
		Action:        "shadow_agent.observed",
		Decision:      "observed",
		Reason:        strings.Join(signalIDs, ","),
		RiskTags:      signalIDs,
		Identity:      principal,
		PolicyVersion: "edge-shadow-v1",
		Extra: map[string]string{
			"source_type":           githubActionsSourceType,
			"ci_provider":           shadow.CIProviderGitHubActions,
			"repo":                  repoFull,
			"workflow_id":           req.WorkflowID,
			"run_id":                req.RunID,
			"tenant_source":         tenantSource,
			"principal_source":      principalSource,
			"false_positive_reason": fpReason,
		},
	})
	return nil
}

func (d *Detector) listWorkflowJobs(ctx context.Context, org, repoName string, runID int64) ([]*gogithub.WorkflowJob, error) {
	opts := &gogithub.ListWorkflowJobsOptions{ListOptions: gogithub.ListOptions{PerPage: 30}}
	out := make([]*gogithub.WorkflowJob, 0, 30)
	for page := 0; page < maxGitHubPagesPerScan; page++ {
		jobs, resp, err := d.gh.Actions.ListWorkflowJobs(ctx, org, repoName, runID, opts)
		d.recordRateLimit(resp)
		if err != nil {
			return nil, fmt.Errorf("ListWorkflowJobs: %w", err)
		}
		out = append(out, jobsSlice(jobs)...)
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (d *Detector) verifiedOIDCClaims(ctx context.Context, run *gogithub.WorkflowRun) *OIDCClaims {
	if d.cfg.OIDCDisabled || d.cfg.OIDCClaimsProvider == nil {
		return nil
	}
	claims, err := d.cfg.OIDCClaimsProvider(ctx, run)
	if err != nil || claims == nil {
		result := classifyOIDCError(err)
		d.observer.OIDCVerifyOutcome(result)
		d.emitOIDCVerifyFailed(result, run)
		return nil
	}
	if !claimMatchesOIDCConfig(claims, d.cfg) {
		d.observer.OIDCVerifyOutcome("aud_mismatch")
		d.emitOIDCVerifyFailed("aud_mismatch", run)
		return nil
	}
	d.observer.OIDCVerifyOutcome("ok")
	return claims
}

func classifyOIDCError(err error) string {
	if err == nil {
		return "sig_invalid"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "exp") || strings.Contains(msg, "expired"):
		return "exp"
	case strings.Contains(msg, "aud"):
		return "aud_mismatch"
	default:
		return "sig_invalid"
	}
}

func (d *Detector) emitOIDCVerifyFailed(result string, run *gogithub.WorkflowRun) {
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: d.now(),
		EventType: "edge.shadow_oidc_verify_failed",
		Severity:  "warning",
		Action:    "shadow_agent.oidc_verify_failed",
		Decision:  "rejected",
		Reason:    result,
		Extra: map[string]string{
			"source_type": githubActionsSourceType,
			"ci_provider": shadow.CIProviderGitHubActions,
			"repo":        safeRunRepo(run),
			"run_id":      strconv.FormatInt(run.GetID(), 10),
			"result":      result,
		},
	})
}

func claimMatchesOIDCConfig(claims *OIDCClaims, cfg Config) bool {
	issuer := strings.TrimRight(strings.TrimSpace(claims.Issuer), "/")
	expectedIssuer := strings.TrimRight(strings.TrimSpace(cfg.OIDCIssuer), "/")
	expectedAudience := strings.TrimSpace(cfg.OIDCAudience)
	return issuer != "" && issuer == expectedIssuer &&
		oidcAudienceMatches(claims, expectedAudience)
}

func oidcAudienceMatches(claims *OIDCClaims, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, aud := range claims.Audiences {
		if strings.TrimSpace(aud) == expected {
			return true
		}
	}
	for _, aud := range strings.Split(claims.Audience, ",") {
		if strings.TrimSpace(aud) == expected {
			return true
		}
	}
	return false
}

// signalCandidate is the internal shape produced by every extractor.
// id is the §8.1 short name; weight contributes to the aggregate
// confidence score. Extractors append zero-or-more candidates; the
// caller orders them and stamps SignalSet + Confidence.
type signalCandidate struct {
	id     string
	weight float64
}

// collectSignals runs the §8.1 extractor set with hardSuppress as a
// hard-skip filter (used for ephemeral runners). Returned candidates
// are sorted + deduped so SignalSet has a deterministic order at emit
// time.
func (d *Detector) collectSignals(ctx context.Context, scan *scanContext, hardSuppress map[string]bool) []signalCandidate {
	out := make([]signalCandidate, 0, 8)
	out = append(out, extractRunnerIdentitySignal(scan)...)
	out = append(out, extractAgentActionSignals(scan, d.cfg)...)
	out = append(out, extractEnvVarNameSignals(scan)...)
	out = append(out, extractAgentConfigPresentSignal(ctx, scan, d)...)
	out = append(out, extractDirectProviderSignal(scan, d.cfg)...)
	if len(hardSuppress) > 0 {
		filtered := out[:0]
		for _, s := range out {
			if hardSuppress[s.id] {
				continue
			}
			filtered = append(filtered, s)
		}
		out = filtered
	}
	dedupSignals(&out)
	return out
}

// evaluateFalsePositives walks the §14 controls and returns:
//
//   - a hard-suppress set of signal IDs that must NOT be emitted (e.g.
//     ephemeral runners suppress self_hosted_runner_unlabeled);
//   - a single fpReason string that, when non-empty, is stamped on the
//     finding's FalsePositiveReason field.
//
// Multiple FP controls can apply to one run; we pick the highest-
// priority reason so dashboards have a single label per finding.
// Priority (highest first): fork_pr_ephemeral > automation_bot >
// scheduled > test_fixture > dev_sandbox.
func (d *Detector) evaluateFalsePositives(scan *scanContext) (map[string]bool, string) {
	hardSuppress := map[string]bool{}
	var fpReason string

	for _, j := range scan.jobs {
		for _, l := range j.Labels {
			if strings.EqualFold(l, "ephemeral") {
				hardSuppress[signalSelfHostedRunner] = true
			}
		}
	}

	if scan.run != nil {
		event := strings.ToLower(strings.TrimSpace(scan.run.GetEvent()))
		head := scan.run.GetHeadRepository()
		if head != nil && head.GetFork() && event == "pull_request" {
			fpReason = fpReasonForkPR
		} else if event == "schedule" {
			fpReason = fpReasonScheduled
		}

		actor := strings.ToLower(strings.TrimSpace(loginOf(scan.run.GetActor())))
		if fpReason == "" && actor != "" {
			for _, a := range d.cfg.BotActorAllowlist {
				if strings.EqualFold(actor, a) {
					fpReason = fpReasonAutomationBot
					break
				}
			}
		}
	}

	if fpReason == "" && d.cfg.TestFixtureRepos[scan.repoFull] {
		fpReason = fpReasonTestFixture
	}
	if fpReason == "" && d.cfg.DevSandboxRepos[scan.repoFull] {
		fpReason = fpReasonDevSandbox
	}
	return hardSuppress, fpReason
}

// extractRunnerIdentitySignal flags any job whose runner labels include
// `self-hosted` without a managed-by tag. Detector tags every label
// case-insensitively because GitHub canonicalizes to lowercase but
// operators sometimes hand-edit YAML with mixed case.
func extractRunnerIdentitySignal(scan *scanContext) []signalCandidate {
	for _, j := range scan.jobs {
		selfHosted := false
		managed := false
		for _, l := range j.Labels {
			ll := strings.ToLower(l)
			if ll == "self-hosted" {
				selfHosted = true
			}
			if strings.HasPrefix(ll, "managed-by:") || ll == "cordum-managed" {
				managed = true
			}
		}
		if selfHosted && !managed {
			return []signalCandidate{{id: signalSelfHostedRunner, weight: 0.3}}
		}
	}
	return nil
}

// extractAgentActionSignals walks the parsed workflow YAML and emits:
//
//   - agent_action_used — known agent action ref or run-token observed
//   - missing_cordum_attach — known agent use AND no attach action or
//     EdgeSession heartbeat in the same workflow run.
//
// Both signals contribute to the finding so dashboards can distinguish
// "agent present but managed" from "agent present without attach".
func extractAgentActionSignals(scan *scanContext, cfg Config) []signalCandidate {
	if scan.workflow == nil {
		return nil
	}
	agentUsed := false
	attachUsed := false
	for _, ref := range scan.workflow.AllUses() {
		if matchActionRef(ref, cfg.KnownAgentActionRefs) {
			agentUsed = true
		}
		if matchActionRef(ref, []string{cfg.CordumAttachActionRef}) {
			attachUsed = true
		}
	}
	if !agentUsed {
		agentUsed = scan.workflow.HasRunLeadToken(cfg.KnownAgentRunTokens)
	}
	if !agentUsed {
		return nil
	}
	if attachUsed || scan.edgeHeartbeat {
		return nil
	}
	out := []signalCandidate{{id: signalAgentActionUsed, weight: 0.2}}
	out = append(out, signalCandidate{id: signalMissingCordumAttach, weight: 0.6})
	return out
}

// matchActionRef compares a workflow `uses:` value against an allow
// list, ignoring the `@<ref>` suffix so `cordum/cordum-edge-attach@v1`
// matches `cordum/cordum-edge-attach`.
func matchActionRef(usesValue string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	base := usesValue
	if i := strings.IndexByte(base, '@'); i > 0 {
		base = base[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	for _, a := range allowed {
		if base == strings.ToLower(strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

// extractEnvVarNameSignals captures the NAMES of every env-var defined
// at workflow/job/step level. The §5.2 contract forbids capturing the
// values, so the extractor never reads the map values into the
// candidate evidence — only keys. The signal id is bounded because
// SignalSet is enum-validated; per-env-var detail is surfaced via
// EvidenceSummary downstream.
func extractEnvVarNameSignals(scan *scanContext) []signalCandidate {
	if scan.workflow == nil {
		return nil
	}
	if len(scan.workflow.AllEnvKeys()) == 0 {
		return nil
	}
	return []signalCandidate{{id: signalEnvVarIndicator, weight: 0.1}}
}

// extractAgentConfigPresentSignal probes each cfg.AgentConfigPaths
// entry with a HEAD-equivalent GetContents call. A 200 response with
// any byte count marks "agent config file present in repo"; a 404
// silently moves on. Other errors are not surfaced as signals because
// the upstream cause (token expired, repo deleted) is the operator's
// problem to triage.
func extractAgentConfigPresentSignal(ctx context.Context, scan *scanContext, d *Detector) []signalCandidate {
	if len(d.cfg.AgentConfigPaths) == 0 {
		return nil
	}
	for _, p := range d.cfg.AgentConfigPaths {
		ref := scan.run.GetHeadSHA()
		file, _, resp, err := d.gh.Repositories.GetContents(ctx, scan.org, scan.repo, p, &gogithub.RepositoryContentGetOptions{Ref: ref})
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			continue
		}
		if err != nil || file == nil {
			continue
		}
		scan.configPaths = append(scan.configPaths, redactCIPath(scan.repoFull, p))
		if summary := redactedConfigSummary(file); summary != "" {
			scan.configSummaries = append(scan.configSummaries, summary)
		}
		return []signalCandidate{{id: signalAgentConfigPresent, weight: 0.4}}
	}
	return nil
}

// extractDirectProviderSignal emits when the operator-supplied
// JobLogHostHits hook reported a hit against a known provider host. We
// re-filter against cfg.ProviderEndpointHosts so a misconfigured hook
// can't smuggle arbitrary hostnames into the signal stream.
func extractDirectProviderSignal(scan *scanContext, cfg Config) []signalCandidate {
	if len(scan.hostHits) == 0 {
		return nil
	}
	allow := map[string]bool{}
	for _, h := range cfg.ProviderEndpointHosts {
		allow[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, h := range scan.hostHits {
		if allow[strings.ToLower(strings.TrimSpace(h))] {
			return []signalCandidate{{id: signalDirectProvider, weight: 0.7}}
		}
	}
	return nil
}

// fetchWorkflowYAML reads the workflow file contents at the run's head
// SHA. Returns empty string on any error so signal extractors degrade
// gracefully — the rest of the run still emits whatever signals it
// can.
func (d *Detector) fetchWorkflowYAML(ctx context.Context, org, repo, path, ref string) (string, error) {
	if path == "" {
		return "", nil
	}
	file, _, _, err := d.gh.Repositories.GetContents(ctx, org, repo, path, &gogithub.RepositoryContentGetOptions{Ref: ref})
	if err != nil || file == nil {
		return "", err
	}
	// RepositoryContent.GetContent auto-decodes base64 when encoding is
	// set; it returns (decoded string, decode error). On decode failure
	// we fall back to the raw `Content` pointer with a manual base64
	// pass so a malformed body never silently swallows the workflow.
	if decoded, derr := file.GetContent(); derr == nil && decoded != "" {
		return decoded, nil
	}
	if c := file.Content; c != nil {
		if file.GetEncoding() == "base64" {
			if decoded, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(*c, "\n", "")); derr == nil {
				return string(decoded), nil
			}
		}
		return *c, nil
	}
	return "", nil
}

func (d *Detector) edgeSessionHeartbeat(ctx context.Context, run *gogithub.WorkflowRun) bool {
	if d.cfg.EdgeSessionHeartbeat == nil {
		return false
	}
	alive, err := d.cfg.EdgeSessionHeartbeat(ctx, run)
	if err == nil {
		return alive
	}
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: d.now(),
		EventType: "edge.shadow_session_lookup_error",
		Severity:  "warning",
		Action:    "shadow_agent.session_lookup",
		Decision:  "error",
		Reason:    "edge_session_lookup_error",
		Extra: map[string]string{
			"source_type": githubActionsSourceType,
			"ci_provider": shadow.CIProviderGitHubActions,
			"repo":        safeRunRepo(run),
			"run_id":      strconv.FormatInt(run.GetID(), 10),
		},
	})
	return false
}

func (d *Detector) emitWorkflowFetchDegraded(path string, err error, run *gogithub.WorkflowRun) {
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: d.now(),
		EventType: "edge.shadow_workflow_fetch_degraded",
		Severity:  "warning",
		Action:    "shadow_agent.workflow_fetch",
		Decision:  "degraded",
		Reason:    "workflow_yaml_unavailable",
		Extra: map[string]string{
			"source_type": githubActionsSourceType,
			"ci_provider": shadow.CIProviderGitHubActions,
			"path":        redactCIPath(safeRunRepo(run), path),
			"run_id":      strconv.FormatInt(run.GetID(), 10),
			"error":       sanitizeDegradedError(err),
		},
	})
}

// dedupSignals keeps the first occurrence of each signal id; output is
// stable so SignalSet has a deterministic order.
func dedupSignals(in *[]signalCandidate) {
	seen := map[string]struct{}{}
	out := (*in)[:0]
	for _, s := range *in {
		if _, ok := seen[s.id]; ok {
			continue
		}
		seen[s.id] = struct{}{}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].id < out[j].id })
	*in = out
}

// signalIDsOf returns the SignalSet projection used by
// CreateFindingRequest.
func signalIDsOf(sigs []signalCandidate) []string {
	out := make([]string, 0, len(sigs))
	for _, s := range sigs {
		out = append(out, s.id)
	}
	return out
}

// signalsToRisk maps the highest-weight signal to a finding risk. The
// mapping is intentionally coarse — operators care about high vs.
// medium more than fine-grained tiers, and risk is a UI/filter input,
// not a policy gate.
func signalsToRisk(sigs []signalCandidate) shadow.FindingRisk {
	var max float64
	for _, s := range sigs {
		if s.weight > max {
			max = s.weight
		}
	}
	switch {
	case max >= 0.6:
		return shadow.FindingRiskHigh
	case max >= 0.3:
		return shadow.FindingRiskMedium
	default:
		return shadow.FindingRiskLow
	}
}

// confidenceForSignals aggregates per-signal weights into a single
// [0..1] score. We take min(1.0, sum) so a stack of low-weight signals
// still saturates to "confident" while no single signal can vault past
// the cap.
func confidenceForSignals(sigs []signalCandidate) float64 {
	var sum float64
	for _, s := range sigs {
		sum += s.weight
	}
	if sum > 1.0 {
		sum = 1.0
	}
	return sum
}

// severityForRisk projects FindingRisk into the SIEM severity
// vocabulary so audit consumers can filter alerts uniformly across
// detectors. Matches the K8s detector's mapping (EDGE-143.1).
func severityForRisk(r shadow.FindingRisk) string {
	switch r {
	case shadow.FindingRiskCritical:
		return "critical"
	case shadow.FindingRiskHigh:
		return "high"
	case shadow.FindingRiskMedium:
		return "medium"
	default:
		return "info"
	}
}

// agentProductForSignals chooses the agent_product label for the
// finding. When the workflow uses a known agent action, we lift that
// action's vendor portion (`anthropic-ai/claude-code-action` →
// `claude_code`). Otherwise we fall back to a generic CI label so the
// store's required-field validation passes.
func agentProductForSignals(scan *scanContext) string {
	if scan.workflow != nil {
		for _, ref := range scan.workflow.AllUses() {
			base := ref
			if i := strings.IndexByte(base, '@'); i > 0 {
				base = base[:i]
			}
			switch {
			case strings.Contains(base, "claude-code"):
				return "claude_code"
			case strings.Contains(base, "cursor"):
				return "cursor"
			case strings.Contains(base, "codex"):
				return "codex"
			}
		}
	}
	return "github_actions_ci"
}

// buildEvidenceSummary produces the redacted human-readable evidence
// blob persisted on the finding. It never includes env-var values or
// secret content — only NAMES + signal labels — so the §5.2 data-
// minimization contract holds. Total length is capped at
// shadow.MaxEvidenceSummaryBytes before persist via the store's
// stripSecretMarkers pass.
func buildEvidenceSummary(scan *scanContext, sigs []signalCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "repo=%s run_id=%d signals=", scan.repoFull, scan.run.GetID())
	for i, s := range sigs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(s.id)
	}
	if path := redactCIPath(scan.repoFull, scan.workflowPath); path != "" {
		b.WriteString(" workflow_path=")
		b.WriteString(path)
	}
	if scan.workflow != nil {
		names := scan.workflow.AllEnvKeys()
		if len(names) > 0 {
			b.WriteString(" env_keys=")
			b.WriteString(strings.Join(sanitizeEnvKeys(names), ","))
		}
	}
	if len(scan.configPaths) > 0 {
		b.WriteString(" agent_config_paths=")
		b.WriteString(strings.Join(sortedUnique(scan.configPaths), ","))
	}
	if len(scan.configSummaries) > 0 {
		b.WriteString(" config_summary=")
		b.WriteString(strings.Join(sortedUnique(scan.configSummaries), ";"))
	}
	if len(scan.hostHits) > 0 {
		b.WriteString(" provider_hosts=")
		b.WriteString(strings.Join(sortedUnique(scan.hostHits), ","))
	}
	return capEvidenceSummary(b.String())
}

// jobsSlice converts go-github's pointer-of-slice list response into a
// plain slice of non-nil pointers, tolerating nil input.
func jobsSlice(jobs *gogithub.Jobs) []*gogithub.WorkflowJob {
	if jobs == nil {
		return nil
	}
	out := make([]*gogithub.WorkflowJob, 0, len(jobs.Jobs))
	for _, j := range jobs.Jobs {
		if j == nil {
			continue
		}
		out = append(out, j)
	}
	return out
}

// primaryJob returns the first non-nil job (used to populate the
// finding's job_id + runner_id fields). The detector currently emits
// one finding per run, not per job; multi-job runs surface a
// representative job id rather than fanning out to many findings.
func primaryJob(jobs []*gogithub.WorkflowJob) *gogithub.WorkflowJob {
	for _, j := range jobs {
		return j
	}
	return nil
}
