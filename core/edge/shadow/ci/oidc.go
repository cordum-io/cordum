package ci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCVerifierConfig configures the generic go-oidc-backed verifier the
// detector wires for any of the four CI providers when an operator
// supplies a TokenProvider. The verifier mirrors the GitHub detector's
// pattern but is generic over provider Run inputs.
type OIDCVerifierConfig struct {
	Issuer        string
	Audience      string
	TokenProvider OIDCTokenProvider
	Provider      Provider
}

type oidcVerifier struct {
	issuer        string
	audience      string
	provider      Provider
	tokenProvider OIDCTokenProvider

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewOIDCClaimsProvider returns an OIDCClaimsProvider that validates JWT
// signature, issuer, expiry, and audience before projecting CI claims.
// Caller must supply non-empty issuer + audience + TokenProvider.
func NewOIDCClaimsProvider(cfg OIDCVerifierConfig) (OIDCClaimsProvider, error) {
	v := &oidcVerifier{
		issuer:        strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/"),
		audience:      strings.TrimSpace(cfg.Audience),
		provider:      cfg.Provider,
		tokenProvider: cfg.TokenProvider,
	}
	if v.issuer == "" {
		return nil, errors.New("ci detector: OIDC issuer is required")
	}
	if v.audience == "" {
		return nil, errors.New("ci detector: OIDC audience is required")
	}
	if v.tokenProvider == nil {
		return nil, errors.New("ci detector: OIDC token provider is required")
	}
	return v.Claims, nil
}

// Claims fetches a raw OIDC JWT for the run, verifies signature +
// issuer + audience + expiry through coreos/go-oidc, and projects the
// verified claims into an OIDCClaims. Returns an error fail-closed —
// callers must NOT consume claims when err != nil.
func (v *oidcVerifier) Claims(ctx context.Context, run Run) (*OIDCClaims, error) {
	raw, err := v.tokenProvider(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("oidc token provider: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("oidc token provider returned empty token")
	}
	verifier, err := v.idTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	var extra struct {
		ProjectPath string `json:"project_path"`
		Repository  string `json:"repository"`
		Ref         string `json:"ref"`
		Branch      string `json:"branch"`
		UserLogin   string `json:"user_login"`
		Actor       string `json:"actor"`
		Subject     string `json:"sub"`
	}
	if err := tok.Claims(&extra); err != nil {
		return nil, fmt.Errorf("oidc claims: %w", err)
	}
	repo := strings.TrimSpace(extra.Repository)
	if repo == "" {
		repo = strings.TrimSpace(extra.ProjectPath)
	}
	if repo == "" {
		repo = repoFromSubject(tok.Subject)
	}
	ref := strings.TrimSpace(extra.Ref)
	if ref == "" {
		ref = strings.TrimSpace(extra.Branch)
	}
	actor := strings.TrimSpace(extra.Actor)
	if actor == "" {
		actor = strings.TrimSpace(extra.UserLogin)
	}
	return &OIDCClaims{
		Subject:   strings.TrimSpace(tok.Subject),
		Repo:      repo,
		Ref:       ref,
		Actor:     actor,
		Audience:  strings.Join(tok.Audience, ","),
		Audiences: append([]string(nil), tok.Audience...),
		Issuer:    strings.TrimRight(strings.TrimSpace(tok.Issuer), "/"),
	}, nil
}

func (v *oidcVerifier) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	provider, err := oidc.NewProvider(ctx, v.issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %q: %w", v.issuer, err)
	}
	v.verifier = provider.VerifierContext(ctx, &oidc.Config{ClientID: v.audience})
	return v.verifier, nil
}

// repoFromSubject best-effort extracts `<owner>/<repo>` from a JWT
// subject claim across CI provider shapes:
//   - GitLab: `project_path:<group>/<project>:ref_type:branch:ref:<ref>`
//   - Buildkite: `organization:<slug>:pipeline:<slug>:ref:<ref>:commit:<sha>:step:<id>`
//   - CircleCI: `org/<orgid>/project/<projid>/user/<userid>`
//   - Jenkins: caller-defined (no canonical shape)
//
// Returns empty when no recognizable owner/repo pair can be parsed —
// the caller MUST treat that as "no claim-derived repo" and fall back
// to org/repo map or quarantine.
func repoFromSubject(sub string) string {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return ""
	}
	// GitLab subject begins with `project_path:`.
	if rest, ok := trimPrefix(sub, "project_path:"); ok {
		if i := strings.IndexByte(rest, ':'); i > 0 {
			rest = rest[:i]
		}
		return rest
	}
	// Buildkite: `organization:<slug>:pipeline:<slug>:...`
	if rest, ok := trimPrefix(sub, "organization:"); ok {
		org := rest
		if i := strings.IndexByte(rest, ':'); i > 0 {
			org = rest[:i]
			rest = rest[i+1:]
			if pipelineRest, ok := trimPrefix(rest, "pipeline:"); ok {
				pipe := pipelineRest
				if j := strings.IndexByte(pipelineRest, ':'); j > 0 {
					pipe = pipelineRest[:j]
				}
				return org + "/" + pipe
			}
			return org
		}
		return org
	}
	return ""
}

func trimPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// LoadOIDCConfigFromEnv overlays operator OIDC env-var overrides onto
// the given OIDCConfig per Q6:
//
//   - GitLab.com SaaS (cfg.GitLabBaseURL omitted or hostname is
//     `gitlab.com`): default issuer `https://gitlab.com`, default
//     audience `cordum-edge`.
//   - Self-hosted GitLab (cfg.GitLabBaseURL points elsewhere): no
//     default; absent operator override sets Disabled=true so the
//     resolver falls through to §6.3 tier-2.
//   - Jenkins / Buildkite / CircleCI: operator-only; absent override
//     sets Disabled=true (Q6).
//   - Literal `disabled` env value sets Disabled=true for any
//     provider.
//   - Any other env value is treated as the operator override issuer
//     verbatim. Discovery + signature validation are deferred to the
//     downstream JWT verifier.
//
// Returns the normalized config. The caller decides whether to
// auto-wire NewOIDCClaimsProvider (Detector does it when TokenProvider
// is supplied).
func LoadOIDCConfigFromEnv(p Provider, cfg OIDCConfig) (OIDCConfig, error) {
	trustEnv, audEnv := envKeysFor(p)
	if trustEnv == "" {
		return cfg, fmt.Errorf("unknown provider %q", p)
	}
	trust := strings.TrimSpace(os.Getenv(trustEnv))
	audience := strings.TrimSpace(os.Getenv(audEnv))

	switch strings.ToLower(trust) {
	case "":
		applyDefaultIssuer(&cfg, p)
	case "disabled":
		cfg.Disabled = true
	default:
		cfg.Issuer = trust
		cfg.Disabled = false
	}
	if audience != "" {
		cfg.Audience = audience
	} else if cfg.Audience == "" {
		cfg.Audience = DefaultOIDCAudience
	}
	return cfg, nil
}

func envKeysFor(p Provider) (string, string) {
	switch p {
	case ProviderGitLab:
		return EnvOIDCTrustGitLab, EnvOIDCAudienceGitLab
	case ProviderJenkins:
		return EnvOIDCTrustJenkins, EnvOIDCAudienceJenkins
	case ProviderBuildkite:
		return EnvOIDCTrustBuildkite, EnvOIDCAudienceBuildkite
	case ProviderCircleCI:
		return EnvOIDCTrustCircleCI, EnvOIDCAudienceCircleCI
	}
	return "", ""
}

// applyDefaultIssuer applies Q6 defaults when the operator env var is
// unset. GitLab.com SaaS gets the default issuer; every other case
// falls back to Disabled so the resolver uses §6.3 tier-2.
func applyDefaultIssuer(cfg *OIDCConfig, p Provider) {
	if p != ProviderGitLab {
		// Jenkins/Buildkite/CircleCI have no Cordum default per Q6.
		cfg.Disabled = true
		return
	}
	if !isGitLabSaaS(cfg.GitLabBaseURL) {
		// Self-hosted GitLab without operator override falls back to
		// tier-2 — refusing to apply the SaaS default to a private
		// instance is the only safe posture under the Q6 ruling.
		cfg.Disabled = true
		return
	}
	if cfg.Issuer == "" {
		cfg.Issuer = DefaultGitLabSaaSIssuer
	}
}

// isGitLabSaaS returns true when the GitLabBaseURL is empty or points
// at gitlab.com. Anything else (including subdomains like
// `acme.gitlab.com`) is treated as self-hosted. Strips scheme, path,
// userinfo, and trailing port before comparing — `https://gitlab.com:443`
// is correctly recognized as SaaS.
func isGitLabSaaS(baseURL string) bool {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return true
	}
	if i := strings.Index(raw, "://"); i > 0 {
		raw = raw[i+3:]
	}
	// Strip userinfo (`user:pass@host`) so a misconfigured creds-in-URL
	// doesn't cause a false-negative.
	if i := strings.IndexByte(raw, '@'); i >= 0 {
		raw = raw[i+1:]
	}
	if i := strings.IndexByte(raw, '/'); i > 0 {
		raw = raw[:i]
	}
	// Strip port suffix (e.g. `gitlab.com:443`).
	if i := strings.IndexByte(raw, ':'); i > 0 {
		raw = raw[:i]
	}
	host := strings.ToLower(strings.TrimSpace(raw))
	return host == "gitlab.com" || host == "www.gitlab.com"
}
