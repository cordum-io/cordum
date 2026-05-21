package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// handleRevokeWorkerSession revokes the active session token for the
// named worker. Admin-only. Idempotent — revoking a worker with no
// active session is a 200 success with "no_active_session" in the
// response so operator scripts can retry safely.
//
// Audit: a worker_trust_change event with reason=session_revoked is
// emitted through the tenant audit chain (task-2497391e) so SOC2
// tooling can reconstruct who revoked which worker when.
func (s *server) handleRevokeWorkerSession(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkersWrite, "admin") {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerSessionInvalid, "worker id required")
		return
	}
	if s.sessionIssuer == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable,
			"session issuer not configured — wire via server.WithSessionIssuer")
		return
	}
	tenant, tenErr := s.resolveTenant(r, "")
	if tenErr != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerSessionInvalid, "tenant required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.sessionIssuer.RevokeByAgent(ctx, tenant, id); err != nil {
		slog.Error("revoke worker session failed",
			"worker_id", id,
			"tenant", tenant,
			"error", err,
		)
		writeErrorJSON(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	s.emitWorkerTrustChangeAudit(ctx, id, tenant, r)
	s.emitWorkerHandshakeRevokeAudit(ctx, id, tenant, r)
	writeJSON(w, map[string]any{
		"worker_id": id,
		"tenant":    tenant,
		"revoked":   true,
	})
}

// emitWorkerTrustChangeAudit records a worker_trust_change SIEMEvent
// for an admin revoke. Delegates to scheduler.EmitTrustChange so the
// canonical helper is the single production call site for revoke
// events (QA reopen requirement). Best-effort — a failing audit
// sink must not block the revoke response.
func (s *server) emitWorkerTrustChangeAudit(ctx context.Context, workerID, tenant string, r *http.Request) {
	if s.auditExporter == nil {
		return
	}
	actor := "admin"
	if ac, ok := r.Context().Value(auth.ContextKey{}).(*auth.AuthContext); ok && ac != nil && ac.PrincipalID != "" {
		actor = ac.PrincipalID
	}
	// Capture the prior JTI when available so the SIEM event can
	// pair with the handshake that originally issued it. Best-effort;
	// the revocation itself doesn't depend on this.
	jti := ""
	if s.sessionIssuer != nil {
		if state, err := scheduler.NewTrustResolver(s.jobStore.Client()).ResolveTrust(ctx, workerID); err == nil {
			jti = state.JTI
		}
	}
	scheduler.EmitTrustChangeWithActor(
		ctx,
		s.auditExporter,
		workerID,
		tenant,
		"valid",
		"revoked",
		scheduler.TrustChangeReasonSessionRevoked,
		jti,
		actor,
	)
}

// emitWorkerHandshakeRevokeAudit emits the worker_handshake SIEMEvent
// with outcome=revoked alongside the trust-change event. The plan
// requires both events to fire for a revoke so SIEM correlation
// rules can join on EventWorkerHandshake AND EventWorkerTrustChange
// independently — revoke is the terminal transition that closes the
// handshake lifecycle.
func (s *server) emitWorkerHandshakeRevokeAudit(ctx context.Context, workerID, tenant string, r *http.Request) {
	if s.auditExporter == nil {
		return
	}
	actor := "admin"
	if ac, ok := r.Context().Value(auth.ContextKey{}).(*auth.AuthContext); ok && ac != nil && ac.PrincipalID != "" {
		actor = ac.PrincipalID
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: scheduler.EventWorkerHandshake,
		Severity:  audit.SeverityHigh,
		TenantID:  tenant,
		AgentID:   workerID,
		Action:    "worker_handshake",
		Reason:    scheduler.TrustChangeReasonSessionRevoked,
		Identity:  actor,
		Extra: map[string]string{
			"agent_id": workerID,
			"tenant":   tenant,
			"outcome":  "revoked",
			"actor":    actor,
		},
	})
	_ = ctx
}

// handleGetWorker returns a single worker by ID from the Redis snapshot.
func (s *server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkersRead, "admin") {
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerSessionInvalid, "worker id required")
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
				writeJSON(w, s.workerStatusFromSummary(r.Context(), ws, snap.CapturedAt))
				return
			}
		}
	}

	// Fallback: check in-memory heartbeat map.
	s.workerMu.RLock()
	hb, ok := s.workers[id]
	lastSeen := time.Time{}
	if s.workerSeen != nil {
		lastSeen = s.workerSeen[id]
	}
	s.workerMu.RUnlock()
	if ok && hb != nil {
		writeJSON(w, s.workerStatusResponse(r.Context(), hb, lastSeen))
		return
	}

	writeJSONError(w, http.StatusNotFound, errorCodeWorkerNotFound, "worker not found")
}

