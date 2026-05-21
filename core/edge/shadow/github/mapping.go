package github

import (
	"context"
	"strings"

	gogithub "github.com/google/go-github/v74/github"
)

// Tenant-source labels surfaced on emitted findings per §6.3 so SIEM
// consumers can audit which tier supplied attribution. Match the
// Cordum-internal vocabulary used by the K8s detector (EDGE-143.1).
const (
	TenantSourceOIDC       = "oidc"
	TenantSourceOrgRepoMap = "org_repo_map"
	TenantSourceQuarantine = "quarantine"
)

// Principal-source labels surfaced on emitted findings per §6.4.
const (
	PrincipalSourceOIDCSubject   = "oidc_subject"
	PrincipalSourceOIDCActor     = PrincipalSourceOIDCSubject // deprecated alias
	PrincipalSourceWorkflowActor = "workflow_actor"
	PrincipalSourceQuarantine    = "quarantine"
)

// OIDCClaims is the verified-JWT-claim subset the detector consumes for
// tier-1 tenant/principal mapping. It is deliberately decoupled from
// any JWT library: a production verifier (built atop coreos/go-oidc)
// would parse and verify an OIDC token, then hand the resolved claim
// fields into this struct. Tests construct it directly. Keeping the
// resolver agnostic to JWT shape lets us swap verifier libraries
// without touching mapping logic.
type OIDCClaims struct {
	// Subject is the JWT `sub` claim — for GitHub Actions, this is the
	// `repo:<org>/<repo>:ref:<ref>` triple. Empty means the verifier
	// could not produce a subject (treat as untrusted).
	Subject string
	// Repo is the `<org>/<repo>` value extracted from `sub` for
	// resolver convenience. Empty falls back to subject parsing.
	Repo string
	// Ref is the head ref (`main`, `refs/heads/feat-x`, …) parsed from
	// the JWT subject. Informational; not used for tenant mapping.
	Ref string
	// Actor is the `actor` claim — the human (or bot) that triggered
	// the workflow. It is informational; tier-1 principal mapping uses
	// Subject per §6.4.
	Actor string
	// Audience is the verified `aud` claim — the detector REQUIRES this
	// to match cfg.OIDCAudience at verify time (caller's
	// responsibility). Surfaced here for downstream audit logging.
	Audience string
	// Audiences is the verified `aud` claim set when the token carries
	// multiple audiences. Audience remains for legacy one-audience tests.
	Audiences []string
	// Issuer is the verified `iss` claim. It must match the Cordum
	// default GitHub Actions issuer or the operator override before the
	// claims can drive tier-1 tenant/principal mapping.
	Issuer string
}

// TenantResolver maps a GitHub run to a Cordum tenant + principal per
// §6.3 / §6.4 precedence. Either argument may be nil; the resolver
// MUST handle the all-nil case by returning quarantine attribution
// rather than panicking.
type TenantResolver interface {
	ResolveTenant(ctx context.Context, claims *OIDCClaims, run *gogithub.WorkflowRun, repo *gogithub.Repository) (tenantID, source string)
	ResolvePrincipal(ctx context.Context, claims *OIDCClaims, run *gogithub.WorkflowRun) (principalID, source string)
}

// DefaultResolver is the §6.3 / §6.4 reference implementation: tier-1
// OIDC, tier-2 org/repo map, tier-3 quarantine. Production wires it
// via NewDefaultResolver; tests may construct their own resolver to
// exercise non-default behavior.
type DefaultResolver struct {
	orgRepoMap         map[string]map[string]string
	quarantineTenantID string
}

// NewDefaultResolver builds a DefaultResolver from a Config. Reads
// OrgRepoMap + QuarantineTenantID; ignores everything else. Safe to
// reuse across goroutines.
func NewDefaultResolver(cfg Config) *DefaultResolver {
	return &DefaultResolver{
		orgRepoMap:         cfg.OrgRepoMap,
		quarantineTenantID: cfg.QuarantineTenantID,
	}
}

