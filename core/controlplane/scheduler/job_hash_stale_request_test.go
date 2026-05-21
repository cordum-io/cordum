package scheduler

// Regression coverage for task-3527fdc5: single-step approval
// workflow was auto-invalidated as stale_request because the
// scheduler's submit-time hash (on the in-memory proto, which
// sometimes carried unknown fields) disagreed with the reconciler's
// later re-hash on the Redis-read form (protojson roundtrip drops
// unknowns).
//
// These tests pin the canonicalisation that now routes BOTH sides
// through a protojson roundtrip, so the hash is stable across:
//   - a fresh in-memory proto vs one read from Redis
//   - a proto carrying proto unknown fields vs the roundtripped form
//   - proto clones via proto.Clone (the path HashJobRequest uses)
//   - scheduler-injected env var churn (EffectiveConfigEnvVar)
//   - the approval flow's label churn (approval_* + bus.LabelBusMsgID)

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
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

func TestHashJobRequest_StableAcrossProtojsonRoundtrip(t *testing.T) {
	t.Parallel()
	req := baseRequest()
	hashIn, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash in-memory: %v", err)
	}

	// Simulate what SetJobRequest + GetJobRequest do in production.
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(req)
	if err != nil {
		t.Fatalf("protojson marshal: %v", err)
	}
	roundtripped := &pb.JobRequest{}
	if err := protojson.Unmarshal(raw, roundtripped); err != nil {
		t.Fatalf("protojson unmarshal: %v", err)
	}
	hashOut, err := HashJobRequest(roundtripped)
	if err != nil {
		t.Fatalf("hash roundtripped: %v", err)
	}
	if hashIn != hashOut {
		t.Fatalf("protojson roundtrip must preserve hash — got %s vs %s", hashIn, hashOut)
	}
}

func TestHashJobRequest_StableAcrossProtoUnknownFields(t *testing.T) {
	// The scheduler's in-memory proto can carry unknown fields when
	// a newer SDK sends a JobRequest with forward-compat fields we
	// don't know about yet. Redis JSON drops those. The canonical
	// hash must IGNORE unknowns so scheduler and reconciler agree.
	t.Parallel()
	req := baseRequest()

	// Hash WITHOUT unknown fields.
	hashPlain, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash plain: %v", err)
	}

	// Clone + splat a synthetic unknown field onto the proto.
	withUnknown, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	// Add bytes that look like a valid proto field (tag=9999 wiretype=2
	// length=5 data="hello"). Real workers never emit field 9999, but
	// the bytes are valid proto wire encoding that Marshal(Deterministic)
	// would include unless we strip it in canonicalisation.
	unknown := []byte{
		// varint tag for field 9999 wire-type 2 (length-delimited):
		// (9999 << 3) | 2 = 79994 = 0xfa, 0x70, 0x04 ... actually we
		// just inject a harmless but valid wire-format blob via
		// ProtoReflect().SetUnknown rather than hand-computing tags.
	}
	withUnknown.ProtoReflect().SetUnknown(unknown)
	// Force non-empty unknowns by appending arbitrary bytes. The
	// protoreflect API accepts any byte slice here; if it's not
	// valid proto, Marshal will still include it but the hash will
	// differ from the canonical form.
	withUnknown.ProtoReflect().SetUnknown([]byte{0x78, 0x01}) // field 15 varint=1

	hashWithUnknown, err := HashJobRequest(withUnknown)
	if err != nil {
		t.Fatalf("hash with unknown: %v", err)
	}
	if hashPlain != hashWithUnknown {
		t.Fatalf("canonical hash must drop unknown fields — got %s vs %s", hashPlain, hashWithUnknown)
	}
}

func TestHashJobRequest_IgnoresApprovalLabels(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := HashJobRequest(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	// Simulate POST /approve adding approval_* labels + LabelBusMsgID.
	withApprovalLabels, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	withApprovalLabels.Labels["approval_granted"] = "true"
	withApprovalLabels.Labels["approval_reason"] = "looks safe"
	withApprovalLabels.Labels[bus.LabelBusMsgID] = "approval:job-abc"
	hashAfter, err := HashJobRequest(withApprovalLabels)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("approval labels must not affect hash — got %s vs %s", hashBase, hashAfter)
	}
}

