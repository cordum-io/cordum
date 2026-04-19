package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// HashJobRequest computes a deterministic hash of a job request,
// excluding mutable approval labels and scheduler-injected env vars.
//
// The hash must be STABLE across the roundtrip the request performs
// through Redis: gateway writes via protojson.Marshal, reconciler
// reads via protojson.Unmarshal. That roundtrip drops proto unknown
// fields. We therefore canonicalise here through the same roundtrip
// so the scheduler's submit-time hash equals the reconciler's later
// re-hash on the same logical request.
//
// Without this normalisation the scheduler hashes an in-memory proto
// that may carry unknown fields (e.g. forward-compat fields from a
// newer SDK worker) and writes the hash into the safety record;
// later, the reconciler reads the Redis-stored JSON (no unknowns),
// re-hashes, and sees a phantom mismatch that the classifier
// interprets as StaleRequest → auto-DENY on a brand-new approval.
// This bug regressed a single-step approval workflow on a fresh
// deployment (task-3527fdc5): run stayed in waiting because the
// reconciler auto-repaired the approval to DENIED before the worker
// could pick it up.
func HashJobRequest(req *pb.JobRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("job request required")
	}
	clone, err := canonicalJobRequest(req)
	if err != nil {
		return "", err
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJobRequest returns a copy of req with mutable fields
// stripped (approval labels, scheduler-injected env vars) AND with
// any proto unknown fields removed via a protojson roundtrip. The
// protojson roundtrip matches what SetJobRequest persists into Redis,
// so hashing the canonical form gives the same answer regardless of
// whether the caller holds the original in-memory proto or a
// Redis-read form.
func canonicalJobRequest(req *pb.JobRequest) (*pb.JobRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("job request required")
	}
	clone, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || clone == nil {
		return nil, fmt.Errorf("job request clone failed")
	}
	if clone.Labels != nil {
		for key := range clone.Labels {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "approval_") || key == bus.LabelBusMsgID {
				delete(clone.Labels, key)
			}
		}
		if len(clone.Labels) == 0 {
			clone.Labels = nil
		}
	}
	if clone.Env != nil {
		delete(clone.Env, config.EffectiveConfigEnvVar)
		if len(clone.Env) == 0 {
			clone.Env = nil
		}
	}
	// Round-trip through protojson so unknown fields are dropped and
	// the hash matches what SetJobRequest persists into Redis.
	marshalOpts := protojson.MarshalOptions{EmitUnpopulated: true}
	raw, err := marshalOpts.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("canonical job request marshal: %w", err)
	}
	out := &pb.JobRequest{}
	unmarshalOpts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshalOpts.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("canonical job request unmarshal: %w", err)
	}
	return out, nil
}
