package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/model"
)

// handleGetWorker returns a single worker by ID from the Redis snapshot.
func (s *server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, PermWorkersRead, "admin") {
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "worker id required")
		return
	}

	// Prefer Redis snapshot (consistent across replicas).
	snap, err := s.snapshotFromRedis()
	if err != nil {
		slog.Warn("worker snapshot read failed, falling back to in-memory", "error", err)
	}
	if snap != nil {
		for _, ws := range snap.Workers {
			if ws.WorkerID == id {
				writeJSON(w, workerSummaryToResponse(ws, snap.CapturedAt))
				return
			}
		}
	}

	// Fallback: check in-memory heartbeat map.
	s.workerMu.RLock()
	hb, ok := s.workers[id]
	s.workerMu.RUnlock()
	if ok && hb != nil {
		ws := registry.WorkerSummary{
			WorkerID:        hb.WorkerId,
			Pool:            hb.Pool,
			ActiveJobs:      hb.ActiveJobs,
			MaxParallelJobs: hb.MaxParallelJobs,
			Capabilities:    hb.Capabilities,
			CpuLoad:         hb.CpuLoad,
			GpuUtilization:  hb.GpuUtilization,
			MemoryLoad:      hb.MemoryLoad,
			Region:          hb.Region,
			Type:            hb.Type,
			Labels:          hb.Labels,
		}
		writeJSON(w, workerSummaryToResponse(ws, ""))
		return
	}

	writeErrorJSON(w, http.StatusNotFound, "worker not found")
}

// handleGetWorkerJobs returns recent jobs processed by a specific worker.
// When the per-worker index is empty (pre-existing jobs), falls back to
// recent jobs filtered by the worker's pool topics.
func (s *server) handleGetWorkerJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, PermWorkersRead, "admin") {
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "worker id required")
		return
	}

	limit := int64(20)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 100 {
		limit = 100
	}

	if s.jobStore == nil {
		writeJSON(w, map[string]any{"items": []any{}})
		return
	}

	// Try per-worker index first (populated for jobs processed after tracking was added).
	jobs, err := s.jobStore.ListWorkerJobs(r.Context(), id, limit)
	if err != nil {
		slog.Error("list worker jobs failed", "worker_id", id, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list worker jobs")
		return
	}

	// Fallback: if per-worker index is empty, return recent jobs filtered by
	// topics routed to this worker's pool. This covers historical jobs that
	// predate per-worker tracking.
	if len(jobs) == 0 {
		pool := s.resolveWorkerPool(id)
		if pool == "" {
			writeErrorJSON(w, http.StatusNotFound, "worker not found")
			return
		}
		jobs = s.recentJobsByPool(r.Context(), pool, limit)
	}

	writeJSON(w, map[string]any{"items": jobs})
}

// resolveWorkerPool returns the pool for a worker from snapshot or in-memory state.
func (s *server) resolveWorkerPool(workerID string) string {
	snap, _ := s.snapshotFromRedis()
	if snap != nil {
		for _, ws := range snap.Workers {
			if ws.WorkerID == workerID {
				return ws.Pool
			}
		}
	}
	s.workerMu.RLock()
	hb, ok := s.workers[workerID]
	s.workerMu.RUnlock()
	if ok && hb != nil {
		return hb.Pool
	}
	return ""
}

// recentJobsByPool returns recent jobs whose topic routes to the given pool.
func (s *server) recentJobsByPool(ctx context.Context, pool string, limit int64) []model.JobRecord {
	// Collect topics routed to this pool.
	poolTopics := map[string]bool{}
	snap, _ := s.snapshotFromRedis()
	if snap != nil {
		for topic, ts := range snap.Topics {
			if ts.Pool == pool {
				poolTopics[topic] = true
			}
		}
	}
	if len(poolTopics) == 0 {
		return nil
	}

	// Fetch a larger batch and filter by topic to fill the requested limit.
	fetchLimit := limit * 5
	if fetchLimit > 200 {
		fetchLimit = 200
	}
	all, err := s.jobStore.ListRecentJobs(ctx, fetchLimit)
	if err != nil {
		slog.Warn("recent jobs fallback failed", "pool", pool, "error", err)
		return nil
	}
	if limit > 100 {
		limit = 100
	}
	filtered := make([]model.JobRecord, 0, limit)
	for _, j := range all {
		if poolTopics[j.Topic] {
			filtered = append(filtered, j)
			if int64(len(filtered)) >= limit {
				break
			}
		}
	}
	return filtered
}

// handleListPools returns all pools with utilization metrics.
func (s *server) handleListPools(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, PermWorkersRead, "admin") {
		return
	}

	snap, err := s.snapshotFromRedis()
	if err != nil {
		slog.Warn("worker snapshot read failed", "error", err)
	}

	items := []map[string]any{}
	if snap != nil {
		for name, ps := range snap.Pools {
			items = append(items, map[string]any{
				"name":        name,
				"workers":     ps.Workers,
				"active_jobs": ps.ActiveJobs,
				"capacity":    ps.Capacity,
				"utilization": poolUtilization(ps),
			})
		}
	}

	writeJSON(w, map[string]any{"items": items})
}

// handleGetPool returns a single pool's detail with its workers and topics.
func (s *server) handleGetPool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, PermWorkersRead, "admin") {
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pool name required")
		return
	}

	snap, err := s.snapshotFromRedis()
	if err != nil {
		slog.Warn("worker snapshot read failed", "error", err)
	}
	if snap == nil {
		writeErrorJSON(w, http.StatusNotFound, "pool not found")
		return
	}

	ps, ok := snap.Pools[name]
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "pool not found")
		return
	}

	// Collect workers belonging to this pool.
	poolWorkers := []map[string]any{}
	for _, ws := range snap.Workers {
		if ws.Pool == name {
			poolWorkers = append(poolWorkers, workerSummaryToResponse(ws, snap.CapturedAt))
		}
	}

	// Collect topics mapped to this pool.
	topics := []string{}
	for topic, ts := range snap.Topics {
		if ts.Pool == name {
			topics = append(topics, topic)
		}
	}

	writeJSON(w, map[string]any{
		"name":        name,
		"workers":     poolWorkers,
		"active_jobs": ps.ActiveJobs,
		"capacity":    ps.Capacity,
		"utilization": poolUtilization(ps),
		"topics":      topics,
	})
}

// workerSummaryToResponse converts a WorkerSummary into a JSON-friendly map.
func workerSummaryToResponse(ws registry.WorkerSummary, capturedAt string) map[string]any {
	resp := map[string]any{
		"worker_id":         ws.WorkerID,
		"pool":              ws.Pool,
		"active_jobs":       ws.ActiveJobs,
		"max_parallel_jobs": ws.MaxParallelJobs,
		"capabilities":      ws.Capabilities,
		"cpu_load":          ws.CpuLoad,
		"gpu_utilization":   ws.GpuUtilization,
		"memory_load":       ws.MemoryLoad,
		"region":            ws.Region,
		"type":              ws.Type,
		"labels":            ws.Labels,
	}
	if capturedAt != "" {
		resp["last_heartbeat"] = capturedAt
	}
	return resp
}

// poolUtilization calculates the utilization ratio for a pool snapshot.
// Returns 0 if the pool has no capacity.
func poolUtilization(ps registry.PoolSnapshot) float64 {
	if ps.Capacity <= 0 {
		return 0
	}
	return float64(ps.ActiveJobs) / float64(ps.Capacity)
}
