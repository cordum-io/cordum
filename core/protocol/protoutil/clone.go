// Package protoutil holds small safe wrappers around google.golang.org/protobuf
// primitives that are easy to misuse. Each helper exists to consolidate an
// idiom whose inline form had drifted or had latent correctness gaps.
//
// See task-625b2ed1 for the original motivation (the drifted proto.Clone
// call site at core/controlplane/scheduler/saga.go that was missing the
// ok-check every sibling site already had).
package protoutil

import (
	"fmt"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// CloneJobRequest deep-clones a JobRequest via proto.Clone with a typed
// ok check and nil guard. Returns (nil, error) when the input is nil or
// the type assertion fails.
//
// The type assertion failure is vanishingly rare in practice — proto.Clone
// is specified to return the same concrete type as its input — but the
// silent nil-deref it allows when the assertion is skipped is a real
// crash path (e.g. memory-pressure corruption, a protobuf-library upgrade
// that changes generation). Callers that need a mutable clone MUST use
// this helper instead of a bare proto.Clone(x).(*pb.JobRequest), so the
// guard pattern cannot drift out of any one site again.
func CloneJobRequest(req *pb.JobRequest) (*pb.JobRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("protoutil: nil JobRequest")
	}
	cloned := proto.Clone(req)
	clone, ok := cloned.(*pb.JobRequest)
	if !ok || clone == nil {
		return nil, fmt.Errorf("protoutil: proto.Clone returned %T, want *pb.JobRequest", cloned)
	}
	return clone, nil
}
