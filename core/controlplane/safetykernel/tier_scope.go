package safetykernel

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func resolvePolicyScope(req *pb.PolicyCheckRequest) (workflowID, jobID string) {
	if req == nil {
		return "", ""
	}
	labels := req.GetLabels()
	workflowID = pickLabel(labels,
		"workflow_id",
		"workflow.id",
		"workflow",
		"workflow_run_id",
		"workflow.run_id",
	)
	jobID = pickLabel(labels, policylabels.PolicyAttachmentID)
	if jobID == "" {
		jobID = strings.TrimSpace(req.GetJobId())
	}
	if jobID == "" {
		jobID = pickLabel(labels,
			"job_id",
			"job.id",
			"edge.job_id",
			"session_id",
			"edge.session_id",
		)
	}
	return workflowID, jobID
}

func scopedPolicyForRequest(
	base *config.SafetyPolicy,
	global *GlobalPolicy,
	workflowID string,
	jobID string,
	topic string,
	labels map[string]string,
) *config.SafetyPolicy {
	if base == nil || global == nil {
		return base
	}
	defaultDecision, defaultTier := global.DefaultDecisionForJobWorkflowTier(workflowID, jobID)
	rules := global.RulesForInput(workflowID, jobID)
	if strings.TrimSpace(topic) == EdgeActionTopic {
		rules = global.RulesForEdgeAction(workflowID, jobID)
	} else if policyScopeMCPUsed(extractMCPRequest(labels)) {
		rules = global.RulesForMCPTool(workflowID, jobID)
	}
	return &config.SafetyPolicy{
		Version:         base.Version,
		Tier:            defaultTier,
		DefaultTenant:   base.DefaultTenant,
		DefaultDecision: defaultDecision,
		Rules:           rules,
		Tenants:         base.Tenants,
	}
}

func policyScopeMCPUsed(req config.MCPRequest) bool {
	return strings.TrimSpace(req.Server) != "" ||
		strings.TrimSpace(req.Tool) != "" ||
		strings.TrimSpace(req.Resource) != "" ||
		strings.TrimSpace(req.Action) != ""
}

func clonePolicyWithTierMetadata(policy *config.SafetyPolicy) *config.SafetyPolicy {
	out := clonePolicy(policy)
	applyKernelTierMetadata(out)
	return out
}

func applyKernelTierMetadata(policy *config.SafetyPolicy) {
	if policy == nil {
		return
	}
	policy.Tier = config.NormalizePolicyTier(policy.Tier)
	policy.Selector = config.TrimPolicySelector(policy.Selector)
	moveKernelScopedDefault(policy)
	normalizeKernelTierDefaults(policy)
	for idx, rule := range policy.Rules {
		tier := rule.Tier
		if strings.TrimSpace(tier) == "" {
			tier = policy.Tier
		}
		rule.Tier = config.NormalizePolicyTier(tier)
		rule.Selector = config.MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == config.PolicyTierGlobal {
			rule.Selector = config.PolicySelector{}
		}
		policy.Rules[idx] = rule
	}
	for idx, rule := range policy.InputRules {
		tier := rule.Tier
		if strings.TrimSpace(tier) == "" {
			tier = policy.Tier
		}
		rule.Tier = config.NormalizePolicyTier(tier)
		rule.Selector = config.MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == config.PolicyTierGlobal {
			rule.Selector = config.PolicySelector{}
		}
		policy.InputRules[idx] = rule
	}
	if policy.Tier == config.PolicyTierGlobal {
		policy.Selector = config.PolicySelector{}
	}
}

func moveKernelScopedDefault(policy *config.SafetyPolicy) {
	decision := strings.TrimSpace(policy.DefaultDecision)
	if decision == "" || policy.Tier == config.PolicyTierGlobal {
		policy.DefaultDecision = decision
		return
	}
	policy.TierDefaults = append(policy.TierDefaults, config.PolicyTierDefault{
		Tier:     policy.Tier,
		Selector: policy.Selector,
		Decision: decision,
	})
	policy.DefaultDecision = ""
}

func normalizeKernelTierDefaults(policy *config.SafetyPolicy) {
	defaults := make([]config.PolicyTierDefault, 0, len(policy.TierDefaults))
	for _, def := range policy.TierDefaults {
		decision := strings.TrimSpace(def.Decision)
		tier := config.NormalizePolicyTier(def.Tier)
		if decision == "" || tier == config.PolicyTierGlobal {
			continue
		}
		defaults = append(defaults, config.PolicyTierDefault{
			Tier:     tier,
			Selector: config.TrimPolicySelector(def.Selector),
			Decision: decision,
		})
	}
	policy.TierDefaults = defaults
}
