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