func TestHashJobRequest_IgnoresEffectiveConfigEnv(t *testing.T) {
	t.Parallel()
	base := baseRequest()
	hashBase, err := HashJobRequest(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	// Scheduler's attachEffectiveConfig mutation.
	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.Env = map[string]string{
		config.EffectiveConfigEnvVar: `{"tenant":"default","effective":true}`,
	}
	hashAfter, err := HashJobRequest(mutated)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if hashBase != hashAfter {
		t.Fatalf("EffectiveConfigEnvVar must not affect hash — got %s vs %s", hashBase, hashAfter)
	}
}

func TestHashJobRequest_DetectsRealPayloadChange(t *testing.T) {
	// Counterpart invariant: legitimate payload changes MUST produce
	// a different hash so the StaleRequest classifier still catches
	// actual tampering.
	t.Parallel()
	base := baseRequest()
	hashBase, err := HashJobRequest(base)
	if err != nil {
		t.Fatalf("hash base: %v", err)
	}

	mutated, ok := proto.Clone(base).(*pb.JobRequest)
	if !ok {
		t.Fatal("clone failed")
	}
	mutated.ContextPtr = "ctx:malicious"
	hashAfter, err := HashJobRequest(mutated)
	if err != nil {
		t.Fatalf("hash mutated: %v", err)
	}
	if hashBase == hashAfter {
		t.Fatalf("payload change must produce a different hash — both were %s", hashBase)
	}
}

func TestHashJobRequest_NilRejected(t *testing.T) {
	t.Parallel()
	_, err := HashJobRequest(nil)
	if err == nil {
		t.Fatal("HashJobRequest(nil) must return an error")
	}
}

// TestHashApprovalJobRequest_MatchesSchedulerHashJobRequest cross-binds
// the scheduler canonical hash (HashJobRequest, this package) against
// the store canonical hash (store.hashApprovalJobRequest, accessed
// indirectly via InspectApprovalRepair → snap.RequestHash). Both must
// produce the same hash for any logical JobRequest so the reconciler's
// drift detection matches the scheduler's submit-time hash.
//
// The test uses the production read path (miniredis + RedisJobStore +
// InspectApprovalRepair), so it pins the end-to-end invariant without
// requiring store.hashApprovalJobRequest to be exported. If someone
// drifts either side's canonicalisation (adds/removes a stripped
// field, removes the protojson roundtrip, etc.) the assertion fails.
//
// Scope: proto unknown fields are NOT exercised here because
// SetJobRequest persists via protojson which drops them; the store-side
// unit test TestHashApprovalJobRequest_IgnoresProtoUnknownFields covers
// that path directly. See task-fa783d7a.
func TestHashApprovalJobRequest_MatchesSchedulerHashJobRequest(t *testing.T) {
	t.Parallel()

	srv := miniredis.RunT(t)
	store, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// A realistic single-step approval payload: body + labels that
	// the canonicaliser keeps (run_id/step_id/workflow_id) AND labels
	// + env that the canonicaliser must strip (approval_*, LabelBusMsgID,
	// EffectiveConfigEnvVar).
	req := &pb.JobRequest{
		JobId:      "job-xcheck",
		Topic:      "job.approval-gate",
		TenantId:   "default",
		ContextPtr: "ctx:job-xcheck",
		Labels: map[string]string{
			"run_id":           "run-xcheck",
			"step_id":          "approve",
			"workflow_id":      "wf-xcheck",
			"approval_granted": "true",
			"approval_reason":  "looks safe",
			bus.LabelBusMsgID:  "approval:job-xcheck",
		},
		Env: map[string]string{
			config.EffectiveConfigEnvVar: `{"tenant":"default","effective":true}`,
			"CUSTOM_VAR":                 "keep-me",
		},
	}

	ctx := context.Background()
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}

	schedulerHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("scheduler HashJobRequest: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect approval repair: %v", err)
	}
	if snap.RequestHash == "" {
		t.Fatal("InspectApprovalRepair returned empty RequestHash")
	}

	if schedulerHash != snap.RequestHash {
		t.Fatalf("canonical hash mismatch across scheduler and store: scheduler=%s store=%s",
			schedulerHash, snap.RequestHash)
	}

	// Also cross-check that roundtripping the in-memory proto through
	// protojson (what SetJobRequest does) doesn't shift the hash —
	// the invariant that lets the reconciler re-hash the stored form
	// and still match the scheduler's submit-time hash.
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(req)
	if err != nil {
		t.Fatalf("protojson marshal: %v", err)
	}
	roundtripped := &pb.JobRequest{}
	if err := protojson.Unmarshal(raw, roundtripped); err != nil {
		t.Fatalf("protojson unmarshal: %v", err)
	}
	roundtrippedHash, err := HashJobRequest(roundtripped)
	if err != nil {
		t.Fatalf("HashJobRequest roundtripped: %v", err)
	}
	if roundtrippedHash != schedulerHash {
		t.Fatalf("scheduler hash not stable across protojson roundtrip: before=%s after=%s",
			schedulerHash, roundtrippedHash)
	}
}
