package reqhash

// Pinning tests for the shared JobRequest canonicaliser. Ported from
// scheduler/job_hash_stale_request_test.go and
// store/job_store_canonical_hash_test.go so the shared implementation
// is held to both packages' contracts simultaneously.

import (
	"testing"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func baseRequest() *pb.JobRequest {
	return &pb.JobRequest{
		JobId:      "job-abc",
		Topic:      "job.default",
		ContextPtr: "ctx:job-abc",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "step-approve",
			"workflow_id": "wf-1",
		},
	}
}

func TestHash_StableAcrossProtojsonRoundtrip(t *testing.T) {
	t.Parallel()
	req := baseRequest()
	hashIn, err := Hash(req)
	if err != nil {
		t.Fatalf("hash in-memory: %v", err)
	}

	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(req)
	if err != nil {
		t.Fatalf("protojson marshal: %v", err)
	}
	roundtripped := &pb.JobRequest{}
	if err := protojson.Unmarshal(raw, roundtripped); err != nil {
		t.Fatalf("protojson unmarshal: %v", err)
	}
	hashOut, err := Hash(roundtripped)
	if err != nil {
		t.Fatalf("hash roundtripped: %v", err)
	}
	if hashIn != hashOut {
		t.Fatalf("protojson roundtrip must preserve hash — got %s vs %s", hashIn, hashOut)
	}
}

func TestHash_StableAcrossProtoUnknownFields(t *testing.T) {
	t.Parallel()
	req := baseRequest()
	hashPlain, err := Hash(req)
	if err != nil {
		t.Fatalf("hash plain: %v", err)
	}

	withUnknown, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	withUnknown.ProtoReflect().SetUnknown([]byte{0x78, 0x01})

	hashWithUnknown, err := Hash(withUnknown)
	if err != nil {
		t.Fatalf("hash with unknown: %v", err)
	}
	if hashPlain != hashWithUnknown {
		t.Fatalf("canonical hash must drop unknown fields — got %s vs %s", hashPlain, hashWithUnknown)
	}
}

func TestHash_IgnoresApprovalLabels(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := Hash(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	withApprovalLabels, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	withApprovalLabels.Labels["approval_granted"] = "true"
	withApprovalLabels.Labels["approval_reason"] = "looks safe"
	withApprovalLabels.Labels[bus.LabelBusMsgID] = "approval:job-abc"
	hashAfter, err := Hash(withApprovalLabels)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("approval labels must not affect hash — got %s vs %s", hashBase, hashAfter)
	}
}

func TestHash_IgnoresEffectiveConfigEnv(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := Hash(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.Env = map[string]string{
		config.EffectiveConfigEnvVar: `{"tenant":"default","effective":true}`,
	}
	hashAfter, err := Hash(mutated)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("EffectiveConfigEnvVar must not affect hash — got %s vs %s", hashBase, hashAfter)
	}
}

// TestHash_IgnoresPolicyAttachmentID pins that the platform-injected
// policy.attachment_id label does not perturb the canonical hash. The label is
// added during policy attachment — after the submit-time JobHash is pinned — so
// without stripping it the reconcile-time RequestHash drifts and the reconciler
// wrongly invalidates a still-pending approval as invalidate_stale_request.
func TestHash_IgnoresPolicyAttachmentID(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := Hash(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.Labels[policylabels.PolicyAttachmentID] = "job/job-abc:governance_evaluate@1/policy"
	hashAfter, err := Hash(mutated)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("policy.attachment_id must not affect hash — got %s vs %s", hashBase, hashAfter)
	}
}

// TestCanonical_StripsPolicyAttachmentID pins the strip as a Canonical API
// contract, mirroring TestCanonical_StripsApprovalLabels.
func TestCanonical_StripsPolicyAttachmentID(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	base.Labels[policylabels.PolicyAttachmentID] = "job/job-abc/policy"

	canon, err := Canonical(base)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if _, present := canon.Labels[policylabels.PolicyAttachmentID]; present {
		t.Fatal("Canonical must strip policy.attachment_id label")
	}
	if _, present := canon.Labels["run_id"]; !present {
		t.Fatal("Canonical must preserve non-injected labels")
	}
}

func TestHash_DetectsRealPayloadChange(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := Hash(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.ContextPtr = "ctx:malicious"
	hashAfter, err := Hash(mutated)
	if err != nil {
		t.Fatalf("hash mutated: %v", err)
	}
	if hashBase == hashAfter {
		t.Fatalf("payload change must produce a different hash — both were %s", hashBase)
	}
}

func TestHash_NilRejected(t *testing.T) {
	t.Parallel()
	if _, err := Hash(nil); err == nil {
		t.Fatal("Hash(nil) must return an error")
	}
	if _, err := Canonical(nil); err == nil {
		t.Fatal("Canonical(nil) must return an error")
	}
}

// TestHash_PreservesBusEnvChurn asserts that a request carrying both
// CustomVar (non-stripped) and EffectiveConfigEnvVar (stripped) has the
// same hash as the same request with only CustomVar present.
func TestHash_PreservesBusEnvChurn(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	base.Env = map[string]string{"CUSTOM_VAR": "keep-me"}
	hashBase, err := Hash(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.Env[config.EffectiveConfigEnvVar] = `{"tenant":"x"}`
	hashAfter, err := Hash(mutated)
	if err != nil {
		t.Fatalf("hash mutated: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("EffectiveConfigEnvVar must not perturb hash — got %s vs %s", hashBase, hashAfter)
	}
}

// TestCanonical_StripsApprovalLabels pins Canonical as an API contract,
// not just via Hash: callers that need the stripped proto for
// downstream comparison must see a clean result.
func TestCanonical_StripsApprovalLabels(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	base.Labels["approval_granted"] = "true"
	base.Labels[bus.LabelBusMsgID] = "approval:x"

	canon, err := Canonical(base)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if _, present := canon.Labels["approval_granted"]; present {
		t.Fatal("Canonical must strip approval_granted label")
	}
	if _, present := canon.Labels[bus.LabelBusMsgID]; present {
		t.Fatal("Canonical must strip bus.LabelBusMsgID label")
	}
	if _, present := canon.Labels["run_id"]; !present {
		t.Fatal("Canonical must preserve non-approval labels")
	}
}
