package scheduler

import (
	"errors"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (e *Engine) handleCompatJobResult(packet *pb.BusPacket, result *pb.JobResult) error {
	if err := e.handleJobResult(result); err != nil {
		return err
	}
	return e.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedResult, packet)
}

// publishSchedulerResult routes scheduler-originated outcomes directly to the
// trusted accepted stream in production. Sending them through the worker
// result subject would incorrectly require a dispatch fence that these local
// control-plane outcomes do not have.
func (e *Engine) publishSchedulerResult(packet *pb.BusPacket) error {
	if e == nil {
		return errors.New("scheduler result engine unavailable")
	}
	if e.productionIdentity.Load() {
		return e.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedResult, packet)
	}
	if e.bus == nil {
		return nil
	}
	e.attachServiceToken(packet)
	if err := e.bus.Publish(capsdk.SubjectResult, packet); err != nil {
		return RetryAfter(err, retryDelayPublish)
	}
	return nil
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
	trusted.CreatedAt, trusted.ProtocolVersion = timestamppb.Now(), protocolVersionV1
	if result := trusted.GetJobResult(); result != nil {
		result.WorkerId = defaultSenderID
	}
	trusted.Signature, trusted.SignatureMetadata = nil, nil
	e.attachServiceToken(trusted)
	if err := e.bus.Publish(subject, trusted); err != nil {
		return RetryAfter(err, retryDelayPublish)
	}
	return nil
}
