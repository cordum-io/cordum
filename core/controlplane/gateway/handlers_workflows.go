package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/gateway/validation"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---- Workflow REST Handlers ----

const (
	workflowAdmissionLockTTL        = 10 * time.Second
	workflowAdmissionLockRetryDelay = 10 * time.Millisecond
	workflowAdmissionLockMaxWait    = 2 * time.Second
)

func workflowAdmissionLockKey(orgID string) string {
	return "cordum:wf:run:admission:" + strings.TrimSpace(orgID)
}

func cleanupRunIdempotencyReservation(ctx context.Context, idempotencyKey, runID, failureContext string, deleteFn func(context.Context, string) error) {
	if deleteFn == nil || strings.TrimSpace(idempotencyKey) == "" {
		return
	}
	if err := deleteFn(ctx, idempotencyKey); err != nil {
		slog.Error(failureContext, "key", idempotencyKey, "run_id", runID, "error", err)
	}
}

type runFailurePersistenceStore interface {
	GetRun(context.Context, string) (*wf.WorkflowRun, error)
	UpdateRun(context.Context, *wf.WorkflowRun) error
	AppendTimelineEvent(context.Context, string, *wf.TimelineEvent) error
}

func markRunFailedAfterStartError(ctx context.Context, workflowStore runFailurePersistenceStore, runID string, startErr error, updateLogMessage, timelineLogMessage string) {
	if workflowStore == nil || startErr == nil {
		return
	}
	failedRun, err := workflowStore.GetRun(ctx, runID)
	if err != nil || failedRun == nil {
		return
	}
	failedRun.Status = wf.RunStatusFailed
	now := time.Now().UTC()
	failedRun.CompletedAt = &now
	if failedRun.Error == nil {
		failedRun.Error = map[string]any{"message": startErr.Error()}
	} else {
		failedRun.Error["message"] = startErr.Error()
	}
	if updateErr := workflowStore.UpdateRun(ctx, failedRun); updateErr != nil {
		slog.Error(updateLogMessage, "run_id", runID, "error", updateErr)
	}
	if timelineErr := workflowStore.AppendTimelineEvent(ctx, failedRun.ID, &wf.TimelineEvent{
		Type:    "run_status",
		Status:  string(wf.RunStatusFailed),
		Message: startErr.Error(),
	}); timelineErr != nil {
		slog.Error(timelineLogMessage, "run_id", runID, "error", timelineErr)
	}
}

func (s *server) acquireWorkflowAdmissionLock(ctx context.Context, orgID string) (func(), error) {
	if s == nil || s.jobStore == nil || strings.TrimSpace(orgID) == "" {
		return func() {}, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, workflowAdmissionLockMaxWait)
	defer cancel()
	lockKey := workflowAdmissionLockKey(orgID)
	for {
		token, err := s.jobStore.TryAcquireLock(waitCtx, lockKey, workflowAdmissionLockTTL)
		if err != nil {
			return nil, err
		}
		if token != "" {
			return func() {
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), time.Second)
				defer releaseCancel()
				if err := s.jobStore.ReleaseLock(releaseCtx, lockKey, token); err != nil {
					slog.Error("release workflow admission lock failed", "org_id", orgID, "error", err)
				}
			}, nil
		}
		timer := time.NewTimer(workflowAdmissionLockRetryDelay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return nil, waitCtx.Err()
		case <-timer.C:
		}
	}
}

