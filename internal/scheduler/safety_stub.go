package scheduler

import pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"

// SafetyStub performs a minimal check, blocking only dangerous topics.
type SafetyStub struct{}

func NewSafetyStub() *SafetyStub {
	return &SafetyStub{}
}

func (s *SafetyStub) Check(req *pb.JobRequest) (SafetyDecision, string) {
	if req == nil {
		return SafetyDeny, "nil job request"
	}
	if req.Topic == "sys.destroy" {
		return SafetyDeny, "forbidden topic"
	}
	return SafetyAllow, ""
}
