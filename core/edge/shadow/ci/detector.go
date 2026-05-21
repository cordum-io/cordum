package ci

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// Detector is the long-lived orchestrator that polls every registered
// ProviderScanner each tick, applies the shared signal-extraction +
// false-positive + tenant-mapping + emit pipeline, and pushes findings
// through the shared shadow.Store. Construction is via NewDetector; the
// zero value is invalid because store + quarantine tenant are required.
type Detector struct {
	cfg      Config
	store    shadow.Store
	resolver TenantResolver
	observer Observer

	mu    sync.Mutex
	clock func() time.Time
}

// NewDetector wires a Detector. store is required. cfg.Scanners may be
// empty (the detector starts cleanly and Scan is a no-op until a
// scanner is added). cfg.OIDC is normalized through LoadOIDCConfigFromEnv
// so operator env-var overrides take effect at construction time.
//
// Returns an error when:
//   - store is nil
//   - cfg.QuarantineTenantID is empty (refuse to silently drop
//     unrouted findings — same posture as the GitHub detector).
func NewDetector(cfg Config, store shadow.Store) (*Detector, error) {
	if store == nil {
		return nil, errors.New("ci detector: store is required")
	}
	if strings.TrimSpace(cfg.QuarantineTenantID) == "" {
		return nil, errors.New("ci detector: QuarantineTenantID is required (refuse to emit unrouted findings)")
	}
	if cfg.CordumAttachActionRef == "" {
		cfg.CordumAttachActionRef = "cordum/cordum-edge-attach"
	}
	if len(cfg.KnownAgentRunTokens) == 0 {
		cfg.KnownAgentRunTokens = []string{"claude", "codex", "cursor"}
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5 * time.Minute
	}
	if cfg.OIDC == nil {
		cfg.OIDC = map[Provider]OIDCConfig{}
	}
	for _, p := range []Provider{ProviderGitLab, ProviderJenkins, ProviderBuildkite, ProviderCircleCI} {
		cur := cfg.OIDC[p]
		next, err := LoadOIDCConfigFromEnv(p, cur)
		if err != nil {
			return nil, fmt.Errorf("ci detector: oidc env for %s: %w", p, err)
		}
		if !next.Disabled && next.ClaimsProvider == nil && next.TokenProvider != nil {
			cp, err := NewOIDCClaimsProvider(OIDCVerifierConfig{
				Issuer:        next.Issuer,
				Audience:      next.Audience,
				TokenProvider: next.TokenProvider,
				Provider:      p,
			})
			if err != nil {
				return nil, fmt.Errorf("ci detector: oidc verifier for %s: %w", p, err)
			}
			next.ClaimsProvider = cp
		}
		cfg.OIDC[p] = next
	}
	observer := cfg.Observer
	if observer == nil {
		observer = nopObserver{}
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = NewDefaultResolver(cfg.OrgRepoMap, cfg.QuarantineTenantID)
	}
	return &Detector{
		cfg:      cfg,
		store:    store,
		resolver: resolver,
		observer: observer,
		clock:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetClock pins the detector's clock. Tests use this for deterministic
// timestamps; production callers leave the default in place.
func (d *Detector) SetClock(f func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f == nil {
		f = func() time.Time { return time.Now().UTC() }
	}
	d.clock = f
}

func (d *Detector) now() time.Time {
	d.mu.Lock()
	f := d.clock
	d.mu.Unlock()
	return f()
}

// Config returns a copy-safe view of the active configuration. Used by
// scanners to read shared knobs (ProviderEndpointHosts, AgentConfigPaths,
// KnownAgentActionRefs, …) without re-plumbing them per scanner.
func (d *Detector) Config() Config { return d.cfg }

// Observer returns the detector's observability sink. Scanner emit
// paths use this directly so they don't have to remember the nop
// fallback.
func (d *Detector) Observer() Observer { return d.observer }

// Run executes Scan in a loop until ctx is cancelled. Scan-level
// errors are logged via the audit observer; transient provider
// outages do not terminate the loop.
func (d *Detector) Run(ctx context.Context) error {
	tick := time.NewTicker(d.cfg.ScanInterval)
	defer tick.Stop()
	for {
		if err := d.Scan(ctx); err != nil {
			d.observer.EmitAudit(audit.SIEMEvent{
				EventType: "edge.shadow_scan_error",
				Decision:  "error",
				Severity:  "warning",
				Reason:    err.Error(),
				Timestamp: d.now(),
			})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// Scan invokes every registered ProviderScanner once. A single failing
// scanner does not abort the others — the first error is returned for
// caller visibility while remaining scanners continue.
func (d *Detector) Scan(ctx context.Context) error {
	var firstErr error
	for _, s := range d.cfg.Scanners {
		if s == nil {
			continue
		}
		if err := s.Scan(ctx, d); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", s.Provider(), err)
			d.observer.EmitAudit(audit.SIEMEvent{
				EventType: "edge.shadow_scan_error",
				Decision:  "error",
				Severity:  "warning",
				Reason:    err.Error(),
				Timestamp: d.now(),
				Extra: map[string]string{
					"source_type": shadow.SourceTypeCI,
					"ci_provider": string(s.Provider()),
				},
			})
		}
	}
	return firstErr
}

// EmitRun is the shared emit pipeline every provider scanner pushes
// finished Run snapshots through. It:
//
//  1. Extracts §8.1 signals (runner identity, agent action, env var
//     names, agent config presence, direct provider endpoint, missing
//     attach).
//  2. Applies §14 false-positive controls.
//  3. Verifies OIDC claims and routes tenant/principal via
//     §6.3 / §6.4 precedence (fork PRs always quarantine).
//  4. Constructs a `shadow.CreateFindingRequest` with the typed CI
//     fields per §10.1, persists through the store, and emits one
//     `shadow_agent.observed` audit event + Prometheus counters.
//
// Returns nil for "no signals raised" — clean runs are NOT errors.
func (d *Detector) EmitRun(ctx context.Context, run Run) error {
	if run.Provider == "" {
		return errors.New("ci detector: run.Provider required")
	}
	if d.cfg.EdgeSessionHeartbeat != nil {
		run.EdgeHeartbeat = d.cfg.EdgeSessionHeartbeat(ctx, run)
	}

	hardSuppress, fpReason := d.evaluateFalsePositives(run)
	signals := d.collectSignals(run, hardSuppress)
	if len(signals) == 0 {
		return nil
	}

	claims := d.verifiedOIDCClaims(ctx, run)
	tenant, tenantSource := d.resolver.ResolveTenant(ctx, claims, run)
	if fpReason == fpReasonForkPR {
		tenant = d.cfg.QuarantineTenantID
		tenantSource = TenantSourceQuarantine
	}
	principal, principalSource := d.resolver.ResolvePrincipal(ctx, claims, run)
	if tenantSource == TenantSourceQuarantine {
		principal = PrincipalUnknown
		principalSource = PrincipalSourceQuarantine
	}

	signalIDs := signalIDsOf(signals)
	risk := signalsToRisk(signals)
	for _, s := range signals {
		d.observer.RecordFindingEmit(run.Provider, s.id, string(risk))
	}

	now := d.now()
	repoFull := strings.TrimSpace(run.Repo)
	req := shadow.CreateFindingRequest{
		TenantID:            tenant,
		OwnerPrincipalID:    principal,
		PrincipalID:         principal,
		AgentProduct:        agentProductForRun(run),
		Risk:                risk,
		EvidenceType:        shadow.EvidenceProcessName,
		EvidenceSummary:     buildEvidenceSummary(run, signals),
		RedactedPath:        RedactCIPath(run.Provider, repoFull, run.WorkflowPath),
		DetectedAt:          now,
		SourceType:          shadow.SourceTypeCI,
		SourceID:            string(run.Provider) + ":" + repoFull,
		CIProvider:          string(run.Provider),
		Repo:                repoFull,
		Ref:                 strings.TrimSpace(run.Ref),
		WorkflowID:          run.WorkflowID,
		JobID:               run.JobID,
		RunID:               run.RunID,
		RunnerID:            run.RunnerID,
		TenantSource:        tenantSource,
		PrincipalSource:     principalSource,
		SignalSet:           signalIDs,
		Confidence:          confidenceForSignals(signals),
		FirstSeen:           &now,
		LastSeen:            &now,
		FalsePositiveReason: fpReason,
		RetentionClass:      shadow.ShadowRetentionDefault,
	}

	if _, err := d.store.CreateFinding(ctx, req); err != nil {
		return fmt.Errorf("CreateFinding: %w", err)
	}

	slog.Info("shadow_detector finding_emit",
		"source_type", shadow.SourceTypeCI,
		"ci_provider", string(run.Provider),
		"repo", repoFull,
		"run_id", run.RunID,
		"signal_count", len(signalIDs),
		"risk", string(risk))

	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp:     now,
		EventType:     "edge.shadow_finding_created",
		Severity:      severityForRisk(risk),
		TenantID:      tenant,
		AgentID:       repoFull,
		AgentName:     string(run.Provider),
		Action:        "shadow_agent.observed",
		Decision:      "observed",
		Reason:        strings.Join(signalIDs, ","),
		RiskTags:      signalIDs,
		Identity:      principal,
		PolicyVersion: "edge-shadow-v1",
		Extra: map[string]string{
			"source_type":           shadow.SourceTypeCI,
			"ci_provider":           string(run.Provider),
			"repo":                  repoFull,
			"workflow_id":           run.WorkflowID,
			"run_id":                run.RunID,
			"signal":                strings.Join(signalIDs, ","),
			"risk":                  string(risk),
			"tenant_source":         tenantSource,
			"principal_source":      principalSource,
			"false_positive_reason": fpReason,
		},
	})
	return nil
}

// verifiedOIDCClaims runs the per-provider OIDC verifier. Returns nil
// when OIDC is disabled, no provider is configured, verification
// fails, or the verified claims don't match the configured issuer +
// audience — in every nil case the resolver falls through to §6.3
// tier-2.
func (d *Detector) verifiedOIDCClaims(ctx context.Context, run Run) *OIDCClaims {
	cfg, ok := d.cfg.OIDC[run.Provider]
	if !ok || cfg.Disabled || cfg.ClaimsProvider == nil {
		d.observer.OIDCVerifyOutcome(run.Provider, "disabled")
		return nil
	}
	claims, err := cfg.ClaimsProvider(ctx, run)
	if err != nil || claims == nil {
		result := classifyOIDCError(err)
		d.observer.OIDCVerifyOutcome(run.Provider, result)
		d.emitOIDCVerifyFailed(run, result)
		return nil
	}
	if !oidcClaimMatchesConfig(claims, cfg) {
		d.observer.OIDCVerifyOutcome(run.Provider, "aud_mismatch")
		d.emitOIDCVerifyFailed(run, "aud_mismatch")
		return nil
	}
	d.observer.OIDCVerifyOutcome(run.Provider, "ok")
	return claims
}

func (d *Detector) emitOIDCVerifyFailed(run Run, result string) {
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: d.now(),
		EventType: "edge.shadow_oidc_verify_failed",
		Severity:  "warning",
		Action:    "shadow_agent.oidc_verify_failed",
		Decision:  "rejected",
		Reason:    result,
		Extra: map[string]string{
			"source_type": shadow.SourceTypeCI,
			"ci_provider": string(run.Provider),
			"repo":        run.Repo,
			"run_id":      run.RunID,
			"result":      result,
		},
	})
}

