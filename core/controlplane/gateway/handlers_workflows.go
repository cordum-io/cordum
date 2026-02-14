package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---- Workflow REST Handlers ----

type createWorkflowRequest struct {
	ID          string             `json:"id"`
	OrgID       string             `json:"org_id"`
	TeamID      string             `json:"team_id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	TimeoutSec  int64              `json:"timeout_sec"`
	CreatedBy   string             `json:"created_by"`
	InputSchema map[string]any     `json:"input_schema"`
	Parameters  []map[string]any   `json:"parameters"`
	Steps       map[string]wf.Step `json:"steps"`
	Config      map[string]any     `json:"config"`
}

func (s *server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	orgID, err := s.resolveTenant(r, req.OrgID)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	req.OrgID = orgID

	// Preserve existing fields on upsert for callers that send partial payloads.
	if existing, err := s.workflowStore.GetWorkflow(r.Context(), req.ID); err == nil && existing != nil {
		if existing.OrgID != "" && existing.OrgID != req.OrgID {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		if req.OrgID == "" {
			req.OrgID = existing.OrgID
		}
		if req.TeamID == "" {
			req.TeamID = existing.TeamID
		}
		if req.Name == "" {
			req.Name = existing.Name
		}
		if req.Description == "" {
			req.Description = existing.Description
		}
		if req.Version == "" {
			req.Version = existing.Version
		}
		if req.TimeoutSec == 0 {
			req.TimeoutSec = existing.TimeoutSec
		}
		if req.CreatedBy == "" {
			req.CreatedBy = existing.CreatedBy
		}
		if req.InputSchema == nil && existing.InputSchema != nil {
			req.InputSchema = existing.InputSchema
		}
		if req.Parameters == nil && existing.Parameters != nil {
			req.Parameters = existing.Parameters
		}
		if req.Config == nil && existing.Config != nil {
			req.Config = existing.Config
		}
	}
	if err := validateWorkflowSteps(req.Steps); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDAG(req.Steps); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	wfDef := &wf.Workflow{
		ID:          req.ID,
		OrgID:       req.OrgID,
		TeamID:      req.TeamID,
		Name:        req.Name,
		Description: req.Description,
		Version:     req.Version,
		TimeoutSec:  req.TimeoutSec,
		Config:      req.Config,
		InputSchema: req.InputSchema,
		Parameters:  req.Parameters,
		CreatedBy:   req.CreatedBy,
		Steps:       map[string]*wf.Step{},
	}
	for id, step := range req.Steps {
		s := step
		s.ID = id
		wfDef.Steps[id] = &s
	}
	if err := s.workflowStore.SaveWorkflow(r.Context(), wfDef); err != nil {
		logging.Error("api-gateway", "workflow save failed", "error", err, "id", wfDef.ID)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "create", "workflow", wfDef.ID, wfDef.Name, policyActorID(r), policyRole(r), "create workflow "+wfDef.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": wfDef.ID})
}

func (s *server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing id")
		return
	}
	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), id)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, wfDef)
}

func (s *server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing id")
		return
	}
	delWfName := ""
	if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), id); err == nil && wfDef != nil {
		delWfName = wfDef.Name
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	if err := s.workflowStore.DeleteWorkflow(r.Context(), id); err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
			return
		}
		logging.Error("api-gateway", "workflow delete failed", "error", err, "id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "workflow", id, delWfName, policyActorID(r), policyRole(r), "delete workflow "+id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	list, err := s.workflowStore.ListWorkflows(r.Context(), orgID, 100)
	if err != nil {
		logging.Error("api-gateway", "workflow list failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, list)
}

func (s *server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing workflow id")
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "workflow not found")
			return
		}
		logging.Error("api-gateway", "workflow get failed", "error", err, "id", wfID)
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	if wfDef != nil {
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	if wfDef != nil && len(wfDef.InputSchema) > 0 {
		if err := schema.ValidateMap(wfDef.InputSchema, payload); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("input schema validation failed: %v", err))
			return
		}
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if wfDef != nil && wfDef.OrgID != "" && orgID != "" && orgID != wfDef.OrgID {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if orgID == "" && wfDef != nil {
		orgID = wfDef.OrgID
	}
	teamID := r.URL.Query().Get("team_id")
	if raw, ok := payload["memory_id"]; ok {
		memStr, ok := raw.(string)
		if !ok {
			writeErrorJSON(w, http.StatusBadRequest, "memory_id must be a string")
			return
		}
		norm := store.NormalizeMemoryID(memStr)
		if strings.TrimSpace(memStr) != "" && norm == "" {
			writeErrorJSON(w, http.StatusBadRequest, "invalid memory id")
			return
		}
		if norm != "" {
			if err := s.enforceMemoryID(r.Context(), orgID, teamID, wfID, "", norm); err != nil {
				var perr memoryPolicyError
				if errors.As(err, &perr) {
					writeErrorJSON(w, perr.status, perr.msg)
					return
				}
				writeErrorJSON(w, http.StatusInternalServerError, "memory policy check failed")
				return
			}
			payload["memory_id"] = norm
		}
	}
	dryRun := parseBool(r.URL.Query().Get("dry_run"))
	idempotencyKey := idempotencyKeyFromRequest(r)
	if idempotencyKey != "" {
		if existingID, err := s.workflowStore.GetRunByIdempotencyKey(r.Context(), idempotencyKey); err == nil && existingID != "" {
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, map[string]string{"run_id": existingID})
			return
		} else if err != nil && !errors.Is(err, redis.Nil) {
			logging.Error("api-gateway", "run idempotency lookup failed", "error", err)
		}
	}
	runID := uuid.NewString()
	reservedKey := false
	if idempotencyKey != "" {
		ok, err := s.workflowStore.TrySetRunIdempotencyKey(r.Context(), idempotencyKey, runID)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "idempotency reservation failed")
			return
		}
		if !ok {
			if existingID, err := s.workflowStore.GetRunByIdempotencyKey(r.Context(), idempotencyKey); err == nil && existingID != "" {
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, map[string]string{"run_id": existingID})
				return
			}
			writeErrorJSON(w, http.StatusConflict, "idempotency key already used")
			return
		}
		reservedKey = true
	}
	if limit := s.maxConcurrentRuns(r.Context(), orgID, teamID); limit > 0 {
		if count, err := s.workflowStore.CountActiveRuns(r.Context(), orgID); err == nil && count >= limit {
			writeErrorJSON(w, http.StatusTooManyRequests, "max concurrent runs reached")
			return
		}
	}
	run := &wf.WorkflowRun{
		ID:             runID,
		WorkflowID:     wfID,
		OrgID:          orgID,
		TeamID:         teamID,
		Input:          payload,
		Status:         wf.RunStatusPending,
		Steps:          map[string]*wf.StepRun{},
		DryRun:         dryRun,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		IdempotencyKey: idempotencyKey,
	}
	if dryRun {
		run.Metadata = map[string]string{"dry_run": "true"}
		run.Labels = map[string]string{"dry_run": "true"}
	}
	if err := s.workflowStore.CreateRun(r.Context(), run); err != nil {
		if reservedKey && idempotencyKey != "" {
			_ = s.workflowStore.DeleteRunIdempotencyKey(r.Context(), idempotencyKey)
		}
		logging.Error("api-gateway", "run create failed", "error", err, "run_id", runID)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to create run")
		return
	}
	// Kick off execution
	if s.workflowEng != nil {
		startErr := func() error {
			if s.jobStore != nil {
				lockKey := "cordum:wf:run:lock:" + runID
				token, lockErr := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
				if lockErr != nil {
					return s.workflowEng.StartRun(r.Context(), wfID, runID)
				} else if token != "" {
					defer func() {
						if err := s.jobStore.ReleaseLock(r.Context(), lockKey, token); err != nil {
							logging.Error("api-gateway", "release run lock failed", "run_id", runID, "error", err)
						}
					}()
					return s.workflowEng.StartRun(r.Context(), wfID, runID)
				}
				return nil
			}
			return s.workflowEng.StartRun(r.Context(), wfID, runID)
		}()
		if startErr != nil {
			logging.Error("api-gateway", "start workflow run failed", "workflow_id", wfID, "run_id", runID, "error", startErr)
		}
	}
	startWfName := ""
	if wfDef != nil {
		startWfName = wfDef.Name
	}
	s.appendAuditEntryNamed(r.Context(), "start", "run", runID, startWfName, policyActorID(r), policyRole(r), "start run "+runID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"run_id": runID})
}

type rerunRequest struct {
	FromStep string `json:"from_step"`
	DryRun   bool   `json:"dry_run"`
}

func (s *server) handleRerunRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowEng == nil || s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow engine unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}
	origRun, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil || origRun == nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "run not found")
			return
		}
		writeErrorJSON(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.requireTenantAccess(r, origRun.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if limit := s.maxConcurrentRuns(r.Context(), origRun.OrgID, origRun.TeamID); limit > 0 {
		if count, err := s.workflowStore.CountActiveRuns(r.Context(), origRun.OrgID); err == nil && count >= limit {
			writeErrorJSON(w, http.StatusTooManyRequests, "max concurrent runs reached")
			return
		}
	}
	var req rerunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	newID, err := s.workflowEng.RerunFrom(r.Context(), runID, strings.TrimSpace(req.FromStep), req.DryRun)
	if err != nil {
		logging.Error("api-gateway", "run rerun failed", "error", err, "run_id", runID)
		writeErrorJSON(w, http.StatusBadRequest, "rerun failed")
		return
	}
	newRun, err := s.workflowStore.GetRun(r.Context(), newID)
	if err != nil || newRun == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "new run not found")
		return
	}
	wfID := newRun.WorkflowID
	startErr := func() error {
		if s.jobStore != nil {
			lockKey := "cordum:wf:run:lock:" + newID
			token, lockErr := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
			if lockErr != nil {
				return s.workflowEng.StartRun(r.Context(), wfID, newID)
			} else if token != "" {
				defer func() {
					if err := s.jobStore.ReleaseLock(r.Context(), lockKey, token); err != nil {
						logging.Error("api-gateway", "release rerun lock failed", "run_id", newID, "error", err)
					}
				}()
				return s.workflowEng.StartRun(r.Context(), wfID, newID)
			}
			return nil
		}
		return s.workflowEng.StartRun(r.Context(), wfID, newID)
	}()
	if startErr != nil {
		logging.Error("api-gateway", "start rerun failed", "workflow_id", wfID, "run_id", newID, "error", startErr)
	}
	rerunWfName := ""
	if s.workflowStore != nil {
		if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID); err == nil && wfDef != nil {
			rerunWfName = wfDef.Name
		}
	}
	s.appendAuditEntryNamed(r.Context(), "rerun", "run", newID, rerunWfName, policyActorID(r), policyRole(r), "rerun run "+newID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"run_id": newID})
}

func (s *server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing workflow id")
		return
	}
	if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID); err == nil && wfDef != nil {
		if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	runs, err := s.workflowStore.ListRunsByWorkflow(r.Context(), wfID, 100)
	if err != nil {
		logging.Error("api-gateway", "run list failed", "error", err, "workflow_id", wfID)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, runs)
}

func (s *server) handleListAllRuns(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	workflowFilter := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
	orgFilter := strings.TrimSpace(r.URL.Query().Get("org_id"))
	teamFilter := strings.TrimSpace(r.URL.Query().Get("team_id"))
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

	cursor = normalizeTimestampSecondsUpper(cursor)
	updatedAfter = normalizeTimestampSecondsUpper(updatedAfter)
	updatedBefore = normalizeTimestampSecondsUpper(updatedBefore)

	resolvedOrg, err := s.resolveTenant(r, orgFilter)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	orgFilter = resolvedOrg

	runs, err := s.workflowStore.ListRuns(r.Context(), cursor, limit)
	if err != nil {
		logging.Error("api-gateway", "run list all failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	filtered := make([]*wf.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		if run == nil {
			continue
		}
		if statusFilter != "" && string(run.Status) != statusFilter {
			continue
		}
		if workflowFilter != "" && run.WorkflowID != workflowFilter {
			continue
		}
		if orgFilter != "" && run.OrgID != orgFilter {
			continue
		}
		if teamFilter != "" && run.TeamID != teamFilter {
			continue
		}
		updatedAt := run.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = run.CreatedAt
		}
		if updatedAfter > 0 && updatedAt.Unix() < updatedAfter {
			continue
		}
		if updatedBefore > 0 && updatedAt.Unix() > updatedBefore {
			continue
		}
		filtered = append(filtered, run)
	}
	var nextCursor *int64
	if int64(len(runs)) == limit {
		last := runs[len(runs)-1]
		if last != nil {
			ts := last.UpdatedAt
			if ts.IsZero() {
				ts = last.CreatedAt
			}
			if !ts.IsZero() {
				nc := ts.Unix() - 1
				nextCursor = &nc
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"items":       filtered,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}
	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, run)
}

func (s *server) handleGetRunTimeline(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}
	if run, err := s.workflowStore.GetRun(r.Context(), id); err == nil && run != nil {
		if err := s.requireTenantAccess(r, run.OrgID); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	limit := int64(200)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	events, err := s.workflowStore.ListTimelineEvents(r.Context(), id, limit)
	if err != nil {
		logging.Error("api-gateway", "run timeline failed", "error", err, "run_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to load timeline")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, events)
}

func (s *server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing id")
		return
	}
	delRunWfName := ""
	if run, err := s.workflowStore.GetRun(r.Context(), id); err == nil && run != nil {
		if err := s.requireTenantAccess(r, run.OrgID); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), run.WorkflowID); err == nil && wfDef != nil {
			delRunWfName = wfDef.Name
		}
	}
	if err := s.workflowStore.DeleteRun(r.Context(), id); err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
			return
		}
		logging.Error("api-gateway", "run delete failed", "error", err, "run_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete run")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "run", id, delRunWfName, policyActorID(r), policyRole(r), "delete run "+id)
	w.WriteHeader(http.StatusNoContent)
}

// jobStepTypes lists workflow step types that dispatch actual jobs and therefore
// need a safety policy evaluation during dry-run simulation.
var jobStepTypes = map[wf.StepType]bool{
	wf.StepTypeLLM:       true,
	wf.StepTypeWorker:    true,
	wf.StepTypeHTTP:      true,
	wf.StepTypeContainer: true,
	wf.StepTypeScript:    true,
}

type dryRunRequest struct {
	Input       map[string]any `json:"input"`
	Environment string         `json:"environment"`
}

type dryRunStepResult struct {
	StepID   string `json:"step_id"`
	StepName string `json:"step_name,omitempty"`
	StepType string `json:"step_type"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	RuleID   string `json:"rule_id,omitempty"`
}

