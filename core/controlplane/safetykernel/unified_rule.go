package safetykernel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// EvaluateRule evaluates one unified Rule and returns legacy + unified shapes.
func (s *server) EvaluateRule(
	ctx context.Context,
	rule policy.Rule,
	req *pb.PolicyCheckRequest,
) (*pb.PolicyCheckResponse, policy.Decision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil {
		return nil, policy.Decision{}, fmt.Errorf("missing policy check request")
	}
	if !RuleScopeMatchesJob(rule.Scope, jobContextFromRequest(req)) {
		return unmatchedRuleResult(s, ctx, rule, "rule scope did not match job")
	}
	switch rule.Type {
	case policy.RuleTypeInput:
		return s.evaluateUnifiedInputRule(ctx, rule, req)
	case policy.RuleTypeOutput:
		return s.evaluateUnifiedOutputRule(ctx, rule, req)
	case policy.RuleTypeVelocity:
		return s.evaluateUnifiedVelocityRule(ctx, rule, req)
	default:
		return nil, policy.Decision{}, fmt.Errorf("unsupported unified rule type %q", rule.Type)
	}
}

func (s *server) unmatchedRuleDecision(
	ctx context.Context,
	rule policy.Rule,
	reason string,
) (*pb.PolicyCheckResponse, policy.Decision) {
	resp := &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:   strings.TrimSpace(reason),
	}
	return resp, emitRuleDecision(ctx, rule, policy.DecisionAllow, resp.Reason, "", "")
}

func unmatchedRuleResult(
	s *server,
	ctx context.Context,
	rule policy.Rule,
	reason string,
) (*pb.PolicyCheckResponse, policy.Decision, error) {
	resp, decision := s.unmatchedRuleDecision(ctx, rule, reason)
	return resp, decision, nil
}

func (s *server) evaluateUnifiedInputRule(
	ctx context.Context,
	rule policy.Rule,
	req *pb.PolicyCheckRequest,
) (*pb.PolicyCheckResponse, policy.Decision, error) {
	compiled, err := RuleToCompiledInput(rule)
	if err != nil {
		return nil, policy.Decision{}, err
	}
	matched, findings := evaluateInputRule(compiled, inputEvalRequestFromPolicy(req), s.scannerSnapshot())
	if !matched {
		return unmatchedRuleResult(s, ctx, rule, "input rule did not match")
	}
	pbDecision, decisionType := inputDecisionTypes(compiled.decision)
	reason := strings.TrimSpace(compiled.reason)
	if reason == "" {
		reason = inputRuleReason(compiled, findings)
	}
	resp := &pb.PolicyCheckResponse{Decision: pbDecision, Reason: reason, RuleId: compiled.id}
	return resp, emitRuleDecision(ctx, rule, decisionType, reason, "", ""), nil
}

func (s *server) evaluateUnifiedOutputRule(
	ctx context.Context,
	rule policy.Rule,
	req *pb.PolicyCheckRequest,
) (*pb.PolicyCheckResponse, policy.Decision, error) {
	compiled, err := RuleToCompiledOutput(rule)
	if err != nil {
		return nil, policy.Decision{}, err
	}
	matched, findings := evaluateOutputRule(compiled, outputEvalRequestFromPolicy(req), s.scannerSnapshot())
	if !matched {
		return unmatchedRuleResult(s, ctx, rule, "output rule did not match")
	}
	legacyDecision, decisionType := outputDecisionTypes(compiled.decision)
	reason := outputRuleReason(compiled, findings)
	resp := &pb.PolicyCheckResponse{Decision: legacyDecision, Reason: reason, RuleId: compiled.id}
	return resp, emitRuleDecision(ctx, rule, decisionType, reason, "", ""), nil
}

func (s *server) evaluateUnifiedVelocityRule(
	ctx context.Context,
	rule policy.Rule,
	req *pb.PolicyCheckRequest,
) (*pb.PolicyCheckResponse, policy.Decision, error) {
	legacyRule, err := RuleToCompiledVelocity(rule)
	if err != nil {
		return nil, policy.Decision{}, err
	}
	policyDoc := &config.SafetyPolicy{Rules: []config.PolicyRule{legacyRule}, DefaultDecision: "allow"}
	decision := s.evaluateRulesWithVelocity(ctx, policyDoc, policyInputFromRequest(req), req.GetJobId(), "evaluate")
	resp := responseFromPolicyDecision(decision)
	decisionType := unifiedDecisionFromLegacy(decision.Decision)
	return resp, emitRuleDecision(ctx, rule, decisionType, resp.GetReason(), "", ""), nil
}

func inputEvalRequestFromPolicy(req *pb.PolicyCheckRequest) inputEvaluateRequest {
	meta := req.GetMeta()
	evalReq := inputEvaluateRequest{
		tenant:      tenantFromPolicyRequest(req),
		topic:       strings.TrimSpace(req.GetTopic()),
		contentType: strings.TrimSpace(req.GetInputContentType()),
		content:     inputContentFromPolicyRequest(req),
		inputSize:   req.GetInputSizeBytes(),
	}
	if meta != nil {
		evalReq.capabilities = append(evalReq.capabilities, meta.GetCapability())
		evalReq.riskTags = append(evalReq.riskTags, meta.GetRiskTags()...)
	}
	return evalReq
}

