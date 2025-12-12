package scheduler

import (
	"sync"

	pb "github.com/yaront1111/cortex-os/core/protocol/pb/v1"
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

// WorkersForPool returns a slice of workers that belong to the given pool.
func (r *MemoryRegistry) WorkersForPool(pool string) []*pb.Heartbeat {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*pb.Heartbeat
	for _, hb := range r.workers {
		if hb.GetPool() == pool {
			result = append(result, hb)
		}
	}
	return result
}
