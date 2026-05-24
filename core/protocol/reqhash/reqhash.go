// Package reqhash is the single source of truth for JobRequest
// canonicalisation used by the scheduler, gateway, and infra/store
// packages. It strips mutable approval labels and scheduler-injected
// env vars, then rounds the proto through protojson so that hashing
// the in-memory form matches hashing the Redis-read form on the same
// logical request. Without a shared helper the three packages had
// independent copies that were required to stay byte-equivalent by
// convention; see task-fa783d7a for the divergence that motivated
// task-090ab6af.
package reqhash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/protocol/protoutil"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Canonical returns a copy of req with mutable fields stripped
// (approval_* labels, bus.LabelBusMsgID, policylabels.PolicyAttachmentID,
// config.EffectiveConfigEnvVar) and proto unknown fields removed via a
// protojson round-trip. The
// round-trip matches what the store persists into Redis, so the
// canonical form is stable regardless of whether the caller holds the
// original in-memory proto or a Redis-read form.
func Canonical(req *pb.JobRequest) (*pb.JobRequest, error) {
	clone, err := protoutil.CloneJobRequest(req)
	if err != nil {
		return nil, err
	}
	if clone.Labels != nil {
		for key := range clone.Labels {
			lower := strings.ToLower(key)
			// policy.attachment_id is injected during policy attachment (after the
			// submit-time hash is pinned), so it must be stripped here or the
			// reconcile-time RequestHash drifts from the pinned JobHash and a
			// still-pending approval is wrongly invalidated as stale_request.
			if strings.HasPrefix(lower, "approval_") || key == bus.LabelBusMsgID || key == policylabels.PolicyAttachmentID {
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
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("canonical job request marshal: %w", err)
	}
	out := &pb.JobRequest{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("canonical job request unmarshal: %w", err)
	}
	return out, nil
}

// Hash computes the deterministic canonical hash of a JobRequest as a
// hex-encoded sha256. Equal logical requests produce equal hashes
// across the protojson round-trip the store performs, across mutable
// approval-label churn, and across scheduler env-var injection.
func Hash(req *pb.JobRequest) (string, error) {
	canonical, err := Canonical(req)
	if err != nil {
		return "", err
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
