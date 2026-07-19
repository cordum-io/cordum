//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"time"
)

func (s *interopServer) cleanupOwnedState() {
	if s == nil || s.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.restoreWorkerConfigState(ctx)
	pipe := s.redis.TxPipeline()
	for _, identity := range s.identities {
		pipe.Del(ctx, "agent:identity:"+identity.agentID)
		pipe.Del(ctx, "agent:by-worker:"+identity.workerID)
		pipe.ZRem(ctx, "agent:identity:index", identity.agentID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.t.Errorf("cleanup owned Redis identity/session keys: %v", err)
	}
	s.cleanupHandshakeState(ctx)
	s.assertNoOwnedHandshakeState(ctx)
}