type createWorkflowRequest struct {
	ID             string             `json:"id"`
	OrgID          string             `json:"org_id"`
	TeamID         string             `json:"team_id"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Version        string             `json:"version"`
	TimeoutSec     int64              `json:"timeout_sec"`
	CreatedBy      string             `json:"created_by"`
	InputSchema    map[string]any     `json:"input_schema"`
	Parameters     []map[string]any   `json:"parameters"`
	PolicyOverride *string            `json:"policy_override"`
	Steps          map[string]wf.Step `json:"steps"`
	Config         map[string]any     `json:"config"`
}

func (s *server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.workflowStore) {
		return
	}
	var req createWorkflowRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	// SECURITY: Validate workflow name length to prevent DoS via oversized names.
	if len(req.Name) > 256 {
		writeErrorJSON(w, http.StatusBadRequest, "workflow name too long (max 256 chars)")
		return
	}
	// SECURITY: Validate timeout bounds to prevent nonsensical values.
	if req.TimeoutSec < 0 {
		writeErrorJSON(w, http.StatusBadRequest, "timeout_sec must be non-negative")
		return
	}
	const maxWorkflowTimeoutSec = 86400 * 7 // 7 days
	if req.TimeoutSec > maxWorkflowTimeoutSec {
		writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("timeout_sec too large (max %d)", maxWorkflowTimeoutSec))
		return
	}
	orgID, err := s.resolveTenant(r, req.OrgID)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	req.OrgID = orgID

	var existing *wf.Workflow
	// Preserve existing fields on upsert for callers that send partial payloads.
	if found, err := s.workflowStore.GetWorkflow(r.Context(), req.ID); err == nil && found != nil {
		existing = found
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
	policyOverride := ""
	if existing != nil {
		policyOverride = existing.PolicyOverride
	}
	if req.PolicyOverride != nil {
		policyOverride, err = validateWorkflowPolicyOverride(req.ID, *req.PolicyOverride)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateWorkflowSteps(req.Steps); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if limitErr := licensing.CheckWorkflowSteps(int64(len(req.Steps)), s.currentEntitlements()); limitErr != nil {
		writeTierLimitJSON(w, limitErr)
		return
	}
	if requestedApprovalMode := requestedApprovalModeForWorkflow(req.Steps); requestedApprovalMode != "" {
		if limitErr := licensing.CheckApprovalMode(requestedApprovalMode, s.approvalModeLimit()); limitErr != nil {
			writeTierLimitJSON(w, limitErr)
			return
		}
	}
	if err := validateDAG(req.Steps); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	wfDef := &wf.Workflow{
		ID:             req.ID,
		OrgID:          req.OrgID,
		TeamID:         req.TeamID,
		Name:           req.Name,
		Description:    req.Description,
		Version:        req.Version,
		TimeoutSec:     req.TimeoutSec,
		Config:         req.Config,
		InputSchema:    req.InputSchema,
		Parameters:     req.Parameters,
		PolicyOverride: policyOverride,
		CreatedBy:      req.CreatedBy,
		Steps:          map[string]*wf.Step{},
	}
	for id, step := range req.Steps {
		s := step
		s.ID = id
		wfDef.Steps[id] = &s
	}
	if err := s.workflowStore.SaveWorkflow(r.Context(), wfDef); err != nil {
		slog.Error("workflow save failed", "error", err, "id", wfDef.ID)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to save workflow")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "create", "workflow", wfDef.ID, wfDef.Name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "create workflow "+wfDef.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": wfDef.ID})
}

func (s *server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
		return
	}
	id, ok := requirePathParam(w, r, "id")
	if !ok {
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.workflowStore) {
		return
	}
	id, ok := requirePathParam(w, r, "id")
	if !ok {
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
		slog.Error("workflow delete failed", "error", err, "id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete workflow")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "workflow", id, delWfName, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "delete workflow "+id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	list, err := s.workflowStore.ListWorkflows(r.Context(), orgID, 100)
	if err != nil {
		slog.Error("workflow list failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list workflows")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, list)
}

func (s *server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.workflowStore) {
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing workflow id")
		return
	}
	var payload map[string]any
	if err := decodeJSONBody(w, r, &payload); err != nil && !errors.Is(err, io.EOF) {
		writeJSONDecodeError(w, err, "invalid json")
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
		slog.Error("workflow get failed", "error", err, "id", wfID)
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
	configLimit := s.maxConcurrentRuns(r.Context(), orgID, teamID)
	tierLimit := s.currentEntitlements().MaxActiveWorkflows
	var releaseAdmissionLock func()
	if configLimit > 0 || tierLimit > 0 {
		releaseAdmissionLock, err = s.acquireWorkflowAdmissionLock(r.Context(), orgID)
		if err != nil {
			slog.Error("workflow admission lock failed", "org_id", orgID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "workflow concurrency gate unavailable")
			return
		}
		defer func() {
			if releaseAdmissionLock != nil {
				releaseAdmissionLock()
			}
		}()
	}
	idempotencyKey := idempotencyKeyFromRequest(r)
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
			} else if err != nil && !errors.Is(err, redis.Nil) {
				slog.Error("run idempotency lookup failed", "error", err)
			}
			writeErrorJSON(w, http.StatusConflict, "idempotency key already used")
			return
		}
		reservedKey = true
	}
	if configLimit > 0 || tierLimit > 0 {
		count, err := s.workflowStore.CountActiveRuns(r.Context(), orgID)
		if err != nil {
			if reservedKey && idempotencyKey != "" {
				cleanupRunIdempotencyReservation(
					r.Context(),
					idempotencyKey,
					runID,
					"failed to cleanup idempotency key after active run count failure",
					s.workflowStore.DeleteRunIdempotencyKey,
				)
			}
			slog.Error("count active runs failed", "org_id", orgID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "failed to enforce max concurrent runs")
			return
		}
		if configLimit > 0 && count >= configLimit {
			if reservedKey && idempotencyKey != "" {
				cleanupRunIdempotencyReservation(
					r.Context(),
					idempotencyKey,
					runID,
					"failed to cleanup idempotency key after concurrency limit rejection",
					s.workflowStore.DeleteRunIdempotencyKey,
				)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(w, map[string]any{
				"error":   "max concurrent workflow runs exceeded",
				"status":  http.StatusTooManyRequests,
				"current": count,
				"limit":   configLimit,
			})
			return
		}
		if limitErr := licensing.CheckActiveWorkflows(int64(count+1), s.currentEntitlements()); limitErr != nil {
			if reservedKey && idempotencyKey != "" {
				cleanupRunIdempotencyReservation(
					r.Context(),
					idempotencyKey,
					runID,
					"failed to cleanup idempotency key after tier workflow limit rejection",
					s.workflowStore.DeleteRunIdempotencyKey,
				)
			}
			writeTierLimitJSON(w, limitErr)
			return
		}
	}
	reqID := requestIdFromContext(r.Context())
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
		Metadata:       map[string]string{},
	}
	if reqID != "" {
		run.Metadata["request_id"] = reqID
	}
	if dryRun {
		run.Metadata["dry_run"] = "true"
		run.Labels = map[string]string{"dry_run": "true"}
	}
	if err := s.workflowStore.CreateRun(r.Context(), run); err != nil {
		if reservedKey && idempotencyKey != "" {
			cleanupRunIdempotencyReservation(
				r.Context(),
				idempotencyKey,
				runID,
				"failed to cleanup idempotency key after run creation failure",
				s.workflowStore.DeleteRunIdempotencyKey,
			)
		}
		slog.Error("run create failed", "error", err, "run_id", runID)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to create run")
		return
	}
	if releaseAdmissionLock != nil {
		releaseAdmissionLock()
		releaseAdmissionLock = nil
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
							slog.Error("release run lock failed", "run_id", runID, "error", err)
						}
					}()
					return s.workflowEng.StartRun(r.Context(), wfID, runID)
				}
				return nil
			}
			return s.workflowEng.StartRun(r.Context(), wfID, runID)
		}()
		if startErr != nil {
			slog.Error("start workflow run failed", "workflow_id", wfID, "run_id", runID, "error", startErr)
			markRunFailedAfterStartError(r.Context(), s.workflowStore, runID, startErr, "failed to persist run failure status", "failed to append run failure timeline event")
		}
		if startErr == nil && s.workflowStore != nil {
			if updated, err := s.workflowStore.GetRun(r.Context(), runID); err == nil && updated != nil && updated.Status == wf.RunStatusFailed {
				slog.Warn("run failed during initialization", "run_id", runID, "workflow_id", wfID)
			}
		}
	}
	startWfName := ""
	if wfDef != nil {
		startWfName = wfDef.Name
	}
	loggerFromContext(r.Context()).Info("workflow run started",
		"runId", runID,
		"workflowId", wfID,
		"requestId", reqID,
	)
	s.appendAuditEntryNamed(r.Context(), "start", "run", runID, startWfName, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "start run "+runID)
	// For workflows, the runId serves as the traceId for the entire execution tree.
	w.Header().Set("X-Trace-Id", runID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"run_id": runID})
}

type rerunRequest struct {
	FromStep string `json:"from_step"`
	DryRun   bool   `json:"dry_run"`
}

func (s *server) handleRerunRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.workflowEng, s.workflowStore) {
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
	configLimit := s.maxConcurrentRuns(r.Context(), origRun.OrgID, origRun.TeamID)
	tierLimit := s.currentEntitlements().MaxActiveWorkflows
	var releaseAdmissionLock func()
	if configLimit > 0 || tierLimit > 0 {
		releaseAdmissionLock, err = s.acquireWorkflowAdmissionLock(r.Context(), origRun.OrgID)
		if err != nil {
			slog.Error("workflow admission lock failed", "org_id", origRun.OrgID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "workflow concurrency gate unavailable")
			return
		}
		defer func() {
			if releaseAdmissionLock != nil {
				releaseAdmissionLock()
			}
		}()
		count, err := s.workflowStore.CountActiveRuns(r.Context(), origRun.OrgID)
		if err != nil {
			slog.Error("count active reruns failed", "org_id", origRun.OrgID, "error", err)
			writeErrorJSON(w, http.StatusServiceUnavailable, "failed to enforce max concurrent runs")
			return
		}
		if configLimit > 0 && count >= configLimit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(w, map[string]any{
				"error":   "max concurrent workflow runs exceeded",
				"status":  http.StatusTooManyRequests,
				"current": count,
				"limit":   configLimit,
			})
			return
		}
		if limitErr := licensing.CheckActiveWorkflows(int64(count+1), s.currentEntitlements()); limitErr != nil {
			writeTierLimitJSON(w, limitErr)
			return
		}
	}
	var req rerunRequest
	if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	newID, err := s.workflowEng.RerunFrom(r.Context(), runID, strings.TrimSpace(req.FromStep), req.DryRun)
	if err != nil {
		slog.Error("run rerun failed", "error", err, "run_id", runID)
		writeErrorJSON(w, http.StatusBadRequest, "rerun failed")
		return
	}
	if releaseAdmissionLock != nil {
		releaseAdmissionLock()
		releaseAdmissionLock = nil
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
						slog.Error("release rerun lock failed", "run_id", newID, "error", err)
					}
				}()
				return s.workflowEng.StartRun(r.Context(), wfID, newID)
			}
			return nil
		}
		return s.workflowEng.StartRun(r.Context(), wfID, newID)
	}()
	if startErr != nil {
		slog.Error("start rerun failed", "workflow_id", wfID, "run_id", newID, "error", startErr)
		markRunFailedAfterStartError(r.Context(), s.workflowStore, newID, startErr, "failed to persist rerun failure status", "failed to append rerun failure timeline event")
	}
	rerunWfName := ""
	if s.workflowStore != nil {
		if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID); err == nil && wfDef != nil {
			rerunWfName = wfDef.Name
		}
	}
	s.appendAuditEntryNamed(r.Context(), "rerun", "run", newID, rerunWfName, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "rerun run "+newID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"run_id": newID})
}

func (s *server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
		return
	}
	wfID := r.PathValue("id")
	if wfID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing workflow id")
		return
	}
	wfDef, err := s.workflowStore.GetWorkflow(r.Context(), wfID)
	if err != nil || wfDef == nil {
		writeErrorJSON(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err := s.requireTenantAccess(r, wfDef.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	runs, err := s.workflowStore.ListRunsByWorkflow(r.Context(), wfID, 100)
	if err != nil {
		slog.Error("run list failed", "error", err, "workflow_id", wfID)
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
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
		return
	}
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	limit = clampListLimit(limit)
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
		slog.Error("run list all failed", "error", err)
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
				nc := ts.UnixMicro() - 1
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

// runDetailResponse embeds a WorkflowRun with an optional timers field.
type runDetailResponse struct {
	*wf.WorkflowRun
	Timers []wf.DelayTimerInfo `json:"timers,omitempty"`
}

func (s *server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
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

	resp := runDetailResponse{WorkflowRun: run}

	// Best-effort: attach any pending delay timer for this run.
	if timer, err := s.workflowStore.GetDelayTimer(r.Context(), run.WorkflowID, run.ID); err == nil && timer != nil {
		resp.Timers = []wf.DelayTimerInfo{*timer}
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleGetRunTimeline(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermWorkflowsRead) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}
	run, err := s.workflowStore.GetRun(r.Context(), id)
	if err != nil || run == nil {
		writeErrorJSON(w, http.StatusNotFound, "run not found")
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	limit, _ := parsePagination(r, 200)
	events, err := s.workflowStore.ListTimelineEvents(r.Context(), id, limit)
	if err != nil {
		slog.Error("run timeline failed", "error", err, "run_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to load timeline")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, events)
}

func (s *server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.workflowStore) {
		return
	}
	id, ok := requirePathParam(w, r, "id")
	if !ok {
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
		// Cancel in-flight jobs before deleting the run data. This prevents
		// orphaned NATS messages from completed jobs arriving after deletion.
		// Best-effort: if cancel fails, proceed with deletion anyway — the
		// gateway-side ErrRunNotFound discard handles any stragglers.
		if s.workflowEng != nil && run.Status != wf.RunStatusSucceeded && run.Status != wf.RunStatusFailed && run.Status != wf.RunStatusDenied && run.Status != wf.RunStatusCancelled && run.Status != wf.RunStatusTimedOut {
			if err := s.workflowEng.CancelRun(r.Context(), id); err != nil {
				slog.Warn("pre-delete cancel failed, proceeding with deletion",
					"run_id", id, "error", err)
			}
		}
	}
	if err := s.workflowStore.DeleteRun(r.Context(), id); err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
			return
		}
		slog.Error("run delete failed", "error", err, "run_id", id)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete run")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "run", id, delRunWfName, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "delete run "+id)
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermWorkflowsWrite, []string{"admin"}, s.safetyClient) {
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
			slog.Error("dry-run build request failed", "step_id", stepID, "error", err)
			result.Decision = "ERROR"
			result.Reason = "internal error during dry-run evaluation"
			results = append(results, result)
			continue
		}

		resp, err := s.safetyClient.Simulate(r.Context(), checkReq)
		if err != nil {
			slog.Error("dry-run safety kernel error", "step_id", stepID, "error", err)
			result.Decision = "ERROR"
			result.Reason = "safety evaluation unavailable"
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

// validateWorkflowStepID delegates to the shared validation package.
func validateWorkflowStepID(stepID string) error {
	return validation.WorkflowStepID(stepID)
}

// validateWorkflowStepMap delegates to the shared validation package.
func validateWorkflowStepMap(steps map[string]any) error {
	return validation.WorkflowStepMap(steps)
}

func validateWorkflowSteps(steps map[string]wf.Step) error {
	for id := range steps {
		if err := validation.WorkflowStepID(id); err != nil {
			return err
		}
	}
	return nil
}

func requestedApprovalModeForWorkflow(steps map[string]wf.Step) string {
	approvalIDs := make(map[string]struct{})
	for stepID, step := range steps {
		if step.Type == wf.StepTypeApproval {
			approvalIDs[stepID] = struct{}{}
		}
	}
	switch len(approvalIDs) {
	case 0:
		return ""
	case 1:
		return string(licensing.ApprovalModeSingle)
	}
	for stepID := range approvalIDs {
		step := steps[stepID]
		for _, dep := range step.DependsOn {
			if _, ok := approvalIDs[strings.TrimSpace(dep)]; ok {
				return string(licensing.ApprovalModeCustom)
			}
		}
		if target := strings.TrimSpace(step.OnError); target != "" {
			if _, ok := approvalIDs[target]; ok {
				return string(licensing.ApprovalModeCustom)
			}
		}
	}
	return string(licensing.ApprovalModeMulti)
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
				return fmt.Errorf("step %q depends on non-existent step %q", validation.TruncateForError(id, 256), validation.TruncateForError(dep, 256))
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
