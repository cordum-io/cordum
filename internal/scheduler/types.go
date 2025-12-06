package scheduler

import pb "github.com/cortex-os/core/pkg/pb/v1/api/proto/v1"

// Bus abstracts the message bus so the scheduler can remain decoupled
// from concrete transport implementations.
type Bus interface {
	Publish(subject string, packet *pb.BusPacket) error
	Subscribe(subject, queue string, handler func(*pb.BusPacket)) error
}
