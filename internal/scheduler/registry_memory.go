package scheduler

import (
	"sync"

	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
)

// MemoryRegistry keeps worker heartbeats in-memory.
type MemoryRegistry struct {
	mu      sync.RWMutex
	workers map[string]*pb.Heartbeat
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		workers: make(map[string]*pb.Heartbeat),
	}
}

func (r *MemoryRegistry) UpdateHeartbeat(hb *pb.Heartbeat) {
	if hb == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[hb.WorkerId] = hb
}

func (r *MemoryRegistry) Snapshot() map[string]*pb.Heartbeat {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := make(map[string]*pb.Heartbeat, len(r.workers))
	for id, hb := range r.workers {
		snapshot[id] = hb
	}
	return snapshot
}
