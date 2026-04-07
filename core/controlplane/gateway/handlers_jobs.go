package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/secrets"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const workerHeartbeatTTL = 30 * time.Second

// statusPipelineSampleLimit bounds the status pipeline aggregation scan cost.
const statusPipelineSampleLimit = int64(500)

// safeUnmarshal attempts JSON unmarshal and logs a warning on failure.
// Returns true if unmarshal succeeded, false otherwise.
func safeUnmarshal(data []byte, v any, field, jobID string) bool {
	if err := json.Unmarshal(data, v); err != nil {
		slog.Warn("job meta: corrupt JSON field",
			"field", field,
			"job_id", jobID,
			"error", err,
		)
		return false
	}
	return true
}

// --- Handlers ---

func (s *server) handleGetWorkers(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	// Prefer Redis snapshot (consistent across all replicas).
	workers, err := s.workersFromRedisSnapshot()
	if err == nil && workers != nil {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"items": workerSummariesToHeartbeats(workers)})
		return
	}
	if err != nil {
		slog.Warn("worker snapshot read failed, falling back to in-memory", "error", err)
	}
	// Fallback: in-memory heartbeat map (local replica only).
	out := s.activeWorkersSnapshot(time.Now().UTC())
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": out})
}

func (s *server) activeWorkersSnapshot(now time.Time) []*pb.Heartbeat {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	staleBefore := now.Add(-workerHeartbeatTTL)

	s.workerMu.Lock()
	defer s.workerMu.Unlock()

	out := make([]*pb.Heartbeat, 0, len(s.workers))
	for id, hb := range s.workers {
		if hb == nil {
			delete(s.workers, id)
			if s.workerSeen != nil {
				delete(s.workerSeen, id)
			}
			continue
		}
		// Backward compatibility: if last-seen is absent, retain the worker.
		if s.workerSeen != nil {
			if seen, ok := s.workerSeen[id]; ok && !seen.IsZero() && seen.Before(staleBefore) {
				delete(s.workers, id)
				delete(s.workerSeen, id)
				continue
			}
		}
		out = append(out, hb)
	}
	return out
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	uptimeSeconds := int64(0)
	if !s.started.IsZero() {
		uptimeSeconds = int64(now.Sub(s.started).Seconds())
	}

	// Prefer Redis snapshot count (consistent across replicas).
	workersCount := 0
	if snapWorkers, snapErr := s.workersFromRedisSnapshot(); snapErr == nil && snapWorkers != nil {
		workersCount = len(snapWorkers)
	} else {
		workersCount = len(s.activeWorkersSnapshot(now))
	}

	natsConnected := false
	natsStatus := "UNKNOWN"
	natsURL := ""
	if nb, ok := s.bus.(*bus.NatsBus); ok {
		natsConnected = nb.IsConnected()
		natsStatus = nb.Status()
		natsURL = nb.ConnectedURL()
	}

	redisOK := false
	redisErr := ""
	if s.jobStore == nil {
		redisErr = "job store unavailable"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		err := s.jobStore.Ping(ctx)
		cancel()
		if err != nil {
			redisErr = err.Error()
		} else {
			redisOK = true
		}
	}

	isAdmin := s.requireRole(r, "admin") == nil

	natsInfo := map[string]any{
		"connected": natsConnected,
		"status":    natsStatus,
	}
	if isAdmin {
		natsInfo["url"] = natsURL
	}

	redisInfo := map[string]any{
		"ok": redisOK,
	}
	if isAdmin && redisErr != "" {
		redisInfo["error"] = redisErr
	}

	tenantID := tenantFromRequest(r)
	if resolvedTenant, err := s.resolveTenant(r, ""); err == nil {
		tenantID = resolvedTenant
	}

	resp := map[string]any{
		"time":           now.Format(time.RFC3339),
		"uptime_seconds": uptimeSeconds,
		"build": map[string]any{
			"version": buildinfo.Version,
			"commit":  buildinfo.Commit,
			"date":    buildinfo.Date,
		},
		"nats":  natsInfo,
		"redis": redisInfo,
		"workers": map[string]any{
			"count": workersCount,
		},
		"pipeline": s.statusPipeline(r.Context(), tenantID),
	}
	if provider, ok := s.auth.(LicenseInfoProvider); ok {
		if info := provider.LicenseInfo(); info != nil {
			resp["license"] = info
		}
	}

	// Infrastructure details are admin-only to prevent information disclosure.
	if isAdmin {
		resp["instance_id"] = s.instanceID
		resp["rate_limiter"] = map[string]any{
			"mode": rateLimiterMode(s.apiRL),
		}

		var cbRedis redis.UniversalClient
		if s.jobStore != nil {
			cbRedis = s.jobStore.Client()
		}
		resp["circuit_breakers"] = map[string]any{
			"input":  readCircuitBreakerStatus(r.Context(), cbRedis, "cordum:cb:safety"),
			"output": readCircuitBreakerStatus(r.Context(), cbRedis, "cordum:cb:safety:output"),
		}

		if cbRedis != nil {
			if val, err := cbRedis.Get(r.Context(), "cordum:scheduler:input_fail_open_total").Int64(); err == nil {
				resp["input_fail_open_total"] = val
			}
		}

		haEnv := map[string]any{
			"redis_pool_size":      os.Getenv("REDIS_POOL_SIZE"),
			"redis_min_idle_conns": os.Getenv("REDIS_MIN_IDLE_CONNS"),
			"audit_transport":      os.Getenv("AUDIT_TRANSPORT"),
		}
		resp["ha_env"] = haEnv

		if snap, snapErr := s.snapshotFromRedis(); snapErr == nil && snap != nil {
			resp["snapshot_meta"] = map[string]any{
				"writer_id":   snap.WriterID,
				"captured_at": snap.CapturedAt,
			}
		}

		if s.instanceRegistry != nil && s.jobStore != nil {
			replicas, err := registry.ListAllInstances(r.Context(), s.jobStore.Client())
			if err == nil {
				resp["replicas"] = replicas
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) statusPipeline(ctx context.Context, tenantID string) map[string]any {
	pipeline := map[string]any{
		"pending":    int64(0),
		"dispatched": int64(0),
		"running":    int64(0),
		"succeeded":  int64(0),
		"failed":     int64(0),
		"denied":     int64(0),
	}
	if s == nil || s.jobStore == nil {
		return pipeline
	}

	listCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	jobs, err := s.jobStore.ListRecentJobs(listCtx, statusPipelineSampleLimit)
	if err != nil {
		slog.Warn("status pipeline list failed", "error", err)
		return pipeline
	}

	tenantID = strings.TrimSpace(tenantID)
	var pending, dispatched, running, succeeded, failed, denied int64
	for _, job := range jobs {
		if tenantID != "" && strings.TrimSpace(job.Tenant) != tenantID {
			continue
		}
		switch job.State {
		case model.JobStatePending, model.JobStateApproval, model.JobStateScheduled:
			pending++
		case model.JobStateDispatched:
			dispatched++
		case model.JobStateRunning:
			running++
		case model.JobStateSucceeded:
			succeeded++
		case model.JobStateDenied:
			denied++
		case model.JobStateFailed, model.JobStateCancelled, model.JobStateTimeout, model.JobStateQuarantined:
			failed++
		}
	}

	pipeline["pending"] = pending
	pipeline["dispatched"] = dispatched
	pipeline["running"] = running
	pipeline["succeeded"] = succeeded
	pipeline["failed"] = failed
	pipeline["denied"] = denied
	return pipeline
}

func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	limit, _ := parsePagination(r, 50)
	stateFilter := strings.ToUpper(r.URL.Query().Get("state"))
	topicFilter := r.URL.Query().Get("topic")
	tenantFilter := r.URL.Query().Get("tenant")
	teamFilter := r.URL.Query().Get("team")
	traceFilter := r.URL.Query().Get("trace_id")
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	updatedAfter := int64(0)
	if q := r.URL.Query().Get("updated_after"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedAfter = v
		}
	}
	updatedBefore := int64(0)
	if q := r.URL.Query().Get("updated_before"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			updatedBefore = v
		}
	}

	cursor = normalizeTimestampMicrosUpper(cursor)
	updatedAfter = normalizeTimestampMicrosLower(updatedAfter)
	updatedBefore = normalizeTimestampMicrosUpper(updatedBefore)

	resolvedTenant, err := s.resolveTenant(r, tenantFilter)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	tenantFilter = resolvedTenant

	var jobs []model.JobRecord
	if traceFilter != "" {
		jobs, err = s.jobStore.GetTraceJobs(r.Context(), traceFilter)
	} else if cursor > 0 {
		jobs, err = s.jobStore.ListRecentJobsByScore(r.Context(), cursor, limit)
	} else {
		jobs, err = s.jobStore.ListRecentJobs(r.Context(), limit)
	}
	if err != nil {
		slog.Error("job list failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	// client-side filter to avoid changing store signature
	filtered := make([]model.JobRecord, 0, len(jobs))
	for _, j := range jobs {
		if stateFilter != "" && strings.ToUpper(string(j.State)) != stateFilter {
			continue
		}
		if topicFilter != "" && j.Topic != topicFilter {
			continue
		}
		if tenantFilter != "" && j.Tenant != tenantFilter {
			continue
		}
		if teamFilter != "" && j.Team != teamFilter {
			continue
		}
		if updatedAfter > 0 && j.UpdatedAt < updatedAfter {
			continue
		}
		if updatedBefore > 0 && j.UpdatedAt > updatedBefore {
			continue
		}
		filtered = append(filtered, j)
	}
	w.Header().Set("Content-Type", "application/json")
	var nextCursor *int64
	if int64(len(filtered)) == limit {
		nc := filtered[len(filtered)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	writeJSON(w, map[string]any{
		"items":       filtered,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}

	// Single HGETALL replaces 15+ individual Redis calls.
	meta, err := s.jobStore.GetAllMeta(r.Context(), id)
	if err != nil || len(meta) == 0 {
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}
	state := model.JobState(meta["state"])
	if state == "" {
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}

	topic := meta["topic"]
	tenant := meta["tenant"]
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	actorID := meta["actor_id"]
	actorType := meta["actor_type"]
	idempotencyKey := meta["idempotency_key"]
	capability := meta["capability"]
	packID := meta["pack_id"]
	attempts := 0
	if raw := meta["attempts"]; raw != "" {
		var parseErr error
		attempts, parseErr = strconv.Atoi(raw)
		if parseErr != nil {
			slog.Warn("job meta: non-numeric attempts field",
				"job_id", id,
				"raw", raw,
				"error", parseErr,
			)
			attempts = 0
		}
	}

	var riskTags []string
	if raw := meta["risk_tags"]; raw != "" {
		safeUnmarshal([]byte(raw), &riskTags, "risk_tags", id)
	}
	var requires []string
	if raw := meta["requires"]; raw != "" {
		safeUnmarshal([]byte(raw), &requires, "requires", id)
	}

	// Build safety record from hash fields.
	safetyRecord := model.SafetyDecisionRecord{
		Decision:         model.SafetyDecision(meta["safety_decision"]),
		Reason:           meta["safety_reason"],
		RuleID:           meta["safety_rule_id"],
		PolicySnapshot:   meta["safety_snapshot"],
		ApprovalRequired: meta["safety_approval_required"] == "true",
		ApprovalRef:      meta["safety_approval_ref"],
		JobHash:          meta["safety_job_hash"],
	}
	if raw := meta["safety_remediations"]; raw != "" {
		safeUnmarshal([]byte(raw), &safetyRecord.Remediations, "safety_remediations", id)
	}

	// Build approval record from hash fields.
	approvalRecord := store.ApprovalRecord{
		ApprovedBy:     meta["approval_by"],
		ApprovedRole:   meta["approval_role"],
		Reason:         meta["approval_reason"],
		Note:           meta["approval_note"],
		PolicySnapshot: meta["approval_policy_snapshot"],
		JobHash:        meta["approval_job_hash"],
	}
	if raw := meta["approval_at"]; raw != "" {
		if parsed, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			approvalRecord.ApprovedAt = parsed
		}
	}

	// Output safety uses a dedicated key — separate call.
	outputSafety, _ := s.jobStore.GetOutputSafety(r.Context(), id)

	ctxPtr := store.PointerForKey(store.MakeContextKey(id))
	resPtr := meta["result_ptr"]

	var resultData any
	if resPtr != "" {
		// Attempt to fetch result payload
		if key, err := store.KeyFromPointer(resPtr); err == nil {
			if bytes, err := s.memStore.GetResult(r.Context(), key); err == nil {
				safeUnmarshal(bytes, &resultData, "result_data", id)
			}
		}
	}

	var contextData any
	if s.memStore != nil {
		if bytes, err := s.memStore.GetContext(r.Context(), store.MakeContextKey(id)); err == nil {
			safeUnmarshal(bytes, &contextData, "context_data", id)
		}
	}

	traceID := meta["trace_id"]
	labels := map[string]string{}
	workflowID := ""
	runID := ""
	stepID := ""
	if s.jobStore != nil {
		if req, err := s.jobStore.GetJobRequest(r.Context(), id); err == nil && req != nil {
			if req.WorkflowId != "" {
				workflowID = req.WorkflowId
			}
			if len(req.Labels) > 0 {
				for k, v := range req.Labels {
					labels[k] = v
				}
				if workflowID == "" {
					workflowID = req.Labels["workflow_id"]
				}
				runID = req.Labels["run_id"]
				stepID = req.Labels["step_id"]
			}
		}
	}

	errorMessage := ""
	errorStatus := ""
	errorCode := ""
	lastState := ""
	attemptsFromDLQ := 0
	if s.dlqStore != nil {
		if entry, err := s.dlqStore.Get(r.Context(), id); err == nil && entry != nil {
			errorMessage = strings.TrimSpace(entry.Reason)
			errorStatus = strings.TrimSpace(entry.Status)
			errorCode = strings.TrimSpace(entry.ReasonCode)
			lastState = strings.TrimSpace(entry.LastState)
			attemptsFromDLQ = entry.Attempts
		}
	}

	resp := map[string]any{
		"id":                  id,
		"state":               state,
		"trace_id":            traceID,
		"context_ptr":         ctxPtr,
		"context":             contextData,
		"result_ptr":          resPtr,
		"result":              resultData,
		"topic":               topic,
		"tenant":              tenant,
		"actor_id":            actorID,
		"actor_type":          actorType,
		"idempotency_key":     idempotencyKey,
		"capability":          capability,
		"pack_id":             packID,
		"risk_tags":           riskTags,
		"requires":            requires,
		"attempts":            attempts,
		"safety_decision":     string(safetyRecord.Decision),
		"safety_reason":       safetyRecord.Reason,
		"safety_rule_id":      safetyRecord.RuleID,
		"safety_snapshot":     safetyRecord.PolicySnapshot,
		"safety_constraints":  safetyRecord.Constraints,
		"safety_remediations": safetyRecord.Remediations,
		"safety_job_hash":     safetyRecord.JobHash,
		"approval_required":   safetyRecord.ApprovalRequired,
		"approval_ref":        safetyRecord.ApprovalRef,
		"labels":              labels,
		"workflow_id":         workflowID,
		"run_id":              runID,
		"step_id":             stepID,
	}
	if outputSafety.Decision != "" {
		resp["output_decision"] = string(outputSafety.Decision)
	}
	if outputSafety.Decision != "" || outputSafety.RedactedPtr != "" || outputSafety.OriginalPtr != "" || len(outputSafety.Findings) > 0 {
		resp["output_safety"] = outputSafety
	}
	// Only surface DLQ error info when the job is actually in a failure state.
	// Recovered/running jobs should not show stale DLQ error messages.
	isFailureState := state == model.JobStateFailed ||
		state == model.JobStateTimeout ||
		state == model.JobStateQuarantined
	if isFailureState {
		if errorMessage != "" {
			resp["error_message"] = errorMessage
		}
		if errorStatus != "" {
			resp["error_status"] = errorStatus
		}
		if errorCode != "" {
			resp["error_code"] = errorCode
		}
		if lastState != "" {
			resp["last_state"] = lastState
		}
		// Use DLQ attempt count only as fallback when meta has no value.
		if attempts == 0 && attemptsFromDLQ > 0 {
			resp["attempts"] = attemptsFromDLQ
		}
	}
	if approvalRecord.ApprovedBy != "" {
		resp["approval_by"] = approvalRecord.ApprovedBy
	}
	if approvalRecord.ApprovedRole != "" {
		resp["approval_role"] = approvalRecord.ApprovedRole
	}
	if approvalRecord.ApprovedAt != 0 {
		resp["approval_at"] = approvalRecord.ApprovedAt
	}
	if approvalRecord.Reason != "" {
		resp["approval_reason"] = approvalRecord.Reason
	}
	if approvalRecord.Note != "" {
		resp["approval_note"] = approvalRecord.Note
	}
	if approvalRecord.PolicySnapshot != "" {
		resp["approval_policy_snapshot"] = approvalRecord.PolicySnapshot
	}
	if approvalRecord.JobHash != "" {
		resp["approval_job_hash"] = approvalRecord.JobHash
	}
	writeJSON(w, resp)
}

func (s *server) handleListJobDecisions(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	id, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}
	if !s.requireJobTenantAccess(w, r, id) {
		return
	}
	limit, _ := parsePagination(r, 50)
	decisions, err := s.jobStore.ListSafetyDecisions(r.Context(), id, limit)
	if err != nil {
		slog.Error("job decisions list failed", "error", err, "job_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list decisions")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, decisions)
}

func (s *server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.memStore) {
		return
	}

	ptr := strings.TrimSpace(r.URL.Query().Get("ptr"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))

	if ptr == "" && key == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing ptr or key")
		return
	}

	if ptr != "" {
		ptr = strings.Trim(ptr, "\"'")
	}

	if key != "" {
		key = strings.Trim(key, "\"'")
		if strings.HasPrefix(key, "redis://") {
			ptr = key
			parsedKey, err := store.KeyFromPointer(key)
			if err != nil {
				writeErrorJSON(w, http.StatusBadRequest, "invalid key pointer")
				return
			}
			key = parsedKey
		}
	}

	if key == "" {
		parsedKey, err := store.KeyFromPointer(ptr)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid pointer")
			return
		}
		key = parsedKey
	}
	if ptr == "" {
		ptr = store.PointerForKey(key)
	}

	if auth := authFromRequest(r); auth != nil {
		slog.Info("memory read", "tenant", auth.Tenant, "principal", auth.PrincipalID, "key", key, "ptr", ptr)
	} else {
		slog.Info("memory read", "tenant", "", "principal", "", "key", key, "ptr", ptr)
	}

	// Tenant isolation: for ctx:{id} and res:{id} keys, extract the job ID
	// and verify the requesting user has access to the job's tenant.
	if (strings.HasPrefix(key, "ctx:") || strings.HasPrefix(key, "res:")) && s.jobStore != nil {
		var jobID string
		if strings.HasPrefix(key, "ctx:") {
			jobID = strings.TrimPrefix(key, "ctx:")
		} else {
			jobID = strings.TrimPrefix(key, "res:")
		}
		if jobID != "" {
			jobTenant, _ := s.jobStore.GetTenant(r.Context(), jobID)
			if jobTenant != "" {
				if err := s.requireTenantAccess(r, jobTenant); err != nil {
					writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
					return
				}
			}
		}
	}
	// Tenant isolation for mem: keys — derive owner from run or job ID
	// embedded in the key structure (mem:run:{runID}:* or mem:{jobID}:*).
	if strings.HasPrefix(key, "mem:") {
		memSuffix := strings.TrimPrefix(key, "mem:")
		if strings.HasPrefix(memSuffix, "run:") {
			// Pattern: mem:run:{runID}:events or mem:run:{runID}
			parts := strings.SplitN(strings.TrimPrefix(memSuffix, "run:"), ":", 2)
			runID := parts[0]
			if runID != "" && s.workflowStore != nil {
				if memRun, rerr := s.workflowStore.GetRun(r.Context(), runID); rerr == nil && memRun != nil {
					if err := s.requireTenantAccess(r, memRun.OrgID); err != nil {
						writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
						return
					}
				}
			}
		} else {
			// Pattern: mem:{jobID}:* — try job tenant lookup.
			parts := strings.SplitN(memSuffix, ":", 2)
			potentialID := parts[0]
			if potentialID != "" && s.jobStore != nil {
				if memTenant, jerr := s.jobStore.GetTenant(r.Context(), potentialID); jerr == nil && memTenant != "" {
					if err := s.requireTenantAccess(r, memTenant); err != nil {
						writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
						return
					}
				}
			}
		}
	}

	var (
		data []byte
		err  error
		kind string
	)
	switch {
	case strings.HasPrefix(key, "ctx:"):
		kind = "context"
		data, err = s.memStore.GetContext(r.Context(), key)
	case strings.HasPrefix(key, "res:"):
		kind = "result"
		data, err = s.memStore.GetResult(r.Context(), key)
	case strings.HasPrefix(key, "mem:"):
		kind = "memory"

		rs, ok := s.memStore.(*store.RedisStore)
		if !ok || rs.Client() == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "memory inspection unavailable: Redis store not configured")
			return
		}
		client := rs.Client()
		redisType, typeErr := client.Type(r.Context(), key).Result()
		if typeErr != nil {
			err = typeErr
			break
		}
		if redisType == "none" {
			writeErrorJSON(w, http.StatusNotFound, "not found")
			return
		}

		decodeMaybeJSON := func(v string) any {
			if strings.TrimSpace(v) == "" {
				return v
			}
			var parsed any
			if json.Unmarshal([]byte(v), &parsed) == nil {
				return parsed
			}
			return v
		}

		var payload any
		switch redisType {
		case "string":
			raw, getErr := client.Get(r.Context(), key).Bytes()
			if getErr != nil {
				err = getErr
				break
			}
			if utf8.Valid(raw) {
				payload = map[string]any{
					"redis_type": redisType,
					"value":      decodeMaybeJSON(string(raw)),
				}
			} else {
				payload = map[string]any{
					"redis_type": redisType,
					"base64":     base64.StdEncoding.EncodeToString(raw),
				}
			}
		case "list":
			items, lErr := client.LRange(r.Context(), key, 0, -1).Result()
			if lErr != nil {
				err = lErr
				break
			}
			decoded := make([]any, 0, len(items))
			for _, item := range items {
				decoded = append(decoded, decodeMaybeJSON(item))
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		case "set":
			items, sErr := client.SMembers(r.Context(), key).Result()
			if sErr != nil {
				err = sErr
				break
			}
			decoded := make([]any, 0, len(items))
			for _, item := range items {
				decoded = append(decoded, decodeMaybeJSON(item))
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		case "hash":
			items, hErr := client.HGetAll(r.Context(), key).Result()
			if hErr != nil {
				err = hErr
				break
			}
			decoded := make(map[string]any, len(items))
			for k, v := range items {
				decoded[k] = decodeMaybeJSON(v)
			}
			payload = map[string]any{
				"redis_type": redisType,
				"length":     len(decoded),
				"items":      decoded,
			}
		default:
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("unsupported redis key type: %s", redisType))
			return
		}
		if err != nil {
			break
		}
		data, err = json.Marshal(payload)
	default:
		writeErrorJSON(w, http.StatusBadRequest, "unsupported pointer key (only ctx:*, res:*, or mem:*)")
		return
	}
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
			return
		}
		slog.Error("memory read failed", "error", err, "key", key)
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := map[string]any{
		"pointer":    ptr,
		"key":        key,
		"kind":       kind,
		"size_bytes": len(data),
		"base64":     base64.StdEncoding.EncodeToString(data),
	}

	if utf8.Valid(data) {
		resp["text"] = string(data)
	}

	var jsonVal any
	if json.Unmarshal(data, &jsonVal) == nil {
		resp["json"] = jsonVal
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

type artifactPutRequest struct {
	ContentBase64 string            `json:"content_base64"`
	Content       string            `json:"content"`
	ContentType   string            `json:"content_type"`
	Retention     string            `json:"retention"`
	Labels        map[string]string `json:"labels"`
}

func (s *server) handlePutArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifactStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "artifact store unavailable")
		return
	}
	maxBytes := artifactMaxBytesLimit(r)
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1)
	}
	var req artifactPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "artifact too large")
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	var content []byte
	if req.ContentBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid base64 content")
			return
		}
		content = data
	} else {
		content = []byte(req.Content)
	}
	if len(content) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "content required")
		return
	}
	if maxBytes > 0 && int64(len(content)) > maxBytes {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "artifact too large")
		return
	}
	meta := artifacts.Metadata{
		ContentType: strings.TrimSpace(req.ContentType),
		Retention:   parseRetention(req.Retention),
		Labels:      req.Labels,
	}
	auth := authFromRequest(r)
	tenant := strings.TrimSpace(headerValue(r, "X-Tenant-ID"))
	allowCrossTenant := false
	if auth != nil {
		if auth.Tenant != "" {
			tenant = strings.TrimSpace(auth.Tenant)
		}
		allowCrossTenant = auth.AllowCrossTenant
	}
	if tenant != "" {
		if meta.Labels == nil {
			meta.Labels = map[string]string{}
		}
		if existing := strings.TrimSpace(meta.Labels["tenant_id"]); existing != "" && existing != tenant {
			if !allowCrossTenant {
				slog.Warn("SECURITY: tenant mismatch in job metadata labels",
					"component", "gateway", "auth_tenant", tenant,
					"label_tenant", existing, "remote_addr", r.RemoteAddr)
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
		// Always stamp the authenticated tenant — prevent client-injected overrides.
		meta.Labels["tenant_id"] = tenant
	}
	ptr, err := s.artifactStore.Put(r.Context(), content, meta)
	if err != nil {
		slog.Error("artifact put failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to store artifact")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"artifact_ptr": ptr,
		"size_bytes":   len(content),
	})
}

