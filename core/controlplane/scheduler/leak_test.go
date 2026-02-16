package scheduler

import (
	"runtime"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stabilizeGoroutines lets background goroutines settle before measuring.
func stabilizeGoroutines() {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
}

func TestStopDoesNotLeakGoroutines(t *testing.T) {
	stabilizeGoroutines()
	baseline := runtime.NumGoroutine()

	bus := &fakeBus{}
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId:        "w1",
		Pool:            "default",
		ActiveJobs:      0,
		MaxParallelJobs: 10,
	})
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	// Process several jobs to exercise goroutine paths.
	for i := 0; i < 10; i++ {
		pkt := &pb.BusPacket{
			TraceId:         "trace-leak",
			SenderId:        "test",
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			CreatedAt:       timestamppb.Now(),
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: &pb.JobRequest{
					JobId: "job-leak-" + time.Now().Format("150405.000000000"),
					Topic: "job.default",
				},
			},
		}
		_ = engine.HandlePacket(pkt)
	}

	engine.Stop()

	// Allow goroutines to wind down.
	stabilizeGoroutines()

	after := runtime.NumGoroutine()
	// Tolerance of 2 for GC finalizers and other runtime goroutines.
	if after > baseline+2 {
		t.Fatalf("goroutine leak: baseline=%d, after Stop()=%d (delta=%d, max allowed=2)",
			baseline, after, after-baseline)
	}
}

func TestMemoryRegistryCloseStopsExpireLoop(t *testing.T) {
	stabilizeGoroutines()
	baseline := runtime.NumGoroutine()

	reg := NewMemoryRegistryWithTTL(50 * time.Millisecond)

	// The expireLoop goroutine should have started.
	afterCreate := runtime.NumGoroutine()
	if afterCreate < baseline+1 {
		t.Logf("expected goroutine count to increase by at least 1 (baseline=%d, after=%d)", baseline, afterCreate)
	}

	reg.Close()
	stabilizeGoroutines()

	afterClose := runtime.NumGoroutine()
	if afterClose > baseline+1 {
		t.Fatalf("goroutine leak after Close: baseline=%d, afterClose=%d (delta=%d, max allowed=1)",
			baseline, afterClose, afterClose-baseline)
	}
}
