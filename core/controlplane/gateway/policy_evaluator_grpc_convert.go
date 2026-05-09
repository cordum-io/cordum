package gateway

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func jobEvaluationContextFromProto(ctx *pb.JobEvaluationContext) *jobEvaluationContext {
	if ctx == nil {
		return nil
	}
	return &jobEvaluationContext{
		TenantID:    strings.TrimSpace(ctx.GetTenantId()),
		JobID:       strings.TrimSpace(ctx.GetJobId()),
		WorkflowID:  strings.TrimSpace(ctx.GetWorkflowId()),
		Topic:       strings.TrimSpace(ctx.GetTopic()),
		PrincipalID: strings.TrimSpace(ctx.GetPrincipalId()),
		Labels:      clonePolicyEvalStringMap(ctx.GetLabels()),
		MemoryID:    strings.TrimSpace(ctx.GetMemoryId()),
		Capability:  strings.TrimSpace(ctx.GetCapability()),
		RiskTags:    append([]string{}, ctx.GetRiskTags()...),
		Input: jobEvaluationInput{
			Content:     string(ctx.GetInputContent()),
			ContentType: strings.TrimSpace(ctx.GetInputContentType()),
			SizeBytes:   ctx.GetInputSizeBytes(),
		},
	}
}

func edgeEvaluationContextFromProto(ctx *pb.EdgeEvaluationContext) *edgeEvaluationContext {
	if ctx == nil {
		return nil
	}
	return &edgeEvaluationContext{
		TenantID:          strings.TrimSpace(ctx.GetTenantId()),
		PrincipalID:       strings.TrimSpace(ctx.GetPrincipalId()),
		SessionID:         strings.TrimSpace(ctx.GetSessionId()),
		ExecutionID:       strings.TrimSpace(ctx.GetExecutionId()),
		AgentProduct:      strings.TrimSpace(ctx.GetAgentProduct()),
		ToolName:          strings.TrimSpace(ctx.GetToolName()),
		ToolInputRedacted: clonePolicyEvalAnyMap(structMap(ctx.GetToolInputRedacted())),
		InputHash:         strings.TrimSpace(ctx.GetInputHash()),
		ToolInputHash:     strings.TrimSpace(ctx.GetToolInputHash()),
		Labels:            clonePolicyEvalStringMap(ctx.GetLabels()),
		RiskTags:          append([]string{}, ctx.GetRiskTags()...),
	}
}

func policyDecisionToProto(decision policy.Decision) *pb.Decision {
	return &pb.Decision{
		Source:        protoDecisionSource(decision.Source),
		RuleId:        strings.TrimSpace(decision.RuleID),
		BundleId:      strings.TrimSpace(decision.BundleID),
		BundleVersion: strings.TrimSpace(decision.BundleVersion),
		Type:          protoDecisionType(decision.Type),
		Trace:         protoTraceSteps(decision.Trace),
		InputRef:      strings.TrimSpace(decision.InputRef),
		OutputRef:     strings.TrimSpace(decision.OutputRef),
		AuditHash:     strings.TrimSpace(decision.AuditHash),
		Timestamp:     timestampFromTime(decision.Timestamp),
	}
}

func protoTraceSteps(trace []policy.TraceStep) []*pb.TraceStep {
	if len(trace) == 0 {
		return nil
	}
	out := make([]*pb.TraceStep, 0, len(trace))
	for _, step := range trace {
		out = append(out, &pb.TraceStep{
			RuleId:       strings.TrimSpace(step.RuleID),
			BundleId:     strings.TrimSpace(step.BundleID),
			DecisionType: protoDecisionType(step.DecisionType),
			Reason:       strings.TrimSpace(step.Reason),
			Timestamp:    timestampFromTime(step.Timestamp),
			Constraints:  rawMessageStruct(step.Constraints),
		})
	}
	return out
}

func policyRuleScopeFromProto(scope *pb.RuleScope) *policy.RuleScope {
	if scope == nil {
		return nil
	}
	out := valuePolicyRuleScopeFromProto(scope)
	return &out
}

func valuePolicyRuleScopeFromProto(scope *pb.RuleScope) policy.RuleScope {
	if scope == nil {
		return policy.RuleScope{}
	}
	return policy.RuleScope{
		Kind:  policyRuleScopeKindFromProto(scope.GetKind()),
		Value: strings.TrimSpace(scope.GetValue()),
	}
}

