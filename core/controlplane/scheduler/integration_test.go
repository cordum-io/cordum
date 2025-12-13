package scheduler

import (
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// in-memory bus that dispatches publishes to subscribed handlers synchronously
type loopbackBus struct {
	mu        sync.Mutex
	handlers  map[string][]func(*pb.BusPacket)
	published []publishedMsg
}

func newLoopbackBus() *loopbackBus {
	return &loopbackBus{handlers: make(map[string][]func(*pb.BusPacket))}
}

func (b *loopbackBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
	handlers := append([]func(*pb.BusPacket){}, b.handlers[subject]...)
	b.mu.Unlock()

	for _, h := range handlers {
		h(packet)
	}
	return nil
}

func (b *loopbackBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[subject] = append(b.handlers[subject], handler)
	return nil
}

// integration-flavored test: heartbeat registers worker, job routes to direct subject, result processed.
func TestEngineDispatchesToDirectWorkerAndMarksSucceeded(t *testing.T) {
	bus := newLoopbackBus()
	reg := NewMemoryRegistry()
	store := newFakeJobStore()

	engine := NewEngine(bus, NewSafetyStub(), reg, NewLeastLoadedStrategy(map[string]string{"job.echo": "echo"}), store, nil)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start failed: %v", err)
	}

	// Simulate worker subscription on direct subject that returns a JobResult.
	bus.Subscribe("worker.w-echo.jobs", "", func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}
		res := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        "worker-w-echo",
			ProtocolVersion: 1,
			CreatedAt:       timestamppb.Now(),
			Payload: &pb.BusPacket_JobResult{
				JobResult: &pb.JobResult{
					JobId:       req.JobId,
					Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
					ResultPtr:   "redis://result",
					WorkerId:    "worker-w-echo",
					ExecutionMs: 5,
				},
			},
		}
		_ = bus.Publish("sys.job.result", res)
	})

	// Heartbeat to register worker in pool "echo"
	hb := &pb.BusPacket{
		TraceId:         "trace-hb",
		SenderId:        "worker-w-echo",
		ProtocolVersion: 1,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				WorkerId:        "w-echo",
				Pool:            "echo",
				ActiveJobs:      0,
				CpuLoad:         1,
				MaxParallelJobs: 5,
			},
		},
	}
	bus.Publish("sys.heartbeat", hb)

	// Submit a job; expect dispatch to direct subject and eventual succeeded state.
	jobID := "job-integration"
	req := &pb.BusPacket{
		TraceId:         "trace-job",
		SenderId:        "client",
		ProtocolVersion: 1,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: &pb.JobRequest{
				JobId: jobID,
				Topic: "job.echo",
			},
		},
	}
	bus.Publish("sys.job.submit", req)

	// Wait briefly for result handling.
	time.Sleep(20 * time.Millisecond)

	if state := store.states[jobID]; state != JobStateSucceeded {
		t.Fatalf("expected job %s state SUCCEEDED, got %s", jobID, state)
	}

	var dispatchedToDirect bool
	for _, msg := range bus.published {
		if strings.HasPrefix(msg.subject, "worker.w-echo.jobs") {
			dispatchedToDirect = true
			break
		}
	}
	if !dispatchedToDirect {
		t.Fatalf("job was not dispatched to direct subject")
	}
}
