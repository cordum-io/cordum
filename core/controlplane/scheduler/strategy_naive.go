package scheduler

import (
	"errors"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// NaiveStrategy forwards jobs directly to the requested topic.
type NaiveStrategy struct{}

func NewNaiveStrategy() *NaiveStrategy {
	return &NaiveStrategy{}
}

func (s *NaiveStrategy) PickSubject(req *pb.JobRequest, _ map[string]*pb.Heartbeat) (string, error) {
	if req == nil || req.Topic == "" {
		return "", errors.New("missing topic")
	}
	return req.Topic, nil
}
