// Package github implements the GitHub Actions CI shadow-agent
// detector (EDGE-143.2). It is observe-mode-only: every external call
// is read-only via go-github; no comment, dispatch, or check-run write
// will ever be issued. Findings are minted through the shared
// shadow.Store contract with §10.1 typed CI fields, tenant-scoped per
// §6.3, and tagged with §14 false-positive reasons when appropriate.
//
// The design doc reference is `docs/edge/kubernetes-ci-shadow-detector-design.md`
// §8.1/§6.3/§6.4/§14 plus governor ruling comment-a17f4f1c (Q6 OIDC
// trust roots).
package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	gogithub "github.com/google/go-github/v74/github"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// DefaultGitHubOIDCIssuer is the GitHub Actions OIDC trust root per
// governor ruling Q6 (comment-a17f4f1c on task-de50a293). Operators can
// override via CORDUM_EDGE_SHADOW_OIDC_TRUST_github=<url>, or refuse
// OIDC entirely with CORDUM_EDGE_SHADOW_OIDC_TRUST_github=disabled
// (forces fall-through to §6.3 tier-2 org/repo map).
const DefaultGitHubOIDCIssuer = "https://token.actions.githubusercontent.com"

// envOIDCTrust and envOIDCAudience are the operator-override env vars
// honored by LoadOIDCConfigFromEnv. The `_github` suffix scopes the
// override to the GitHub Actions detector so a future GitLab/Bitbucket
// detector can take a parallel `_gitlab`/`_bitbucket` knob without
// re-namespacing.
const (
	envOIDCTrust    = "CORDUM_EDGE_SHADOW_OIDC_TRUST_github"
	envOIDCAudience = "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_github"
)

const maxGitHubPagesPerScan = 10

const githubActionsSourceType = shadow.CIProviderGitHubActions

// JobLogHostHitFn is the contract a caller can plug to feed
// directProviderEndpoint signals into the detector without forcing the
// package to vendor a job-log streaming dependency. Returning a
// hostname slice (already deduped) signals an active call to a known
// provider hostname from inside the job run. Empty slice = no hits.
type JobLogHostHitFn func(ctx context.Context, org, repo string, runID int64) ([]string, error)

// EdgeSessionHeartbeatLookup reports whether the workflow run already
// produced a Cordum EdgeSession heartbeat. A true result suppresses the
// missing_cordum_attach signal for managed CI runs.
type EdgeSessionHeartbeatLookup func(ctx context.Context, run *gogithub.WorkflowRun) (bool, error)

// OIDCTokenProvider supplies a raw GitHub Actions OIDC JWT for a
// workflow run. The detector verifies the token with coreos/go-oidc
// before using any claims for tenant/principal mapping.
type OIDCTokenProvider func(ctx context.Context, run *gogithub.WorkflowRun) (string, error)

// OIDCClaimsProvider returns a claims subset that has been
// cryptographically verified against the configured issuer + audience.
// NewDetector wires a real go-oidc-backed provider automatically when
// OIDCTokenProvider is configured; tests may inject a provider directly.
type OIDCClaimsProvider func(ctx context.Context, run *gogithub.WorkflowRun) (*OIDCClaims, error)

