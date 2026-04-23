package store

// Regression coverage for task-fa783d7a: the store-side canonical
// hash (hashApprovalJobRequest) must drop proto unknown fields just
// like the scheduler-side canonicaliser (scheduler.HashJobRequest)
// does, so that an in-memory JobRequest carrying forward-compat
// unknown fields from a newer SDK doesn't produce a different hash
// from the Redis-read form the reconciler later recomputes.
//
// Context: SetJobRequest persists the request via protojson, which
// drops unknowns. In practice the reconciler reads from Redis so it
// only ever sees the roundtripped form. The risk lives in any future
// caller that hashes the in-memory proto directly (e.g. a new
// code-path that mirrors what the scheduler does), where the hashes
// would silently diverge. This test pins the invariant.

import (
	"testing"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func canonicalHashBase() *pb.JobRequest {
	return &pb.JobRequest{
		JobId:      "job-canon",
		Topic:      "job.approval-gate",
		TenantId:   "default",
		ContextPtr: "ctx:job-canon",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "approve",
			"workflow_id": "wf-1",
		},
	}
}

func TestHashApprovalJobRequest_IgnoresProtoUnknownFields(t *testing.T) {
	t.Parallel()

	plain := canonicalHashBase()
	hashPlain, err := hashApprovalJobRequest(plain)
	if err != nil {
		t.Fatalf("hash plain: %v", err)
	}

	withUnknown, ok := proto.Clone(plain).(*pb.JobRequest)
	if !ok || withUnknown == nil {
		t.Fatal("proto.Clone failed")
	}
	// Field 15, wire-type varint, value=1. Valid proto wire bytes that
	// Marshal(Deterministic) would otherwise include in the hash.
	withUnknown.ProtoReflect().SetUnknown([]byte{0x78, 0x01})

	hashWithUnknown, err := hashApprovalJobRequest(withUnknown)
	if err != nil {
		t.Fatalf("hash with unknown: %v", err)
	}
	if hashPlain != hashWithUnknown {
		t.Fatalf("canonical hash must drop unknown fields — got %s vs %s",
			hashPlain, hashWithUnknown)
	}
}

func TestHashApprovalJobRequest_IgnoresApprovalLabelsAndConfigEnv(t *testing.T) {
	t.Parallel()

	base := canonicalHashBase()
	hashBase, err := hashApprovalJobRequest(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok || mutated == nil {
		t.Fatal("proto.Clone failed")
	}
	mutated.Labels["approval_granted"] = "true"
	mutated.Labels["approval_reason"] = "looks safe"
	mutated.Labels[bus.LabelBusMsgID] = "approval:job-canon"
	mutated.Env = map[string]string{
		config.EffectiveConfigEnvVar: `{"tenant":"default","effective":true}`,
	}

	hashMutated, err := hashApprovalJobRequest(mutated)
	if err != nil {
		t.Fatalf("hash mutated: %v", err)
	}
	if hashBase != hashMutated {
		t.Fatalf("approval labels + EffectiveConfigEnvVar must not affect hash — got %s vs %s",
			hashBase, hashMutated)
	}
}

func TestHashApprovalJobRequest_DetectsRealPayloadChange(t *testing.T) {
	// Counterpart invariant: the canonicaliser must still catch
	// payload tampering. Flip a field that ISN'T stripped.
	t.Parallel()

	base := canonicalHashBase()
	hashBase, err := hashApprovalJobRequest(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok || mutated == nil {
		t.Fatal("proto.Clone failed")
	}
	mutated.ContextPtr = "ctx:malicious"

	hashMutated, err := hashApprovalJobRequest(mutated)
	if err != nil {
		t.Fatalf("hash mutated: %v", err)
	}
	if hashBase == hashMutated {
		t.Fatalf("real payload change must alter hash — both were %s", hashBase)
	}
}
