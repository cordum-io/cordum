package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// HashJobRequest computes a deterministic hash of a job request, excluding mutable approval labels.
func HashJobRequest(req *pb.JobRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("job request required")
	}
	clone, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || clone == nil {
		return "", fmt.Errorf("job request clone failed")
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
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