// ResolveTenant implements §6.3 precedence:
//
//  1. OIDC claim subject parsed into org/repo → org/repo map lookup
//     (source=oidc).
//  2. WorkflowRun + Repository org/repo → org/repo map lookup
//     (source=org_repo_map).
//  3. Quarantine tenant (source=quarantine).
//
// claims, run, repo are all optional. The resolver short-circuits at
// the first tier that resolves to a non-empty mapped tenant.
func (r *DefaultResolver) ResolveTenant(_ context.Context, claims *OIDCClaims, run *gogithub.WorkflowRun, repo *gogithub.Repository) (string, string) {
	if claims != nil {
		if owner, name := parseOIDCRepo(claims); owner != "" && name != "" {
			if tenant := r.lookup(owner, name); tenant != "" {
				return tenant, TenantSourceOIDC
			}
		}
	}
	if owner, name := repoOwnerName(run, repo); owner != "" && name != "" {
		if tenant := r.lookup(owner, name); tenant != "" {
			return tenant, TenantSourceOrgRepoMap
		}
	}
	return r.quarantineTenantID, TenantSourceQuarantine
}

// ResolvePrincipal implements §6.4 precedence:
//
//  1. OIDC subject claim (source=oidc_subject).
//  2. WorkflowRun actor login (source=workflow_actor).
//  3. Quarantine principal (source=quarantine).
//
// Returns "unknown" as the quarantine principal id so downstream
// dashboards and SIEM filters never see a literally-empty principal —
// the shadow store validation also rejects empty principal_id.
func (r *DefaultResolver) ResolvePrincipal(_ context.Context, claims *OIDCClaims, run *gogithub.WorkflowRun) (string, string) {
	if claims != nil {
		if subject := strings.TrimSpace(claims.Subject); subject != "" {
			return subject, PrincipalSourceOIDCSubject
		}
	}
	if run != nil {
		if a := run.GetActor(); a != nil {
			if login := strings.TrimSpace(a.GetLogin()); login != "" {
				return login, PrincipalSourceWorkflowActor
			}
		}
	}
	return "unknown", PrincipalSourceQuarantine
}

// lookup is a defensive map-of-maps read that tolerates nil submaps.
func (r *DefaultResolver) lookup(owner, name string) string {
	repos := r.orgRepoMap[owner]
	if repos == nil {
		return ""
	}
	return repos[name]
}

// parseOIDCRepo extracts the (owner, name) tuple from either the
// explicit OIDCClaims.Repo field or the GitHub Actions subject shape
// `repo:<owner>/<name>:ref:<ref>`. Empty owner/name signals "not
// derivable from claim".
func parseOIDCRepo(c *OIDCClaims) (string, string) {
	if c == nil {
		return "", ""
	}
	if c.Repo != "" {
		if i := strings.IndexByte(c.Repo, '/'); i > 0 && i < len(c.Repo)-1 {
			return c.Repo[:i], c.Repo[i+1:]
		}
	}
	if strings.HasPrefix(c.Subject, "repo:") {
		rest := strings.TrimPrefix(c.Subject, "repo:")
		if i := strings.IndexByte(rest, ':'); i > 0 {
			rest = rest[:i]
		}
		if i := strings.IndexByte(rest, '/'); i > 0 && i < len(rest)-1 {
			return rest[:i], rest[i+1:]
		}
	}
	return "", ""
}

// repoOwnerName prefers the explicit Repository pointer over the
// WorkflowRun's embedded repository so callers that pass a richer
// Repository (e.g. with HTML URL) get their data through. Empty
// owner/name means neither input identified the repo.
func repoOwnerName(run *gogithub.WorkflowRun, repo *gogithub.Repository) (string, string) {
	if repo != nil {
		if owner := strings.TrimSpace(loginOf(repo.GetOwner())); owner != "" {
			if name := strings.TrimSpace(repo.GetName()); name != "" {
				return owner, name
			}
		}
		if full := strings.TrimSpace(repo.GetFullName()); full != "" {
			if i := strings.IndexByte(full, '/'); i > 0 && i < len(full)-1 {
				return full[:i], full[i+1:]
			}
		}
	}
	if run != nil {
		if rr := run.GetRepository(); rr != nil {
			if owner := strings.TrimSpace(loginOf(rr.GetOwner())); owner != "" {
				if name := strings.TrimSpace(rr.GetName()); name != "" {
					return owner, name
				}
			}
			if full := strings.TrimSpace(rr.GetFullName()); full != "" {
				if i := strings.IndexByte(full, '/'); i > 0 && i < len(full)-1 {
					return full[:i], full[i+1:]
				}
			}
		}
	}
	return "", ""
}

// loginOf reads the Login from a *gogithub.User, tolerating nil.
func loginOf(u *gogithub.User) string {
	if u == nil {
		return ""
	}
	return u.GetLogin()
}
