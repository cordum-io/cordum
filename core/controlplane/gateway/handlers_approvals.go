package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if s.workflowEng == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow engine unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	runID := r.PathValue("run_id")
	if runID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run_id")
		return
	}
	if s.workflowStore != nil {
		if run, err := s.workflowStore.GetRun(r.Context(), runID); err == nil && run != nil {
			if err := s.requireTenantAccess(r, run.OrgID); err != nil {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
	}

	// Serialize workflow run mutations with the same lock used by the workflow-engine reconciler.
	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		token, err := s.jobStore.TryAcquireLock(r.Context(), lockKey, 30*time.Second)
		if err != nil || token == "" {
			writeErrorJSON(w, http.StatusConflict, "workflow run is busy, retry")
			return
		}
		defer func() { _ = s.jobStore.ReleaseLock(context.Background(), lockKey, token) }()
	}

	if err := s.workflowEng.CancelRun(r.Context(), runID); err != nil {
		writeInternalError(w, r, "cancel run", err)
		return
	}
	cancelRunWfName := ""
	if s.workflowStore != nil {
		if run, err := s.workflowStore.GetRun(r.Context(), runID); err == nil && run != nil {
			if wfDef, err := s.workflowStore.GetWorkflow(r.Context(), run.WorkflowID); err == nil && wfDef != nil {
				cancelRunWfName = wfDef.Name
			}
		}
	}
	s.appendAuditEntryNamed(r.Context(), "cancel", "run", runID, cancelRunWfName, policyActorID(r), policyRole(r), "cancel run "+runID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	limit = clampListLimit(limit)
	cursor := time.Now().UnixNano() / int64(time.Microsecond)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	// Pending approvals (APPROVAL_REQUIRED state).
	jobs, err := s.jobStore.ListJobsByState(r.Context(), model.JobStateApproval, cursor, limit)
	if err != nil {
		writeInternalError(w, r, "list approvals", err)
		return
	}
	// Also include recently resolved approvals from post-approval states.
	// These are jobs that passed through the approval flow and have an
	// ApprovalRecord, now in PENDING (approved), DENIED, SUCCEEDED, or FAILED.
	includeResolved := r.URL.Query().Get("include_resolved") != "false"
	if includeResolved {
		resolvedLimit := limit - int64(len(jobs))
		if resolvedLimit < 0 {
			resolvedLimit = 0
		}
		seenIDs := make(map[string]bool, len(jobs))
		for _, j := range jobs {
			seenIDs[j.ID] = true
		}
		for _, state := range []model.JobState{model.JobStatePending, model.JobStateDenied, model.JobStateSucceeded, model.JobStateFailed} {
			if resolvedLimit <= 0 {
				break
			}
			resolved, err := s.jobStore.ListJobsByState(r.Context(), state, cursor, resolvedLimit)
			if err != nil {
				continue
			}
			for _, rj := range resolved {
				if seenIDs[rj.ID] {
					continue
				}
				// Only include jobs that have an approval record (went through approval flow).
				if approval, err := s.jobStore.GetApprovalRecord(r.Context(), rj.ID); err != nil || approval.ApprovedBy == "" {
					continue
				}
				jobs = append(jobs, rj)
				seenIDs[rj.ID] = true
				resolvedLimit--
			}
		}
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		if err := s.requireTenantAccess(r, job.Tenant); err != nil {
			continue
		}
		record, _ := s.jobStore.GetSafetyDecision(r.Context(), job.ID)
		item := map[string]any{
			"job":               job,
			"decision":          record.Decision,
			"policy_snapshot":   record.PolicySnapshot,
			"policy_rule_id":    record.RuleID,
			"policy_reason":     record.Reason,
			"constraints":       record.Constraints,
			"job_hash":          record.JobHash,
			"approval_required": record.ApprovalRequired,
			"approval_ref":      record.ApprovalRef,
		}
		// Merge approval resolution fields when an approval record exists.
		if approval, err := s.jobStore.GetApprovalRecord(r.Context(), job.ID); err == nil && approval.ApprovedBy != "" {
			item["resolved_by"] = approval.ApprovedBy
			item["resolved_comment"] = approval.Note
			item["resolution"] = approval.Reason
			if approval.ApprovedAt > 0 {
				item["resolved_at"] = approval.ApprovedAt
			}
		}
		// Enrich with workflow labels from the original job request so the
		// dashboard can distinguish gate approvals from policy approvals.
		if req, err := s.jobStore.GetJobRequest(r.Context(), job.ID); err == nil && req != nil {
			if req.Labels != nil {
				if v := req.Labels["workflow_id"]; v != "" {
					item["workflow_id"] = v
				}
				if v := req.Labels["run_id"]; v != "" {
					item["workflow_run_id"] = v
				}
				if v := req.Labels["step_id"]; v != "" {
					item["step_name"] = v
				}
				if v := req.Labels["gate_type"]; v != "" {
					item["gate_type"] = v
				}
			}
			// Dereference context_ptr to include the actual job input payload
			// (e.g. transfer amount, customer, reason) so approvers see what
			// they are approving.
			if ptr := strings.TrimSpace(req.GetContextPtr()); ptr != "" {
				item["context_ptr"] = ptr
				if s.memStore != nil {
					if key, err := store.KeyFromPointer(ptr); err == nil {
						if raw, err := s.memStore.GetContext(r.Context(), key); err == nil && len(raw) > 0 {
							var payload map[string]any
							if err := json.Unmarshal(raw, &payload); err == nil {
								item["job_input"] = payload
							}
						}
					}
				}
			}
		}
		items = append(items, item)
	}
	var nextCursor *int64
	if int64(len(jobs)) == limit {
		nc := jobs[len(jobs)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleApproveJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store or bus unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		if !errors.Is(err, io.EOF) {
			writeJSONDecodeError(w, err, "invalid body")
			return
		}
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing job_id")
		return
	}
	state, err := s.jobStore.GetState(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}
	if state != model.JobStateApproval {
		writeErrorJSON(w, http.StatusConflict, "job not awaiting approval")
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	req, err := s.jobStore.GetJobRequest(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "job request not found")
		return
	}
	safetyRecord, err := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "safety decision unavailable")
		return
	}
	if !safetyRecord.ApprovalRequired && safetyRecord.Decision != model.SafetyRequireApproval {
		writeErrorJSON(w, http.StatusConflict, "job not awaiting approval")
		return
	}
	if safetyRecord.JobHash == "" {
		writeErrorJSON(w, http.StatusConflict, "approval job hash unavailable")
		return
	}
	isWorkflowGate := strings.TrimSpace(req.GetTopic()) == capsdk.SubjectApprovalGate
	if !isWorkflowGate && req.Labels != nil && strings.EqualFold(strings.TrimSpace(req.Labels["gate_type"]), "workflow_approval") {
		isWorkflowGate = true
	}
	policySnapshot := strings.TrimSpace(safetyRecord.PolicySnapshot)
	if isWorkflowGate {
		if policySnapshot == "" {
			policySnapshot = "workflow-gate"
		}
	} else {
		if policySnapshot == "" {
			writeErrorJSON(w, http.StatusConflict, "approval policy snapshot unavailable")
			return
		}
		if s.safetyClient == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "safety kernel unavailable")
			return
		}
		snapResp, err := s.safetyClient.ListSnapshots(r.Context(), &pb.ListSnapshotsRequest{})
		if err != nil {
			writeBadGateway(w, r, "list safety snapshots", err)
			return
		}
		currentSnapshot := ""
		if snapResp != nil && len(snapResp.Snapshots) > 0 {
			currentSnapshot = strings.TrimSpace(snapResp.Snapshots[0])
		}
		if currentSnapshot == "" || snapshotBase(currentSnapshot) != snapshotBase(policySnapshot) {
			writeErrorJSON(w, http.StatusConflict, "policy snapshot changed; re-evaluate before approving")
			return
		}
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to hash job request")
		return
	}
	if hash != safetyRecord.JobHash {
		writeErrorJSON(w, http.StatusConflict, "job request changed; approval rejected")
		return
	}
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	req.Labels["approval_granted"] = "true"
	reason := strings.TrimSpace(body.Reason)
	note := strings.TrimSpace(body.Note)
	if reason != "" {
		req.Labels["approval_reason"] = reason
	}
	if note != "" {
		req.Labels["approval_note"] = note
	}
	req.Labels[bus.LabelBusMsgID] = "approval:" + uuid.NewString()
	if err := s.jobStore.SetJobRequest(r.Context(), req); err != nil {
		if strings.Contains(err.Error(), "transaction failed") {
			writeErrorJSON(w, http.StatusConflict, "concurrent approval conflict; retry")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to persist approval request")
		return
	}
	approvedBy := strings.TrimSpace(policyActorID(r))
	if approvedBy == "" {
		approvedBy = "system/unknown"
	}
	approvalRole := strings.TrimSpace(policyRole(r))
	if err := s.jobStore.SetApprovalRecord(r.Context(), jobID, store.ApprovalRecord{
		ApprovedBy:     approvedBy,
		ApprovedRole:   approvalRole,
		ApprovedAt:     time.Now().UnixMicro(),
		Reason:         reason,
		Note:           note,
		PolicySnapshot: policySnapshot,
		JobHash:        safetyRecord.JobHash,
	}); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to persist approval record")
		return
	}
	if err := s.jobStore.SetState(r.Context(), jobID, model.JobStatePending); err != nil {
		if strings.Contains(err.Error(), "transaction failed") {
			writeErrorJSON(w, http.StatusConflict, "concurrent approval conflict; retry")
			return
		}
		writeInternalError(w, r, "set job state", err)
		return
	}
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}
	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		writeBadGateway(w, r, "publish approval", err)
		return
	}
	s.appendAuditEntryNamed(r.Context(), "approve", "job", jobID, req.GetTopic(), policyActorID(r), policyRole(r), "approve job "+jobID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"job_id": jobID, "trace_id": traceID})
}

