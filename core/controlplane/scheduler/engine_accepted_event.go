package scheduler

import (
	"errors"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func (e *Engine) handleCompatJobResult(packet *pb.BusPacket, result *pb.JobResult) error {
	if err := e.handleJobResult(result); err != nil {
		return err
	}
	return e.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedResult, packet)
}

func (e *Engine) publishSchedulerAcceptedPacket(subject string, packet *pb.BusPacket) error {
	if e.bus == nil {
		return RetryAfter(errors.New("scheduler accepted-event bus unavailable"), retryDelayPublish)
	}
	if packet == nil {
		return errors.New("scheduler accepted event missing packet")
	}
	trusted := proto.Clone(packet).(*pb.BusPacket)
	trusted.SenderId, trusted.AuthToken = defaultSenderID, ""
	trusted.Signature, trusted.SignatureMetadata = nil, nil
	e.attachServiceToken(trusted)
	if err := e.bus.Publish(subject, trusted); err != nil {
		return RetryAfter(err, retryDelayPublish)
	}
	return nil
}
