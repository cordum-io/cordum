package model

import (
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const DefaultTenant = "default"

// ExtractTenant returns tenant ID with fallbacks to env.
func ExtractTenant(req *pb.JobRequest) string {
	if req == nil {
		return DefaultTenant
	}
	if tenant := req.GetTenantId(); tenant != "" {
		return tenant
	}
	if env := req.GetEnv(); env != nil {
		if tenant := env["tenant_id"]; tenant != "" {
			return tenant
		}
	}
	return DefaultTenant
}

// ResolveTenantForAudit returns a non-empty tenant ID for an audit
// SIEMEvent emission. Producer sites use this so the audit chain never
// receives a tenantless event (the sink-level fallback in
// auditChainSender.Send would warn and rewrite, but the explicit
// producer-side attribution surfaces the source of the missing tenant
// in slog context rather than a generic chain-sender log).
//
// Priority: authCtxTenant (resolved auth) → headerTenant (X-Tenant-ID
// on the request) → DefaultTenant. Trims surrounding whitespace at
// every layer so a header like `X-Tenant-ID: ` does not flow through
// as a "non-empty" tenant ID.
//
// String-based signature (rather than *auth.AuthContext + http.Header)
// keeps `core/model` free of higher-layer imports; callers with
// request access pass `r.Header.Get("X-Tenant-ID")`, callers without
// pass "".
func ResolveTenantForAudit(authCtxTenant, headerTenant string) string {
	if v := strings.TrimSpace(authCtxTenant); v != "" {
		return v
	}
	if v := strings.TrimSpace(headerTenant); v != "" {
		return v
	}
	return DefaultTenant
}

// ExtractPrincipal extracts principal ID if present.
func ExtractPrincipal(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	return req.GetPrincipalId()
}