// Config drives a Detector. Every field has a safe zero-value default,
// so an operator can stand up the detector with `Config{Orgs: [...]}`
// and lean on Cordum-shipped defaults for everything else. Defaults are
// applied in NewDetector so the validation logic stays centralized.
type Config struct {
	// GHToken authenticates the GitHub API client. PAT or App-installation
	// token; the caller is responsible for refresh. Empty == anonymous
	// (lower rate-limit ceiling, public-repo-only).
	GHToken string

	// Orgs is the set of GitHub organizations the detector scans on each
	// tick. Empty == nothing scanned (the detector still starts cleanly).
	Orgs []string

	// OrgRepoMap maps org+repo to a Cordum tenant id per §6.3 tier-2. A
	// missing entry routes the finding to QuarantineTenantID.
	OrgRepoMap map[string]map[string]string

	// QuarantineTenantID is the tenant that catches every unmapped
	// finding. Empty == the detector refuses to start (NewDetector errors)
	// because emitting unrouted findings would risk cross-tenant leak.
	QuarantineTenantID string

	// AgentConfigPaths is the closed set of repo paths the detector
	// probes for known agent configuration files. Presence alone (not
	// content) drives the agent_config_present signal.
	AgentConfigPaths []string

	// KnownAgentActionRefs is the closed set of GitHub Actions action
	// references that mark a workflow as running an agent. A workflow
	// step `uses:` matching one of these contributes to the
	// missing_cordum_attach signal (when CordumAttachActionRef is also
	// absent).
	KnownAgentActionRefs []string

	// KnownAgentRunTokens is the closed set of leading shell tokens that
	// indicate run-command agent use. Only the leading token is compared;
	// arguments are never persisted.
	KnownAgentRunTokens []string

	// CordumAttachActionRef is the action ref that, when present in a
	// workflow, marks the workflow as governed by Cordum Edge. Default
	// `cordum/cordum-edge-attach`.
	CordumAttachActionRef string

	// ProviderEndpointHosts is the closed set of host names that count
	// as direct-provider endpoints when reported via JobLogHostHits.
	// Defaults to api.anthropic.com / api.openai.com /
	// generativelanguage.googleapis.com.
	ProviderEndpointHosts []string

	// BotActorAllowlist names automation actors (dependabot[bot],
	// renovate[bot], …) whose runs are tagged with
	// FalsePositiveReason=automation_bot per §14.
	BotActorAllowlist []string

	// TestFixtureRepos / DevSandboxRepos are the §14 operator-curated
	// repo sets that surface findings with FP reasons but DO NOT
	// suppress them (operators still want visibility, just labelled).
	TestFixtureRepos map[string]bool
	DevSandboxRepos  map[string]bool

	// JobLogHostHits is the caller-supplied hook that surfaces direct
	// provider-endpoint calls without forcing this package to vendor a
	// job-log streamer. Nil == the signal is never raised.
	JobLogHostHits JobLogHostHitFn

	// EdgeSessionHeartbeat reports whether a run has a live Cordum
	// EdgeSession heartbeat. Nil == no heartbeat observed, so unmanaged
	// agent workflows still emit missing_cordum_attach.
	EdgeSessionHeartbeat EdgeSessionHeartbeatLookup

	// OIDCIssuer is the expected JWT `iss` claim. Defaults to
	// DefaultGitHubOIDCIssuer when LoadOIDCConfigFromEnv is used and the
	// env var is unset; operator override is honored when present.
	OIDCIssuer string

	// OIDCAudience is the expected JWT `aud` claim. Defaults to
	// "cordum-edge" via LoadOIDCConfigFromEnv when the env var is unset.
	OIDCAudience string

	// OIDCDisabled, when true, skips OIDC verification entirely so the
	// resolver falls through to §6.3 tier-2 org/repo map. Set when the
	// operator pins CORDUM_EDGE_SHADOW_OIDC_TRUST_github=disabled.
	OIDCDisabled bool

	// OIDCTokenProvider supplies a raw GitHub Actions OIDC JWT. When set
	// and OIDCClaimsProvider is nil, NewDetector wires the real go-oidc
	// verifier using OIDCIssuer + OIDCAudience.
	OIDCTokenProvider OIDCTokenProvider

	// OIDCClaimsProvider supplies verified GitHub Actions OIDC claims for
	// a workflow run. Nil means the scanner cannot observe a signed claim
	// and falls through to the org/repo map. Mutually safe with
	// OIDCDisabled: when disabled, this provider is not called.
	OIDCClaimsProvider OIDCClaimsProvider

	// ScanInterval is the loop period for Run. Defaults to 5 minutes
	// when zero. Scan() is single-shot and ignores this knob.
	ScanInterval time.Duration
}

// Observer is the metrics/audit sink a Detector hands its observability
// events to. Each method MUST be cheap and non-blocking — the
// production wiring fans them out to Prometheus counters and the audit
// chain. Tests can substitute a spy to assert exact call shapes.
type Observer interface {
	RecordFindingEmit(signal, risk string)
	EmitAudit(event audit.SIEMEvent)
	OIDCVerifyOutcome(result string)
	RateLimitRemaining(remaining int)
}

// nopObserver is the safe default when NewDetector is given nil. It
// discards every call so callers don't have to nil-check at emit time.
type nopObserver struct{}

