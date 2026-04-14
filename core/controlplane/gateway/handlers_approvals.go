package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type approvalDecisionSummary struct {
	Source           string   `json:"source,omitempty"`
	Completeness     string   `json:"completeness,omitempty"`
	ContextStatus    string   `json:"context_status,omitempty"`
	Title            string   `json:"title"`
	Subject          string   `json:"subject,omitempty"`
	Why              string   `json:"why,omitempty"`
	NextEffect       string   `json:"next_effect,omitempty"`
	Amount           *float64 `json:"amount,omitempty"`
	Currency         string   `json:"currency,omitempty"`
	Vendor           string   `json:"vendor,omitempty"`
	ItemCount        *int     `json:"item_count,omitempty"`
	ItemsPreview     []string `json:"items_preview,omitempty"`
	EscalationReason string   `json:"escalation_reason,omitempty"`
	MissingFields    []string `json:"missing_fields,omitempty"`
}

func approvalWorkflowMetadata(payload map[string]any) map[string]any {
	if payload == nil || strings.TrimSpace(approvalStringFromAny(payload["kind"])) != wf.ApprovalContextKindWorkflow {
		return nil
	}
	meta, _ := payload["workflow"].(map[string]any)
	return meta
}

func approvalDecisionPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	if strings.TrimSpace(approvalStringFromAny(payload["kind"])) == wf.ApprovalContextKindWorkflow {
		decision, _ := payload["decision"].(map[string]any)
		return decision
	}
	return payload
}

func approvalStringFromAny(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case map[string]any:
		for _, key := range []string{"name", "title", "label", "summary", "id"} {
			if candidate := strings.TrimSpace(approvalStringFromAny(value[key])); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func firstStringValue(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if candidate := approvalStringFromAny(raw[key]); candidate != "" {
			return candidate
		}
	}
	return ""
}

func numberFromAny(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	}
	return 0, false
}

