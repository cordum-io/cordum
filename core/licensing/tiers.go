package licensing

import "strings"

const Unlimited int64 = -1

type Plan string

const (
	PlanCommunity  Plan = "community"
	PlanTeam       Plan = "team"
	PlanEnterprise Plan = "enterprise"
)

type ApprovalMode string

const (
	ApprovalModeSingle ApprovalMode = "single"
	ApprovalModeMulti  ApprovalMode = "multi"
	ApprovalModeCustom ApprovalMode = "custom"
)

type TierDefaultSpec struct {
	MaxWorkers         int64
	MaxConcurrentJobs  int64
	RequestsPerSecond  int64
	AuditRetentionDays int64
	ApprovalMode       ApprovalMode
	Audit              bool
	RBAC               bool
	SSO                bool
	SAML               bool
	SCIM               bool
	SIEMExport         bool
	LegalHold          bool
	VelocityRules      bool
	BreakGlassAdmin    bool
	AgentIdentity      bool
	LLMChatAssistant   bool
	SupportSLA         bool
}

var TierDefaults = map[Plan]TierDefaultSpec{
	PlanCommunity: {
		MaxWorkers:         3,
		MaxConcurrentJobs:  3,
		RequestsPerSecond:  500,
		AuditRetentionDays: 7,
		ApprovalMode:       ApprovalModeSingle,
		Audit:              true,
		BreakGlassAdmin:    true,
	},
	PlanTeam: {
		MaxWorkers:         25,
		MaxConcurrentJobs:  25,
		RequestsPerSecond:  2000,
		AuditRetentionDays: 90,
		ApprovalMode:       ApprovalModeMulti,
		Audit:              true,
		BreakGlassAdmin:    true,
	},
	PlanEnterprise: {
		MaxWorkers:         Unlimited,
		MaxConcurrentJobs:  Unlimited,
		RequestsPerSecond:  10000,
		AuditRetentionDays: Unlimited,
		ApprovalMode:       ApprovalModeCustom,
		Audit:              true,
		RBAC:               true,
		SSO:                true,
		SAML:               true,
		SCIM:               true,
		SIEMExport:         true,
		LegalHold:          true,
		VelocityRules:      true,
		BreakGlassAdmin:    true,
		AgentIdentity:      true,
		LLMChatAssistant:   true,
		SupportSLA:         true,
	},
}

func ParsePlan(raw string) Plan {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PlanEnterprise):
		return PlanEnterprise
	case string(PlanTeam):
		return PlanTeam
	case "", string(PlanCommunity):
		return PlanCommunity
	default:
		return PlanCommunity
	}
}

func (p Plan) Normalized() Plan {
	return ParsePlan(string(p))
}

func (p Plan) DisplayName() string {
	switch p.Normalized() {
	case PlanEnterprise:
		return "Enterprise"
	case PlanTeam:
		return "Team"
	default:
		return "Community"
	}
}

func (p Plan) Licensed() bool {
	return p.Normalized() != PlanCommunity
}

func ParseApprovalMode(raw string) ApprovalMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ApprovalModeCustom):
		return ApprovalModeCustom
	case string(ApprovalModeMulti):
		return ApprovalModeMulti
	case "", string(ApprovalModeSingle):
		return ApprovalModeSingle
	default:
		return ApprovalModeSingle
	}
}

func DefaultEntitlements(plan Plan) Entitlements {
	spec, ok := TierDefaults[plan.Normalized()]
	if !ok {
		spec = TierDefaults[PlanCommunity]
	}
	var entitlements Entitlements
	applyTierDefaultSpec(&entitlements, spec)
	return entitlements
}

func applyTierDefaultSpec(target *Entitlements, spec TierDefaultSpec) {
	if target == nil {
		return
	}
	setNamedIntField(target, spec.MaxWorkers, "MaxWorkers")
	setNamedIntField(target, spec.MaxConcurrentJobs, "MaxConcurrentJobs")
	setNamedIntField(target, spec.RequestsPerSecond, "RequestsPerSecond", "RateLimitRPS", "MaxRequestsPerSecond", "RPS")
	setNamedIntField(target, spec.AuditRetentionDays, "AuditRetentionDays")
	setNamedStringField(target, string(spec.ApprovalMode), "ApprovalMode")
	setNamedBoolField(target, spec.Audit, "Audit")
	setNamedBoolField(target, spec.RBAC, "RBAC", "AdvancedRBAC")
	setNamedBoolField(target, spec.SSO, "SSO")
	setNamedBoolField(target, spec.SAML, "SAML")
	setNamedBoolField(target, spec.SCIM, "SCIM")
	setNamedBoolField(target, spec.SIEMExport, "SIEMExport", "AuditExport")
	setNamedBoolField(target, spec.LegalHold, "LegalHold")
	setNamedBoolField(target, spec.VelocityRules, "VelocityRules")
	setNamedBoolField(target, spec.BreakGlassAdmin, "BreakGlassAdmin")
	setNamedBoolField(target, spec.AgentIdentity, "AgentIdentity")
	setNamedBoolField(target, spec.LLMChatAssistant, "LLMChatAssistant")
	setNamedBoolField(target, spec.SupportSLA, "SupportSLA")
}
