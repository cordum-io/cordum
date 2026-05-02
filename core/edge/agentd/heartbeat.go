package agentd

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

type HeartbeatConfig struct {
	Gateway                HeartbeatClient
	SessionID              string
	Timeout                time.Duration
	MaxConsecutiveFailures int
	PolicyMode             edgecore.PolicyMode
	FailClosed             bool
	OnStatus               func(HeartbeatStatus)
}

type HeartbeatStatus struct {
	ConsecutiveFailures int
	Degraded            bool
	FailClosed          bool
	Reason              string
}

type HeartbeatService struct {
	cfg          HeartbeatConfig
	inFlight     atomic.Bool
	wg           sync.WaitGroup
	mu           sync.Mutex
	failures     int
	lastDegrade  HeartbeatStatus
	doneMu       sync.Mutex
	inFlightDone chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once
}

func NewHeartbeatService(cfg HeartbeatConfig) *HeartbeatService {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHookTimeout
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	return &HeartbeatService{cfg: cfg, stopped: make(chan struct{})}
}

func (s *HeartbeatService) Run(ctx context.Context, ticks <-chan time.Time) {
	if s == nil {
		return
	}
	defer s.markStopped()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			if !s.inFlight.CompareAndSwap(false, true) {
				continue
			}
			done := make(chan struct{})
			s.setInFlightDone(done)
			s.wg.Add(1)
			go func() {
				defer s.finishInFlight(done)
				callCtx := ctx
				var cancel context.CancelFunc
				if s.cfg.Timeout > 0 {
					callCtx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
					defer cancel()
				}
				if s.cfg.Gateway != nil {
					_, err := s.cfg.Gateway.Heartbeat(callCtx, s.cfg.SessionID)
					s.recordResult(err)
				}
			}()
		}
	}
}

func (s *HeartbeatService) Wait() {
	if s == nil {
		return
	}
	s.wg.Wait()
}

func (s *HeartbeatService) WaitContext(ctx context.Context) bool {
	if s == nil {
		return true
	}
	if ctx == nil {
		s.Wait()
		return true
	}
	if s.stopped != nil {
		select {
		case <-s.stopped:
		case <-ctx.Done():
			return false
		}
	}
	done := s.currentInFlightDone()
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *HeartbeatService) InFlight() bool {
	if s == nil {
		return false
	}
	return s.inFlight.Load()
}

func (s *HeartbeatService) recordResult(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.failures = 0
		return
	}
	s.failures++
	if s.failures < s.cfg.MaxConsecutiveFailures {
		return
	}
	status := HeartbeatStatus{
		ConsecutiveFailures: s.failures,
		Degraded:            true,
		FailClosed:          s.cfg.FailClosed || s.cfg.PolicyMode == edgecore.PolicyModeEnterpriseStrict,
		Reason:              "gateway heartbeat failures exceeded threshold",
	}
	s.lastDegrade = status
	if s.cfg.OnStatus != nil {
		s.cfg.OnStatus(status)
	}
}

func (s *HeartbeatService) setInFlightDone(done chan struct{}) {
	s.doneMu.Lock()
	defer s.doneMu.Unlock()
	s.inFlightDone = done
}

func (s *HeartbeatService) finishInFlight(done chan struct{}) {
	s.inFlight.Store(false)
	s.doneMu.Lock()
	if s.inFlightDone == done {
		close(done)
		s.inFlightDone = nil
	}
	s.doneMu.Unlock()
	s.wg.Done()
}

func (s *HeartbeatService) currentInFlightDone() <-chan struct{} {
	s.doneMu.Lock()
	defer s.doneMu.Unlock()
	return s.inFlightDone
}

func (s *HeartbeatService) markStopped() {
	s.stopOnce.Do(func() {
		if s.stopped != nil {
			close(s.stopped)
		}
	})
}