func firstNumberValue(raw map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := numberFromAny(raw[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func summarizeApprovalItems(raw any) (int, []string) {
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return 0, nil
	}
	preview := make([]string, 0, min(len(values), 3))
	for _, value := range values {
		if candidate := approvalStringFromAny(value); candidate != "" {
			preview = append(preview, candidate)
			continue
		}
		if item, ok := value.(map[string]any); ok {
			if candidate := firstStringValue(item, "name", "title", "sku", "id", "category"); candidate != "" {
				preview = append(preview, candidate)
			}
		}
		if len(preview) == 3 {
			break
		}
	}
	return len(values), preview
}

func formatApprovalAmount(amount float64) string {
	if math.Trunc(amount) == amount {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

func buildApprovalDecisionSummary(req *pb.JobRequest, policyReason string, payload map[string]any, contextStatus string) approvalDecisionSummary {
	workflowMeta := approvalWorkflowMetadata(payload)
	decision := approvalDecisionPayload(payload)
	labels := req.GetLabels()
	stepName := firstStringValue(workflowMeta, "step_name")
	if stepName == "" {
		stepName = strings.TrimSpace(labels["step_id"])
	}
	workflowID := firstStringValue(workflowMeta, "workflow_id")
	if workflowID == "" {
		workflowID = strings.TrimSpace(labels["workflow_id"])
	}
	topic := strings.TrimSpace(req.GetTopic())
	contextPtr := strings.TrimSpace(req.GetContextPtr())

	source := "policy_only"
	if contextPtr != "" || workflowMeta != nil {
		source = "workflow_payload"
	} else if workflowID != "" || stepName != "" {
		source = "workflow_labels"
	}

	subject := firstStringValue(decision, "title", "subject", "summary", "request_name", "name", "approval_for")
	vendor := firstStringValue(decision, "vendor", "merchant", "supplier", "payee", "counterparty")
	amount, amountOK := firstNumberValue(decision, "amount", "total", "value", "subtotal")
	currency := firstStringValue(decision, "currency", "currency_code")
	itemCount, itemsPreview := summarizeApprovalItems(decision["items"])
	if itemCount == 0 {
		if itemCountValue, ok := firstNumberValue(decision, "item_count", "items_count"); ok {
			itemCount = int(itemCountValue)
		}
	}
	escalationReason := firstStringValue(decision, "escalation_reason", "escalation", "escalated_because")
	why := firstNonEmpty(
		escalationReason,
		firstStringValue(decision, "approval_reason", "business_reason", "justification", "reason", "why"),
		strings.TrimSpace(policyReason),
	)

	if subject == "" {
		subject = composeApprovalSubject(stepName, topic, vendor, amount, amountOK, currency, itemCount)
	}
	nextEffect := firstStringValue(decision, "next_effect", "approve_effect", "impact", "outcome", "next_step")
	if nextEffect == "" {
		switch {
		case stepName != "":
			nextEffect = fmt.Sprintf("Approve to continue %s.", stepName)
		case workflowID != "":
			nextEffect = "Approve to continue the workflow."
		}
	}

	businessContextPresent := vendor != "" || (amountOK && currency != "") || itemCount > 0
	completeness := "minimal"
	switch {
	case source == "workflow_payload" && contextStatus == "available" && businessContextPresent && why != "":
		completeness = "rich"
	case source == "workflow_payload" && (businessContextPresent || why != "" || stepName != ""):
		completeness = "partial"
	}

	summary := approvalDecisionSummary{
		Source:           source,
		Completeness:     completeness,
		ContextStatus:    contextStatus,
		Title:            subject,
		Subject:          subject,
		Why:              why,
		NextEffect:       nextEffect,
		Currency:         currency,
		Vendor:           vendor,
		ItemsPreview:     itemsPreview,
		EscalationReason: escalationReason,
	}
	if amountOK {
		summary.Amount = &amount
	}
	if itemCount > 0 {
		summary.ItemCount = &itemCount
	}
	if source == "workflow_payload" {
		if contextStatus != "available" {
			summary.MissingFields = append(summary.MissingFields, "approval_context")
		}
		if !businessContextPresent {
			summary.MissingFields = append(summary.MissingFields, "business_context")
		}
		if why == "" {
			summary.MissingFields = append(summary.MissingFields, "why")
		}
	}
	return summary
}

func composeApprovalSubject(stepName, topic, vendor string, amount float64, amountOK bool, currency string, itemCount int) string {
	switch {
	case amountOK && currency != "" && vendor != "":
		return fmt.Sprintf("Approve %s %s request with %s", formatApprovalAmount(amount), currency, vendor)
	case amountOK && currency != "":
		return fmt.Sprintf("Approve %s %s request", formatApprovalAmount(amount), currency)
	case vendor != "" && itemCount > 0:
		return fmt.Sprintf("Approve %d-item request with %s", itemCount, vendor)
	case vendor != "":
		return fmt.Sprintf("Approve request with %s", vendor)
	case itemCount > 0:
		return fmt.Sprintf("Approve %d-item request", itemCount)
	case stepName != "":
		return fmt.Sprintf("Approve %s", stepName)
	case topic != "":
		return fmt.Sprintf("Review %s", topic)
	default:
		return "Approval requested"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.workflowEng) {
		return
	}
	runID, ok := requirePathParam(w, r, "run_id")
	if !ok {
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
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.jobStore.ReleaseLock(ctx, lockKey, token); err != nil {
				slog.Warn("approval lock release failed, will expire via TTL",
					"lock_key", lockKey, "error", err)
			}
		}()
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
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.jobStore) {
		return
	}
	limit, cursor := parsePagination(r, 100)
	// Overfetch slightly to account for items that will be filtered out
	// by tenant access checks, so the client receives close to `limit` items.
	fetchLimit := limit + 10
	// Pending approvals (APPROVAL_REQUIRED state).
	jobs, err := s.jobStore.ListJobsByState(r.Context(), model.JobStateApproval, cursor, fetchLimit)
	if err != nil {
		writeInternalError(w, r, "list approvals", err)
		return
	}
	// Also include recently resolved approvals from post-approval states.
	// These are jobs that passed through the approval flow and have an
	// ApprovalRecord, now in PENDING (approved), DENIED, SUCCEEDED, or FAILED.
	includeResolved := r.URL.Query().Get("include_resolved") != "false"
	if includeResolved {
		resolvedLimit := fetchLimit - int64(len(jobs))
		if resolvedLimit < 0 {
			resolvedLimit = 0
		}
		seenIDs := make(map[string]bool, len(jobs))
		for _, j := range jobs {
			seenIDs[j.ID] = true
		}
		for _, state := range []model.JobState{model.JobStatePending, model.JobStateDenied, model.JobStateSucceeded, model.JobStateFailed, model.JobStateTimeout} {
			if resolvedLimit <= 0 {
				break
			}
			resolved, err := s.jobStore.ListJobsByState(r.Context(), state, cursor, resolvedLimit)
			if err != nil {
				slog.Warn("list approvals: resolved jobs query failed",
					"state", string(state), "error", err)
				continue
			}
			for _, rj := range resolved {
				if seenIDs[rj.ID] {
					continue
				}
				// Only include jobs that have an approval record (went through approval flow).
				approval, aprErr := s.jobStore.GetApprovalRecord(r.Context(), rj.ID)
				if aprErr != nil {
					slog.Warn("list approvals: approval record lookup failed for resolved job",
						"job_id", rj.ID, "state", string(state), "error", aprErr)
					continue
				}
				if approval.ApprovedBy == "" {
					// TIMEOUT jobs were never resolved — include them if
					// they had approval_required set (expired approval gates).
					if state == model.JobStateTimeout {
						sd, sdErr := s.jobStore.GetSafetyDecision(r.Context(), rj.ID)
						if sdErr != nil || !sd.ApprovalRequired {
							continue
						}
					} else {
						continue
					}
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
			slog.Debug("list approvals: skipping item for tenant mismatch",
				"job_id", job.ID, "tenant", job.Tenant)
			continue
		}
		record, sdErr := s.jobStore.GetSafetyDecision(r.Context(), job.ID)
		if sdErr != nil {
			slog.Warn("list approvals: safety decision unavailable, skipping item",
				"job_id", job.ID, "error", sdErr)
			continue
		}
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
		approvalRecord, approvalErr := s.jobStore.GetApprovalRecord(r.Context(), job.ID)
		approvalRecord = store.NormalizeApprovalRecord(job.State, record, approvalRecord)
		hasResolvedApproval := approvalErr == nil && approvalRecord.ApprovedBy != ""
		if approvalRecord.Status != "" {
			item["approval_status"] = approvalRecord.Status
		}
		if approvalRecord.Actionability != "" {
			item["approval_actionability"] = approvalRecord.Actionability
		}
		if approvalRecord.Revision > 0 {
			item["approval_revision"] = approvalRecord.Revision
		}
		if approvalRecord.Decision != "" {
			item["approval_decision"] = approvalRecord.Decision
		}
		// Merge approval resolution fields when an approval record exists.
		if hasResolvedApproval {
			item["resolved_by"] = approvalRecord.ApprovedBy
			item["resolved_comment"] = approvalRecord.Note
			item["resolution"] = approvalRecord.Reason
			if approvalRecord.ApprovedAt > 0 {
				item["resolved_at"] = approvalRecord.ApprovedAt
			}
		}
		// Enrich with workflow labels from the original job request so the
		// dashboard can distinguish gate approvals from policy approvals.
		// Also skip unresolved approvals whose workflow run has already
		// terminated. Resolved approvals must remain visible in history.
		if req, err := s.jobStore.GetJobRequest(r.Context(), job.ID); err == nil && req != nil {
			var (
				payload       map[string]any
				contextStatus = "absent"
			)
			if req.Labels != nil {
				// Filter out stale unresolved approvals: if the run is terminal,
				// skip only items that have not been resolved yet.
				if runID := strings.TrimSpace(req.Labels["run_id"]); runID != "" && s.workflowStore != nil {
					if run, runErr := s.workflowStore.GetRun(r.Context(), runID); runErr == nil && run != nil {
						if wf.IsTerminalRunStatus(run.Status) && !hasResolvedApproval && job.State != model.JobStateTimeout {
							continue
						}
					}
				}
				if v := req.Labels["workflow_id"]; v != "" {
					item["workflow_id"] = v
				}
				if v := req.Labels["run_id"]; v != "" {
					item["workflow_run_id"] = v
				}
				if v := req.Labels["step_id"]; v != "" {
					item["workflow_step_id"] = v
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
				contextStatus = "unavailable"
				if s.memStore != nil {
					if key, err := store.KeyFromPointer(ptr); err == nil {
						if raw, err := s.memStore.GetContext(r.Context(), key); err == nil {
							if len(raw) == 0 {
								contextStatus = "missing"
							} else if err := json.Unmarshal(raw, &payload); err == nil {
								contextStatus = "available"
								item["job_input"] = payload
							} else {
								contextStatus = "malformed"
							}
						} else if errors.Is(err, redis.Nil) {
							contextStatus = "missing"
						}
					} else {
						contextStatus = "malformed"
					}
				}
			}
			if workflowMeta := approvalWorkflowMetadata(payload); workflowMeta != nil {
				if _, exists := item["workflow_id"]; !exists {
					if value := firstStringValue(workflowMeta, "workflow_id"); value != "" {
						item["workflow_id"] = value
					}
				}
				if _, exists := item["workflow_run_id"]; !exists {
					if value := firstStringValue(workflowMeta, "run_id"); value != "" {
						item["workflow_run_id"] = value
					}
				}
				if value := firstStringValue(workflowMeta, "workflow_name"); value != "" {
					item["workflow_name"] = value
				}
				if value := firstStringValue(workflowMeta, "step_id"); value != "" {
					item["workflow_step_id"] = value
				}
				if value := firstStringValue(workflowMeta, "step_name"); value != "" {
					item["step_name"] = value
				}
			}
			item["decision_summary"] = buildApprovalDecisionSummary(req, record.Reason, payload, contextStatus)

			// Enriched approval context fields for decision-grade UX.
			// blast_radius, rollback_hint, and policy_snapshot_summary are
			// cheap to compute per item. prior_approvals is expensive (Redis
			// query) so it's only available via GET /approvals/{id}/context.
			item["blast_radius"] = buildBlastRadius(req, req.GetLabels())

			rollbackHint := strings.TrimSpace(req.GetLabels()["rollback_hint"])
			item["rollback_hint"] = rollbackHint

			item["policy_snapshot_summary"] = buildPolicySnapshotSummary(record)
		}
		items = append(items, item)
	}
	// Cap items to the requested limit (we may have overfetched).
	if int64(len(items)) > limit {
		items = items[:limit]
	}
	// Set pagination cursor based on actual results: if we fetched
	// at least `limit` items worth of data, there may be more pages.
	var nextCursor *int64
	if int64(len(jobs)) >= limit {
		nc := jobs[len(jobs)-1].UpdatedAt - 1
		nextCursor = &nc
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

// approvalLockPrefix must match the scheduler's jobLockPrefix so the
// gateway and scheduler coordinate on the same distributed lock.
const approvalLockPrefix = "cordum:scheduler:job:"

// approvalLockTTL is the distributed lock TTL for approval operations.
const approvalLockTTL = 10 * time.Second

// handlerResult carries the HTTP status and body from inside a lock closure
// back to the handler's response writer.
type handlerResult struct {
	status int
	body   any
}

type approvalRepairRequest struct {
	Apply bool   `json:"apply"`
	Note  string `json:"note"`
}

func approvalConflictPayload(status int, code model.ApprovalConflictCode, message string) map[string]any {
	payload := map[string]any{
		"error":  message,
		"status": status,
		"code":   string(code),
	}
	if code == model.ApprovalConflictRetryableLock {
		payload["retryable"] = true
	}
	return payload
}

// withApprovalLock acquires a per-job distributed lock, executes fn, and
// releases the lock on return. Returns store.ErrLockBusy-style error if
// the lock cannot be acquired within a short deadline.
func (s *server) withApprovalLock(ctx context.Context, jobID string, fn func(ctx context.Context) error) error {
	key := approvalLockPrefix + jobID
	lockStart := time.Now()
	deadline := lockStart.Add(2 * time.Second)
	var token string
	var err error
	for {
		lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		token, err = s.jobStore.TryAcquireLock(lockCtx, key, approvalLockTTL)
		cancel()
		if err != nil {
			slog.Error("approval lock acquire failed", "job_id", jobID, "error", err)
			return fmt.Errorf("lock acquire: %w", err)
		}
		if token != "" {
			break
		}
		if time.Now().After(deadline) {
			slog.Warn("approval lock busy", "job_id", jobID, "waited_ms", time.Since(lockStart).Milliseconds())
			return fmt.Errorf("approval lock busy")
		}
		time.Sleep(25 * time.Millisecond)
	}
	slog.Debug("approval lock acquired", "job_id", jobID, "wait_ms", time.Since(lockStart).Milliseconds())
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if rErr := s.jobStore.ReleaseLock(releaseCtx, key, token); rErr != nil {
			slog.Warn("approval lock release failed", "job_id", jobID, "error", rErr)
		}
	}()
	return fn(ctx)
}

func (s *server) approvalRepairPlan(ctx context.Context, jobID string) (*store.ApprovalRepairSnapshot, store.ApprovalRepairPlan, error) {
	snapshot, err := s.jobStore.InspectApprovalRepair(ctx, jobID)
	if err != nil {
		return nil, store.ApprovalRepairPlan{}, err
	}
	opts := store.ApprovalRepairClassifyOptions{}
	if snapshot.RunID != "" && s.workflowStore != nil {
		if run, err := s.workflowStore.GetRun(ctx, snapshot.RunID); err == nil && run != nil {
			opts.WorkflowTerminal = wf.IsTerminalRunStatus(run.Status)
		}
	}
	if snapshot.Topic != capsdk.SubjectApprovalGate &&
		snapshot.Topic != capsdk.SubjectWorkflowApprovalGate &&
		strings.TrimSpace(snapshot.SafetyRecord.PolicySnapshot) != "" &&
		s.safetyClient != nil {
		if resp, err := s.safetyClient.ListSnapshots(ctx, &pb.ListSnapshotsRequest{}); err == nil && resp != nil {
			currentSnapshot := ""
			if len(resp.GetSnapshots()) > 0 {
				currentSnapshot = strings.TrimSpace(resp.GetSnapshots()[0])
			}
			if currentSnapshot != "" && snapshotBase(currentSnapshot) != snapshotBase(snapshot.SafetyRecord.PolicySnapshot) {
				opts.StaleSnapshot = true
			}
		}
	}
	plan := store.ClassifyApprovalRepair(*snapshot, opts)
	return snapshot, plan, nil
}

func (s *server) publishApprovalRepair(ctx context.Context, repaired *store.ApprovalRepairResult) error {
	if repaired == nil || repaired.Request == nil || repaired.ApprovalRecord.PublishTarget == "" {
		return nil
	}
	switch repaired.ApprovalRecord.PublishTarget {
	case model.ApprovalPublishTargetSubmit:
		packet := &pb.BusPacket{
			TraceId:         repaired.TraceID,
			SenderId:        "api-gateway",
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: repaired.Request,
			},
		}
		if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
			return err
		}
	case model.ApprovalPublishTargetDLQ, model.ApprovalPublishTargetDLQAndResult:
		errorMessage := "approval rejected"
		if msg := strings.TrimSpace(repaired.ApprovalRecord.Reason); msg != "" {
			errorMessage = msg
		}
		packet := &pb.BusPacket{
			TraceId:         repaired.TraceID,
			SenderId:        "api-gateway",
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			Payload: &pb.BusPacket_JobResult{
				JobResult: &pb.JobResult{
					JobId:         repaired.JobID,
					Status:        pb.JobStatus_JOB_STATUS_DENIED,
					ErrorCode:     "approval_rejected",
					ErrorCodeEnum: pb.ErrorCode_ERROR_CODE_SAFETY_DENIED,
					ErrorMessage:  errorMessage,
				},
			},
		}
		if err := s.bus.Publish(capsdk.SubjectDLQ, packet); err != nil {
			return err
		}
		if repaired.ApprovalRecord.PublishTarget == model.ApprovalPublishTargetDLQAndResult {
			if err := s.bus.Publish(capsdk.SubjectResult, packet); err != nil {
				return err
			}
		}
	}
	if err := s.jobStore.MarkApprovalPublishComplete(ctx, repaired.JobID, repaired.ApprovalRecord.Revision, repaired.ApprovalRecord.PublishTarget); err != nil {
		slog.Warn("mark repaired approval publish complete failed", "job_id", repaired.JobID, "error", err)
	}
	return nil
}

func (s *server) handleRepairApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.jobStore) {
		return
	}
	var body approvalRepairRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		if !errors.Is(err, io.EOF) {
			writeJSONDecodeError(w, err, "invalid body")
			return
		}
	}
	jobID, ok := requirePathParam(w, r, "job_id")
	if !ok {
		return
	}

	var result handlerResult
	lockErr := s.withApprovalLock(r.Context(), jobID, func(ctx context.Context) error {
		if tenant, tenantErr := s.jobStore.GetTenant(ctx, jobID); tenantErr != nil {
			slog.Warn("repair: tenant lookup failed", "job_id", jobID, "error", tenantErr)
		} else if tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				result = handlerResult{http.StatusForbidden, "tenant access denied"}
				return nil
			}
		}

		snapshot, plan, err := s.approvalRepairPlan(ctx, jobID)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				result = handlerResult{http.StatusNotFound, "job not found"}
				return nil
			}
			slog.Error("approval repair inspection failed", "job_id", jobID, "error", err)
			result = handlerResult{http.StatusInternalServerError, "failed to inspect approval repair"}
			return nil
		}

		response := map[string]any{
			"job_id":     jobID,
			"apply":      body.Apply,
			"applied":    false,
			"repairable": plan.Repairable,
			"state":      snapshot.State,
			"trace_id":   snapshot.TraceID,
			"approval":   snapshot.ApprovalRecord,
			"plan":       plan,
		}
		if !body.Apply {
			result = handlerResult{http.StatusOK, response}
			return nil
		}
		if !plan.Repairable {
			response["error"] = "approval does not require repair"
			response["status"] = http.StatusConflict
			response["code"] = "not_repairable"
			result = handlerResult{http.StatusConflict, response}
			return nil
		}

		actorID := strings.TrimSpace(policyActorID(r))
		if actorID == "" {
			actorID = "system/repair"
		}
		repaired, err := s.jobStore.ApplyApprovalRepair(ctx, store.ApprovalRepairApplyParams{
			JobID: jobID,
			Plan:  plan,
			Actor: actorID,
			Note:  strings.TrimSpace(body.Note),
		})
		if err != nil {
			if errors.Is(err, redis.Nil) {
				result = handlerResult{http.StatusNotFound, "job not found"}
				return nil
			}
			if strings.Contains(err.Error(), "state changed") {
				result = handlerResult{
					http.StatusConflict,
					approvalConflictPayload(http.StatusConflict, model.ApprovalConflictRetryableLock, "approval changed during repair; retry"),
				}
				return nil
			}
			slog.Error("approval repair apply failed", "job_id", jobID, "kind", plan.Kind, "error", err)
			result = handlerResult{http.StatusInternalServerError, "failed to apply approval repair"}
			return nil
		}

		finalApproval := repaired.ApprovalRecord
		publishDeferred := false
		publishError := ""
		if repaired.ApprovalRecord.PublishTarget != "" {
			if s.bus == nil {
				publishDeferred = true
			} else if err := s.publishApprovalRepair(ctx, repaired); err != nil {
				publishDeferred = true
				publishError = err.Error()
				slog.Error("approval repair publish failed",
					"job_id", jobID,
					"kind", plan.Kind,
					"target", repaired.ApprovalRecord.PublishTarget,
					"error", err)
			} else if updated, err := s.jobStore.GetApprovalRecord(ctx, jobID); err == nil {
				finalApproval = updated
			}
		}

		slog.Info("approval repaired",
			"job_id", jobID,
			"kind", plan.Kind,
			"actor", actorID,
			"role", policyRole(r),
			"target_state", repaired.State,
			"publish_target", finalApproval.PublishTarget,
			"publish_status", finalApproval.PublishStatus)
		s.appendAuditEntryNamed(ctx, "repair", "job", jobID, snapshot.Topic, actorID, policyRole(r), "repair approval "+jobID+" ("+string(plan.Kind)+")")

		response["applied"] = true
		response["state"] = repaired.State
		response["approval"] = finalApproval
		response["repairable"] = true
		if publishDeferred {
			response["publish_deferred"] = true
		}
		if publishError != "" {
			response["publish_error"] = publishError
		}
		result = handlerResult{http.StatusOK, response}
		return nil
	})
	if lockErr != nil {
		if strings.Contains(lockErr.Error(), "lock busy") {
			writeJSON(w, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictRetryableLock, "repair in progress; retry"))
			return
		}
		writeInternalError(w, r, "repair lock", lockErr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if result.status >= 400 {
		if msg, ok := result.body.(string); ok {
			writeErrorJSON(w, result.status, msg)
		} else {
			w.WriteHeader(result.status)
			if err := json.NewEncoder(w).Encode(result.body); err != nil {
				slog.Warn("json encode approval repair error failed", "error", err)
			}
		}
		return
	}
	writeJSON(w, result.body)
}