func (s *server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifactStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "artifact store unavailable")
		return
	}
	ptr := strings.TrimSpace(r.PathValue("ptr"))
	if ptr == "" {
		writeErrorJSON(w, http.StatusBadRequest, "artifact pointer required")
		return
	}
	content, meta, err := s.artifactStore.Get(r.Context(), ptr)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "artifact not found")
			return
		}
		slog.Error("artifact get failed", "error", err, "ptr", ptr)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to retrieve artifact")
		return
	}
	maxBytes := artifactMaxBytesLimit(r)
	if maxBytes > 0 {
		if meta.SizeBytes > maxBytes {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "artifact too large")
			return
		}
		if int64(len(content)) > maxBytes {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "artifact too large")
			return
		}
	}
	auth := authFromRequest(r)
	tenant := strings.TrimSpace(headerValue(r, "X-Tenant-ID"))
	allowCrossTenant := false
	if auth != nil {
		if auth.Tenant != "" {
			tenant = strings.TrimSpace(auth.Tenant)
		}
		allowCrossTenant = auth.AllowCrossTenant
	}
	if tenant != "" && !allowCrossTenant {
		labelTenant := strings.TrimSpace(meta.Labels["tenant_id"])
		if labelTenant == "" || labelTenant != tenant {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"artifact_ptr":   ptr,
		"content_base64": base64.StdEncoding.EncodeToString(content),
		"metadata":       meta,
	})
}

