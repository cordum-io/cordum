package licensing

import (
	"bytes"
	"encoding/json"
	"strings"
)

type legacyClaims struct {
	OrgID          string           `json:"org_id"`
	LicenseID      string           `json:"license_id"`
	Plan           string           `json:"plan"`
	Features       map[string]bool  `json:"features,omitempty"`
	Limits         map[string]int64 `json:"limits,omitempty"`
	DeploymentType string           `json:"deployment_type,omitempty"`
	IssuedAt       string           `json:"issued_at,omitempty"`
	NotBefore      string           `json:"not_before,omitempty"`
	ExpiresAt      string           `json:"expires_at,omitempty"`
	GraceDays      *int             `json:"grace_days,omitempty"`
	InstallID      string           `json:"install_id,omitempty"`
	ClusterID      string           `json:"cluster_id,omitempty"`
}

func normalizeJSON(raw []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func isLegacyClaims(raw []byte) bool {
	var legacy struct {
		Features map[string]bool  `json:"features"`
		Limits   map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return false
	}
	return len(legacy.Features) > 0 || len(legacy.Limits) > 0
}

func migrateLegacyClaims(in legacyClaims) Claims {
	out := Claims{
		OrgID:          strings.TrimSpace(in.OrgID),
		LicenseID:      strings.TrimSpace(in.LicenseID),
		Plan:           strings.TrimSpace(in.Plan),
		DeploymentType: strings.TrimSpace(in.DeploymentType),
		IssuedAt:       strings.TrimSpace(in.IssuedAt),
		NotBefore:      strings.TrimSpace(in.NotBefore),
		ExpiresAt:      strings.TrimSpace(in.ExpiresAt),
		GraceDays:      cloneInt(in.GraceDays),
		InstallID:      strings.TrimSpace(in.InstallID),
		ClusterID:      strings.TrimSpace(in.ClusterID),
	}

	var rights *Rights
	var entitlements *Entitlements

	for name, enabled := range in.Features {
		switch normalizeName(name) {
		case "hosted_service":
			rights = ensureRights(rights)
			rights.HostedService = enabled
		case "embedding":
			rights = ensureRights(rights)
			rights.Embedding = enabled
		case "resale":
			rights = ensureRights(rights)
			rights.Resale = enabled
		case "white_label":
			rights = ensureRights(rights)
			rights.WhiteLabel = enabled
		case "support_sla":
			rights = ensureRights(rights)
			rights.SupportSLA = enabled
		case "sso":
			entitlements = ensureEntitlements(entitlements)
			entitlements.SSO = enabled
		case "saml":
			entitlements = ensureEntitlements(entitlements)
			entitlements.SAML = enabled
		case "scim":
			entitlements = ensureEntitlements(entitlements)
			entitlements.SCIM = enabled
		case "rbac":
			entitlements = ensureEntitlements(entitlements)
			entitlements.RBAC = enabled
		case "audit":
			entitlements = ensureEntitlements(entitlements)
			entitlements.Audit = enabled
		case "audit_export":
			entitlements = ensureEntitlements(entitlements)
			entitlements.AuditExport = enabled
		case "siem_export":
			entitlements = ensureEntitlements(entitlements)
			entitlements.SIEMExport = enabled
		case "legal_hold":
			entitlements = ensureEntitlements(entitlements)
			entitlements.LegalHold = enabled
		case "velocity_rules":
			entitlements = ensureEntitlements(entitlements)
			entitlements.VelocityRules = enabled
		case "break_glass_admin":
			entitlements = ensureEntitlements(entitlements)
			entitlements.BreakGlassAdmin = enabled
		default:
			entitlements = ensureEntitlements(entitlements)
			if entitlements.Features == nil {
				entitlements.Features = map[string]bool{}
			}
			entitlements.Features[normalizeName(name)] = enabled
		}
	}

	for name, limit := range in.Limits {
		entitlements = ensureEntitlements(entitlements)
		switch normalizeName(name) {
		case "max_workers":
			entitlements.MaxWorkers = limit
		case "requests_per_second", "rate_limit_rps", "max_requests_per_second":
			entitlements.RequestsPerSecond = limit
		case "max_concurrent_jobs":
			entitlements.MaxConcurrentJobs = limit
		case "max_workflow_steps":
			entitlements.MaxWorkflowSteps = limit
		case "max_active_workflows":
			entitlements.MaxActiveWorkflows = limit
		case "max_tenants":
			entitlements.MaxTenants = limit
		case "max_schema_count", "max_schemas":
			entitlements.MaxSchemaCount = limit
		case "max_prompt_chars":
			entitlements.MaxPromptChars = limit
		case "max_body_bytes":
			entitlements.MaxBodyBytes = limit
		case "max_artifact_bytes":
			entitlements.MaxArtifactBytes = limit
		case "max_policy_bundles":
			entitlements.MaxPolicyBundles = limit
		case "audit_retention_days":
			entitlements.AuditRetentionDays = limit
		default:
			if entitlements.Limits == nil {
				entitlements.Limits = map[string]int64{}
			}
			entitlements.Limits[normalizeName(name)] = limit
		}
	}

	out.Rights = rights
	out.Entitlements = entitlements
	return out
}

func ensureRights(rights *Rights) *Rights {
	if rights != nil {
		return rights
	}
	return &Rights{}
}

func ensureEntitlements(entitlements *Entitlements) *Entitlements {
	if entitlements != nil {
		return entitlements
	}
	return &Entitlements{}
}

func cloneInt(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func normalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