func (s *server) handleApproveJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.jobStore, s.bus) {
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
	jobID, ok := requirePathParam(w, r, "job_id")
	if !ok {
		return
	}

	// Acquire distributed lock so concurrent approve/reject requests are
	// serialised. Uses the same key prefix as the scheduler engine.
	var result handlerResult
	lockErr := s.withApprovalLock(r.Context(), jobID, func(ctx context.Context) error {
		state, err := s.jobStore.GetState(ctx, jobID)
		if err != nil {
			result = handlerResult{http.StatusNotFound, "job not found"}
			return nil
		}

		// Idempotency: if the job was already approved (state moved past
		// APPROVAL), return the existing approval record instead of 409.
		if state != model.JobStateApproval {
			if state == model.JobStatePending || state == model.JobStateSucceeded ||
				state == model.JobStateScheduled || state == model.JobStateDispatched ||
				state == model.JobStateRunning {
				req, reqErr := s.jobStore.GetJobRequest(ctx, jobID)
				if reqErr != nil {
					slog.Warn("approve: idempotency check skipped, job request lookup failed",
						"job_id", jobID, "error", reqErr)
				}
				if req != nil && req.Labels != nil && req.Labels["approval_granted"] == "true" {
					rec, recErr := s.jobStore.GetApprovalRecord(ctx, jobID)
					if recErr != nil {
						slog.Warn("approve: idempotent approval record lookup failed",
							"job_id", jobID, "error", recErr)
					}
					traceID, traceErr := s.jobStore.GetTraceID(ctx, jobID)
					if traceErr != nil {
						slog.Warn("approve: idempotent trace ID lookup failed",
							"job_id", jobID, "error", traceErr)
					}
					slog.Info("approval idempotent — already approved",
						"job_id", jobID, "trace_id", traceID,
						"state", string(state), "approved_by", rec.ApprovedBy,
						"actor", policyActorID(r))
					result = handlerResult{http.StatusOK, map[string]any{
						"job_id":      jobID,
						"trace_id":    traceID,
						"status":      "already_approved",
						"approved_by": rec.ApprovedBy,
						"approved_at": rec.ApprovedAt,
					}}
					return nil
				}
			}
			s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), "job not awaiting approval (state="+string(state)+")")
			conflictCode := model.ApprovalConflictNotActionable
			conflictMessage := "job not awaiting approval"
			switch state {
			case model.JobStatePending, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning,
				model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateDenied, model.JobStateQuarantined:
				conflictCode = model.ApprovalConflictAlreadyResolved
				conflictMessage = "approval already resolved"
			case model.JobStateTimeout:
				conflictMessage = "approval expired"
			}
			result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, conflictCode, conflictMessage)}
			return nil
		}

		if tenant, tenantErr := s.jobStore.GetTenant(ctx, jobID); tenantErr != nil {
			slog.Warn("approve: tenant lookup failed", "job_id", jobID, "error", tenantErr)
		} else if tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				result = handlerResult{http.StatusForbidden, "tenant access denied"}
				return nil
			}
		}
		req, err := s.jobStore.GetJobRequest(ctx, jobID)
		if err != nil {
			result = handlerResult{http.StatusNotFound, "job request not found"}
			return nil
		}

		// Check if workflow run is terminal (under lock to prevent TOCTOU).
		if req.Labels != nil {
			if runID := strings.TrimSpace(req.Labels["run_id"]); runID != "" && s.workflowStore != nil {
				if run, runErr := s.workflowStore.GetRun(ctx, runID); runErr == nil && run != nil {
					if wf.IsTerminalRunStatus(run.Status) {
						msg := fmt.Sprintf("workflow run %s — approval no longer valid", run.Status)
						s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), msg)
						result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictTerminalRun, msg)}
						return nil
					}
				}
			}
		}

		safetyRecord, err := s.jobStore.GetSafetyDecision(ctx, jobID)
		if err != nil {
			result = handlerResult{http.StatusServiceUnavailable, "safety decision unavailable"}
			return nil
		}
		if !safetyRecord.ApprovalRequired && safetyRecord.Decision != model.SafetyRequireApproval {
			s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), "job not awaiting approval (safety record)")
			result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictNotActionable, "job not awaiting approval")}
			return nil
		}
		if safetyRecord.JobHash == "" {
			s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), "approval job hash unavailable")
			result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictStaleRequest, "approval job hash unavailable")}
			return nil
		}
		topic := strings.TrimSpace(req.GetTopic())
		isWorkflowGate := topic == capsdk.SubjectApprovalGate || topic == capsdk.SubjectWorkflowApprovalGate
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
				s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), "approval policy snapshot unavailable")
				result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictStaleSnapshot, "approval policy snapshot unavailable")}
				return nil
			}
			if s.safetyClient == nil {
				result = handlerResult{http.StatusServiceUnavailable, "safety kernel unavailable"}
				return nil
			}
			snapResp, err := s.safetyClient.ListSnapshots(ctx, &pb.ListSnapshotsRequest{})
			if err != nil {
				result = handlerResult{http.StatusBadGateway, "list safety snapshots failed"}
				return nil
			}
			currentSnapshot := ""
			if snapResp != nil && len(snapResp.Snapshots) > 0 {
				currentSnapshot = strings.TrimSpace(snapResp.Snapshots[0])
			}
			if currentSnapshot == "" || snapshotBase(currentSnapshot) != snapshotBase(policySnapshot) {
				s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), "policy snapshot changed")
				result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictStaleSnapshot, "policy snapshot changed; re-evaluate before approving")}
				return nil
			}
		}
		reason := strings.TrimSpace(body.Reason)
		note := strings.TrimSpace(body.Note)
		approvedBy := strings.TrimSpace(policyActorID(r))
		if approvedBy == "" {
			approvedBy = "system/unknown"
		}
		approvalRole := strings.TrimSpace(policyRole(r))

		// Re-check workflow run terminal status under lock right before
		// the state transition to prevent approving dead workflow runs.
		if req.Labels != nil {
			if runID := strings.TrimSpace(req.Labels["run_id"]); runID != "" && s.workflowStore != nil {
				if run, runErr := s.workflowStore.GetRun(ctx, runID); runErr == nil && run != nil {
					if wf.IsTerminalRunStatus(run.Status) {
						msg := fmt.Sprintf("workflow run %s — approval no longer valid", run.Status)
						s.appendAuditEntryNamed(ctx, "approve_failed", "job", jobID, "", policyActorID(r), policyRole(r), msg)
						result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictTerminalRun, msg)}
						return nil
					}
				}
			}
		}

		labelUpdates := map[string]string{
			"approval_granted": "true",
			bus.LabelBusMsgID:  "approval:" + jobID,
		}
		if reason != "" {
			labelUpdates["approval_reason"] = reason
		}
		if note != "" {
			labelUpdates["approval_note"] = note
		}
		resolved, err := s.jobStore.ResolveApproval(ctx, store.ApprovalResolutionParams{
			JobID:          jobID,
			Decision:       model.ApprovalDecisionApprove,
			ResultState:    model.JobStatePending,
			ApprovedBy:     approvedBy,
			ApprovedRole:   approvalRole,
			Reason:         reason,
			Note:           note,
			PolicySnapshot: policySnapshot,
			LabelUpdates:   labelUpdates,
		})
		if err != nil {
			var conflict *store.ApprovalConflictError
			if errors.As(err, &conflict) {
				result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, conflict.Code, conflict.Message)}
				return nil
			}
			result = handlerResult{http.StatusInternalServerError, "failed to persist approval resolution"}
			return nil
		}
		packet := &pb.BusPacket{
			TraceId:         resolved.TraceID,
			SenderId:        "api-gateway",
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: resolved.Request,
			},
		}
		if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
			result = handlerResult{http.StatusBadGateway, "publish approval failed"}
			return nil
		}
		if err := s.jobStore.MarkApprovalPublishComplete(ctx, jobID, resolved.ApprovalRecord.Revision, resolved.ApprovalRecord.PublishTarget); err != nil {
			slog.Warn("mark approval publish complete failed", "job_id", jobID, "error", err)
		}
		slog.Info("job approved",
			"job_id", jobID, "trace_id", resolved.TraceID,
			"topic", resolved.Request.GetTopic(), "actor", policyActorID(r),
			"role", policyRole(r))
		s.appendAuditEntryNamed(ctx, "approve", "job", jobID, resolved.Request.GetTopic(), policyActorID(r), policyRole(r), "approve job "+jobID)
		result = handlerResult{http.StatusOK, map[string]string{"job_id": jobID, "trace_id": resolved.TraceID}}
		return nil
	})
	if lockErr != nil {
		if strings.Contains(lockErr.Error(), "lock busy") {
			writeJSON(w, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictRetryableLock, "approval in progress; retry"))
			return
		}
		writeInternalError(w, r, "approval lock", lockErr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if result.status >= 400 {
		if msg, ok := result.body.(string); ok {
			writeErrorJSON(w, result.status, msg)
		} else {
			w.WriteHeader(result.status)
			if err := json.NewEncoder(w).Encode(result.body); err != nil {
				slog.Warn("json encode approval error failed", "error", err)
			}
		}
		return
	}
	writeJSON(w, result.body)
}

