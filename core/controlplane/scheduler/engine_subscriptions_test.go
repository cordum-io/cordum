package scheduler

import (
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type recordingBus struct {
	subs []subCall
}

type subCall struct {
	subject string
	queue   string
}

func (b *recordingBus) Publish(_ string, _ *pb.BusPacket) error {
	return nil
}

func (b *recordingBus) Subscribe(subject, queue string, _ func(*pb.BusPacket) error) error {
	b.subs = append(b.subs, subCall{subject: subject, queue: queue})
	return nil
}

func TestEngineStartSubscriptions(t *testing.T) {
	bus := &recordingBus{}

	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewLeastLoadedStrategy(PoolRouting{}), newFakeJobStore(), nil)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start failed: %v", err)
	}

	var hbQueue *string
	for _, sub := range bus.subs {
		if sub.subject == capsdk.SubjectHeartbeat {
			q := sub.queue
			hbQueue = &q
			break
		}
	}
	if hbQueue == nil {
		t.Fatalf("expected subscription to %s", capsdk.SubjectHeartbeat)
	}
	if *hbQueue != "" {
		t.Fatalf("expected %s subscription without queue group, got %q", capsdk.SubjectHeartbeat, *hbQueue)
	}
}
