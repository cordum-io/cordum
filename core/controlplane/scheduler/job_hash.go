package scheduler

import (
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/protocol/reqhash"
)

// HashJobRequest forwards to reqhash.Hash, the repo's single
// canonicaliser for JobRequest. Kept as a thin wrapper so existing
// callers in this package (and the approvals_test.go suite in the
// gateway package which references the `scheduler.HashJobRequest`
// symbol) don't need to change their imports. The canonicalisation
// semantics — strip approval_* labels + bus.LabelBusMsgID +
// config.EffectiveConfigEnvVar, round through protojson, sha256 hex —
// live in reqhash (see task-090ab6af).
func HashJobRequest(req *pb.JobRequest) (string, error) {
	return reqhash.Hash(req)
}
