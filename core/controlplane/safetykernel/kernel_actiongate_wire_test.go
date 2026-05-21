package safetykernel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestWireActionGatePipeline_BootSetsPipeline asserts that the
// production wiring function installs a non-nil pipeline on a freshly
// constructed server. This is the regression guard for the QA-flagged
// "gates are dead code at runtime" bug: a future refactor that drops
// the wireActionGatePipeline call site (or returns early before
// SetActionGatePipeline runs) MUST break this test.
func TestWireActionGatePipeline_BootSetsPipeline(t *testing.T) {
	t.Parallel()
	srv := &server{}
	if err := wireActionGatePipeline(srv, nil); err != nil {
		t.Fatalf("wireActionGatePipeline returned err: %v", err)
	}
	if srv.actionGatePipeline == nil {
		t.Fatal("server.actionGatePipeline still nil after boot wiring")
	}
	if got := len(srv.actionGatePipeline.Gates()); got == 0 {
		t.Fatalf("pipeline has no gates after boot wiring; len=%d", got)
	}
	if srv.actionExtractor == nil {
		t.Fatal("server.actionExtractor still nil after boot wiring")
	}
	if srv.actionGateAuditSink == nil {
		t.Fatal("server.actionGateAuditSink still nil after boot wiring")
	}
}

// TestWireActionGatePipeline_NilServerNoOp asserts the wiring function
// guards against a nil receiver and returns cleanly (so a misconfigured
// test fixture cannot mask the wiring bug by panicking on a nil pointer).
func TestWireActionGatePipeline_NilServerNoOp(t *testing.T) {
	t.Parallel()
	if err := wireActionGatePipeline(nil, nil); err != nil {
		t.Fatalf("expected nil-server wire to no-op, got err: %v", err)
	}
}

// TestActionDescriptorFromRequest_RoundTrip asserts the kernel extractor
// reads the gateway-encoded JSON label and reconstructs the original
// ActionDescriptor. The encoding contract (Labels key, JSON marshaling,
// size cap) is what allows the descriptor to traverse the gateway→kernel
// gRPC boundary without a proto schema change.
func TestActionDescriptorFromRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	desc := &config.ActionDescriptor{
		Kind:       config.ActionKindMutation,
		Verb:       config.ActionVerbDelete,
		TargetPath: "/var/data/users.db",
		TargetResource: &config.ActionTargetResource{
			Type:        "user",
			ID:          "user_42",
			OwnerTenant: "tnt_a",
		},
		ApprovalClaim: &config.ActionApprovalClaim{
			ApprovalRef: "appr_42",
		},
	}
	encoded, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: string(encoded)},
	}
	got := actionDescriptorFromRequest(context.Background(), req)
	if got == nil {
		t.Fatal("extractor returned nil on well-formed payload")
	}
	if got.Kind != desc.Kind || got.Verb != desc.Verb || got.TargetPath != desc.TargetPath {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", got, desc)
	}
	if got.ApprovalClaim == nil || got.ApprovalClaim.ApprovalRef != "appr_42" {
		t.Fatalf("approval claim not preserved across encoding: %+v", got.ApprovalClaim)
	}
	if got.TargetResource == nil || got.TargetResource.OwnerTenant != "tnt_a" {
		t.Fatalf("target resource not preserved across encoding: %+v", got.TargetResource)
	}
}

func TestActionDescriptorFromRequest_AbsentLabelReturnsNil(t *testing.T) {
	t.Parallel()
	if got := actionDescriptorFromRequest(context.Background(), &pb.PolicyCheckRequest{}); got != nil {
		t.Fatalf("expected nil for missing label, got %+v", got)
	}
	if got := actionDescriptorFromRequest(context.Background(), nil); got != nil {
		t.Fatalf("expected nil for nil request, got %+v", got)
	}
}

func TestActionDescriptorFromRequest_MalformedLabelReturnsNil(t *testing.T) {
	t.Parallel()
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: "{not valid json"},
	}
	if got := actionDescriptorFromRequest(context.Background(), req); got != nil {
		t.Fatalf("malformed JSON should drop to nil to avoid bypass; got %+v", got)
	}
}

func TestActionDescriptorFromRequest_OversizeLabelReturnsNil(t *testing.T) {
	t.Parallel()
	huge := make([]byte, config.ActionArgsMaxSerializedBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: string(huge)},
	}
	if got := actionDescriptorFromRequest(context.Background(), req); got != nil {
		t.Fatalf("oversize payload should drop to nil; got %+v", got)
	}
}