func (nopObserver) RecordFindingEmit(string, string) {}
func (nopObserver) EmitAudit(audit.SIEMEvent)        {}
func (nopObserver) OIDCVerifyOutcome(string)         {}
func (nopObserver) RateLimitRemaining(int)           {}

// Detector is the long-lived scanner for one operator-owned set of
// GitHub orgs. Construction is via NewDetector; the zero value is
// invalid because state-store + GH client are required.
type Detector struct {
	cfg      Config
	gh       *gogithub.Client
	store    shadow.Store
	resolver TenantResolver
	observer Observer

	mu    sync.Mutex
	clock func() time.Time
}

// NewDetector wires a Detector. ghClient is required (the detector
// makes no internal Authn decision — the caller passes a pre-
// authenticated client). store is required. resolver may be nil and
// defaults to NewDefaultResolver(cfg). observer may be nil and defaults
// to a no-op sink so the call site does not have to nil-check.
//
// Defaults applied here:
//   - CordumAttachActionRef ← "cordum/cordum-edge-attach"
//   - ScanInterval ← 5 * time.Minute (only used by Run)
//   - clock ← time.Now (override via SetClock for tests)
//
// Returns an error when ghClient or store is nil, or when
// QuarantineTenantID is empty (refuse-to-start rather than risk
// silently dropping unmapped findings).
func NewDetector(cfg Config, ghClient *gogithub.Client, store shadow.Store, observer Observer, resolver TenantResolver) (*Detector, error) {
	var err error
	cfg, err = LoadOIDCConfigFromEnv(cfg)
	if err != nil {
		return nil, err
	}
	if ghClient == nil {
		return nil, errors.New("github detector: ghClient is required")
	}
	if store == nil {
		return nil, errors.New("github detector: store is required")
	}
	if strings.TrimSpace(cfg.QuarantineTenantID) == "" {
		return nil, errors.New("github detector: QuarantineTenantID is required (refuse to emit unrouted findings)")
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
	if !cfg.OIDCDisabled && cfg.OIDCClaimsProvider == nil && cfg.OIDCTokenProvider != nil {
		cfg.OIDCClaimsProvider, err = NewOIDCClaimsProvider(OIDCVerifierConfig{
			Issuer:        cfg.OIDCIssuer,
			Audience:      cfg.OIDCAudience,
			TokenProvider: cfg.OIDCTokenProvider,
		})
		if err != nil {
			return nil, err
		}
	}
	if observer == nil {
		observer = nopObserver{}
	}
	if resolver == nil {
		resolver = NewDefaultResolver(cfg)
	}
	return &Detector{
		cfg:      cfg,
		gh:       ghClient,
		store:    store,
		resolver: resolver,
		observer: observer,
		clock:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetClock pins the detector's clock. Used by tests to make timestamps
// deterministic; production callers leave the default in place.
func (d *Detector) SetClock(f func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f == nil {
		f = func() time.Time { return time.Now().UTC() }
	}
	d.clock = f
}

// now returns the detector's notion of the current time.
func (d *Detector) now() time.Time {
	d.mu.Lock()
	f := d.clock
	d.mu.Unlock()
	return f()
}

// Run executes Scan in a loop until ctx is cancelled. It is the
// production entry point. Each cycle is bounded by ScanInterval; a
// scan-level error is logged via the audit observer but does not
// terminate the loop — transient GH outages are normal.
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

// Scan walks every configured org, lists workflow runs, builds the
// signal set per run, applies §14 false-positive controls, and emits
// findings through the shared store. Single-shot — Run wraps this for
// the long-lived cycle.
func (d *Detector) Scan(ctx context.Context) error {
	if len(d.cfg.Orgs) == 0 {
		return nil
	}
	var firstErr error
	for _, org := range d.cfg.Orgs {
		if err := d.scanOrg(ctx, org); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// scanOrg walks every repo we know about for the given org. The set
// of repos comes from cfg.OrgRepoMap because the detector deliberately
// avoids the wide /orgs/<org>/repos enumeration — that endpoint
// surfaces every public+private repo and inflates rate-limit cost. An
// operator can scan a repo they care about by listing it explicitly.
func (d *Detector) scanOrg(ctx context.Context, org string) error {
	repos := d.cfg.OrgRepoMap[org]
	if len(repos) == 0 {
		return nil
	}
	var firstErr error
	for repoName := range repos {
		if err := d.scanRepo(ctx, org, repoName); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// scanRepo lists workflow runs for one repo, fans out into per-run
// signal extraction, and emits findings. Pagination is bounded by
// maxGitHubPagesPerScan so a large organization cannot exhaust the
// GitHub API quota in one poll.
func (d *Detector) scanRepo(ctx context.Context, org, repoName string) error {
	runs, err := d.listWorkflowRuns(ctx, org, repoName)
	if err != nil {
		return err
	}
	slog.Info("shadow_detector scan_repo",
		"source_type", githubActionsSourceType,
		"ci_provider", shadow.CIProviderGitHubActions,
		"repo", org+"/"+repoName,
		"run_count", len(runs))
	for _, run := range runs {
		if run == nil {
			continue
		}
		if err := d.scanRun(ctx, org, repoName, run); err != nil && err != errSkipRun {
			d.observer.EmitAudit(audit.SIEMEvent{
				EventType: "edge.shadow_scan_error",
				Decision:  "error",
				Severity:  "warning",
				Reason:    fmt.Sprintf("%s/%s run=%d: %v", org, repoName, run.GetID(), err),
				Timestamp: d.now(),
			})
		}
	}
	return nil
}

func (d *Detector) listWorkflowRuns(ctx context.Context, org, repoName string) ([]*gogithub.WorkflowRun, error) {
	opts := &gogithub.ListWorkflowRunsOptions{ListOptions: gogithub.ListOptions{PerPage: 30}}
	out := make([]*gogithub.WorkflowRun, 0, 30)
	for page := 0; page < maxGitHubPagesPerScan; page++ {
		runs, resp, err := d.gh.Actions.ListRepositoryWorkflowRuns(ctx, org, repoName, opts)
		d.recordRateLimit(resp)
		if err != nil {
			return nil, fmt.Errorf("ListRepositoryWorkflowRuns(%s/%s): %w", org, repoName, err)
		}
		if runs != nil {
			out = append(out, runs.WorkflowRuns...)
		}
		if resp == nil || resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (d *Detector) recordRateLimit(resp *gogithub.Response) {
	if resp != nil && resp.Rate.Remaining >= 0 {
		d.observer.RateLimitRemaining(resp.Rate.Remaining)
	}
}

// errSkipRun signals scanRun chose to skip emitting a finding (e.g. no
// signals raised or a §14 hard-suppress fired). It is treated as a
// non-error sentinel by scanRepo.
var errSkipRun = errors.New("github detector: run skipped")

// LoadOIDCConfigFromEnv overlays operator OIDC env-var overrides onto
// the given Config and returns the resulting Config. Per governor
// ruling Q6:
//
//   - Empty / unset env var ⇒ default issuer (DefaultGitHubOIDCIssuer)
//     and default audience ("cordum-edge").
//   - Literal "disabled" ⇒ OIDCDisabled=true, fall through to §6.3
//     tier-2 org/repo map.
//   - Any other value ⇒ operator override; the issuer string is
//     accepted verbatim. (Discovery-endpoint validation is the
//     responsibility of the production JWT verifier wired downstream;
//     see comment in OIDCClaims doc.)
//
// Returns an error only on a malformed env value the caller cannot
// recover from (currently: none — strings always parse).
func LoadOIDCConfigFromEnv(cfg Config) (Config, error) {
	trust := strings.TrimSpace(os.Getenv(envOIDCTrust))
	audience := strings.TrimSpace(os.Getenv(envOIDCAudience))
	switch strings.ToLower(trust) {
	case "":
		if cfg.OIDCIssuer == "" {
			cfg.OIDCIssuer = DefaultGitHubOIDCIssuer
		}
	case "disabled":
		cfg.OIDCDisabled = true
		if cfg.OIDCIssuer == "" {
			cfg.OIDCIssuer = DefaultGitHubOIDCIssuer
		}
	default:
		cfg.OIDCIssuer = trust
	}
	if audience == "" {
		if cfg.OIDCAudience == "" {
			cfg.OIDCAudience = "cordum-edge"
		}
	} else {
		cfg.OIDCAudience = audience
	}
	return cfg, nil
}
