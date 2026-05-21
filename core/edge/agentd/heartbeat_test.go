package agentd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

const heartbeatTestSyncTimeout = 5 * time.Second

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
	callCh := make(chan int, 4)
	statusCh := make(chan HeartbeatStatus, 4)
	gateway := &stubHeartbeatGateway{}
	gateway.heartbeat = func(context.Context, string) (HeartbeatResponse, error) {
		call := gateway.recordCall()
		select {
		case callCh <- call:
		default:
		}
		return HeartbeatResponse{}, ErrGatewayTimeout
	}
	ticks := make(chan time.Time)
	service := NewHeartbeatService(HeartbeatConfig{
		Gateway:                gateway,
		SessionID:              "sess-failures",
		Timeout:                time.Second,
		MaxConsecutiveFailures: 2,
		PolicyMode:             edgecore.PolicyModeObserve,
		OnStatus: func(status HeartbeatStatus) {
			rec.record(status)
			select {
			case statusCh <- status:
			default:
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, ticks)
	}()

	ticks <- time.Now()
	requireHeartbeatCall(t, callCh, gateway, 1)
	requireHeartbeatIdle(t, service, gateway, 1)
	ticks <- time.Now()
	requireHeartbeatCall(t, callCh, gateway, 2)
	last := requireHeartbeatStatus(t, statusCh, rec, gateway)
	requireHeartbeatIdle(t, service, gateway, 2)
	if last.ConsecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2", last.ConsecutiveFailures)
	}
	if last.FailClosed {
		t.Fatal("observe heartbeat status failClosed = true, want degraded but not fail-closed")
	}
	if got := gateway.callCount(); got != 2 {
		t.Fatalf("heartbeat calls before degraded status = %d, want exactly 2", got)
	}
	cancel()
	requireHeartbeatRunStopped(t, done)
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

func (r *heartbeatStatusRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.statuses)
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

func (s *stubHeartbeatGateway) recordCall() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.calls
}

func requireHeartbeatCall(
	t *testing.T,
	callCh <-chan int,
	gateway *stubHeartbeatGateway,
	want int,
) {
	t.Helper()
	select {
	case got := <-callCh:
		if got != want {
			t.Fatalf("heartbeat call order = %d, want %d (total calls=%d)", got, want, gateway.callCount())
		}
	case <-time.After(heartbeatTestSyncTimeout):
		t.Fatalf("heartbeat call %d not observed within %s; total calls=%d", want, heartbeatTestSyncTimeout, gateway.callCount())
	}
}

func requireHeartbeatIdle(
	t *testing.T,
	service *HeartbeatService,
	gateway *stubHeartbeatGateway,
	wantCalls int,
) {
	t.Helper()
	deadline := time.Now().Add(heartbeatTestSyncTimeout)
	for time.Now().Before(deadline) {
		if !service.InFlight() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("heartbeat call %d did not complete within %s; total calls=%d inFlight=%t",
		wantCalls, heartbeatTestSyncTimeout, gateway.callCount(), service.InFlight())
}

func requireHeartbeatStatus(
	t *testing.T,
	statusCh <-chan HeartbeatStatus,
	rec *heartbeatStatusRecorder,
	gateway *stubHeartbeatGateway,
) HeartbeatStatus {
	t.Helper()
	select {
	case status := <-statusCh:
		if !status.Degraded {
			t.Fatalf("heartbeat status = %#v, want degraded status", status)
		}
		return status
	case <-time.After(heartbeatTestSyncTimeout):
		last, _ := rec.last()
		t.Fatalf("degraded heartbeat status not observed within %s; total calls=%d statusCount=%d last=%#v",
			heartbeatTestSyncTimeout, gateway.callCount(), rec.count(), last)
		return HeartbeatStatus{}
	}
}

func requireHeartbeatRunStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(heartbeatTestSyncTimeout):
		t.Fatalf("heartbeat Run did not stop within %s after context cancellation", heartbeatTestSyncTimeout)
	}
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

// TestHeartbeatServiceCallbackInvokedWithoutMutexHeld pins the
// invariant that OnStatus is invoked OUTSIDE s.mu.
//
// Pre-fix recordResult took s.mu under defer-unlock and called
// OnStatus while still holding it. Any callback path that ends up
// acquiring s.mu (now or in the future) lock-order-inverts against
// a caller that takes the same locks in the opposite order — a
// latent deadlock waiting for the next maintainer.
//
// The test calls TryLock on s.mu from inside OnStatus: pre-fix it
// returns false (mu held by recordResult), post-fix it returns true.
func TestHeartbeatServiceCallbackInvokedWithoutMutexHeld(t *testing.T) {
	t.Parallel()

	var service *HeartbeatService
	tryLockResult := make(chan bool, 1)

	gateway := &stubHeartbeatGateway{
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{}, errors.New("gateway unavailable")
		},
	}
	ticks := make(chan time.Time)
	service = NewHeartbeatService(HeartbeatConfig{
		Gateway:                gateway,
		SessionID:              "sess-callback",
		Timeout:                time.Second,
		MaxConsecutiveFailures: 1,
		PolicyMode:             edgecore.PolicyModeObserve,
		OnStatus: func(_ HeartbeatStatus) {
			got := service.mu.TryLock()
			if got {
				service.mu.Unlock()
			}
			select {
			case tryLockResult <- got:
			default:
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(ctx, ticks)
	}()

	ticks <- time.Now()

	select {
	case got := <-tryLockResult:
		if !got {
			t.Fatal("OnStatus invoked while HeartbeatService.mu still held — deadlock risk; callback must run after the mutex is released")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog: OnStatus never fired within 5s (possible deadlock during recordResult)")
	}

	cancel()
	<-done
}