func (s *server) handleRejectJob(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil || s.bus == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store or bus unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		if !errors.Is(err, io.EOF) {
			writeJSONDecodeError(w, err, "invalid body")
			return
		}
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing job_id")
		return
	}
	state, err := s.jobStore.GetState(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}
	if state != model.JobStateApproval {
		writeErrorJSON(w, http.StatusConflict, "job not awaiting approval")
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	safetyRecord, _ := s.jobStore.GetSafetyDecision(r.Context(), jobID)
	reason := strings.TrimSpace(body.Reason)
	note := strings.TrimSpace(body.Note)
	approvedBy := strings.TrimSpace(policyActorID(r))
	if approvedBy == "" {
		approvedBy = "system/unknown"
	}
	approvalRole := strings.TrimSpace(policyRole(r))
	if err := s.jobStore.SetApprovalRecord(r.Context(), jobID, store.ApprovalRecord{
		ApprovedBy:     approvedBy,
		ApprovedRole:   approvalRole,
		ApprovedAt:     time.Now().UnixMicro(),
		Reason:         reason,
		Note:           note,
		PolicySnapshot: safetyRecord.PolicySnapshot,
		JobHash:        safetyRecord.JobHash,
	}); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to persist approval record")
		return
	}
	if err := s.jobStore.SetState(r.Context(), jobID, model.JobStateDenied); err != nil {
		writeInternalError(w, r, "set job state", err)
		return
	}
	traceID, _ := s.jobStore.GetTraceID(r.Context(), jobID)
	errorMessage := "approval rejected"
	if reason != "" {
		errorMessage = reason
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:         jobID,
				Status:        pb.JobStatus_JOB_STATUS_DENIED,
				ErrorCode:     "approval_rejected",
				ErrorCodeEnum: pb.ErrorCode_ERROR_CODE_SAFETY_DENIED,
				ErrorMessage:  errorMessage,
			},
		},
	}
	if err := s.bus.Publish(capsdk.SubjectDLQ, packet); err != nil {
		logging.Error("api-gateway", "publish dlq on approval reject failed", "job_id", jobID, "error", err)
	}
	rejectTopic, _ := s.jobStore.GetTopic(r.Context(), jobID)
	s.appendAuditEntryNamed(r.Context(), "reject", "job", jobID, rejectTopic, policyActorID(r), policyRole(r), "reject job "+jobID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"job_id": jobID})
}

// snapshotBase returns the base policy hash from a combined snapshot string.
// Combined snapshots have the form "base|cfg:hash"; this extracts just "base"
// so that config-overlay changes don't invalidate existing approvals.
func snapshotBase(snap string) string {
	if i := strings.Index(snap, "|"); i >= 0 {
		return snap[:i]
	}
	return snap
}