func (s *server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	id, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}
	if !s.requireJobTenantAccess(w, r, id) {
		return
	}

	state, err := s.jobStore.CancelJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "job not found")
			return
		}
		slog.Error("job cancel failed", "error", err, "job_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to cancel job")
		return
	}
	if state == "" {
		state = model.JobStateCancelled
	}
	if state != model.JobStateCancelled {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{
			"id":     id,
			"state":  state,
			"reason": "job already terminal",
		})
		return
	}

	// Broadcast a synthetic cancellation event for listeners.
	cancelPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:  id,
				Status: pb.JobStatus_JOB_STATUS_CANCELLED,
			},
		},
	}
	s.enqueueBusPacket(cancelPacket)
	// Publish cancel result so scheduler/system listeners can observe the cancel.
	if err := s.bus.Publish(capsdk.SubjectResult, cancelPacket); err != nil {
		slog.Error("publish cancel result failed", "job_id", id, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to publish cancel")
		return
	}

	// Cancel broadcast to workers.
	cancelReq := &pb.JobCancel{
		JobId:       id,
		Reason:      "cancelled via api",
		RequestedBy: "api-gateway",
	}
	cancelBusPacket := &pb.BusPacket{
		TraceId:         id,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobCancel{JobCancel: cancelReq},
	}
	if err := s.bus.Publish(capsdk.SubjectCancel, cancelBusPacket); err != nil {
		slog.Error("publish cancel broadcast failed", "job_id", id, "error", err)
	}

	cancelTopic, _ := s.jobStore.GetTopic(r.Context(), id)
	s.appendAuditEntryNamed(r.Context(), "cancel", "job", id, cancelTopic, policyActorID(r), policyRole(r), "cancel job "+id)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"id":    id,
		"state": model.JobStateCancelled,
	})
}

