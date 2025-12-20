package scheduler

import pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"

// SafetyBasic performs a minimal safety check for tests.
type SafetyBasic struct{}

func NewSafetyBasic() *SafetyBasic {
	return &SafetyBasic{}
}

func (s *SafetyBasic) Check(req *pb.JobRequest) (SafetyDecision, string) {
	if req == nil {
		return SafetyDeny, "nil job request"
	}
	if req.Topic == "sys.destroy" {
		return SafetyDeny, "forbidden topic"
	}
	return SafetyAllow, ""
}

