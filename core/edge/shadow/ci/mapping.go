package ci

import (
	"context"
	"strings"
)

// DefaultResolver is the §6.3 / §6.4 reference implementation across the
// four CI providers. It treats every provider symmetrically: a verified
// OIDC subject is tried first; otherwise the (org, repo) pair from the
// run is looked up in the operator-maintained map; otherwise the
// finding is routed to the quarantine tenant with an "unknown"
// principal. Empty inputs are tolerated — production scanners always
// hand the resolver a Run with at least Provider set.
type DefaultResolver struct {
	orgRepoMap         map[string]map[string]string
	quarantineTenantID string
}

// NewDefaultResolver constructs a DefaultResolver. orgRepoMap may be
// nil; quarantineTenantID must be non-empty for the resolver to be
// useful (the Detector enforces this at NewDetector time).
func NewDefaultResolver(orgRepoMap map[string]map[string]string, quarantineTenantID string) *DefaultResolver {
	return &DefaultResolver{
		orgRepoMap:         orgRepoMap,
		quarantineTenantID: quarantineTenantID,
	}
}

// ResolveTenant implements §6.3 precedence:
//
//  1. OIDC claim repo (or subject-derived org/repo) → map lookup
//     (source=oidc).
//  2. Run's Repo field → map lookup (source=org_repo_map).
//  3. Quarantine tenant (source=quarantine).
//
// Either of claims or run.Repo may be empty/nil; the resolver short-
// circuits at the first tier that resolves to a non-empty mapped
// tenant. Empty `<owner>/<repo>` pairs do NOT count as a hit.
func (r *DefaultResolver) ResolveTenant(_ context.Context, claims *OIDCClaims, run Run) (string, string) {
	if claims != nil {
		if owner, name := parseOwnerRepo(claims.Repo); owner != "" && name != "" {
			if tenant := r.lookup(owner, name); tenant != "" {
				return tenant, TenantSourceOIDC
			}
		}
		if owner, name := parseOwnerRepo(repoFromSubject(claims.Subject)); owner != "" && name != "" {
			if tenant := r.lookup(owner, name); tenant != "" {
				return tenant, TenantSourceOIDC
			}
		}
	}
	if owner, name := parseOwnerRepo(run.Repo); owner != "" && name != "" {
		if tenant := r.lookup(owner, name); tenant != "" {
			return tenant, TenantSourceOrgRepoMap
		}
	}
	return r.quarantineTenantID, TenantSourceQuarantine
}

// ResolvePrincipal implements §6.4 precedence:
//
//  1. OIDC subject claim (source=oidc_subject).
//  2. Run.Actor (workflow_actor source).
//  3. PrincipalUnknown / source=quarantine.
//
// The quarantine fallback returns the literal `"unknown"` so the
// shadow.Store's non-empty-principal validation always succeeds and
// downstream dashboards never see a bare empty string.
func (r *DefaultResolver) ResolvePrincipal(_ context.Context, claims *OIDCClaims, run Run) (string, string) {
	if claims != nil {
		if sub := strings.TrimSpace(claims.Subject); sub != "" {
			return sub, PrincipalSourceOIDCSubject
		}
	}
	if actor := strings.TrimSpace(run.Actor); actor != "" {
		return actor, PrincipalSourceWorkflowActor
	}
	return PrincipalUnknown, PrincipalSourceQuarantine
}

// lookup is a defensive map-of-maps read that tolerates nil submaps so
// a partially-populated config (e.g. only one tenant defined) cannot
// panic the resolver.
func (r *DefaultResolver) lookup(owner, name string) string {
	repos := r.orgRepoMap[owner]
	if repos == nil {
		return ""
	}
	return repos[name]
}

// parseOwnerRepo splits `<owner>/<repo>` into its halves, tolerating
// extra slashes (uses the FIRST slash so subpath segments stay with
// the repo name). Empty owner OR name means the input is not a
// resolvable repo.
func parseOwnerRepo(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if i := strings.IndexByte(s, '/'); i > 0 && i < len(s)-1 {
		return s[:i], s[i+1:]
	}
	return "", ""
}