func policyAuditMetadataFromProto(audit *pb.AuditMetadata) policy.AuditMetadata {
	if audit == nil {
		return policy.AuditMetadata{}
	}
	return policy.AuditMetadata{
		CreatedAt: timestampToTime(audit.GetCreatedAt()),
		CreatedBy: strings.TrimSpace(audit.GetCreatedBy()),
		UpdatedAt: timestampToTime(audit.GetUpdatedAt()),
		UpdatedBy: strings.TrimSpace(audit.GetUpdatedBy()),
	}
}

func structRawMessage(value *structpb.Struct) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := protojson.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(raw) == "{}" {
		return nil, nil
	}
	return json.RawMessage(raw), nil
}

func rawMessageStruct(raw json.RawMessage) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || len(object) == 0 {
		return nil
	}
	value, err := structpb.NewStruct(object)
	if err != nil {
		return nil
	}
	return value
}

func structMap(value *structpb.Struct) map[string]any {
	if value == nil {
		return nil
	}
	return value.AsMap()
}

func timestampFromTime(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value.UTC())
}

func timestampToTime(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.AsTime().UTC()
}

func policyRuleTypeFromProto(value pb.RuleType) policy.RuleType {
	switch value {
	case pb.RuleType_RULE_TYPE_INPUT:
		return policy.RuleTypeInput
	case pb.RuleType_RULE_TYPE_OUTPUT:
		return policy.RuleTypeOutput
	case pb.RuleType_RULE_TYPE_VELOCITY:
		return policy.RuleTypeVelocity
	case pb.RuleType_RULE_TYPE_EDGE:
		return policy.RuleTypeEdge
	default:
		return policy.RuleType("")
	}
}

func policyRuleStatusFromProto(value pb.RuleStatus) policy.RuleStatus {
	switch value {
	case pb.RuleStatus_RULE_STATUS_DRAFT:
		return policy.RuleStatusDraft
	case pb.RuleStatus_RULE_STATUS_PUBLISHED:
		return policy.RuleStatusPublished
	case pb.RuleStatus_RULE_STATUS_DEPRECATED:
		return policy.RuleStatusDeprecated
	default:
		return policy.RuleStatus("")
	}
}

func policyRuleScopeKindFromProto(value pb.RuleScopeKind) policy.RuleScopeKind {
	switch value {
	case pb.RuleScopeKind_RULE_SCOPE_KIND_GLOBAL:
		return policy.RuleScopeGlobal
	case pb.RuleScopeKind_RULE_SCOPE_KIND_TENANT:
		return policy.RuleScopeTenant
	case pb.RuleScopeKind_RULE_SCOPE_KIND_WORKFLOW:
		return policy.RuleScopeWorkflow
	case pb.RuleScopeKind_RULE_SCOPE_KIND_EDGE_FLEET:
		return policy.RuleScopeEdgeFleet
	case pb.RuleScopeKind_RULE_SCOPE_KIND_EDGE_USER:
		return policy.RuleScopeEdgeUser
	default:
		return policy.RuleScopeKind("")
	}
}

func protoDecisionSource(value policy.DecisionSource) pb.DecisionSource {
	if value == policy.DecisionSourceEdge {
		return pb.DecisionSource_DECISION_SOURCE_EDGE
	}
	if value == policy.DecisionSourceJob {
		return pb.DecisionSource_DECISION_SOURCE_JOB
	}
	return pb.DecisionSource_DECISION_SOURCE_UNSPECIFIED
}

func protoDecisionType(value policy.DecisionType) pb.DecisionType {
	switch value {
	case policy.DecisionDeny:
		return pb.DecisionType_DECISION_TYPE_DENY
	case policy.DecisionRequireHuman:
		return pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	case policy.DecisionThrottle:
		return pb.DecisionType_DECISION_TYPE_THROTTLE
	case policy.DecisionAllowWithConstraints:
		return pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	case policy.DecisionQuarantine:
		return pb.DecisionType_DECISION_TYPE_QUARANTINE
	case policy.DecisionRedact:
		return pb.DecisionType_DECISION_TYPE_REDACT
	default:
		return pb.DecisionType_DECISION_TYPE_ALLOW
	}
}
