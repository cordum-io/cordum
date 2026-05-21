// Package ci implements observe-only shadow-agent detectors for the four
// CI providers covered by EDGE-143.3: GitLab CI, Jenkins, Buildkite, and
// CircleCI. The package mirrors the EDGE-143.2 GitHub Actions detector
// contract — same Observer / TenantResolver / OIDC interfaces, same
// shadow.Store emit pipeline — so SIEM and dashboard consumers see one
// uniform CI finding shape regardless of source provider.
//
// Q6 binding governor ruling (comment-a17f4f1c on task-de50a293) drives
// the OIDC trust policy:
//
//   - GitLab.com SaaS: ships a Cordum default trust root
//     `https://gitlab.com` with audience `cordum-edge`. Self-hosted
//     GitLab requires operator override via
//     `CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab=<issuer-url>`.
//   - Jenkins / Buildkite / CircleCI: operator-only OIDC config; no
//     defaults shipped. Absent operator config the detector falls back
//     to the §6.3 tier-2 org/repo map (no quarantine bypass).
//
// All provider clients are READ-ONLY. The package uses minimal HTTP
// clients rather than vendoring per-provider SDKs (xanzy/go-gitlab was
// archived March 2025; the read surface area is small enough across all
// four providers that a uniform httptest-injectable client is the
// production-shaped boundary). Every external request goes through GET /
// HEAD only; the test suite asserts no other methods are issued.
package ci

