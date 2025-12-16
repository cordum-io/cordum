package scheduler

import (
	"sync"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// MemoryRegistry keeps worker heartbeats in-memory.
type MemoryRegistry struct {
	mu      sync.RWMutex
	workers map[string]*workerEntry
	ttl     time.Duration
	stopCh  chan struct{}
}

type workerEntry struct {
	hb       *pb.Heartbeat
	lastSeen time.Time
}

const defaultWorkerTTL = 30 * time.Second

func NewMemoryRegistry() *MemoryRegistry {
	return NewMemoryRegistryWithTTL(defaultWorkerTTL)
}

// NewMemoryRegistryWithTTL allows customizing worker heartbeat TTL (primarily for tests).
func NewMemoryRegistryWithTTL(ttl time.Duration) *MemoryRegistry {
	if ttl <= 0 {
		ttl = defaultWorkerTTL
	}
	r := &MemoryRegistry{
		workers: make(map[string]*workerEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go r.expireLoop()
	return r
}

func (r *MemoryRegistry) UpdateHeartbeat(hb *pb.Heartbeat) {
	if hb == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[hb.WorkerId] = &workerEntry{hb: hb, lastSeen: time.Now()}
}

// WorkersForPool returns a slice of workers that belong to the given pool.
func (r *MemoryRegistry) WorkersForPool(pool string) []*pb.Heartbeat {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*pb.Heartbeat
	now := time.Now()
	for _, entry := range r.workers {
		if now.Sub(entry.lastSeen) > r.ttl {
			continue
		}
		if entry.hb.GetPool() == pool {
			result = append(result, entry.hb)
		}
	}
	return result
}

func (r *MemoryRegistry) Snapshot() map[string]*pb.Heartbeat {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	snapshot := make(map[string]*pb.Heartbeat, len(r.workers))
	for id, entry := range r.workers {
		if now.Sub(entry.lastSeen) > r.ttl {
			continue
		}
		snapshot[id] = entry.hb
	}
	return snapshot
}

func (r *MemoryRegistry) expireLoop() {
	ticker := time.NewTicker(r.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.expire()
		}
	}
}

func (r *MemoryRegistry) expire() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, entry := range r.workers {
		if now.Sub(entry.lastSeen) > r.ttl {
			delete(r.workers, id)
		}
	}
}

// Close stops background expiry loop.
func (r *MemoryRegistry) Close() {
	close(r.stopCh)
}
