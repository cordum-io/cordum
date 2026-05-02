package agentd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestHeartbeatServiceSkipsOverlappingTicksAndStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	release := make(chan struct{})
	var gateway *stubHeartbeatGateway
	gateway = &stubHeartbeatGateway{
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			gateway.mu.Lock()
			gateway.calls++
			call := gateway.calls
			gateway.mu.Unlock()
			if call == 1 {
				close(started)
				<-release
			}
			return HeartbeatResponse{SessionID: "sess-heartbeat", HeartbeatAlive: true}, nil
		},
	}
	ticks := make(chan time.Time)
	service := NewHeartbeatService(HeartbeatConfig{
		Gateway:   gateway,
		SessionID: "sess-heartbeat",
		Timeout:   time.Second,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, ticks)
	}()

	ticks <- time.Now()
	<-started
	ticks <- time.Now()
	if got := gateway.callCount(); got != 1 {
		t.Fatalf("heartbeat calls while first is in-flight = %d, want 1", got)
	}
	close(release)
	eventually(t, time.Second, func() bool { return !service.InFlight() })

	ticks <- time.Now()
	eventually(t, time.Second, func() bool { return gateway.callCount() == 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat Run did not stop after context cancellation")
	}
	service.Wait()
}

func TestHeartbeatServiceMarksDegradedAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()

	rec := &heartbeatStatusRecorder{}
	gateway := &stubHeartbeatGateway{
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{}, ErrGatewayTimeout
		},
	}
	ticks := make(chan time.Time)
	service := NewHeartbeatService(HeartbeatConfig{
		Gateway:                gateway,
		SessionID:              "sess-failures",
		Timeout:                time.Second,
		MaxConsecutiveFailures: 2,
		PolicyMode:             edgecore.PolicyModeObserve,
		OnStatus:               rec.record,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, ticks)
	}()

	ticks <- time.Now()
	eventually(t, time.Second, func() bool { return !service.InFlight() })
	ticks <- time.Now()
	eventually(t, time.Second, func() bool {
		last, ok := rec.last()
		return ok && last.Degraded
	})
	last, _ := rec.last()
	if last.ConsecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2", last.ConsecutiveFailures)
	}
	if last.FailClosed {
		t.Fatal("observe heartbeat status failClosed = true, want degraded but not fail-closed")
	}
	cancel()
	<-done
}

func TestHeartbeatServiceFailClosesEnterpriseStrictAfterFailures(t *testing.T) {
	t.Parallel()

	rec := &heartbeatStatusRecorder{}
	gateway := &stubHeartbeatGateway{heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
		return HeartbeatResponse{}, errors.New("gateway unavailable")
	}}
	ticks := make(chan time.Time)
	service := NewHeartbeatService(HeartbeatConfig{
		Gateway:                gateway,
		SessionID:              "sess-strict",
		Timeout:                time.Second,
		MaxConsecutiveFailures: 1,
		PolicyMode:             edgecore.PolicyModeEnterpriseStrict,
		FailClosed:             true,
		OnStatus:               rec.record,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, ticks)
	}()

	ticks <- time.Now()
	eventually(t, time.Second, func() bool {
		last, ok := rec.last()
		return ok && last.Degraded
	})
	last, _ := rec.last()
	if !last.FailClosed {
		t.Fatalf("last status = %#v, want fail-closed", last)
	}
	cancel()
	<-done
}

// heartbeatStatusRecorder is a goroutine-safe collector for OnStatus callbacks.
// Heartbeat callbacks fire from the heartbeat goroutine while the test asserts
// from the main goroutine via eventually(); without this mutex, -race flags
// the captured-variable read/write as a data race.
type heartbeatStatusRecorder struct {
	mu       sync.Mutex
	statuses []HeartbeatStatus
}

func (r *heartbeatStatusRecorder) record(status HeartbeatStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, status)
}

func (r *heartbeatStatusRecorder) last() (HeartbeatStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.statuses) == 0 {
		return HeartbeatStatus{}, false
	}
	return r.statuses[len(r.statuses)-1], true
}

type stubHeartbeatGateway struct {
	mu        sync.Mutex
	calls     int
	heartbeat func(context.Context, string) (HeartbeatResponse, error)
}

func (s *stubHeartbeatGateway) Heartbeat(ctx context.Context, sessionID string) (HeartbeatResponse, error) {
	if s.heartbeat == nil {
		return HeartbeatResponse{SessionID: sessionID, HeartbeatAlive: true}, nil
	}
	return s.heartbeat(ctx, sessionID)
}

func (s *stubHeartbeatGateway) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !ok() {
		t.Fatalf("condition not met within %s", timeout)
	}
}
