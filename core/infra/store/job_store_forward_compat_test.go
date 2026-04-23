package store

// Regression test for task-090ab6af: every protojson.Unmarshal in
// job_store.go that decodes a JobRequest must pass
// UnmarshalOptions{DiscardUnknown: true} so a forward-compat proto
// field from a newer SDK never survives the round-trip. Without this
// the reconciler's drift detection could trip ApprovalConflictStaleRequest
// on a benign approval during a mixed-version rollout.

import (
	"context"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/protocol/reqhash"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TestGetJobRequest_DiscardsUnknownJSONFields constructs a JobRequest,
// serialises it with protojson, splices an unknown JSON key into the
// serialised form, writes the resulting bytes directly to Redis under
// the JobRequest key, and reads back via GetJobRequest. The returned
// proto MUST NOT carry the synthetic unknown field, and a fresh
// proto.Marshal of it must match the marshal of the original clean
// request.
func TestGetJobRequest_DiscardsUnknownJSONFields(t *testing.T) {
	t.Parallel()

	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	clean := &pb.JobRequest{
		JobId:      "job-forward-compat",
		Topic:      "job.default",
		TenantId:   "default",
		ContextPtr: "ctx:job-forward-compat",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "step-1",
			"workflow_id": "wf-1",
		},
	}

	ctx := context.Background()
	if err := store.SetJobRequest(ctx, clean); err != nil {
		t.Fatalf("SetJobRequest: %v", err)
	}

	// Re-read the JSON blob from Redis, splice an unknown field into the
	// object's top level, and write it back under the same key. The
	// unknown field simulates a forward-compat field from a newer SDK
	// that the current proto schema doesn't know about.
	key := jobRequestKeyPrefix + clean.JobId
	raw, err := srv.Get(key)
	if err != nil {
		t.Fatalf("miniredis get %q: %v", key, err)
	}

	var asMap map[string]any
	if err := json.Unmarshal([]byte(raw), &asMap); err != nil {
		t.Fatalf("json.Unmarshal stored blob: %v", err)
	}
	asMap["future_sdk_field_that_does_not_exist_yet"] = "hello"
	asMap["another_unknown"] = map[string]any{"nested": true, "count": 42}

	injected, err := json.Marshal(asMap)
	if err != nil {
		t.Fatalf("re-marshal with unknown field: %v", err)
	}
	if err := srv.Set(key, string(injected)); err != nil {
		t.Fatalf("miniredis set %q: %v", key, err)
	}

	// Sanity: confirm a bare protojson.Unmarshal WOULD have carried the
	// unknown field through (this is the behaviour we're guarding
	// against). Without DiscardUnknown, protojson.Unmarshal returns an
	// error on unknown fields instead of silently accepting them; the
	// store's old behaviour was to propagate that error to the caller
	// as "unmarshal job request", which breaks on any forward-compat
	// field. Verify both branches here: bare Unmarshal rejects; our
	// fixed path accepts.
	var viaBare pb.JobRequest
	if err := protojson.Unmarshal(injected, &viaBare); err == nil {
		t.Fatal("bare protojson.Unmarshal was expected to reject unknown fields; regression test premise is invalid")
	}

	got, err := store.GetJobRequest(ctx, clean.JobId)
	if err != nil {
		t.Fatalf("GetJobRequest (with unknown fields stored): %v", err)
	}

	// The canonical hash of the returned request must match the canonical
	// hash of the original clean request — that's the real invariant the
	// fix protects.
	hGot, err := reqhash.Hash(got)
	if err != nil {
		t.Fatalf("reqhash.Hash(got): %v", err)
	}
	hClean, err := reqhash.Hash(clean)
	if err != nil {
		t.Fatalf("reqhash.Hash(clean): %v", err)
	}
	if hGot != hClean {
		t.Fatalf("unknown-field injection changed the canonical hash: got=%s clean=%s", hGot, hClean)
	}

	// And a deterministic proto marshal of the returned request must
	// equal the deterministic marshal of the clean request — pins that
	// no unknown bytes leaked into the in-memory proto.
	gotBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(got)
	if err != nil {
		t.Fatalf("proto.Marshal(got): %v", err)
	}
	cleanBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(clean)
	if err != nil {
		t.Fatalf("proto.Marshal(clean): %v", err)
	}
	if string(gotBytes) != string(cleanBytes) {
		t.Fatalf("deterministic proto marshal differs after unknown-field roundtrip: len(got)=%d len(clean)=%d", len(gotBytes), len(cleanBytes))
	}
}
