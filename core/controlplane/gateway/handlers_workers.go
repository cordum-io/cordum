package gateway

import (
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/registry"
)

// handleGetWorker returns a single worker by ID from the Redis snapshot.
func (s *server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
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
		logging.Warn("api-gateway", "worker snapshot read failed, falling back to in-memory", "error", err)
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

// handleGetWorkerJobs returns jobs assigned to a specific worker.
// Currently returns an empty list since we don't track worker→job assignments yet.
func (s *server) handleGetWorkerJobs(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "worker id required")
		return
	}

	// Validate worker exists in snapshot or in-memory.
	found := false
	snap, _ := s.snapshotFromRedis()
	if snap != nil {
		for _, ws := range snap.Workers {
			if ws.WorkerID == id {
				found = true
				break
			}
		}
	}
	if !found {
		s.workerMu.RLock()
		_, found = s.workers[id]
		s.workerMu.RUnlock()
	}
	if !found {
		writeErrorJSON(w, http.StatusNotFound, "worker not found")
		return
	}

	writeJSON(w, map[string]any{"items": []any{}})
}

// handleListPools returns all pools with utilization metrics.
func (s *server) handleListPools(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	snap, err := s.snapshotFromRedis()
	if err != nil {
		logging.Warn("api-gateway", "worker snapshot read failed", "error", err)
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "pool name required")
		return
	}

	snap, err := s.snapshotFromRedis()
	if err != nil {
		logging.Warn("api-gateway", "worker snapshot read failed", "error", err)
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