type dryRunResponse struct {
	WorkflowID string             `json:"workflow_id"`
	Steps      []dryRunStepResult `json:"steps"`
}

func decisionString(d pb.DecisionType) string {
	switch d {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return "ALLOW"
	case pb.DecisionType_DECISION_TYPE_DENY:
		return "DENY"
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return "REQUIRE_APPROVAL"
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return "THROTTLE"
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return "ALLOW_WITH_CONSTRAINTS"
	default:
		return "UNKNOWN"
	}
}

func (s *server) handleWorkflowDryRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if s.safetyClient == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing workflow id")
		return
	}

	var body dryRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && err != io.EOF {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}

	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), id)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	tenant, _ := s.resolveTenant(r, "")
	results := make([]dryRunStepResult, 0, len(wfDef.Steps))

	for stepID, step := range wfDef.Steps {
		if step == nil {
			continue
		}
		result := dryRunStepResult{
			StepID:   stepID,
			StepName: step.Name,
			StepType: string(step.Type),
		}

		if !jobStepTypes[step.Type] {
			result.Decision = "N/A"
			result.Reason = "non-job step"
			results = append(results, result)
			continue
		}

		topic := step.Topic
		if topic == "" {
			topic = "job." + string(step.Type)
		}

		checkReq, err := buildPolicyCheckRequest(r.Context(), &policyCheckRequest{
			Topic:      topic,
			WorkflowId: id,
			StepId:     stepID,
			Tenant:     tenant,
			OrgId:      tenant,
			Labels:     step.RouteLabels,
		}, s.configSvc, s.tenant)
		if err != nil {
			result.Decision = "ERROR"
			result.Reason = fmt.Sprintf("failed to build request: %v", err)
			results = append(results, result)
			continue
		}

		resp, err := s.safetyClient.Simulate(r.Context(), checkReq)
		if err != nil {
			result.Decision = "ERROR"
			result.Reason = fmt.Sprintf("safety kernel error: %v", err)
			results = append(results, result)
			continue
		}

		result.Decision = decisionString(resp.GetDecision())
		result.Reason = resp.GetReason()
		result.RuleID = resp.GetRuleId()
		results = append(results, result)
	}

	// Sort by step ID for deterministic output (map iteration is random).
	sort.Slice(results, func(i, j int) bool {
		return results[i].StepID < results[j].StepID
	})

	writeJSON(w, dryRunResponse{
		WorkflowID: id,
		Steps:      results,
	})
}

