package scheduler

import pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"

const DefaultTenant = "default"
const EffectiveConfigEnvVar = "CORETEX_EFFECTIVE_CONFIG"

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

// ExtractPrincipal extracts principal ID if present.
func ExtractPrincipal(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	return req.GetPrincipalId()
}
