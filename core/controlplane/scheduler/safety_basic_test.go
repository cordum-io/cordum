package scheduler

import pb "github.com/cordum/cordum/core/protocol/pb/v1"

// SafetyBasic performs a minimal safety check for tests.
type SafetyBasic struct{}

func NewSafetyBasic() *SafetyBasic {
	return &SafetyBasic{}
}

func (s *SafetyBasic) Check(req *pb.JobRequest) (SafetyDecisionRecord, error) {
	if req == nil {
		return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "nil job request"}, nil
	}
	if req.Topic == "sys.destroy" {
		return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "forbidden topic"}, nil
	}
	return SafetyDecisionRecord{Decision: SafetyAllow}, nil
}
