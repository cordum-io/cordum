package safetykernel

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDecisionCacheDoesNotServeAllowForUnverifiedReference(t *testing.T) {
	srv := newContentCacheServer(t, "deny")
	req := referencedPolicyRequest([]byte("trusted"), false)
	key := cacheKeyForRequest(req, "content-snapshot")
	srv.setCachedDecision(key, &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW})

	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY instead of cached ALLOW", resp.GetDecision())
	}
}

func TestDecisionCacheDoesNotStoreAllowForUnverifiedReference(t *testing.T) {
	srv := newContentCacheServer(t, "allow")
	req := referencedPolicyRequest([]byte("trusted"), false)

	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("evaluate = %#v, %v", resp, err)
	}
	if got := cacheSize(srv); got != 0 {
		t.Fatalf("unverified referenced ALLOW entered cache; entries=%d", got)
	}
}

func TestDecisionCacheDoesNotStoreAllowForOmittedSensitiveContent(t *testing.T) {
	srv := newContentCacheServer(t, "allow")
	req := &pb.PolicyCheckRequest{
		JobId: "job-a", Topic: "job.test", Tenant: "tenant-a",
	}

	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("evaluate = %#v, %v", resp, err)
	}
	if got := cacheSize(srv); got != 0 {
		t.Fatalf("omitted content-sensitive ALLOW entered cache; entries=%d", got)
	}
}

func TestDecisionCacheStoresAllowForIntegrityVerifiedReference(t *testing.T) {
	srv := newContentCacheServer(t, "allow")
	req := referencedPolicyRequest([]byte("trusted"), true)

	resp, err := srv.evaluate(context.Background(), req, "check")
	if err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("evaluate = %#v, %v", resp, err)
	}
	if got := cacheSize(srv); got != 1 {
		t.Fatalf("verified referenced ALLOW cache entries=%d, want 1", got)
	}
}

func TestReferencedInputVerifiedRejectsIntegrityDrift(t *testing.T) {
	tests := map[string]func(*pb.PolicyCheckRequest){
		"digest": func(req *pb.PolicyCheckRequest) { req.InputRef.Sha256[0] ^= 0xff },
		"size":   func(req *pb.PolicyCheckRequest) { req.InputRef.SizeBytes++ },
		"type":   func(req *pb.PolicyCheckRequest) { req.InputContentType = "application/json" },
		"expiry": func(req *pb.PolicyCheckRequest) {
			req.InputRef.ExpiresAt = timestamppb.New(time.Now().Add(-time.Minute))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := referencedPolicyRequest([]byte("trusted"), true)
			mutate(req)
			if referencedInputVerified(req, time.Now()) {
				t.Fatalf("accepted referenced input with %s drift", name)
			}
		})
	}
}

func newContentCacheServer(t *testing.T, decision string) *server {
	t.Helper()
	policy := &config.SafetyPolicy{
		DefaultTenant: "tenant-a", DefaultDecision: decision,
		InputRules: []config.InputPolicyRule{{
			ID: "inspect-content", Decision: "deny",
			Match: config.InputPolicyMatch{Topics: []string{"job.test"}, Keywords: []string{"blocked"}},
		}},
	}
	srv := &server{cacheTTL: time.Minute, cache: map[string]cacheEntry{}, cacheMaxSize: 100}
	if err := srv.setPolicy(context.Background(), policy, "content-snapshot"); err != nil {
		t.Fatalf("setPolicy: %v", err)
	}
	return srv
}

func referencedPolicyRequest(content []byte, verified bool) *pb.PolicyCheckRequest {
	digest := sha256.Sum256(content)
	ref := &agentv1.ResourceRef{
		ResolverId: "cache", Uri: "blob:job-a", Sha256: digest[:], SizeBytes: uint64(len(content)),
		MediaType: "text/plain", ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)), Purpose: "job.input",
	}
	req := &pb.PolicyCheckRequest{
		JobId: "job-a", Topic: "job.test", Tenant: "tenant-a", InputRef: ref,
		InputContentType: "text/plain",
	}
	if verified {
		req.InputContent = append([]byte(nil), content...)
		req.InputSizeBytes = int64(len(content))
	}
	return req
}
