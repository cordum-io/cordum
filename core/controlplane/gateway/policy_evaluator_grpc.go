package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) EvaluateUnified(ctx context.Context, req *pb.PolicyEvaluateRequest) (*pb.PolicyEvaluateResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := s.requireRoleGRPC(ctx, "admin", "operator"); err != nil {
		return nil, err
	}
	evalReq, err := policyEvaluationRequestFromProto(req)
	if err != nil {
		return nil, policyEvaluateGRPCError(err)
	}
	if err := s.authorizePolicyEvaluateGRPC(ctx, &evalReq); err != nil {
		return nil, err
	}
	result, err := s.evaluateUnifiedPolicy(ctx, evalReq)
	if err != nil {
		return nil, policyEvaluateGRPCError(err)
	}
	return &pb.PolicyEvaluateResponse{Decision: policyDecisionToProto(result.Decision)}, nil
}

func policyEvaluationRequestFromProto(req *pb.PolicyEvaluateRequest) (policyEvaluationRequest, error) {
	rule, err := policyRuleFromProto(req.GetRule())
	if err != nil {
		return policyEvaluationRequest{}, err
	}
	return policyEvaluationRequest{
		Rule:        rule,
		BundleID:    strings.TrimSpace(req.GetBundleId()),
		Scope:       policyRuleScopeFromProto(req.GetScope()),
		JobContext:  jobEvaluationContextFromProto(req.GetJobContext()),
		EdgeContext: edgeEvaluationContextFromProto(req.GetEdgeContext()),
	}, nil
}

func policyRuleFromProto(rule *pb.Rule) (*policy.Rule, error) {
	if rule == nil {
		return nil, nil
	}
	match, err := structRawMessage(rule.GetMatch())
	if err != nil {
		return nil, newPolicyEvaluateError(policyEvaluateValidation, "invalid rule match", err)
	}
	decide, err := structRawMessage(rule.GetDecide())
	if err != nil {
		return nil, newPolicyEvaluateError(policyEvaluateValidation, "invalid rule decide", err)
	}
	return &policy.Rule{
		ID:          strings.TrimSpace(rule.GetId()),
		Name:        strings.TrimSpace(rule.GetName()),
		Type:        policyRuleTypeFromProto(rule.GetType()),
		Scope:       valuePolicyRuleScopeFromProto(rule.GetScope()),
		Status:      policyRuleStatusFromProto(rule.GetStatus()),
		Version:     strings.TrimSpace(rule.GetVersion()),
		Audit:       policyAuditMetadataFromProto(rule.GetAudit()),
		Match:       match,
		Decide:      decide,
		Description: strings.TrimSpace(rule.GetDescription()),
	}, nil
}

func (s *server) authorizePolicyEvaluateGRPC(ctx context.Context, req *policyEvaluationRequest) error {
	if req.JobContext != nil {
		tenant, err := s.resolvePolicyEvaluateGRPCTenant(ctx, req.JobContext.TenantID)
		if err != nil {
			return err
		}
		req.JobContext.TenantID = tenant
		req.JobContext.PrincipalID = resolvePolicyEvaluateGRPCPrincipal(ctx, req.JobContext.PrincipalID)
	}
	if req.EdgeContext != nil {
		tenant, err := s.resolvePolicyEvaluateGRPCTenant(ctx, req.EdgeContext.TenantID)
		if err != nil {
			return err
		}
		req.EdgeContext.TenantID = tenant
		req.EdgeContext.PrincipalID = resolvePolicyEvaluateGRPCPrincipal(ctx, req.EdgeContext.PrincipalID)
	}
	return nil
}

func (s *server) resolvePolicyEvaluateGRPCTenant(ctx context.Context, requested string) (string, error) {
	if auth.FromContext(ctx) != nil {
		return resolveGRPCTenant(ctx, requested, s.tenant)
	}
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed, nil
	}
	return strings.TrimSpace(s.tenant), nil
}

func resolvePolicyEvaluateGRPCPrincipal(ctx context.Context, requested string) string {
	if authCtx := auth.FromContext(ctx); authCtx != nil && authCtx.PrincipalID != "" {
		return authCtx.PrincipalID
	}
	return strings.TrimSpace(requested)
}

func policyEvaluateGRPCError(err error) error {
	var evalErr *policyEvaluateError
	if !errors.As(err, &evalErr) {
		return status.Error(codes.Internal, "internal error")
	}
	switch evalErr.Kind {
	case policyEvaluateValidation:
		return status.Error(codes.InvalidArgument, policyEvaluateErrorMessage(evalErr))
	case policyEvaluateNotFound:
		return status.Error(codes.NotFound, policyEvaluateErrorMessage(evalErr))
	case policyEvaluateUnavailable, policyEvaluateUpstream:
		return status.Error(codes.Unavailable, policyEvaluateErrorMessage(evalErr))
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
