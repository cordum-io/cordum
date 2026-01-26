package scheduler

import (
	"errors"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestEngineStopSkipsHandling(t *testing.T) {
	store := newFakeJobStore()
	bus := &observedBus{}
	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil)
	engine.Stop()

	req := &pb.JobRequest{JobId: "job-stop", Topic: "job.test", TenantId: "default"}
	packet := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: req}}
	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bus.count() != 0 {
		t.Fatalf("expected no publishes when stopped")
	}
}

func TestRetryableErrorDelay(t *testing.T) {
	err := RetryAfter(errors.New("boom"), 250*time.Millisecond)
	retryErr, ok := err.(*retryableError)
	if !ok {
		t.Fatalf("expected retryableError")
	}
	if retryErr.RetryDelay() != 250*time.Millisecond {
		t.Fatalf("unexpected retry delay")
	}
	if retryErr.Unwrap() == nil {
		t.Fatalf("expected unwrap to return error")
	}
	if retryErr.Error() == "" {
		t.Fatalf("expected error string")
	}
}

func TestReconcilerUpdateTimeouts(t *testing.T) {
	rec := NewReconciler(newFakeReconcileStore(), 10*time.Second, 20*time.Second, time.Second)
	rec.UpdateTimeouts(0, 0)
	d1, r1 := rec.currentTimeouts()
	if d1 != 10*time.Second || r1 != 20*time.Second {
		t.Fatalf("expected timeouts unchanged")
	}
	rec.UpdateTimeouts(5*time.Second, 15*time.Second)
	d2, r2 := rec.currentTimeouts()
	if d2 != 5*time.Second || r2 != 15*time.Second {
		t.Fatalf("expected timeouts updated")
	}
}
