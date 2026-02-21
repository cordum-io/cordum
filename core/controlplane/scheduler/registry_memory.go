package scheduler

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/registry"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// MemoryRegistry keeps worker heartbeats in-memory.
type MemoryRegistry struct {
	mu        sync.RWMutex
	workers   map[string]*workerEntry
	ttl       time.Duration
	stopCh    chan struct{}
	closeOnce sync.Once
}

type workerEntry struct {
	hb        *pb.Heartbeat
	handshake *pb.Handshake
	lastSeen  time.Time
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

func (r *MemoryRegistry) UpdateHandshake(hs *pb.Handshake) {
	if hs == nil || hs.ComponentId == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.workers[hs.ComponentId]
	if ok {
		entry.handshake = hs
		entry.lastSeen = time.Now()
	} else {
		r.workers[hs.ComponentId] = &workerEntry{handshake: hs, lastSeen: time.Now()}
	}
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

// Stats returns worker counts: total active workers and a breakdown by pool.
// It only counts non-expired workers. This is an extra method on the concrete
// type (not part of the WorkerRegistry interface).
func (r *MemoryRegistry) Stats() (total int, byPool map[string]int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	byPool = make(map[string]int)
	for _, entry := range r.workers {
		if now.Sub(entry.lastSeen) > r.ttl {
			continue
		}
		total++
		pool := entry.hb.GetPool()
		if pool == "" {
			pool = "(none)"
		}
		byPool[pool]++
	}
	return total, byPool
}

// HydrateFromSnapshot populates the registry from a JSON-encoded worker snapshot.
// This provides instant warm-start on new replicas instead of waiting up to 30s
// for heartbeats. Workers are inserted with lastSeen=time.Now() so normal TTL
// expiry applies. Returns nil for nil/empty data (no-op).
func (r *MemoryRegistry) HydrateFromSnapshot(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var snap registry.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	if len(snap.Workers) == 0 {
		slog.Warn("registry warm-start: snapshot has no workers")
		return nil
	}

	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range snap.Workers {
		if w.WorkerID == "" {
			continue
		}
		hb := &pb.Heartbeat{
			WorkerId:        w.WorkerID,
			Pool:            w.Pool,
			ActiveJobs:      w.ActiveJobs,
			MaxParallelJobs: w.MaxParallelJobs,
			Capabilities:    w.Capabilities,
			CpuLoad:         w.CpuLoad,
			GpuUtilization:  w.GpuUtilization,
		}
		r.workers[w.WorkerID] = &workerEntry{hb: hb, lastSeen: now}
	}
	slog.Info("registry hydrated from snapshot", "workers", len(snap.Workers))
	return nil
}

// Close stops background expiry loop. It is safe to call multiple times.
func (r *MemoryRegistry) Close() {
	r.closeOnce.Do(func() { close(r.stopCh) })
}
