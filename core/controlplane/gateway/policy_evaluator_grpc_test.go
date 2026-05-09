package gateway

import (
	"context"
	"net"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const policyEvaluatorBufSize = 1024 * 1024

func TestPolicyEvaluatorGRPCEvaluateUnifiedMatchesHTTPJobDispatch(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newPolicyEvaluatorAuthProvider(t)
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_DENY,
		Reason:   "grpc job denied",
		RuleId:   "grpc-job-rule",
	})
	client, cleanup := newPolicyEvaluatorGRPCClient(t, s)
	defer cleanup()

	resp, err := client.EvaluateUnified(policyEvaluatorAuthContext(), &pb.PolicyEvaluateRequest{
		Rule:       grpcPolicyRule(t, "grpc-job-rule", pb.RuleType_RULE_TYPE_INPUT),
		JobContext: grpcJobContext(),
	})

	require.NoError(t, err)
	require.NotNil(t, resp.GetDecision())
	require.Equal(t, pb.DecisionSource_DECISION_SOURCE_JOB, resp.GetDecision().GetSource())
	require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, resp.GetDecision().GetType())
	require.Equal(t, "grpc-job-rule", resp.GetDecision().GetRuleId())
}

func TestPolicyEvaluatorGRPCEvaluateUnifiedRejectsEdgeRuleWithJobContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newPolicyEvaluatorAuthProvider(t)
	client, cleanup := newPolicyEvaluatorGRPCClient(t, s)
	defer cleanup()

	_, err := client.EvaluateUnified(policyEvaluatorAuthContext(), &pb.PolicyEvaluateRequest{
		Rule:       grpcPolicyRule(t, "grpc-edge-rule", pb.RuleType_RULE_TYPE_EDGE),
		JobContext: grpcJobContext(),
	})

	require.ErrorContains(t, err, "rule type edge requires edge_context")
}

func newPolicyEvaluatorGRPCClient(t *testing.T, s *server) (pb.PolicyEvaluatorClient, func()) {
	t.Helper()
	listener := bufconn.Listen(policyEvaluatorBufSize)
	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(apiKeyUnaryInterceptor(s.auth)))
	pb.RegisterPolicyEvaluatorServer(grpcServer, s)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	conn, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithInsecure(),
	)
	require.NoError(t, err)
	cleanup := func() {
		_ = conn.Close()
		grpcServer.Stop()
		_ = listener.Close()
	}
	return pb.NewPolicyEvaluatorClient(conn), cleanup
}

func policyEvaluatorAuthContext() context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"x-api-key", "policy-evaluator-test-key",
		"x-principal-id", "alice",
	))
}

func newPolicyEvaluatorAuthProvider(t *testing.T) *auth.BasicAuthProvider {
	t.Helper()
	return newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"policy-evaluator-test-key","role":"admin","principal_id":"alice","tenant":"tenant-acme"}]`,
	})
}

func grpcJobContext() *pb.JobEvaluationContext {
	return &pb.JobEvaluationContext{
		TenantId:         "tenant-acme",
		JobId:            "job-grpc",
		WorkflowId:       "wf-grpc",
		Topic:            "job.acme.evaluate",
		PrincipalId:      "alice",
		InputContent:     []byte("contains blocked-token"),
		InputContentType: "text/plain",
	}
}

func grpcPolicyRule(t *testing.T, id string, ruleType pb.RuleType) *pb.Rule {
	t.Helper()
	match, err := structpb.NewStruct(map[string]any{
		"tenants":       []any{"tenant-acme"},
		"topics":        []any{"job.acme.evaluate"},
		"keywords":      []any{"blocked-token"},
		"content_types": []any{"text/plain"},
	})
	require.NoError(t, err)
	decide, err := structpb.NewStruct(map[string]any{
		"decision": "deny",
		"reason":   "grpc job denied",
	})
	require.NoError(t, err)
	return &pb.Rule{
		Id:      id,
		Name:    "gRPC rule",
		Type:    ruleType,
		Scope:   &pb.RuleScope{Kind: pb.RuleScopeKind_RULE_SCOPE_KIND_TENANT, Value: "tenant-acme"},
		Status:  pb.RuleStatus_RULE_STATUS_PUBLISHED,
		Version: "v1",
		Audit:   &pb.AuditMetadata{CreatedAt: timestamppb.Now(), CreatedBy: "alice"},
		Match:   match,
		Decide:  decide,
	}
}

var _ = policy.DecisionSourceJob
