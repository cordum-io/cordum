package gateway

import (
	"context"
	"net/http"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type policyEvaluationRequest struct {
	Rule        *policy.Rule
	BundleID    string
	Scope       *policy.RuleScope
	JobContext  *jobEvaluationContext
	EdgeContext *edgeEvaluationContext
}

type jobEvaluationContext struct {
	TenantID    string             `json:"tenant_id"`
	JobID       string             `json:"job_id"`
	WorkflowID  string             `json:"workflow_id"`
	Topic       string             `json:"topic"`
	PrincipalID string             `json:"principal_id"`
	Labels      map[string]string  `json:"labels"`
	MemoryID    string             `json:"memory_id"`
	Capability  string             `json:"capability"`
	RiskTags    []string           `json:"risk_tags"`
	Input       jobEvaluationInput `json:"input"`
}

type jobEvaluationInput struct {
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type edgeEvaluationContext struct {
	TenantID          string            `json:"tenant_id"`
	PrincipalID       string            `json:"principal_id"`
	SessionID         string            `json:"session_id"`
	ExecutionID       string            `json:"execution_id"`
	AgentProduct      string            `json:"agent_product"`
	ToolName          string            `json:"tool_name"`
	ToolInputRedacted map[string]any    `json:"tool_input_redacted"`
	InputHash         string            `json:"input_hash"`
	ToolInputHash     string            `json:"tool_input_hash"`
	Labels            map[string]string `json:"labels"`
	RiskTags          []string          `json:"risk_tags"`
}

type policyEvaluateResult struct {
	Decision policy.Decision
}

type policyEvaluateErrorKind string

const (
	policyEvaluateValidation  policyEvaluateErrorKind = "validation"
	policyEvaluateNotFound    policyEvaluateErrorKind = "not_found"
	policyEvaluateUnavailable policyEvaluateErrorKind = "unavailable"
	policyEvaluateUpstream    policyEvaluateErrorKind = "upstream"
)

type policyEvaluateError struct {
	Kind    policyEvaluateErrorKind
	Message string
	Err     error
}

func (e *policyEvaluateError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *policyEvaluateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *policyEvaluateError) StatusCode() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Kind {
	case policyEvaluateValidation:
		return http.StatusBadRequest
	case policyEvaluateNotFound:
		return http.StatusNotFound
	case policyEvaluateUnavailable:
		return http.StatusServiceUnavailable
	case policyEvaluateUpstream:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func newPolicyEvaluateError(kind policyEvaluateErrorKind, message string, err error) *policyEvaluateError {
	return &policyEvaluateError{Kind: kind, Message: strings.TrimSpace(message), Err: err}
}

func (s *server) evaluateUnifiedPolicy(ctx context.Context, req policyEvaluationRequest) (policyEvaluateResult, error) {
	resolved, err := s.resolvePolicyEvaluationTarget(ctx, req)
	if err != nil {
		return policyEvaluateResult{}, err
	}
	if err := validatePolicyEvaluationContext(resolved.rule, req); err != nil {
		return policyEvaluateResult{}, err
	}
	result, err := s.dispatchUnifiedPolicyRule(ctx, resolved, req)
	if err != nil {
		return policyEvaluateResult{}, err
	}
	s.emitUnifiedPolicyEvaluateAudit(ctx, req, result.Decision)
	return result, nil
}

type resolvedPolicyEvaluationTarget struct {
	rule    policy.Rule
	bundle  *policy.Bundle
	binding policyBundleBinding
}

type policyBundleBinding struct {
	BundleID string
	Version  string
}

func (s *server) resolvePolicyEvaluationTarget(ctx context.Context, req policyEvaluationRequest) (resolvedPolicyEvaluationTarget, error) {
	hasRule := req.Rule != nil
	hasBundle := strings.TrimSpace(req.BundleID) != "" || req.Scope != nil
	if hasRule == hasBundle {
		return resolvedPolicyEvaluationTarget{}, newPolicyEvaluateError(policyEvaluateValidation, "provide exactly one of rule or bundle_id+scope", nil)
	}
	if hasRule {
		return resolvedPolicyEvaluationTarget{rule: *req.Rule}, nil
	}
	return s.resolvePolicyEvaluationBundle(ctx, req)
}

func (s *server) resolvePolicyEvaluationBundle(ctx context.Context, req policyEvaluationRequest) (resolvedPolicyEvaluationTarget, error) {
	bundleID := strings.TrimSpace(req.BundleID)
	if bundleID == "" || req.Scope == nil || req.Scope.Kind == "" {
		return resolvedPolicyEvaluationTarget{}, newPolicyEvaluateError(policyEvaluateValidation, "bundle_id and scope are required", nil)
	}
	if s == nil || s.policyBundleStore == nil {
		return resolvedPolicyEvaluationTarget{}, newPolicyEvaluateError(policyEvaluateUnavailable, "policy bundle store unavailable", nil)
	}
	deployment, err := s.policyBundleStore.GetActiveDeployment(ctx, *req.Scope)
	if err != nil {
		return resolvedPolicyEvaluationTarget{}, bundleStoreEvaluateError("resolve active deployment", err)
	}
	if strings.TrimSpace(deployment.BundleID) != bundleID {
		return resolvedPolicyEvaluationTarget{}, newPolicyEvaluateError(policyEvaluateNotFound, "active deployment for scope does not match bundle_id", nil)
	}
	version, err := s.policyBundleStore.GetBundleVersion(ctx, deployment.BundleID, deployment.Version)
	if err != nil {
		return resolvedPolicyEvaluationTarget{}, bundleStoreEvaluateError("load active bundle version", err)
	}
	bundle, _ := s.policyBundleStore.GetBundle(ctx, deployment.BundleID)
	return pickRuleFromBundleVersion(bundle, deployment, version, req)
}

func pickRuleFromBundleVersion(bundle *policy.Bundle, deployment *policy.Deployment, version *policy.BundleVersion, req policyEvaluationRequest) (resolvedPolicyEvaluationTarget, error) {
	for _, rule := range version.RuleSnapshot {
		if rule.Status != policy.RuleStatusPublished {
			continue
		}
		if policyRuleContextCompatible(rule.Type, req) {
			return resolvedPolicyEvaluationTarget{rule: rule, bundle: bundle, binding: policyBundleBinding{BundleID: deployment.BundleID, Version: deployment.Version}}, nil
		}
	}
	return resolvedPolicyEvaluationTarget{}, newPolicyEvaluateError(policyEvaluateNotFound, "active bundle has no published rule for evaluation context", nil)
}

func validatePolicyEvaluationContext(rule policy.Rule, req policyEvaluationRequest) error {
	hasJob := req.JobContext != nil
	hasEdge := req.EdgeContext != nil
	if hasJob == hasEdge {
		return newPolicyEvaluateError(policyEvaluateValidation, "provide exactly one of job_context or edge_context", nil)
	}
	if _, err := policy.ParseRuleType(rule.Type.String()); err != nil {
		return newPolicyEvaluateError(policyEvaluateValidation, "unsupported rule type "+rule.Type.String(), err)
	}
	if !policyRuleContextCompatible(rule.Type, req) {
		if rule.Type == policy.RuleTypeEdge {
			return newPolicyEvaluateError(policyEvaluateValidation, "rule type edge requires edge_context", nil)
		}
		return newPolicyEvaluateError(policyEvaluateValidation, "rule type "+rule.Type.String()+" requires job_context", nil)
	}
	return nil
}

func policyRuleContextCompatible(ruleType policy.RuleType, req policyEvaluationRequest) bool {
	if ruleType == policy.RuleTypeEdge {
		return req.EdgeContext != nil && req.JobContext == nil
	}
	switch ruleType {
	case policy.RuleTypeInput, policy.RuleTypeOutput, policy.RuleTypeVelocity:
		return req.JobContext != nil && req.EdgeContext == nil
	default:
		return false
	}
}

func (s *server) dispatchUnifiedPolicyRule(ctx context.Context, target resolvedPolicyEvaluationTarget, req policyEvaluationRequest) (policyEvaluateResult, error) {
	if target.rule.Type == policy.RuleTypeEdge {
		return s.evaluateUnifiedEdgeRule(ctx, target, *req.EdgeContext)
	}
	return s.evaluateUnifiedJobRule(ctx, target, *req.JobContext)
}

func (s *server) evaluateUnifiedJobRule(ctx context.Context, target resolvedPolicyEvaluationTarget, jobCtx jobEvaluationContext) (policyEvaluateResult, error) {
	if s == nil || s.safetyClient == nil {
		return policyEvaluateResult{}, newPolicyEvaluateError(policyEvaluateUnavailable, "safety kernel unavailable", nil)
	}
	resp, err := s.safetyClient.Evaluate(ctx, policyCheckRequestFromJobContext(jobCtx))
	if err != nil {
		return policyEvaluateResult{}, newPolicyEvaluateError(policyEvaluateUpstream, "safety kernel evaluate failed", err)
	}
	decision := decisionFromPolicyCheckResponse(policy.DecisionSourceJob, target.rule, target.binding, resp)
	return policyEvaluateResult{Decision: decision}, nil
}

func policyCheckRequestFromJobContext(ctx jobEvaluationContext) *pb.PolicyCheckRequest {
	inputSize := ctx.Input.SizeBytes
	if inputSize == 0 && ctx.Input.Content != "" {
		inputSize = int64(len([]byte(ctx.Input.Content)))
	}
	return &pb.PolicyCheckRequest{
		JobId:            strings.TrimSpace(ctx.JobID),
		Topic:            strings.TrimSpace(ctx.Topic),
		Tenant:           strings.TrimSpace(ctx.TenantID),
		PrincipalId:      strings.TrimSpace(ctx.PrincipalID),
		Labels:           clonePolicyEvalStringMap(ctx.Labels),
		MemoryId:         strings.TrimSpace(ctx.MemoryID),
		Meta:             jobMetadataFromEvaluationContext(ctx),
		InputContent:     []byte(ctx.Input.Content),
		InputContentType: strings.TrimSpace(ctx.Input.ContentType),
		InputSizeBytes:   inputSize,
	}
}

func jobMetadataFromEvaluationContext(ctx jobEvaluationContext) *pb.JobMetadata {
	return &pb.JobMetadata{
		TenantId:   strings.TrimSpace(ctx.TenantID),
		ActorId:    strings.TrimSpace(ctx.PrincipalID),
		ActorType:  pb.ActorType_ACTOR_TYPE_HUMAN,
		Capability: strings.TrimSpace(ctx.Capability),
		RiskTags:   append([]string{}, ctx.RiskTags...),
		Labels:     clonePolicyEvalStringMap(ctx.Labels),
	}
}

func (s *server) evaluateUnifiedEdgeRule(ctx context.Context, target resolvedPolicyEvaluationTarget, edgeCtx edgeEvaluationContext) (policyEvaluateResult, error) {
	adapted, err := edgecore.AdaptUnifiedEdgeRule(target.rule, edgecore.EdgeRuleAdapterOptions{Bundle: target.bundle})
	if err != nil {
		return policyEvaluateResult{}, newPolicyEvaluateError(policyEvaluateValidation, "adapt edge rule", err)
	}
	event := edgeEventFromEvaluationContext(edgeCtx)
	classification, err := edgecore.ClassifyEvent(event)
	if err != nil {
		return policyEvaluateResult{}, newPolicyEvaluateError(policyEvaluateValidation, "classify edge context", err)
	}
	legacyReq, err := edgecore.MapEventToPolicyCheckRequest(event, classification, edgecore.PolicyMappingOptions{ActorID: edgeCtx.PrincipalID})
	if err != nil {
		return policyEvaluateResult{}, newPolicyEvaluateError(policyEvaluateValidation, "map edge context", err)
	}
	decision := evaluateAdaptedEdgeRule(target, adapted.Rule, legacyReq)
	return policyEvaluateResult{Decision: decision}, nil
}
