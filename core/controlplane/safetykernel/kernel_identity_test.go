package safetykernel

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestEvaluateRejectsIdentityMismatchBeforeDecisionCache(t *testing.T) {
	srv := &server{cacheTTL: time.Minute, cache: map[string]cacheEntry{}}
	if err := srv.setPolicy(context.Background(), &config.SafetyPolicy{
		DefaultDecision: "allow",
	}, "identity-snapshot"); err != nil {
		t.Fatalf("setPolicy() error = %v", err)
	}
	req := &pb.PolicyCheckRequest{
		JobId: "job-1", Topic: "job.test", Tenant: "tenant-attacker",
		Identity: &pb.IdentityBinding{
			TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
		},
	}
	before := proto.Clone(req)
	srv.setCachedDecision(cacheKeyForRequest(req, "identity-snapshot"), &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})

	resp, err := srv.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY", resp.GetDecision())
	}
	if resp.GetReason() != "identity validation failed" {
		t.Fatalf("reason = %q, want bounded identity failure", resp.GetReason())
	}
	if !proto.Equal(req, before) {
		t.Fatal("Evaluate() mutated rejected request")
	}
}