// handleGetWorkerJobs returns recent jobs processed by a specific worker.
// When the per-worker index is empty (pre-existing jobs), falls back to
// recent jobs filtered by the worker's pool topics.
func (s *server) handleGetWorkerJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkersRead, "admin") {
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerSessionInvalid, "worker id required")
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
			writeJSONError(w, http.StatusNotFound, errorCodeWorkerNotFound, "worker not found")
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
	filtered := make([]model.JobRecord, 0, 100)
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
	if !s.requirePermissionOrRole(w, r, auth.PermWorkersRead, "admin") {
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
	if !s.requirePermissionOrRole(w, r, auth.PermWorkersRead, "admin") {
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, "pool name required")
		return
	}

	snap, err := s.snapshotFromRedis()
	if err != nil {
		slog.Warn("worker snapshot read failed", "error", err)
	}
	if snap == nil {
		writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, "pool not found")
		return
	}

	ps, ok := snap.Pools[name]
	if !ok {
		writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, "pool not found")
		return
	}

	// Collect workers belonging to this pool.
	poolWorkers := []map[string]any{}
	for _, ws := range snap.Workers {
		if ws.Pool == name {
			poolWorkers = append(poolWorkers, s.workerStatusFromSummary(r.Context(), ws, snap.CapturedAt))
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

// workerStatusResponse builds the canonical worker payload for the
// /api/v1/workers endpoints under the heartbeat-demotion rollout.
//
// Session authority (online, session_valid, session_exp_ms,
// session_revoked, session_state) is sourced from WorkerTrustState
// when the gateway has a trust resolver wired; otherwise we fall back
// to heartbeat-recency (legacy semantics), letting operators stay on
// authority mode without wiring the resolver.
//
// Telemetry fields (last_heartbeat_at, heartbeat_age_seconds,
// last_heartbeat) are always emitted when a lastSeen timestamp is
// available. Consumers should display them as freshness indicators
// only — never as policy gates.
func (s *server) workerStatusResponse(ctx context.Context, hb *pb.Heartbeat, lastSeen time.Time) map[string]any {
	resp := map[string]any{
		"worker_id":         hb.GetWorkerId(),
		"pool":              hb.GetPool(),
		"active_jobs":       hb.GetActiveJobs(),
		"max_parallel_jobs": hb.GetMaxParallelJobs(),
		"capabilities":      hb.GetCapabilities(),
		"cpu_load":          hb.GetCpuLoad(),
		"gpu_utilization":   hb.GetGpuUtilization(),
		"memory_load":       hb.GetMemoryLoad(),
		"region":            hb.GetRegion(),
		"type":              hb.GetType(),
		"labels":            hb.GetLabels(),
	}
	now := time.Now().UTC()
	if !lastSeen.IsZero() {
		iso := lastSeen.UTC().Format(time.RFC3339)
		resp["last_heartbeat_at"] = iso
		resp["last_heartbeat"] = iso
		age := int64(now.Sub(lastSeen.UTC()).Seconds())
		if age < 0 {
			age = 0
		}
		resp["heartbeat_age_seconds"] = age
	}
	trust := s.resolveWorkerTrust(ctx, hb.GetWorkerId())
	resp["session_state"] = trust.Reason
	resp["session_valid"] = trust.SessionValid
	if !trust.SessionExp.IsZero() {
		resp["session_exp_ms"] = trust.SessionExp.UnixMilli()
	}
	if trust.RevokedAt != nil {
		resp["session_revoked"] = true
	}
	if s.trustResolver != nil {
		// Session authority is wired — use it as the canonical online
		// signal, regardless of heartbeat staleness.
		resp["online"] = trust.IsAlive()
	} else {
		// Authority-mode / legacy: online is whatever the TTL gate says.
		resp["online"] = !lastSeen.IsZero() && now.Sub(lastSeen.UTC()) <= workerHeartbeatTTL
	}
	return resp
}

// workerStatusFromSummary wraps a snapshot-derived WorkerSummary into
// the canonical workerStatusResponse shape. It threads the snapshot's
// CapturedAt as the last-seen approximation (accurate within the
// snapshot interval).
func (s *server) workerStatusFromSummary(ctx context.Context, ws registry.WorkerSummary, capturedAt string) map[string]any {
	hb := &pb.Heartbeat{
		WorkerId:        ws.WorkerID,
		Pool:            ws.Pool,
		ActiveJobs:      ws.ActiveJobs,
		MaxParallelJobs: ws.MaxParallelJobs,
		Capabilities:    ws.Capabilities,
		CpuLoad:         ws.CpuLoad,
		GpuUtilization:  ws.GpuUtilization,
		MemoryLoad:      ws.MemoryLoad,
		Region:          ws.Region,
		Type:            ws.Type,
		Labels:          ws.Labels,
	}
	lastSeen := time.Time{}
	if t, err := time.Parse(time.RFC3339, capturedAt); err == nil {
		lastSeen = t
	}
	return s.workerStatusResponse(ctx, hb, lastSeen)
}

// resolveWorkerTrust reads WorkerTrustState for workerID, returning a
// store-unready sentinel when the resolver isn't wired or returns an
// error. Never panics — the caller can always surface the Reason
// string to operators.
func (s *server) resolveWorkerTrust(ctx context.Context, workerID string) scheduler.WorkerTrustState {
	if s == nil || s.trustResolver == nil {
		return scheduler.WorkerTrustState{Reason: scheduler.TrustReasonStoreUnready}
	}
	state, err := s.trustResolver.ResolveTrust(ctx, workerID)
	if err != nil {
		slog.Warn("worker trust resolve failed",
			"worker_id", workerID,
			"error", err,
		)
		return scheduler.WorkerTrustState{Reason: scheduler.TrustReasonStoreUnready}
	}
	return state
}

// poolUtilization calculates the utilization ratio for a pool snapshot.
// Returns 0 if the pool has no capacity.
func poolUtilization(ps registry.PoolSnapshot) float64 {
	if ps.Capacity <= 0 {
		return 0
	}
	return float64(ps.ActiveJobs) / float64(ps.Capacity)
}
