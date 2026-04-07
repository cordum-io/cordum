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
	readyTTL  time.Duration
	stopCh    chan struct{}
	closeOnce sync.Once
}

type workerEntry struct {
	hb          *pb.Heartbeat
	handshake   *pb.Handshake
	ready       bool
	readyTopics []string
	readyAt     time.Time
	lastSeen    time.Time
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
		workers:  make(map[string]*workerEntry),
		ttl:      ttl,
		readyTTL: workerReadinessTTLFromEnv(),
		stopCh:   make(chan struct{}),
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
	now := time.Now()
	if entry, ok := r.workers[hb.WorkerId]; ok {
		entry.hb = hb
		entry.lastSeen = now
		return
	}
	r.workers[hb.WorkerId] = &workerEntry{hb: hb, lastSeen: now}
}

func (r *MemoryRegistry) UpdateHandshake(hs *pb.Handshake) {
	if hs == nil || hs.ComponentId == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	entry, ok := r.workers[hs.ComponentId]
	if ok {
		entry.handshake = hs
		entry.lastSeen = now
	} else {
		entry = &workerEntry{handshake: hs, lastSeen: now}
		r.workers[hs.ComponentId] = entry
	}
	topics := readyTopicsFromHandshake(hs)
	if len(topics) == 0 {
		entry.clearReadiness()
		return
	}
	entry.ready = true
	entry.readyTopics = topics
	entry.readyAt = now
}

// WorkersForPool returns a slice of workers that belong to the given pool.
func (r *MemoryRegistry) WorkersForPool(pool string) []*pb.Heartbeat {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*pb.Heartbeat
	now := time.Now()
	for _, entry := range r.workers {
		if entry.hb == nil || now.Sub(entry.lastSeen) > r.ttl {
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
		if entry.hb == nil || now.Sub(entry.lastSeen) > r.ttl {
			continue
		}
		snapshot[id] = entry.hb
	}
	return snapshot
}

func (r *MemoryRegistry) ReadinessSnapshot() map[string]WorkerReadiness {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	snapshot := make(map[string]WorkerReadiness, len(r.workers))
	for id, entry := range r.workers {
		if entry.hb == nil || now.Sub(entry.lastSeen) > r.ttl {
			continue
		}
		state := WorkerReadiness{}
		if entry.readinessActive(now, r.readyTTL) {
			state.Ready = true
			state.ReadyTopics = append([]string(nil), entry.readyTopics...)
		}
		snapshot[id] = state
	}
	return snapshot
}

// IsAlive reports whether the given worker has been seen within the TTL window.
func (r *MemoryRegistry) IsAlive(workerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.workers[workerID]
	if !ok {
		return false
	}
	return time.Since(entry.lastSeen) <= r.ttl
}

func (r *MemoryRegistry) expireLoop() {
	ticker := time.NewTicker(r.expiryInterval())
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
			continue
		}
		if entry.readinessExpired(now, r.readyTTL) {
			entry.clearReadiness()
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
		if entry.hb == nil || now.Sub(entry.lastSeen) > r.ttl {
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

func (entry *workerEntry) clearReadiness() {
	entry.ready = false
	entry.readyTopics = nil
	entry.readyAt = time.Time{}
}

func (entry *workerEntry) readinessActive(now time.Time, readyTTL time.Duration) bool {
	if entry == nil || !entry.ready || len(entry.readyTopics) == 0 || readyTTL <= 0 || entry.readyAt.IsZero() {
		return false
	}
	return now.Sub(entry.readyAt) <= readyTTL
}

func (entry *workerEntry) readinessExpired(now time.Time, readyTTL time.Duration) bool {
	if entry == nil || !entry.ready || readyTTL <= 0 || entry.readyAt.IsZero() {
		return false
	}
	return now.Sub(entry.readyAt) > readyTTL
}

func (r *MemoryRegistry) expiryInterval() time.Duration {
	interval := r.ttl / 2
	if readyInterval := r.readyTTL / 2; readyInterval > 0 && (interval <= 0 || readyInterval < interval) {
		interval = readyInterval
	}
	if interval <= 0 {
		return 10 * time.Millisecond
	}
	return interval
}