func classifyOIDCError(err error) string {
	if err == nil {
		return "sig_invalid"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "expir"):
		return "exp"
	case strings.Contains(msg, "aud"):
		return "aud_mismatch"
	default:
		return "sig_invalid"
	}
}

func oidcClaimMatchesConfig(claims *OIDCClaims, cfg OIDCConfig) bool {
	expectedIssuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	claimIssuer := strings.TrimRight(strings.TrimSpace(claims.Issuer), "/")
	if expectedIssuer == "" || claimIssuer == "" || claimIssuer != expectedIssuer {
		return false
	}
	expectedAud := strings.TrimSpace(cfg.Audience)
	for _, a := range claims.Audiences {
		if strings.TrimSpace(a) == expectedAud {
			return true
		}
	}
	for _, a := range strings.Split(claims.Audience, ",") {
		if strings.TrimSpace(a) == expectedAud {
			return true
		}
	}
	return false
}

// signalCandidate is the internal shape produced by every extractor.
type signalCandidate struct {
	id     string
	weight float64
}

// Signal IDs (mirror github detector vocabulary so SIEM filters apply
// uniformly across all CI providers).
const (
	signalSelfHostedRunner    = "self_hosted_runner_unlabeled"
	signalMissingCordumAttach = "missing_cordum_attach"
	signalAgentConfigPresent  = "agent_config_present"
	signalEnvVarIndicator     = "env_var_name_indicator"
	signalDirectProvider      = "direct_provider_endpoint"
	signalAgentActionUsed     = "agent_action_used"
)

