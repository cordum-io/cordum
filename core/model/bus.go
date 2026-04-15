package model

import (
	"context"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Bus abstracts the message bus so the scheduler can remain decoupled
// from concrete transport implementations.
type Bus interface {
	Publish(subject string, packet *pb.BusPacket) error
	Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error
}

// ContextPublisher is an optional interface for Bus implementations that
// support trace context propagation via transport headers. When the bus
// implements this interface, callers should prefer PublishWithContext over
// Publish to propagate distributed tracing context.
type ContextPublisher interface {
	PublishWithContext(ctx context.Context, subject string, packet *pb.BusPacket) error
}

// ContextSubscriber is an optional interface for Bus implementations that
// support extracting trace context from transport headers and passing it
// to handlers via context.Context. This enables downstream handlers to
// join upstream distributed traces.
type ContextSubscriber interface {
	SubscribeWithContext(subject, queue string, handler func(context.Context, *pb.BusPacket) error) error
}