type remediateJobRequest struct {
	RemediationID string `json:"remediation_id"`
}

func (s *server) handleRemediateJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil || s.memStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store or bus unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	jobID, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}
	var body remediateJobRequest
	if err := decodeJSONBody(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONDecodeError(w, err, "invalid body")
		return
	}
	origReq, err := s.jobStore.GetJobRequest(r.Context(), jobID)
	if err != nil || origReq == nil {
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}
	if tenant := strings.TrimSpace(origReq.GetTenantId()); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	safetyRecord, err := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	if err != nil || len(safetyRecord.Remediations) == 0 {
		writeErrorJSON(w, http.StatusConflict, "no remediations available")
		return
	}
	var remediation *pb.PolicyRemediation
	if id := strings.TrimSpace(body.RemediationID); id != "" {
		for _, rem := range safetyRecord.Remediations {
			if rem != nil && rem.GetId() == id {
				remediation = rem
				break
			}
		}
		if remediation == nil {
			writeErrorJSON(w, http.StatusNotFound, "remediation not found")
			return
		}
	} else if len(safetyRecord.Remediations) == 1 {
		remediation = safetyRecord.Remediations[0]
	} else {
		writeErrorJSON(w, http.StatusBadRequest, "remediation_id required")
		return
	}

	newJobID := uuid.NewString()
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	if traceID == "" {
		traceID = uuid.NewString()
	}

	ctxPtr := origReq.GetContextPtr()
	if ctxPtr != "" {
		if key, err := store.KeyFromPointer(ctxPtr); err == nil {
			if data, err := s.memStore.GetContext(r.Context(), key); err == nil {
				newKey := store.MakeContextKey(newJobID)
				if err := s.memStore.PutContext(r.Context(), newKey, data); err == nil {
					ctxPtr = store.PointerForKey(newKey)
				}
			}
		}
	}

	newReq, ok := proto.Clone(origReq).(*pb.JobRequest)
	if !ok || newReq == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to clone job request")
		return
	}
	newReq.JobId = newJobID
	newReq.ParentJobId = origReq.GetJobId()
	if ctxPtr != "" {
		newReq.ContextPtr = ctxPtr
	}
	if remediation.GetReplacementTopic() != "" {
		newReq.Topic = remediation.GetReplacementTopic()
	}
	if newReq.Meta == nil {
		newReq.Meta = &pb.JobMetadata{}
	}
	if remediation.GetReplacementCapability() != "" {
		newReq.Meta.Capability = remediation.GetReplacementCapability()
	}

	labels := map[string]string{}
	for key, value := range origReq.GetLabels() {
		lower := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(lower, "approval_") || key == bus.LabelBusMsgID {
			continue
		}
		labels[key] = value
	}
	labels["remediation_of"] = origReq.GetJobId()
	if remediation.GetId() != "" {
		labels["remediation_id"] = remediation.GetId()
	}
	for key, value := range remediation.GetAddLabels() {
		if strings.TrimSpace(key) == "" {
			continue
		}
		labels[key] = value
	}
	for _, key := range remediation.GetRemoveLabels() {
		delete(labels, key)
	}
	if len(labels) > 0 {
		newReq.Labels = labels
		newReq.Meta.Labels = labels
	} else {
		newReq.Labels = nil
		newReq.Meta.Labels = nil
	}

	if err := s.jobStore.SetState(r.Context(), newJobID, model.JobStatePending); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job state")
		return
	}
	if err := s.jobStore.SetTopic(r.Context(), newJobID, newReq.GetTopic()); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to set job topic")
		return
	}
	if err := s.jobStore.SetTenant(r.Context(), newJobID, newReq.GetTenantId()); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to set job tenant")
		return
	}
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, newJobID); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to add job to trace")
		return
	}
	if err := s.jobStore.SetJobMeta(r.Context(), newReq); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job metadata")
		return
	}
	if err := s.jobStore.SetJobRequest(r.Context(), newReq); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job request")
		return
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: newReq,
		},
	}
	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "failed to enqueue job")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "remediate", "job", newJobID, newReq.GetTopic(), policyActorID(r), policyRole(r), "remediate job "+newJobID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"job_id": newJobID, "trace_id": traceID})
}

