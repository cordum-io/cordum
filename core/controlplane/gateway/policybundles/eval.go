package policybundles

import (
	"fmt"
	"path"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// EvaluatePolicyCheck evaluates a policy check request against a merged policy.
func EvaluatePolicyCheck(policy *config.SafetyPolicy, snapshot string, req *pb.PolicyCheckRequest) *pb.PolicyCheckResponse {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(req.GetTenant())
	meta := req.GetMeta()
	if tenant == "" && meta != nil {
		tenant = strings.TrimSpace(meta.GetTenantId())
	}

	defaultTenant := ""
	if policy != nil {
		defaultTenant = strings.TrimSpace(policy.DefaultTenant)
	}
	if tenant == "" {
		tenant = defaultTenant
	}
	if tenant == "" {
		tenant = "default"
	}

	if topic == "" {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "missing topic"}
	}
	if !strings.HasPrefix(topic, "job.") {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "unsupported topic"}
	}

	input := config.PolicyInput{
		Tenant: tenant,
		Topic:  topic,
		Labels: req.GetLabels(),
		Meta:   PolicyMetaFromRequest(req),
		MCP:    ExtractMCPRequest(req.GetLabels()),
	}
	input.SecretsPresent = SecretsPresent(input.Meta, req.GetLabels())

	policyDecision := config.PolicyDecision{Decision: "allow"}
	if policy != nil {
		policyDecision = policy.Evaluate(input)
		if tp, ok := policy.Tenants[tenant]; ok {
			if ok, mcpReason := config.MCPAllowed(tp.MCP, input.MCP); !ok {
				policyDecision.Decision = "deny"
				policyDecision.Reason = mcpReason
			}
		}
	}

	constraints := ToProtoConstraints(policyDecision.Constraints)
	switch policyDecision.Decision {
	case "deny":
		decision = pb.DecisionType_DECISION_TYPE_DENY
		reason = policyDecision.Reason
	case "require_approval":
		decision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
		reason = policyDecision.Reason
	case "throttle":
		decision = pb.DecisionType_DECISION_TYPE_THROTTLE
		reason = policyDecision.Reason
	case "allow_with_constraints":
		decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	case "allow":
		if constraints != nil {
			decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
		}
	}

	if eff, ok := config.ParseEffectiveSafety(req.GetEffectiveConfig()); ok {
		if MatchAny(eff.DeniedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic %q denied by effective config", topic)
		}
		if len(eff.AllowedTopics) > 0 && !MatchAny(eff.AllowedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic %q not allowed by effective config", topic)
		}
		if ok, mcpReason := config.MCPAllowed(eff.MCP, input.MCP); !ok {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = mcpReason
		}
	}

	approvalRequired := policyDecision.ApprovalRequired || decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	approvalRef := ""
	if approvalRequired {
		approvalRef = req.GetJobId()
	}

	return &pb.PolicyCheckResponse{
		Decision:         decision,
		Reason:           reason,
		PolicySnapshot:   snapshot,
		RuleId:           policyDecision.RuleID,
		Constraints:      constraints,
		ApprovalRequired: approvalRequired,
		ApprovalRef:      approvalRef,
		Remediations:     ToProtoRemediations(policyDecision.Remediations),
	}
}

// PolicyMetaFromRequest extracts policy metadata from a gRPC request.
func PolicyMetaFromRequest(req *pb.PolicyCheckRequest) config.PolicyMeta {
	meta := req.GetMeta()
	out := config.PolicyMeta{}
	if meta == nil {
		if req.GetPrincipalId() != "" {
			out.ActorID = req.GetPrincipalId()
		}
		return out
	}
	out.ActorID = meta.GetActorId()
	out.ActorType = ActorTypeString(meta.GetActorType())
	out.IdempotencyKey = meta.GetIdempotencyKey()
	out.Capability = meta.GetCapability()
	out.RiskTags = append(out.RiskTags, meta.GetRiskTags()...)
	out.Requires = append(out.Requires, meta.GetRequires()...)
	out.PackID = meta.GetPackId()
	if out.ActorID == "" {
		out.ActorID = req.GetPrincipalId()
	}
	return out
}

// ActorTypeString converts a protobuf ActorType to a string.
func ActorTypeString(val pb.ActorType) string {
	switch val {
	case pb.ActorType_ACTOR_TYPE_HUMAN:
		return "human"
	case pb.ActorType_ACTOR_TYPE_SERVICE:
		return "service"
	default:
		return ""
	}
}