func outputEvalRequestFromPolicy(req *pb.PolicyCheckRequest) *OutputEvaluateRequest {
	return &OutputEvaluateRequest{
		JobID:           strings.TrimSpace(req.GetJobId()),
		Topic:           strings.TrimSpace(req.GetTopic()),
		Tenant:          tenantFromPolicyRequest(req),
		Labels:          req.GetLabels(),
		OutputContent:   inputContentFromPolicyRequest(req),
		OutputSizeBytes: req.GetInputSizeBytes(),
		ContentType:     strings.TrimSpace(req.GetInputContentType()),
	}
}

func policyInputFromRequest(req *pb.PolicyCheckRequest) config.PolicyInput {
	input := config.PolicyInput{
		Tenant:     tenantFromPolicyRequest(req),
		Topic:      strings.TrimSpace(req.GetTopic()),
		Labels:     req.GetLabels(),
		Meta:       policyMetaFromRequest(req),
		MCP:        extractMCPRequest(req.GetLabels()),
		Delegation: delegationContextFromRequest(req),
	}
	input.SecretsPresent = secretsPresent(input.Meta, req.GetLabels())
	return input
}

func inputContentFromPolicyRequest(req *pb.PolicyCheckRequest) []byte {
	content := req.GetInputContent()
	if len(content) > 0 {
		return content
	}
	if prompt, ok := req.GetLabels()["_content.prompt"]; ok && prompt != "" {
		return []byte(prompt)
	}
	return nil
}

func tenantFromPolicyRequest(req *pb.PolicyCheckRequest) string {
	tenant := strings.TrimSpace(req.GetTenant())
	if tenant != "" {
		return tenant
	}
	if meta := req.GetMeta(); meta != nil {
		return strings.TrimSpace(meta.GetTenantId())
	}
	return ""
}

func jobContextFromRequest(req *pb.PolicyCheckRequest) JobContext {
	workflowID, jobID := resolvePolicyScope(req)
	return JobContext{
		Tenant:     tenantFromPolicyRequest(req),
		WorkflowID: workflowID,
		JobID:      jobID,
	}
}

func (s *server) scannerSnapshot() map[string]OutputScanner {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]OutputScanner, len(s.scanners))
	for name, scanner := range s.scanners {
		out[name] = scanner
	}
	return out
}

func emitRuleDecision(
	ctx context.Context,
	rule policy.Rule,
	decisionType policy.DecisionType,
	reason string,
	inputRef string,
	outputRef string,
) policy.Decision {
	trace := []policy.TraceStep{{
		RuleID:       strings.TrimSpace(rule.ID),
		DecisionType: decisionType,
		Reason:       strings.TrimSpace(reason),
		Timestamp:    time.Now().UTC(),
	}}
	return EmitDecision(ctx, rule, decisionType, trace, inputRef, outputRef, "")
}

func inputDecisionTypes(decision string) (pb.DecisionType, policy.DecisionType) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "require_approval", "require-approval", "require_human":
		return pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, policy.DecisionRequireHuman
	default:
		return pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionDeny
	}
}

func outputDecisionTypes(decision pb.OutputDecision) (pb.DecisionType, policy.DecisionType) {
	switch decision {
	case pb.OutputDecision_OUTPUT_DECISION_REDACT:
		return pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionRedact
	case pb.OutputDecision_OUTPUT_DECISION_QUARANTINE:
		return pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionQuarantine
	default:
		return pb.DecisionType_DECISION_TYPE_ALLOW, policy.DecisionAllow
	}
}

func responseFromPolicyDecision(decision config.PolicyDecision) *pb.PolicyCheckResponse {
	pbDecision := pb.DecisionType_DECISION_TYPE_ALLOW
	switch decision.Decision {
	case "deny":
		pbDecision = pb.DecisionType_DECISION_TYPE_DENY
	case "require_approval":
		pbDecision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	case "throttle":
		pbDecision = pb.DecisionType_DECISION_TYPE_THROTTLE
	case "allow_with_constraints":
		pbDecision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	}
	return &pb.PolicyCheckResponse{
		Decision:    pbDecision,
		Reason:      strings.TrimSpace(decision.Reason),
		RuleId:      strings.TrimSpace(decision.RuleID),
		Constraints: toProtoConstraints(decision.Constraints),
	}
}

func unifiedDecisionFromLegacy(decision string) policy.DecisionType {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny":
		return policy.DecisionDeny
	case "require_approval", "require_human":
		return policy.DecisionRequireHuman
	case "throttle":
		return policy.DecisionThrottle
	case "allow_with_constraints":
		return policy.DecisionAllowWithConstraints
	default:
		return policy.DecisionAllow
	}
}
