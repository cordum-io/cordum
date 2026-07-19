//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"sync"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/scheduler"
)

type interopAudit struct {
	mu    sync.Mutex
	count int
}

func (s *interopAudit) Emit(_ context.Context, _ audit.SIEMEvent) {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
}

type countedService struct {
	service *scheduler.HandshakeService
	mu      sync.Mutex
	count   int
	called  chan struct{}
}

func (s *countedService) HandleChallenge(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	s.record()
	return s.service.HandleChallenge(ctx, packet)
}

func (s *countedService) HandleAuthenticate(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	s.record()
	return s.service.HandleAuthenticate(ctx, packet)
}

func (s *countedService) record() {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	select {
	case s.called <- struct{}{}:
	default:
	}
}

func (s *countedService) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.count }
