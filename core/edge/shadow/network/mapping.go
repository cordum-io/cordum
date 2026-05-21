package network

import (
	"context"
	"time"

	"github.com/cordum/cordum/core/edge/shadow"
)

// TenantResolver maps a LogRecord onto its owning tenant and the raw
// principal value (pre-PII). Production wiring backs this with the
// operator-supplied workload / OIDC maps in Config; tests substitute
// a recording stub to verify the §6.1 precedence chain.
type TenantResolver interface {
	// ResolveTenant returns the tenant_id and the source enum value
	// recording which precedence tier was hit (workload_identity /
	// oidc / quarantine). Empty tenant_id means the caller should
	// fall back to QuarantineTenantID — the contract leaves the
	// fallback to ProcessRecord so resolver implementations stay
	// stateless.
	ResolveTenant(ctx context.Context, rec LogRecord) (tenantID string, source string)

	// ResolvePrincipal returns the raw (pre-PII) principal value and
	// the source enum. applyPIIMode runs on the raw value once
	// ProcessRecord receives it; resolvers MUST NOT pre-hash so the
	// configured PII mode has full authority over the persisted
	// principal_id.
	ResolvePrincipal(ctx context.Context, rec LogRecord) (rawPrincipal string, source string)
}

// Tenant / principal source enum values. Persisted into
// finding.TenantSource / finding.PrincipalSource (§10.1) so SIEM
// consumers can disambiguate which precedence tier resolved the
// owner of the finding.
const (
	TenantSourceWorkloadIdentity    = "workload_identity"
	TenantSourceOIDC                = "oidc"
	TenantSourceQuarantine          = "quarantine"
	PrincipalSourceWorkloadIdentity = "workload_identity"
	PrincipalSourceOIDC             = "oidc"
	PrincipalSourceQuarantine       = "quarantine"
)

// defaultResolver implements §6.1 precedence using the operator-
// supplied maps in Config. workload-identity wins; oidc is consulted
// only when the workload column is empty or absent from the workload
// map; both empty / both unmapped fall to quarantine.
type defaultResolver struct {
	workloadMap        map[string]string
	oidcMap            map[string]string
	quarantineTenantID string
}

func newDefaultResolver(cfg Config) *defaultResolver {
	return &defaultResolver{
		workloadMap:        cfg.WorkloadTenantMap,
		oidcMap:            cfg.OIDCTenantMap,
		quarantineTenantID: cfg.QuarantineTenantID,
	}
}

// ResolveTenant satisfies TenantResolver.
func (r *defaultResolver) ResolveTenant(_ context.Context, rec LogRecord) (string, string) {
	if rec.WorkloadID != "" {
		if t, ok := r.workloadMap[rec.WorkloadID]; ok && t != "" {
			return t, TenantSourceWorkloadIdentity
		}
	}
	if rec.OIDCSub != "" {
		if t, ok := r.oidcMap[rec.OIDCSub]; ok && t != "" {
			return t, TenantSourceOIDC
		}
	}
	return r.quarantineTenantID, TenantSourceQuarantine
}

// ResolvePrincipal satisfies TenantResolver. The raw value flows
// through applyPIIMode in ProcessRecord; this method MUST NOT
// pre-hash.
func (r *defaultResolver) ResolvePrincipal(_ context.Context, rec LogRecord) (string, string) {
	if rec.WorkloadID != "" {
		return rec.WorkloadID, PrincipalSourceWorkloadIdentity
	}
	if rec.OIDCSub != "" {
		return rec.OIDCSub, PrincipalSourceOIDC
	}
	return "", PrincipalSourceQuarantine
}

// classifyRisk applies the §9.3 ladder: quarantine tenant dominates
// at high; absent attach record yields medium; stale attach yields
// high; everything else falls through to low (workload is known and
// fresh, traffic is still observably direct-to-provider — a real
// finding but the lowest priority bucket).
func classifyRisk(tenantSource string, rec LogRecord, attachMap map[string]time.Time, now time.Time, staleThreshold time.Duration) shadow.FindingRisk {
	if tenantSource == TenantSourceQuarantine {
		return shadow.FindingRiskHigh
	}
	if rec.WorkloadID == "" {
		return shadow.FindingRiskMedium
	}
	lastSeen, ok := attachMap[rec.WorkloadID]
	if !ok {
		return shadow.FindingRiskMedium
	}
	if staleThreshold > 0 && now.Sub(lastSeen) > staleThreshold {
		return shadow.FindingRiskHigh
	}
	return shadow.FindingRiskLow
}