func (s *server) handleSubmitJobHTTP(w http.ResponseWriter, r *http.Request) {
	// RBAC: only admin and user roles may submit jobs.
	// This matches the gRPC handler (SubmitJob) which also allows "admin"
	// and "user" roles. Keep both in sync to avoid role asymmetry.
	if err := s.requireRole(r, "admin", "user"); err != nil {
		actorID, role := "anonymous", "none"
		if ac := authFromRequest(r); ac != nil {
			actorID, role = ac.PrincipalID, ac.Role
		}
		s.appendAuditEntryNamed(r.Context(), "submit_denied", "job", "", "", actorID, role, "job submit denied: "+err.Error())
		writeForbidden(w, r, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJobPayloadBytes)

	var req submitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}

	req.applyDefaults(s.tenant)
	if err := req.validate(s.tenant); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	reg, registryEmpty, err := s.topicRegistrationForSubmit(r.Context(), req.Topic)
	if err != nil {
		writeInternalError(w, r, "topic validation", err)
		return
	}
	if !registryEmpty {
		if reg == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{
				"error":      "unknown topic",
				"status":     http.StatusBadRequest,
				"error_code": "unknown_topic",
			})
			return
		}
		if reg.Status == "disabled" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{
				"error":      "topic is disabled",
				"status":     http.StatusBadRequest,
				"error_code": "topic_disabled",
			})
			return
		}
	}

	orgID, err := s.resolveTenant(r, req.OrgId)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	req.OrgId = orgID
	req.TenantId = orgID
	if violations, err := s.validateSubmitJobSchema(r.Context(), req, orgID, reg); err != nil {
		writeInternalError(w, r, "submit schema validation", err)
		return
	} else if len(violations) > 0 {
		mode := s.schemaValidationMode()
		if mode.Enforced() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{
				"error":      "schema_validation_failed",
				"status":     http.StatusBadRequest,
				"error_code": "schema_validation_failed",
				"violations": violations,
			})
			return
		}
		slog.Warn("submit payload violated topic input schema",
			"topic", req.Topic,
			"tenant_id", orgID,
			"schema_id", strings.TrimSpace(reg.InputSchemaID),
			"mode", mode,
			"violations", violations,
		)
	}
	principalID, err := s.resolvePrincipal(r, req.PrincipalId)
	if err != nil {
		writeForbidden(w, r, err)
		return
	}
	teamID := req.TeamId
	projectID := req.ProjectId

	key := strings.TrimSpace(req.IdempotencyKey)
	if key != "" && s.jobStore != nil {
		existingID, err := s.jobStore.GetJobByIdempotencyKeyScoped(r.Context(), orgID, key)
		if err == nil && existingID != "" {
			traceID, _ := s.jobStore.GetTraceID(r.Context(), existingID)
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, map[string]string{
				"job_id":   existingID,
				"trace_id": traceID,
			})
			return
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("idempotency lookup failed", "error", err)
		}
	}
	if err := s.enforceJobBackpressure(r.Context(), orgID, teamID); err != nil {
		var bp jobBackpressureError
		if errors.As(err, &bp) {
			writeErrorJSON(w, http.StatusTooManyRequests, bp.Error())
			return
		}
		slog.Error("job backpressure check failed", "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "job submission unavailable")
		return
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()

	// --- Secrets & memory validation (needed for policy check metadata) ---
	secretsPresent := secrets.ContainsSecretRefs(req.Prompt) || secrets.ContainsSecretRefs(req.Context)
	if secretsPresent {
		req.RiskTags = appendUniqueTag(req.RiskTags, "secrets")
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels["secrets_present"] = "true"
	}

	rawMemoryID := strings.TrimSpace(req.MemoryId)
	explicitMemoryID := store.NormalizeMemoryID(rawMemoryID)
	if rawMemoryID != "" && explicitMemoryID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "invalid memory id")
		return
	}
	if explicitMemoryID != "" {
		if err := s.enforceMemoryID(r.Context(), orgID, teamID, "", "", explicitMemoryID); err != nil {
			var perr memoryPolicyError
			if errors.As(err, &perr) {
				writeErrorJSON(w, perr.status, perr.msg)
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "memory policy check failed")
			return
		}
	}
	memoryID := explicitMemoryID
	if memoryID == "" {
		memoryID = deriveMemoryIDFromReq(req.Topic, "", jobID)
	}

	// --- Build metadata (needed for policy check) ---
	actorID := strings.TrimSpace(req.ActorId)
	if actorID == "" {
		actorID = principalID
	}
	meta := &pb.JobMetadata{
		TenantId:       orgID,
		ActorId:        actorID,
		ActorType:      parseActorType(req.ActorType),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		Capability:     strings.TrimSpace(req.Capability),
		RiskTags:       append([]string{}, req.RiskTags...),
		Requires:       append([]string{}, req.Requires...),
		PackId:         strings.TrimSpace(req.PackId),
	}
	if len(req.Labels) > 0 {
		meta.Labels = req.Labels
	}

	// --- Submit-time policy check (before any state persistence) ---
	policyResult := s.evaluateSubmitPolicy(r.Context(), jobID, req.Topic, orgID, principalID, req.Priority, meta, req.Labels, &pb.Budget{
		MaxInputTokens:  int64(req.MaxInputTokens),
		MaxOutputTokens: req.MaxOutputTokens,
		MaxTotalTokens:  req.MaxTotalTokens,
		DeadlineMs:      req.DeadlineMs,
	}, memoryID)
	if policyResult.Denied {
		reason := "policy denied"
		if policyResult.Reason != "" {
			reason = policyResult.Reason
		}
		s.appendAuditEntryNamed(r.Context(), "submit_denied", "job", jobID, req.Topic, policyActorID(r), policyRole(r), "submit-time policy denied: "+reason)
		writeErrorJSON(w, http.StatusForbidden, reason)
		return
	}
	if policyResult.Throttled {
		reason := "policy throttled"
		if policyResult.Reason != "" {
			reason = policyResult.Reason
		}
		s.appendAuditEntryNamed(r.Context(), "submit_throttled", "job", jobID, req.Topic, policyActorID(r), policyRole(r), "submit-time policy throttled: "+reason)
		w.Header().Set("Retry-After", "30")
		writeErrorJSON(w, http.StatusTooManyRequests, reason)
		return
	}

	// For approval_required, the job is created in APPROVAL state with full
	// persistence (idempotency, context, metadata, safety decision record) so the
	// approval endpoint can validate snapshot/hash and publish later. The job is
	// NOT published to SubjectSubmit until explicitly approved.
	if policyResult.ApprovalRequired {
		slog.Info("submit-time policy requires approval",
			"job_id", jobID, "topic", req.Topic, "reason", policyResult.Reason)
		s.appendAuditEntryNamed(r.Context(), "submit_approval_required", "job", jobID, req.Topic, policyActorID(r), policyRole(r), "submit-time policy requires approval: "+policyResult.Reason)

		// Reserve idempotency key to prevent duplicate approval jobs.
		if key != "" && s.jobStore != nil {
			reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(r.Context(), orgID, key, jobID)
			if err != nil {
				writeErrorJSON(w, http.StatusInternalServerError, "idempotency reservation failed")
				return
			}
			if !reserved {
				if existingID == "" {
					existingID, _ = s.jobStore.GetJobByIdempotencyKeyScoped(r.Context(), orgID, key)
				}
				if existingID != "" {
					w.Header().Set("Content-Type", "application/json")
					writeJSON(w, map[string]string{
						"job_id": existingID,
						"status": "approval_required",
					})
					return
				}
				writeErrorJSON(w, http.StatusConflict, "idempotency key already used")
				return
			}
		}

		// Build and persist the full job context + request so the approval
		// endpoint can retrieve, hash-validate, and publish them later.
		ctxKey := store.MakeContextKey(jobID)
		ctxPtr := store.PointerForKey(ctxKey)
		jobPriority := parsePriority(req.Priority)
		envVars := map[string]string{
			"tenant_id":         orgID,
			"max_input_tokens":  fmt.Sprintf("%d", req.MaxInputTokens),
			"max_output_tokens": fmt.Sprintf("%d", req.MaxOutputTokens),
		}
		if teamID != "" {
			envVars["team_id"] = teamID
		}
		if projectID != "" {
			envVars["project_id"] = projectID
		}
		if memoryID != "" {
			envVars["memory_id"] = memoryID
		}
		if req.Mode != "" {
			envVars["context_mode"] = req.Mode
		}
		payloadBytes, err := marshalSubmitJobPayload(req, orgID, time.Now().UTC())
		if err != nil {
			writeInternalError(w, r, "encode approval job payload", err)
			return
		}
		if s.memStore != nil {
			if err := s.memStore.PutContext(r.Context(), ctxKey, payloadBytes); err != nil {
				slog.Error("failed to persist approval job context", "job_id", jobID, "error", err)
			}
		}

		jobReq := &pb.JobRequest{
			JobId: jobID, Topic: req.Topic, Priority: jobPriority,
			ContextPtr: ctxPtr, AdapterId: req.AdapterId, Env: envVars,
			MemoryId: memoryID, TenantId: orgID, PrincipalId: principalID,
			Labels: req.Labels, Meta: meta,
			ContextHints: &pb.ContextHints{
				MaxInputTokens: req.MaxInputTokens, AllowSummarization: req.AllowSummarization,
				AllowRetrieval: req.AllowRetrieval, Tags: req.Tags,
			},
			Budget: &pb.Budget{
				MaxInputTokens: int64(req.MaxInputTokens), MaxOutputTokens: req.MaxOutputTokens,
				MaxTotalTokens: req.MaxTotalTokens, DeadlineMs: req.DeadlineMs,
			},
		}

		if s.jobStore != nil {
			if err := s.jobStore.SetState(r.Context(), jobID, model.JobStateApproval); err != nil {
				slog.Error("failed to set approval state", "job_id", jobID, "error", err)
				writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job state")
				return
			}
			if err := s.jobStore.SetTopic(r.Context(), jobID, req.Topic); err != nil {
				slog.Error("failed to set job topic", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetTenant(r.Context(), jobID, orgID); err != nil {
				slog.Error("failed to set job tenant", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.AddJobToTrace(r.Context(), traceID, jobID); err != nil {
				slog.Error("failed to add approval job to trace", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetJobMeta(r.Context(), jobReq); err != nil {
				slog.Error("failed to persist approval job metadata", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetJobRequest(r.Context(), jobReq); err != nil {
				slog.Error("failed to persist approval job request", "job_id", jobID, "error", err)
			}

			// Persist safety decision record so the approval endpoint can
			// validate policy snapshot stability and job request integrity.
			jobHash, _ := scheduler.HashJobRequest(jobReq)
			safetyRecord := model.SafetyDecisionRecord{
				Decision:         model.SafetyRequireApproval,
				Reason:           policyResult.Reason,
				RuleID:           policyResult.RuleId,
				PolicySnapshot:   policyResult.PolicySnapshot,
				Constraints:      policyResult.Constraints,
				ApprovalRequired: true,
				ApprovalRef:      jobID,
				JobHash:          jobHash,
				Remediations:     policyResult.Remediations,
				CheckedAt:        time.Now().UnixMicro(),
			}
			if err := s.jobStore.SetSafetyDecision(r.Context(), jobID, safetyRecord); err != nil {
				slog.Error("failed to persist safety decision for approval", "job_id", jobID, "error", err)
			}
		}

		w.Header().Set("X-Trace-Id", traceID)
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]string{
			"job_id": jobID,
			"status": "approval_required",
			"reason": policyResult.Reason,
		})
		return
	}

	// --- Idempotency reservation (after policy check to avoid orphaned keys) ---
	if key != "" && s.jobStore != nil {
		reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(r.Context(), orgID, key, jobID)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "idempotency reservation failed")
			return
		}
		if !reserved {
			if existingID == "" {
				existingID, err = s.jobStore.GetJobByIdempotencyKeyScoped(r.Context(), orgID, key)
			}
			if err == nil && existingID != "" {
				traceID, _ := s.jobStore.GetTraceID(r.Context(), existingID)
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, map[string]string{
					"job_id":   existingID,
					"trace_id": traceID,
				})
				return
			}
			if err != nil && !errors.Is(err, redis.Nil) {
				slog.Error("idempotency lookup failed", "error", err)
			}
			writeErrorJSON(w, http.StatusConflict, "idempotency key already used")
			return
		}
	}
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.Priority)

	envVars := map[string]string{
		"tenant_id": orgID, // Use OrgId as tenant_id in env for now
	}
	if teamID != "" {
		envVars["team_id"] = teamID
	}
	if projectID != "" {
		envVars["project_id"] = projectID
	}
	if memoryID != "" {
		envVars["memory_id"] = memoryID
	}
	if req.Mode != "" {
		envVars["context_mode"] = req.Mode
	}
	envVars["max_input_tokens"] = fmt.Sprintf("%d", req.MaxInputTokens)
	envVars["max_output_tokens"] = fmt.Sprintf("%d", req.MaxOutputTokens)

	payloadBytes, err := marshalSubmitJobPayload(req, orgID, time.Now().UTC())
	if err != nil {
		writeInternalError(w, r, "encode job payload", err)
		return
	}
	if s.memStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "memory store unavailable")
		return
	}
	if err := s.memStore.PutContext(r.Context(), ctxKey, payloadBytes); err != nil {
		slog.Error("failed to persist job context", "job_id", jobID, "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job context")
		return
	}

	// Set initial state
	if err := s.jobStore.SetState(r.Context(), jobID, model.JobStatePending); err != nil {
		slog.Error("failed to initialize job state", "job_id", jobID, "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job state")
		return
	}
	if err := s.jobStore.SetTopic(r.Context(), jobID, req.Topic); err != nil {
		slog.Error("failed to set job topic", "job_id", jobID, "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job metadata")
		return
	}
	if err := s.jobStore.SetTenant(r.Context(), jobID, orgID); err != nil {
		slog.Error("failed to set job tenant", "job_id", jobID, "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job metadata")
		return
	} // Use OrgId here too
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, jobID); err != nil {
		slog.Error("failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job metadata")
		return
	}

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       req.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   req.AdapterId,
		Env:         envVars,
		MemoryId:    memoryID,
		TenantId:    orgID,       // Use OrgId here
		PrincipalId: principalID, // Populated from new field
		Labels:      req.Labels,
		Meta:        meta,
		ContextHints: &pb.ContextHints{
			MaxInputTokens:     req.MaxInputTokens,
			AllowSummarization: req.AllowSummarization,
			AllowRetrieval:     req.AllowRetrieval,
			Tags:               req.Tags,
		},
		Budget: &pb.Budget{
			MaxInputTokens:  int64(req.MaxInputTokens),
			MaxOutputTokens: req.MaxOutputTokens,
			MaxTotalTokens:  req.MaxTotalTokens,
			DeadlineMs:      req.DeadlineMs,
		},
	}

	if s.jobStore != nil {
		if err := s.jobStore.SetJobMeta(r.Context(), jobReq); err != nil {
			slog.Error("failed to persist job metadata", "job_id", jobID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job metadata")
			return
		}
		if err := s.jobStore.SetJobRequest(r.Context(), jobReq); err != nil {
			slog.Error("failed to persist job request", "job_id", jobID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job metadata")
			return
		}
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway-http",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		slog.Error("job publish failed", "job_id", jobID, "error", err)
		_ = s.jobStore.SetState(r.Context(), jobID, model.JobStateFailed)
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to enqueue job")
		return
	}

	reqID := requestIdFromContext(r.Context())
	loggerFromContext(r.Context()).Info("job submitted",
		"jobId", jobID,
		"traceId", traceID,
		"requestId", reqID,
		"topic", req.Topic,
	)

	s.appendAuditEntryNamed(r.Context(), "submit", "job", jobID, req.Topic, policyActorID(r), policyRole(r), "submit job "+jobID)
	w.Header().Set("X-Trace-Id", traceID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{
		"job_id":   jobID,
		"trace_id": traceID,
	})
}

func (s *server) validateSubmitJobSchema(ctx context.Context, req submitJobRequest, tenantID string, reg *topicregistry.Registration) ([]schemaValidationError, error) {
	mode := s.schemaValidationMode()
	if !mode.Enabled() || reg == nil {
		return nil, nil
	}
	schemaID := strings.TrimSpace(reg.InputSchemaID)
	if schemaID == "" {
		return nil, nil
	}
	if s == nil || s.schemaRegistry == nil {
		return nil, fmt.Errorf("schema registry unavailable")
	}
	schemaJSON, err := s.schemaRegistry.Get(ctx, schemaID)
	if err != nil {
		return nil, fmt.Errorf("load input schema %s for topic %s: %w", schemaID, strings.TrimSpace(req.Topic), err)
	}
	payloadJSON, err := marshalSubmitJobPayload(req, tenantID, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("encode submit payload: %w", err)
	}
	violations, err := newSchemaValidator(s.schemaRegistry).Validate(ctx, schemaID, schemaJSON, payloadJSON)
	if err != nil {
		return nil, fmt.Errorf("validate submit payload for topic %s: %w", strings.TrimSpace(req.Topic), err)
	}
	return violations, nil
}

func marshalSubmitJobPayload(req submitJobRequest, tenantID string, createdAt time.Time) ([]byte, error) {
	payload := map[string]any{
		"prompt":     req.Prompt,
		"adapter_id": req.AdapterId,
		"priority":   req.Priority,
		"topic":      req.Topic,
		"created_at": createdAt.UTC().Format(time.RFC3339),
		"tenant_id":  tenantID,
	}
	if req.Context != nil {
		payload["context"] = req.Context
	}
	return json.Marshal(payload)
}

func (s *server) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing trace id")
		return
	}

	jobs, err := s.jobStore.GetTraceJobs(r.Context(), id)
	if err != nil {
		slog.Error("trace jobs lookup failed", "error", err, "trace_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to load trace")
		return
	}
	filtered := make([]model.JobRecord, 0, len(jobs))
	for _, job := range jobs {
		if err := s.requireTenantAccess(r, job.Tenant); err != nil {
			continue
		}
		filtered = append(filtered, job)
	}
	// Enrich with details if needed, but for now list is enough
	writeJSON(w, filtered)
}