// ---------------------------------------------------------------------------
// Workflow validation helpers (from workflow_validate.go)
// ---------------------------------------------------------------------------

const maxWorkflowStepIDLen = 64

var workflowStepIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateWorkflowStepID(stepID string) error {
	if stepID == "" {
		return errors.New("workflow step id required")
	}
	if len(stepID) > maxWorkflowStepIDLen {
		return fmt.Errorf("workflow step id %q exceeds %d characters", stepID, maxWorkflowStepIDLen)
	}
	if !workflowStepIDPattern.MatchString(stepID) {
		return fmt.Errorf("workflow step id %q must match %s", stepID, workflowStepIDPattern.String())
	}
	return nil
}

func validateWorkflowSteps(steps map[string]wf.Step) error {
	for id := range steps {
		if err := validateWorkflowStepID(id); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowStepMap(steps map[string]any) error {
	for id := range steps {
		if err := validateWorkflowStepID(id); err != nil {
			return err
		}
	}
	return nil
}

// validateDAG checks for circular dependencies and dangling references in a step graph.
// Uses DFS with three-color marking: 0=unvisited, 1=in-progress, 2=done.
func validateDAG(steps map[string]wf.Step) error {
	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully processed
	)
	color := make(map[string]int, len(steps))

	// Check for dangling references first.
	for id, step := range steps {
		for _, dep := range step.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if _, ok := steps[dep]; !ok {
				return fmt.Errorf("step %q depends on non-existent step %q", id, dep)
			}
		}
	}

	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		if color[id] == black {
			return nil
		}
		if color[id] == gray {
			// Build cycle description from path.
			cycle := append(path, id)
			return fmt.Errorf("circular dependency detected: %s", strings.Join(cycle, " -> "))
		}
		color[id] = gray
		step := steps[id]
		for _, dep := range step.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if err := visit(dep, append(path, id)); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}

	for id := range steps {
		if color[id] == white {
			if err := visit(id, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