// §14 false-positive reason vocabulary.
const (
	fpReasonForkPR        = "fork_pr_ephemeral"
	fpReasonScheduled     = "scheduled"
	fpReasonAutomationBot = "automation_bot"
	fpReasonTestFixture   = "test_fixture"
	fpReasonDevSandbox    = "dev_sandbox"
)

func (d *Detector) collectSignals(run Run, hardSuppress map[string]bool) []signalCandidate {
	out := make([]signalCandidate, 0, 6)
	out = append(out, extractRunnerIdentitySignal(run)...)
	out = append(out, extractAgentActionSignals(run, d.cfg)...)
	out = append(out, extractEnvVarNameSignals(run)...)
	out = append(out, extractAgentConfigPresentSignal(run)...)
	out = append(out, extractDirectProviderSignal(run, d.cfg)...)
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

func (d *Detector) evaluateFalsePositives(run Run) (map[string]bool, string) {
	hardSuppress := map[string]bool{}
	var fpReason string

	// Ephemeral runner labels suppress the runner-identity signal so
	// short-lived auto-scaled runners don't spam findings.
	for _, l := range run.Labels {
		if strings.EqualFold(l, "ephemeral") || strings.EqualFold(l, "spot") {
			hardSuppress[signalSelfHostedRunner] = true
		}
	}

	if run.IsForkPR {
		fpReason = fpReasonForkPR
	} else if run.IsScheduled {
		fpReason = fpReasonScheduled
	}
	if fpReason == "" {
		actor := strings.ToLower(strings.TrimSpace(run.Actor))
		for _, a := range d.cfg.BotActorAllowlist {
			if actor != "" && strings.EqualFold(actor, a) {
				fpReason = fpReasonAutomationBot
				break
			}
		}
	}
	if fpReason == "" && d.cfg.TestFixtureRepos[run.Repo] {
		fpReason = fpReasonTestFixture
	}
	if fpReason == "" && d.cfg.DevSandboxRepos[run.Repo] {
		fpReason = fpReasonDevSandbox
	}
	return hardSuppress, fpReason
}

// extractRunnerIdentitySignal flags any runner whose labels include
// `self-hosted` without a managed-by tag (mirrors github detector).
func extractRunnerIdentitySignal(run Run) []signalCandidate {
	selfHosted := false
	managed := false
	for _, l := range run.Labels {
		ll := strings.ToLower(strings.TrimSpace(l))
		if ll == "self-hosted" || strings.HasPrefix(ll, "queue=self-hosted") || ll == "self_hosted" {
			selfHosted = true
		}
		if strings.HasPrefix(ll, "managed-by:") || ll == "cordum-managed" {
			managed = true
		}
	}
	if selfHosted && !managed {
		return []signalCandidate{{id: signalSelfHostedRunner, weight: 0.3}}
	}
	return nil
}

// extractAgentActionSignals emits both `agent_action_used` and
// `missing_cordum_attach` when a known agent action or run-token is
// observed without the Cordum attach action or an active heartbeat.
func extractAgentActionSignals(run Run, cfg Config) []signalCandidate {
	agentUsed := false
	attachUsed := false
	for _, ref := range run.UsesActions {
		if matchActionRef(ref, cfg.KnownAgentActionRefs) {
			agentUsed = true
		}
		if matchActionRef(ref, []string{cfg.CordumAttachActionRef}) {
			attachUsed = true
		}
	}
	if !agentUsed {
		for _, tok := range run.RunCommands {
			for _, known := range cfg.KnownAgentRunTokens {
				if strings.EqualFold(strings.TrimSpace(tok), known) {
					agentUsed = true
				}
			}
		}
	}
	if !agentUsed {
		return nil
	}
	if attachUsed || run.EdgeHeartbeat {
		return nil
	}
	out := []signalCandidate{{id: signalAgentActionUsed, weight: 0.2}}
	out = append(out, signalCandidate{id: signalMissingCordumAttach, weight: 0.6})
	return out
}

func extractEnvVarNameSignals(run Run) []signalCandidate {
	if len(run.EnvNames) == 0 {
		return nil
	}
	return []signalCandidate{{id: signalEnvVarIndicator, weight: 0.1}}
}

func extractAgentConfigPresentSignal(run Run) []signalCandidate {
	if len(run.AgentConfigPaths) == 0 {
		return nil
	}
	return []signalCandidate{{id: signalAgentConfigPresent, weight: 0.4}}
}

func extractDirectProviderSignal(run Run, cfg Config) []signalCandidate {
	if len(run.ProviderEndpointHits) == 0 {
		return nil
	}
	allow := map[string]bool{}
	for _, h := range cfg.ProviderEndpointHosts {
		allow[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, h := range run.ProviderEndpointHits {
		if allow[strings.ToLower(strings.TrimSpace(h))] {
			return []signalCandidate{{id: signalDirectProvider, weight: 0.7}}
		}
	}
	return nil
}

func matchActionRef(usesValue string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	base := usesValue
	if i := strings.IndexByte(base, '@'); i > 0 {
		base = base[:i]
	}
	if i := strings.IndexByte(base, ':'); i > 0 {
		// docker.io/foo/bar:tag → foo/bar
		base = base[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	for _, a := range allowed {
		al := strings.ToLower(strings.TrimSpace(a))
		if base == al {
			return true
		}
		// Tolerate registry-prefixed refs (e.g. docker.io/<allowed>).
		if strings.HasSuffix(base, "/"+al) {
			return true
		}
	}
	return false
}

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

func signalIDsOf(sigs []signalCandidate) []string {
	out := make([]string, 0, len(sigs))
	for _, s := range sigs {
		out = append(out, s.id)
	}
	return out
}

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

func agentProductForRun(run Run) string {
	for _, ref := range run.UsesActions {
		base := strings.ToLower(ref)
		switch {
		case strings.Contains(base, "claude-code"):
			return "claude_code"
		case strings.Contains(base, "cursor"):
			return "cursor"
		case strings.Contains(base, "codex"):
			return "codex"
		}
	}
	for _, tok := range run.RunCommands {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "claude":
			return "claude_code"
		case "codex":
			return "codex"
		case "cursor":
			return "cursor"
		}
	}
	switch run.Provider {
	case ProviderGitLab:
		return "gitlab_ci"
	case ProviderJenkins:
		return "jenkins_ci"
	case ProviderBuildkite:
		return "buildkite_ci"
	case ProviderCircleCI:
		return "circleci_ci"
	}
	return "ci"
}

func buildEvidenceSummary(run Run, sigs []signalCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider=%s repo=%s run_id=%s signals=", run.Provider, run.Repo, run.RunID)
	for i, s := range sigs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(s.id)
	}
	if path := RedactCIPath(run.Provider, run.Repo, run.WorkflowPath); path != "" {
		b.WriteString(" workflow_path=")
		b.WriteString(path)
	}
	if names := SanitizeEnvKeys(run.EnvNames); len(names) > 0 {
		b.WriteString(" env_keys=")
		b.WriteString(strings.Join(names, ","))
	}
	if len(run.AgentConfigPaths) > 0 {
		b.WriteString(" agent_config_paths=")
		b.WriteString(strings.Join(sortedUnique(redactAllPaths(run.Provider, run.Repo, run.AgentConfigPaths)), ","))
	}
	if len(run.AgentConfigSummaries) > 0 {
		b.WriteString(" config_summary=")
		b.WriteString(strings.Join(sortedUnique(run.AgentConfigSummaries), ";"))
	}
	if len(run.ProviderEndpointHits) > 0 {
		b.WriteString(" provider_hosts=")
		b.WriteString(strings.Join(sortedUnique(run.ProviderEndpointHits), ","))
	}
	return capEvidenceSummary(b.String())
}

func redactAllPaths(p Provider, repo string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		if r := RedactCIPath(p, repo, raw); r != "" {
			out = append(out, r)
		}
	}
	return out
}