import (
	"context"
	"net/http"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// Provider identifies one CI source the detector polls. The constants
// alias the corresponding `shadow.CIProvider*` values so the package
// surfaces the same enum the store validates on write.
type Provider string

const (
	ProviderGitLab    Provider = shadow.CIProviderGitLabCI
	ProviderJenkins   Provider = shadow.CIProviderJenkins
	ProviderBuildkite Provider = shadow.CIProviderBuildkite
	ProviderCircleCI  Provider = shadow.CIProviderCircleCI
)

// OIDC env-var keys per Q6. The `_<provider>` suffix lets operators
// tune each provider independently; absent / empty values follow the
// Q6 defaults (GitLab.com SaaS only) and operator-only fall-through.
const (
	EnvOIDCTrustGitLab       = "CORDUM_EDGE_SHADOW_OIDC_TRUST_gitlab"
	EnvOIDCAudienceGitLab    = "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_gitlab"
	EnvOIDCTrustJenkins      = "CORDUM_EDGE_SHADOW_OIDC_TRUST_jenkins"
	EnvOIDCAudienceJenkins   = "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_jenkins"
	EnvOIDCTrustBuildkite    = "CORDUM_EDGE_SHADOW_OIDC_TRUST_buildkite"
	EnvOIDCAudienceBuildkite = "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_buildkite"
	EnvOIDCTrustCircleCI     = "CORDUM_EDGE_SHADOW_OIDC_TRUST_circleci"
	EnvOIDCAudienceCircleCI  = "CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_circleci"
)

// DefaultGitLabSaaSIssuer is the Cordum-shipped OIDC issuer for
// GitLab.com SaaS per Q6. Self-hosted GitLab is detected by the
// `GitLabBaseURL` field on OIDCConfig (any non-`gitlab.com` host).
const DefaultGitLabSaaSIssuer = "https://gitlab.com"

// DefaultOIDCAudience is the audience Cordum publishes for CI workflows
// to request when minting OIDC tokens.
const DefaultOIDCAudience = "cordum-edge"

// Tenant- and principal-source labels surfaced on emitted findings per
// design §6.3 / §6.4. Match the GitHub detector vocabulary so SIEM
// dashboards filter uniformly across CI providers.
const (
	TenantSourceOIDC       = "oidc"
	TenantSourceOrgRepoMap = "org_repo_map"
	TenantSourceQuarantine = "quarantine"

	PrincipalSourceOIDCSubject   = "oidc_subject"
	PrincipalSourceWorkflowActor = "workflow_actor"
	PrincipalSourceQuarantine    = "quarantine"

	PrincipalUnknown = "unknown"
)

// Bounded scan pagination. The package never lists more than
// MaxRunsPerScan runs nor MaxBuildsPerScan builds per provider per
// scan tick — a noisy CI account cannot exhaust the request budget.
const (
	MaxRunsPerScan       = 30
	MaxBuildsPerScan     = 30
	MaxPagesPerScan      = 5
	MaxResponseBodyBytes = 2 * 1024 * 1024 // 2 MiB ceiling per provider response
	DefaultHTTPTimeout   = 15 * time.Second
)

// Run is the provider-agnostic snapshot of one CI workflow / build /
// pipeline. Each provider scanner projects its native shape into this
// struct so the shared emit pipeline can produce one finding per run
// without re-deriving common fields. Field semantics:
//
//   - Workspace: the provider's outer container (GitLab group, Jenkins
//     folder, Buildkite organization, CircleCI org).
//   - Repo: `<org>/<repo>` as resolved from the run metadata. Empty
//     when the provider does not expose a repo binding (rare).
//   - Ref: head branch / tag (never includes user-supplied free text).
//   - EnvNames: workflow-level env var **NAMES only** — values are
//     never read into this slice.
//   - UsesActions / RunCommands: workflow steps' `uses:` references
//     and `run:` leading tokens, used for agent-action detection.
//   - ProviderEndpointHits: hostnames observed in the operator's CI
//     log index that match the configured direct-provider allowlist.
//   - WorkflowYAML: raw fetched YAML / Groovy contents (config-file
//     redaction happens at emit time; do NOT leak this into evidence
//     verbatim).
type Run struct {
	Provider             Provider
	Workspace            string
	Repo                 string
	Ref                  string
	HeadSHA              string
	RunID                string
	JobID                string
	WorkflowID           string
	RunnerID             string
	Event                string
	Actor                string
	Labels               []string
	EnvNames             []string
	UsesActions          []string
	RunCommands          []string
	ProviderEndpointHits []string
	WorkflowPath         string
	WorkflowYAML         string
	AgentConfigPaths     []string
	AgentConfigSummaries []string
	EdgeHeartbeat        bool
	IsForkPR             bool
	IsScheduled          bool
}

// OIDCClaims is the verified-JWT-claim subset the detector consumes for
// tier-1 tenant/principal mapping. Provider-specific subject shapes
// (`project_path:<group>/<project>:ref:<ref>` for GitLab, `org:<org>`
// for Buildkite, etc.) are parsed by the per-provider scanner before
// the resolver runs.
type OIDCClaims struct {
	Subject   string
	Repo      string
	Ref       string
	Actor     string
	Issuer    string
	Audience  string
	Audiences []string
}

// OIDCClaimsProvider returns verified claims for a run, or an error
// when no JWT could be obtained / verified. The shared Detector caches
// the per-provider provider in cfg.OIDC[p].ClaimsProvider.
type OIDCClaimsProvider func(ctx context.Context, run Run) (*OIDCClaims, error)

// OIDCTokenProvider supplies a raw OIDC JWT for a given run. Used by
// NewOIDCClaimsProvider to wire a real go-oidc verifier.
type OIDCTokenProvider func(ctx context.Context, run Run) (string, error)

// OIDCConfig is the per-provider OIDC trust configuration consumed by
// the detector. Zero value is safe — the detector treats it as
// `Disabled=true` and falls through to §6.3 tier-2 mapping.
type OIDCConfig struct {
	// Issuer is the expected JWT `iss` claim. For GitLab SaaS the
	// default is DefaultGitLabSaaSIssuer.
	Issuer string
	// Audience is the expected JWT `aud` claim. Default is
	// DefaultOIDCAudience when unset.
	Audience string
	// Disabled means OIDC is off for this provider — claims provider
	// is never invoked, tier-2 fallback is always used.
	Disabled bool
	// ClaimsProvider supplies verified claims for the per-provider
	// scanner. Nil + non-empty TokenProvider triggers automatic
	// wiring through NewOIDCClaimsProvider.
	ClaimsProvider OIDCClaimsProvider
	// TokenProvider supplies raw JWTs to NewOIDCClaimsProvider when
	// ClaimsProvider is nil.
	TokenProvider OIDCTokenProvider
	// GitLabBaseURL is the operator-configured GitLab instance host
	// (e.g. `https://gitlab.acme.internal`). Only consulted for
	// Provider==ProviderGitLab — drives self-hosted detection.
	GitLabBaseURL string
}

// TenantResolver maps a run to a Cordum tenant + principal per §6.3 /
// §6.4 precedence. nil claims means "no verified OIDC token" — the
// resolver must fall back to org/repo map and finally quarantine.
type TenantResolver interface {
	ResolveTenant(ctx context.Context, claims *OIDCClaims, run Run) (tenantID, source string)
	ResolvePrincipal(ctx context.Context, claims *OIDCClaims, run Run) (principalID, source string)
}

// ProviderScanner is implemented by each per-provider scanner. Scan is
// invoked once per Detector.Scan tick; the scanner walks its read-only
// API surface, produces zero-or-more Run snapshots, and emits findings
// via d.EmitRun. ScanInterval / pagination is enforced by the shared
// Detector — the scanner only needs to project provider shapes.
type ProviderScanner interface {
	Provider() Provider
	Scan(ctx context.Context, d *Detector) error
}

// Observer is the metrics/audit sink the detector hands observability
// events to. Production wiring fans out to Prometheus + Cordum audit
// chain; tests substitute a spy.
type Observer interface {
	RecordFindingEmit(provider Provider, signal, risk string)
	EmitAudit(event audit.SIEMEvent)
	OIDCVerifyOutcome(provider Provider, result string)
}

// nopObserver is the no-op sink wired when Config.Observer is nil so
// scanner emit paths don't need nil-checks.
type nopObserver struct{}

func (nopObserver) RecordFindingEmit(Provider, string, string) {}
func (nopObserver) EmitAudit(audit.SIEMEvent)                  {}
func (nopObserver) OIDCVerifyOutcome(Provider, string)         {}

// Config aggregates every input the shared Detector needs.
type Config struct {
	// Scanners is the set of per-provider scanners the Detector
	// orchestrates. Order is preserved; one failing scanner does not
	// abort the rest.
	Scanners []ProviderScanner

	// OrgRepoMap is the §6.3 tier-2 source of tenant attribution. Map
	// shape: `org -> repo -> tenant_id`. Used when OIDC is disabled
	// or returns no claims.
	OrgRepoMap map[string]map[string]string

	// QuarantineTenantID catches every unmapped finding so emit never
	// drops a record. Empty == NewDetector errors out.
	QuarantineTenantID string

	// AgentConfigPaths is the closed set of repo paths each scanner
	// probes for known agent configuration files.
	AgentConfigPaths []string

	// KnownAgentActionRefs is the closed set of action / image refs
	// that mark a workflow as running an agent.
	KnownAgentActionRefs []string

	// KnownAgentRunTokens is the closed set of leading shell tokens
	// that indicate run-command agent use.
	KnownAgentRunTokens []string

	// CordumAttachActionRef is the action ref that, when present in a
	// workflow, marks the workflow as governed by Cordum Edge.
	CordumAttachActionRef string

	// ProviderEndpointHosts is the closed set of host names that
	// count as direct-provider endpoints.
	ProviderEndpointHosts []string

	// BotActorAllowlist names automation actors whose runs are
	// tagged with `false_positive_reason=automation_bot`.
	BotActorAllowlist []string

	// TestFixtureRepos / DevSandboxRepos are §14 operator-curated
	// repo sets that surface findings with FP reasons but DO NOT
	// suppress them.
	TestFixtureRepos map[string]bool
	DevSandboxRepos  map[string]bool

	// Resolver overrides the default §6.3 / §6.4 resolver. Nil
	// causes NewDetector to construct a DefaultResolver.
	Resolver TenantResolver

	// Observer is the metrics/audit sink. Nil defaults to a no-op.
	Observer Observer

	// OIDC is the per-provider OIDC trust configuration.
	OIDC map[Provider]OIDCConfig

	// EdgeSessionHeartbeat reports whether a run has a live Cordum
	// EdgeSession heartbeat. Nil == no heartbeat observed.
	EdgeSessionHeartbeat func(ctx context.Context, run Run) bool

	// ScanInterval is the loop period for Run. Defaults to 5 min.
	ScanInterval time.Duration
}

// boundedHTTPClient returns a context-bound *http.Client with bounded
// timeout. Provider scanners use this when callers don't inject a
// pre-built client — keeps test injection trivial while bounding
// production blast radius.
func boundedHTTPClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: DefaultHTTPTimeout}
}