// SecretsPresent checks whether secrets are indicated in metadata or labels.
func SecretsPresent(meta config.PolicyMeta, labels map[string]string) bool {
	if labels != nil {
		if v := strings.TrimSpace(labels["secrets_present"]); v != "" {
			return v == "true" || v == "1" || strings.EqualFold(v, "yes")
		}
	}
	for _, tag := range meta.RiskTags {
		if strings.EqualFold(tag, "secrets") {
			return true
		}
	}
	return false
}

// ExtractMCPRequest extracts MCP request data from labels.
func ExtractMCPRequest(labels map[string]string) config.MCPRequest {
	if len(labels) == 0 {
		return config.MCPRequest{}
	}
	return config.MCPRequest{
		Server:   PickLabel(labels, "mcp.server", "mcp_server", "mcpServer"),
		Tool:     PickLabel(labels, "mcp.tool", "mcp_tool", "mcpTool"),
		Resource: PickLabel(labels, "mcp.resource", "mcp_resource", "mcpResource"),
		Action:   strings.ToLower(PickLabel(labels, "mcp.action", "mcp_action", "mcpAction")),
	}
}

// PickLabel returns the first non-empty trimmed value from the given label keys.
func PickLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if val, ok := labels[key]; ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// ToProtoConstraints converts policy constraints to a protobuf message.
func ToProtoConstraints(c config.PolicyConstraints) *pb.PolicyConstraints {
	if IsConstraintsEmpty(c) {
		return nil
	}
	return &pb.PolicyConstraints{
		Budgets: &pb.BudgetConstraints{
			MaxRuntimeMs:      c.Budgets.MaxRuntimeMs,
			MaxRetries:        c.Budgets.MaxRetries,
			MaxArtifactBytes:  c.Budgets.MaxArtifactBytes,
			MaxConcurrentJobs: c.Budgets.MaxConcurrentJobs,
		},
		Sandbox: &pb.SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: c.Sandbox.NetworkAllowlist,
			FsReadOnly:       c.Sandbox.FsReadOnly,
			FsReadWrite:      c.Sandbox.FsReadWrite,
		},
		Toolchain: &pb.ToolchainConstraints{
			AllowedTools:    c.Toolchain.AllowedTools,
			AllowedCommands: c.Toolchain.AllowedCommands,
		},
		Diff: &pb.DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: c.Diff.DenyPathGlobs,
		},
		RedactionLevel: c.RedactionLevel,
	}
}

// ToProtoRemediations converts policy remediations to protobuf messages.
func ToProtoRemediations(remediations []config.PolicyRemediation) []*pb.PolicyRemediation {
	if len(remediations) == 0 {
		return nil
	}
	out := make([]*pb.PolicyRemediation, 0, len(remediations))
	for _, rem := range remediations {
		r := rem
		out = append(out, &pb.PolicyRemediation{
			Id:                    r.ID,
			Title:                 r.Title,
			Summary:               r.Summary,
			ReplacementTopic:      r.ReplacementTopic,
			ReplacementCapability: r.ReplacementCapability,
			AddLabels:             r.AddLabels,
			RemoveLabels:          append([]string{}, r.RemoveLabels...),
		})
	}
	return out
}

// IsConstraintsEmpty returns true if all constraint fields are zero values.
func IsConstraintsEmpty(c config.PolicyConstraints) bool {
	return c.Budgets.MaxRuntimeMs == 0 && c.Budgets.MaxRetries == 0 && c.Budgets.MaxArtifactBytes == 0 && c.Budgets.MaxConcurrentJobs == 0 &&
		!c.Sandbox.Isolated && len(c.Sandbox.NetworkAllowlist) == 0 && len(c.Sandbox.FsReadOnly) == 0 && len(c.Sandbox.FsReadWrite) == 0 &&
		len(c.Toolchain.AllowedTools) == 0 && len(c.Toolchain.AllowedCommands) == 0 &&
		c.Diff.MaxFiles == 0 && c.Diff.MaxLines == 0 && len(c.Diff.DenyPathGlobs) == 0 &&
		strings.TrimSpace(c.RedactionLevel) == ""
}

// MatchAny returns true if any pattern matches the given value.
func MatchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pat := range patterns {
		if ConfigMatch(pat, value) {
			return true
		}
	}
	return false
}

// ConfigMatch performs a path.Match style pattern match.
func ConfigMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := path.Match(pattern, value)
	return ok
}