func (s *server) handleRejectJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.jobStore, s.bus) {
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
	jobID, ok := requirePathParam(w, r, "job_id")
	if !ok {
		return
	}

	var result handlerResult
	lockErr := s.withApprovalLock(r.Context(), jobID, func(ctx context.Context) error {
		state, err := s.jobStore.GetState(ctx, jobID)
		if err != nil {
			result = handlerResult{http.StatusNotFound, "job not found"}
			return nil
		}
		if state != model.JobStateApproval {
			// Idempotency: if already denied, return success.
			if state == model.JobStateDenied {
				rec, recErr := s.jobStore.GetApprovalRecord(ctx, jobID)
				if recErr != nil {
					slog.Warn("reject: idempotent approval record lookup failed",
						"job_id", jobID, "error", recErr)
				}
				result = handlerResult{http.StatusOK, map[string]any{
					"job_id":      jobID,
					"status":      "already_rejected",
					"rejected_by": rec.ApprovedBy,
					"rejected_at": rec.ApprovedAt,
				}}
				return nil
			}
			s.appendAuditEntryNamed(ctx, "reject_failed", "job", jobID, "", policyActorID(r), policyRole(r), "job not awaiting approval (state="+string(state)+")")
			conflictCode := model.ApprovalConflictNotActionable
			conflictMessage := "job not awaiting approval"
			switch state {
			case model.JobStateDenied:
				conflictCode = model.ApprovalConflictAlreadyResolved
				conflictMessage = "approval already resolved"
			case model.JobStateTimeout:
				conflictMessage = "approval expired"
			}
			result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, conflictCode, conflictMessage)}
			return nil
		}
		if tenant, tenantErr := s.jobStore.GetTenant(ctx, jobID); tenantErr != nil {
			slog.Warn("reject: tenant lookup failed", "job_id", jobID, "error", tenantErr)
		} else if tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				result = handlerResult{http.StatusForbidden, "tenant access denied"}
				return nil
			}
		}
		req, reqErr := s.jobStore.GetJobRequest(ctx, jobID)
		if reqErr != nil {
			result = handlerResult{http.StatusNotFound, "job request not found"}
			return nil
		}
		if req.Labels != nil {
			if runID := strings.TrimSpace(req.Labels["run_id"]); runID != "" && s.workflowStore != nil {
				if run, runErr := s.workflowStore.GetRun(ctx, runID); runErr == nil && run != nil {
					if wf.IsTerminalRunStatus(run.Status) {
						msg := fmt.Sprintf("workflow run %s — approval no longer valid", run.Status)
						s.appendAuditEntryNamed(ctx, "reject_failed", "job", jobID, "", policyActorID(r), policyRole(r), msg)
						result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictTerminalRun, msg)}
						return nil
					}
				}
			}
		}
		safetyRecord, safetyErr := s.jobStore.GetSafetyDecision(ctx, jobID)
		if safetyErr != nil {
			slog.Warn("reject: safety decision unavailable, proceeding with empty record",
				"job_id", jobID, "error", safetyErr)
		}
		reason := strings.TrimSpace(body.Reason)
		note := strings.TrimSpace(body.Note)
		approvedBy := strings.TrimSpace(policyActorID(r))
		if approvedBy == "" {
			approvedBy = "system/unknown"
		}
		approvalRole := strings.TrimSpace(policyRole(r))
		publishTarget := model.ApprovalPublishTargetDLQ
		if req != nil && strings.TrimSpace(req.GetTopic()) == capsdk.SubjectWorkflowApprovalGate {
			publishTarget = model.ApprovalPublishTargetDLQAndResult
		}
		resolved, err := s.jobStore.ResolveApproval(ctx, store.ApprovalResolutionParams{
			JobID:          jobID,
			Decision:       model.ApprovalDecisionReject,
			ResultState:    model.JobStateDenied,
			ApprovedBy:     approvedBy,
			ApprovedRole:   approvalRole,
			Reason:         reason,
			Note:           note,
			PolicySnapshot: safetyRecord.PolicySnapshot,
			PublishTarget:  publishTarget,
		})
		if err != nil {
			var conflict *store.ApprovalConflictError
			if errors.As(err, &conflict) {
				result = handlerResult{http.StatusConflict, approvalConflictPayload(http.StatusConflict, conflict.Code, conflict.Message)}
				return nil
			}
			result = handlerResult{http.StatusInternalServerError, "failed to persist approval resolution"}
			return nil
		}
		errorMessage := "approval rejected"
		if reason != "" {
			errorMessage = reason
		}
		packet := &pb.BusPacket{
			TraceId:         resolved.TraceID,
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
			slog.Error("publish dlq on approval reject failed", "job_id", jobID, "error", err)
		} else {
			rejectTopic := strings.TrimSpace(resolved.Request.GetTopic())
			publishComplete := true
			if rejectTopic == capsdk.SubjectWorkflowApprovalGate {
				if err := s.bus.Publish(capsdk.SubjectResult, packet); err != nil {
					slog.Error("publish result on workflow gate reject failed", "job_id", jobID, "error", err)
					publishComplete = false
				}
			}
			if publishComplete {
				if err := s.jobStore.MarkApprovalPublishComplete(ctx, jobID, resolved.ApprovalRecord.Revision, resolved.ApprovalRecord.PublishTarget); err != nil {
					slog.Warn("mark approval reject publish complete failed", "job_id", jobID, "error", err)
				}
			}
		}
		rejectTopic := strings.TrimSpace(resolved.Request.GetTopic())
		slog.Info("job rejected",
			"job_id", jobID, "topic", rejectTopic,
			"actor", policyActorID(r), "role", policyRole(r),
			"reason", reason)
		s.appendAuditEntryNamed(ctx, "reject", "job", jobID, rejectTopic, policyActorID(r), policyRole(r), "reject job "+jobID)
		result = handlerResult{http.StatusOK, map[string]string{"job_id": jobID}}
		return nil
	})
	if lockErr != nil {
		if strings.Contains(lockErr.Error(), "lock busy") {
			writeJSON(w, approvalConflictPayload(http.StatusConflict, model.ApprovalConflictRetryableLock, "rejection in progress; retry"))
			return
		}
		writeInternalError(w, r, "rejection lock", lockErr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if result.status >= 400 {
		if msg, ok := result.body.(string); ok {
			writeErrorJSON(w, result.status, msg)
		} else {
			w.WriteHeader(result.status)
			if err := json.NewEncoder(w).Encode(result.body); err != nil {
				slog.Warn("json encode approval error failed", "error", err)
			}
		}
		return
	}
	writeJSON(w, result.body)
}

// handleApprovalContext returns enriched approval context for a single job,
// combining blast radius, prior approvals, rollback hints, policy snapshot
// summary, time remaining, and parsed constraints in one response.
func (s *server) handleApprovalContext(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, []string{"admin"}, s.jobStore) {
		return
	}
	jobID, ok := requirePathParam(w, r, "job_id")
	if !ok {
		return
	}

	ctx := r.Context()

	// Verify job exists.
	state, err := s.jobStore.GetState(ctx, jobID)
	if err != nil {
		slog.Debug("approval context: job not found", "job_id", jobID, "error", err)
		writeErrorJSON(w, http.StatusNotFound, "job not found")
		return
	}

	// Tenant access check — fail closed on Redis error to prevent cross-tenant leakage.
	tenant, tenantErr := s.jobStore.GetTenant(ctx, jobID)
	if tenantErr != nil {
		slog.Error("approval context: tenant lookup failed, denying access",
			"job_id", jobID, "error", tenantErr)
		writeErrorJSON(w, http.StatusServiceUnavailable, "tenant lookup unavailable")
		return
	}
	if tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}

	// Load safety decision.
	safetyRecord, err := s.jobStore.GetSafetyDecision(ctx, jobID)
	if err != nil {
		slog.Error("approval context: safety decision unavailable",
			"job_id", jobID, "error", err)
		writeInternalError(w, r, "get safety decision", err)
		return
	}

	// Load approval record for resolution info.
	approvalRecord, approvalErr := s.jobStore.GetApprovalRecord(ctx, jobID)
	approvalRecord = store.NormalizeApprovalRecord(state, safetyRecord, approvalRecord)

	// Build the approval item (same shape as list endpoint).
	approvalItem := map[string]any{
		"job_id":              jobID,
		"state":               string(state),
		"decision":            safetyRecord.Decision,
		"policy_snapshot":     safetyRecord.PolicySnapshot,
		"policy_rule_id":      safetyRecord.RuleID,
		"policy_reason":       safetyRecord.Reason,
		"constraints":         safetyRecord.Constraints,
		"approval_required":   safetyRecord.ApprovalRequired,
		"approval_ref":        safetyRecord.ApprovalRef,
		"approval_status":     approvalRecord.Status,
		"approval_actionability": approvalRecord.Actionability,
		"approval_decision":   approvalRecord.Decision,
	}
	if approvalErr == nil && approvalRecord.ApprovedBy != "" {
		approvalItem["resolved_by"] = approvalRecord.ApprovedBy
		approvalItem["resolved_comment"] = approvalRecord.Note
		approvalItem["resolution"] = approvalRecord.Reason
		if approvalRecord.ApprovedAt > 0 {
			approvalItem["resolved_at"] = approvalRecord.ApprovedAt
		}
	}

	// Load job request for labels, topic, tenant, and decision summary.
	var (
		decisionSummary *approvalDecisionSummary
		blastRadius     map[string]any
		rollbackHint    string
		jobTopic        string
		jobTenant       string
	)
	if req, reqErr := s.jobStore.GetJobRequest(ctx, jobID); reqErr == nil && req != nil {
		labels := req.GetLabels()
		jobTopic = strings.TrimSpace(req.GetTopic())
		jobTenant = strings.TrimSpace(req.GetTenantId())
		if jobTenant == "" && labels != nil {
			jobTenant = strings.TrimSpace(labels["tenant_id"])
		}
		blastRadius = buildBlastRadius(req, labels)
		rollbackHint = strings.TrimSpace(labels["rollback_hint"])

		// Build decision summary with context pointer resolution.
		var (
			payload       map[string]any
			contextStatus = "absent"
		)
		if ptr := strings.TrimSpace(req.GetContextPtr()); ptr != "" {
			contextStatus = "unavailable"
			if s.memStore != nil {
				if key, keyErr := store.KeyFromPointer(ptr); keyErr == nil {
					if raw, getErr := s.memStore.GetContext(ctx, key); getErr == nil {
						if len(raw) == 0 {
							contextStatus = "missing"
						} else if jsonErr := json.Unmarshal(raw, &payload); jsonErr == nil {
							contextStatus = "available"
							approvalItem["job_input"] = payload
						} else {
							contextStatus = "malformed"
						}
					}
				}
			}
		}
		summary := buildApprovalDecisionSummary(req, safetyRecord.Reason, payload, contextStatus)
		decisionSummary = &summary

		// Add workflow metadata.
		if labels != nil {
			if v := labels["workflow_id"]; v != "" {
				approvalItem["workflow_id"] = v
			}
			if v := labels["run_id"]; v != "" {
				approvalItem["workflow_run_id"] = v
			}
			if v := labels["step_id"]; v != "" {
				approvalItem["workflow_step_id"] = v
				approvalItem["step_name"] = v
			}
			if v := labels["gate_type"]; v != "" {
				approvalItem["gate_type"] = v
			}
		}
	} else {
		slog.Warn("approval context: job request unavailable",
			"job_id", jobID, "error", reqErr)
		blastRadius = map[string]any{
			"systems": []string{}, "namespaces": []string{},
			"resources": []string{}, "scope_description": "",
		}
	}
	if decisionSummary != nil {
		approvalItem["decision_summary"] = *decisionSummary
	}

	// Compute time remaining from deadline.
	var timeRemainingMs *int64
	metas, metaErr := s.jobStore.GetJobMetas(ctx, []string{jobID})
	if metaErr != nil {
		slog.Warn("approval context: job meta lookup failed", "job_id", jobID, "error", metaErr)
	}
	if metaErr == nil {
		if meta, exists := metas[jobID]; exists {
			if raw := meta["deadline_unix"]; raw != "" {
				if deadlineMicros, parseErr := parseIntSafe(raw); parseErr == nil && deadlineMicros > 0 {
					remaining := (deadlineMicros - time.Now().UnixMicro()) / 1000
					if remaining < 0 {
						remaining = 0
					}
					timeRemainingMs = &remaining
				}
			}
		}
	}

	// Parse constraints into a structured object.
	var constraintsParsed any
	if safetyRecord.Constraints != nil {
		constraintsParsed = safetyRecord.Constraints
	}

	response := map[string]any{
		"approval":                 approvalItem,
		"blast_radius":             blastRadius,
		"prior_approvals":          s.queryPriorApprovals(ctx, jobTopic, jobTenant, 10),
		"rollback_hint":            rollbackHint,
		"policy_snapshot_summary":  buildPolicySnapshotSummary(safetyRecord),
		"time_remaining_ms":        timeRemainingMs,
		"constraints":              constraintsParsed,
	}

	priorCount := len(response["prior_approvals"].([]map[string]any))
	slog.Debug("approval context served",
		"job_id", jobID,
		"state", string(state),
		"topic", jobTopic,
		"tenant", jobTenant,
		"prior_approvals", priorCount,
		"has_rollback_hint", rollbackHint != "",
		"has_deadline", timeRemainingMs != nil,
		"actor", policyActorID(r))

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, response)
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

