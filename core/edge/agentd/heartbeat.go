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
	cfg         HeartbeatConfig
	inFlight    atomic.Bool
	wg          sync.WaitGroup
	mu          sync.Mutex
	failures    int
	lastDegrade HeartbeatStatus
}

func NewHeartbeatService(cfg HeartbeatConfig) *HeartbeatService {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHookTimeout
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	return &HeartbeatService{cfg: cfg}
}

func (s *HeartbeatService) Run(ctx context.Context, ticks <-chan time.Time) {
	if s == nil {
		return
	}
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
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer s.inFlight.Store(false)
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