// buildBlastRadius extracts blast radius information from job request labels
// and metadata. Packs populate labels with prefixed keys like "system:X",
// "namespace:Y", "resource:Z" to describe what a job affects.
func buildBlastRadius(req *pb.JobRequest, meta map[string]string) map[string]any {
	systems := []string{}
	namespaces := []string{}
	resources := []string{}

	labels := req.GetLabels()
	for key, val := range labels {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch {
		case strings.HasPrefix(key, "system:"):
			systems = append(systems, val)
		case strings.HasPrefix(key, "namespace:"):
			namespaces = append(namespaces, val)
		case strings.HasPrefix(key, "resource:"):
			resources = append(resources, val)
		}
	}
	// Also check top-level label values that packs may set directly.
	if v := strings.TrimSpace(labels["target_system"]); v != "" && !containsStr(systems, v) {
		systems = append(systems, v)
	}
	if v := strings.TrimSpace(labels["target_namespace"]); v != "" && !containsStr(namespaces, v) {
		namespaces = append(namespaces, v)
	}
	if v := strings.TrimSpace(labels["target_resource"]); v != "" && !containsStr(resources, v) {
		resources = append(resources, v)
	}

	// Build scope description from available data.
	scope := ""
	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(meta["tenant_id"])
	if tenant == "" {
		tenant = strings.TrimSpace(req.GetTenantId())
	}
	switch {
	case len(systems) > 0 && tenant != "":
		scope = fmt.Sprintf("Affects %d system(s) in tenant %s", len(systems), tenant)
	case len(resources) > 0:
		scope = fmt.Sprintf("Affects %d resource(s)", len(resources))
	case topic != "":
		scope = fmt.Sprintf("Topic: %s", topic)
	}

	return map[string]any{
		"systems":           systems,
		"namespaces":        namespaces,
		"resources":         resources,
		"scope_description": scope,
	}
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// queryPriorApprovals finds recently resolved approvals with the same
// topic+tenant combination. Returns at most maxResults items.
func (s *server) queryPriorApprovals(ctx context.Context, topic, tenant string, maxResults int) []map[string]any {
	// Require both topic and tenant for tenant-safe queries. If either is empty,
	// return empty to prevent cross-tenant data leakage.
	if s.jobStore == nil || topic == "" || tenant == "" {
		return []map[string]any{}
	}
	now := time.Now()
	from := now.Add(-30 * 24 * time.Hour).UnixMicro()
	to := now.UnixMicro()

	jobIDs, err := s.jobStore.ListRecentJobsByTimeRange(ctx, from, to, 0, 200)
	if err != nil {
		slog.Warn("query prior approvals: list recent jobs failed", "error", err)
		return []map[string]any{}
	}
	if len(jobIDs) == 0 {
		return []map[string]any{}
	}

	metas, err := s.jobStore.GetJobMetas(ctx, jobIDs)
	if err != nil {
		slog.Warn("query prior approvals: get job metas failed", "error", err)
		return []map[string]any{}
	}

	results := make([]map[string]any, 0, maxResults)
	for _, id := range jobIDs {
		if len(results) >= maxResults {
			break
		}
		meta, ok := metas[id]
		if !ok {
			continue
		}
		// Must have been an approval that was resolved.
		if meta["safety_decision"] != string(model.SafetyRequireApproval) {
			continue
		}
		if meta["approval_by"] == "" {
			continue
		}
		// Match topic+tenant (both always required — never skip tenant filter).
		jobTopic := strings.TrimSpace(meta["topic"])
		jobTenant := strings.TrimSpace(meta["tenant"])
		if jobTopic != topic {
			continue
		}
		if jobTenant != tenant {
			continue
		}

		wasApproved := meta["approval_decision"] == string(model.ApprovalDecisionApprove)
		if meta["approval_decision"] == "" {
			// Legacy: infer from state.
			wasApproved = meta["state"] != string(model.JobStateDenied)
		}
		approvedAt := int64(0)
		if raw := meta["approval_at"]; raw != "" {
			if parsed, parseErr := parseIntSafe(raw); parseErr == nil {
				approvedAt = parsed
			}
		}

		results = append(results, map[string]any{
			"job_id":      id,
			"topic":       jobTopic,
			"tenant":      jobTenant,
			"decision":    meta["approval_decision"],
			"resolved_by": meta["approval_by"],
			"resolved_at": approvedAt,
			"was_approved": wasApproved,
		})
	}
	return results
}

func parseIntSafe(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// buildPolicySnapshotSummary parses the safety snapshot and rule information
// into a human-readable summary object.
func buildPolicySnapshotSummary(record model.SafetyDecisionRecord) map[string]any {
	ruleCount := 0
	policyVersion := ""
	snapshot := strings.TrimSpace(record.PolicySnapshot)

	if snapshot != "" {
		policyVersion = snapshotBase(snapshot)
		// Count is not stored directly; report 1 if a rule was matched.
		if record.RuleID != "" {
			ruleCount = 1
		}
	}

	matchedRule := map[string]any{
		"id":                  record.RuleID,
		"description":         record.Reason,
		"decision":            string(record.Decision),
		"constraints_summary": formatConstraintsSummary(record),
	}

	return map[string]any{
		"rule_count":     ruleCount,
		"matched_rule":   matchedRule,
		"policy_version": policyVersion,
	}
}

func formatConstraintsSummary(record model.SafetyDecisionRecord) string {
	if record.Constraints == nil {
		return ""
	}
	parts := []string{}
	if budgets := record.Constraints.GetBudgets(); budgets != nil {
		if budgets.GetMaxRetries() > 0 {
			parts = append(parts, fmt.Sprintf("max_retries=%d", budgets.GetMaxRetries()))
		}
		if budgets.GetMaxRuntimeMs() > 0 {
			parts = append(parts, fmt.Sprintf("timeout=%dms", budgets.GetMaxRuntimeMs()))
		}
		if budgets.GetMaxConcurrentJobs() > 0 {
			parts = append(parts, fmt.Sprintf("max_concurrent=%d", budgets.GetMaxConcurrentJobs()))
		}
	}
	if sandbox := record.Constraints.GetSandbox(); sandbox != nil {
		parts = append(parts, "sandboxed")
	}
	if record.Constraints.GetRedactionLevel() != "" {
		parts = append(parts, fmt.Sprintf("redaction=%s", record.Constraints.GetRedactionLevel()))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
