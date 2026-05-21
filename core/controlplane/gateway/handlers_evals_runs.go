package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	evalRunSyncThreshold      = 500
	evalRunLockTTL            = 30 * time.Minute
	evalRunPendingTTL         = 2 * time.Hour
	evalRunLockResourcePrefix = "eval:run:lock:"
	evalRunPendingKeyPrefix   = "eval:run:pending:"
)

var (
	evalRunRateLimiter = newFixedWindowLimiter(12, time.Hour)
	evalRunAsyncSpawn  = func(fn func()) { go fn() }
)

type runEvalDatasetRequest struct {
	UseCurrentPolicy  bool   `json:"use_current_policy,omitempty"`
	CandidateBundleID string `json:"candidate_bundle_id,omitempty"`
	CandidateContent  string `json:"candidate_content,omitempty"`
	MaxEntries        int    `json:"max_entries,omitempty"`
}

type evalRunAcceptedResponse struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	PollURL string `json:"poll_url,omitempty"`
}

type evalRunPendingRecord struct {
	RunID          string `json:"run_id"`
	DatasetID      string `json:"dataset_id"`
	DatasetName    string `json:"dataset_name"`
	DatasetVersion int    `json:"dataset_version"`
	Tenant         string `json:"tenant"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at,omitempty"`
	PolicySnapshot string `json:"policy_snapshot,omitempty"`
	Error          string `json:"error,omitempty"`
}

type evalRunListResponse struct {
	Items      []runner.RunResult `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type gatewayEvalPolicyEvaluator struct {
	policy   *config.SafetyPolicy
	snapshot string
}

func (e gatewayEvalPolicyEvaluator) Evaluate(req *pb.JobRequest) (string, string, string, error) {
	resp := policybundles.EvaluatePolicyCheck(e.policy, e.snapshot, jobRequestToPolicyCheckRequest(req))
	if resp == nil {
		return "", "", "", fmt.Errorf("policy evaluation returned nil response")
	}
	return protoDecisionToString(resp.GetDecision()), resp.GetRuleId(), resp.GetReason(), nil
}

func (s *server) handleRunEvalDataset(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsRunsExecute, "admin") {
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.evalDatasetStore == nil || s.evalRunStore == nil || s.configSvc == nil || s.jobStore == nil || s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval runner unavailable")
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if !evalRunRateLimiter.Allow(tenant) {
		writeErrorJSON(w, http.StatusTooManyRequests, "eval run rate limit exceeded")
		return
	}

	datasetID := strings.TrimSpace(r.PathValue("id"))
	if datasetID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "eval dataset id required")
		return
	}

	dataset, err := s.evalDatasetStore.GetEvalDataset(r.Context(), tenant, datasetID)
	if err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset", err)
		return
	}

	var body runEvalDatasetRequest
	if r.Body != nil && r.Body != http.NoBody {
		if err := decodeJSONBody(w, r, &body); err != nil {
			if !errors.Is(err, io.EOF) {
				writeJSONDecodeError(w, err, "invalid json")
				return
			}
		}
	}
	body.CandidateBundleID = strings.TrimSpace(body.CandidateBundleID)
	body.CandidateContent = strings.TrimSpace(body.CandidateContent)
	if body.MaxEntries < 0 {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "max_entries must be >= 0")
		return
	}
	if body.MaxEntries > runner.HardMaxEntries {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "max_entries must be <= 10000")
		return
	}
	if !body.UseCurrentPolicy && body.CandidateBundleID == "" && body.CandidateContent == "" {
		body.UseCurrentPolicy = true
	}

	policy, snapshot, err := s.loadEvalRunPolicy(r.Context(), body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, err.Error())
		return
	}

	runReq := runner.RunRequest{
		Tenant:            tenant,
		DatasetID:         dataset.ID,
		UseCurrentPolicy:  body.UseCurrentPolicy,
		CandidateBundleID: body.CandidateBundleID,
		CandidateContent:  body.CandidateContent,
		MaxEntries:        body.MaxEntries,
	}

	runID := uuid.NewString()
	entryCount := effectiveEvalRunEntryCount(dataset.EntryCount, len(dataset.Entries), runReq.MaxEntries)
	if entryCount <= evalRunSyncThreshold {
		result, err := s.executeEvalRun(r.Context(), runID, tenant, dataset, runReq, policy, snapshot)
		if err != nil {
			writeInternalError(w, r, "execute eval run", err)
			return
		}
		if err := s.evalRunStore.CreateRun(r.Context(), result); err != nil {
			writeInternalError(w, r, "store eval run", err)
			return
		}
		slog.Info("eval dataset run completed",
			"run_id", result.RunID,
			"dataset_id", result.DatasetID,
			"tenant", tenant,
			"entry_count", result.Summary.Total,
			"regressions", result.Summary.Regressions,
			"actor", policybundles.PolicyActorID(r),
			"role", policybundles.PolicyRole(r),
		)
		s.appendAuditEntryNamed(r.Context(), "run", "eval_dataset", dataset.ID, dataset.Name,
			policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
			"run eval dataset "+dataset.Name+" v"+strconv.Itoa(dataset.Version))
		writeJSON(w, result)
		return
	}

	lockResource := evalRunLockResource(tenant, dataset.ID)
	if _, ok, err := s.lockStore.Acquire(r.Context(), lockResource, runID, locks.ModeExclusive, evalRunLockTTL); err != nil {
		writeInternalError(w, r, "acquire eval run lock", err)
		return
	} else if !ok {
		writeJSONError(w, http.StatusConflict, errorCodeEvalRunConflict, "eval dataset run already in progress")
		return
	}

	startedAt := time.Now().UTC()
	pending := evalRunPendingRecord{
		RunID:          runID,
		DatasetID:      dataset.ID,
		DatasetName:    dataset.Name,
		DatasetVersion: dataset.Version,
		Tenant:         tenant,
		Status:         "pending",
		StartedAt:      startedAt.Format(time.RFC3339Nano),
		PolicySnapshot: snapshot,
	}
	if err := s.saveEvalRunPending(r.Context(), pending); err != nil {
		_, _, _ = s.lockStore.Release(r.Context(), lockResource, runID)
		writeInternalError(w, r, "record pending eval run", err)
		return
	}

	reqCtx := r.Context()
	evalRunAsyncSpawn(func() {
		s.completeEvalRunAsync(reqCtx, lockResource, pending, runReq, dataset, policy, snapshot)
	})

	slog.Info("eval dataset run accepted",
		"run_id", runID,
		"dataset_id", dataset.ID,
		"tenant", tenant,
		"entry_count", entryCount,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, evalRunAcceptedResponse{
		RunID:   runID,
		Status:  "pending",
		PollURL: "/api/v1/evals/runs/" + runID,
	})
}

func (s *server) handleListEvalRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsRunsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalDatasetStore == nil || s.evalRunStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval runner unavailable")
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	datasetID := strings.TrimSpace(r.PathValue("id"))
	if datasetID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "eval dataset id required")
		return
	}
	if _, err := s.evalDatasetStore.GetEvalDataset(r.Context(), tenant, datasetID); err != nil {
		if errors.Is(err, store.ErrEvalDatasetNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval dataset not found")
			return
		}
		writeInternalError(w, r, "get eval dataset", err)
		return
	}

	filter := store.RunFilter{DatasetID: datasetID}
	q := r.URL.Query()
	if raw := strings.TrimSpace(q.Get("has_regression")); raw != "" {
		parsed, err := parseStrictBool(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "has_regression must be true or false")
			return
		}
		filter.HasRegression = parsed
	}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		ms, err := parseEvalDatasetQueryTimestamp(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "since must be RFC3339")
			return
		}
		filter.SinceMS = ms
	}
	if raw := strings.TrimSpace(q.Get("until")); raw != "" {
		ms, err := parseEvalDatasetQueryTimestamp(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "until must be RFC3339")
			return
		}
		filter.UntilMS = ms
	}
	if filter.SinceMS > 0 && filter.UntilMS > 0 && filter.SinceMS > filter.UntilMS {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "since must be <= until")
		return
	}
	if raw := strings.TrimSpace(q.Get("min_score")); raw != "" {
		minScore, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "min_score must be a number")
			return
		}
		filter.MinScore = minScore
		filter.MinScoreSet = true
	}

	cursor := strings.TrimSpace(q.Get("cursor"))
	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "limit must be a positive integer")
			return
		}
		limit = n
	}

	page, err := s.evalRunStore.ListRuns(r.Context(), tenant, filter, cursor, limit)
	if err != nil {
		writeInternalError(w, r, "list eval runs", err)
		return
	}
	if page.Items == nil {
		page.Items = []runner.RunResult{}
	}
	writeJSON(w, evalRunListResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleGetEvalRun(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsRunsRead, "admin", "operator", "viewer") {
		return
	}
	if s.evalRunStore == nil || s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval runner unavailable")
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "run id required")
		return
	}

	result, err := s.evalRunStore.GetRun(r.Context(), tenant, runID)
	if err == nil {
		writeJSON(w, result)
		return
	}
	if !errors.Is(err, store.ErrEvalRunNotFound) {
		writeInternalError(w, r, "get eval run", err)
		return
	}

	pending, ok, err := s.getEvalRunPending(r.Context(), tenant, runID)
	if err != nil {
		writeInternalError(w, r, "get pending eval run", err)
		return
	}
	if ok {
		if pending.Status == "failed" {
			writeJSON(w, pending)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, pending)
		return
	}

	writeErrorJSON(w, http.StatusNotFound, "eval run not found")
}

func (s *server) handleDeleteEvalRun(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsRunsDelete, "admin") {
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.evalRunStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "eval runner unavailable")
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "run id required")
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("force")) != "true" {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalRunNotRunnable, "eval run delete requires force=true")
		return
	}

	existing, err := s.evalRunStore.GetRun(r.Context(), tenant, runID)
	if err != nil {
		if errors.Is(err, store.ErrEvalRunNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "eval run not found")
			return
		}
		writeInternalError(w, r, "get eval run", err)
		return
	}
	if err := s.evalRunStore.DeleteRun(r.Context(), tenant, runID); err != nil {
		writeInternalError(w, r, "delete eval run", err)
		return
	}
	_ = s.deleteEvalRunPending(r.Context(), tenant, runID)

	s.appendAuditEntryNamed(r.Context(), "delete", "eval_run", runID, existing.DatasetName,
		policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		"force-delete eval run "+runID+" for dataset "+existing.DatasetName)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) completeEvalRunAsync(parentCtx context.Context, lockResource string, pending evalRunPendingRecord, req runner.RunRequest, dataset model.EvalDataset, policy *config.SafetyPolicy, snapshot string) {
	ctx := context.Background()
	if parentCtx != nil {
		ctx = parentCtx
	}
	ctx = context.WithoutCancel(ctx)
	defer func() {
		if s != nil && s.lockStore != nil {
			_, _, _ = s.lockStore.Release(context.Background(), lockResource, pending.RunID)
		}
	}()

	pending.Status = "running"
	_ = s.saveEvalRunPending(ctx, pending)

	result, err := s.executeEvalRun(ctx, pending.RunID, pending.Tenant, dataset, req, policy, snapshot)
	if err != nil {
		pending.Status = "failed"
		pending.Error = err.Error()
		pending.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_ = s.saveEvalRunPending(context.Background(), pending)
		slog.Error("async eval dataset run failed",
			"run_id", pending.RunID,
			"dataset_id", pending.DatasetID,
			"tenant", pending.Tenant,
			"error", err,
		)
		return
	}
	if err := s.evalRunStore.CreateRun(ctx, result); err != nil {
		pending.Status = "failed"
		pending.Error = err.Error()
		pending.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		_ = s.saveEvalRunPending(context.Background(), pending)
		slog.Error("async eval dataset run persist failed",
			"run_id", pending.RunID,
			"dataset_id", pending.DatasetID,
			"tenant", pending.Tenant,
			"error", err,
		)
		return
	}
	_ = s.deleteEvalRunPending(context.Background(), pending.Tenant, pending.RunID)
}

func (s *server) executeEvalRun(ctx context.Context, runID, tenant string, dataset model.EvalDataset, req runner.RunRequest, policy *config.SafetyPolicy, snapshot string) (runner.RunResult, error) {
	return runner.Run(ctx, runner.RunContext{
		RunID:          runID,
		Tenant:         tenant,
		DatasetName:    dataset.Name,
		DatasetVersion: dataset.Version,
		PolicySnapshot: snapshot,
	}, dataset, gatewayEvalPolicyEvaluator{policy: policy, snapshot: snapshot}, req)
}

func (s *server) loadEvalRunPolicy(ctx context.Context, req runEvalDatasetRequest) (*config.SafetyPolicy, string, error) {
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("load policy bundles: %w", err)
	}
	if req.UseCurrentPolicy {
		policy, snapshot, err := policybundles.BuildPolicyFromBundles(bundles)
		if err != nil {
			return nil, "", fmt.Errorf("current policy invalid: %w", err)
		}
		return policy, snapshot, nil
	}

	working := policybundles.CloneBundleMap(bundles)
	if req.CandidateContent != "" {
		bundleID := req.CandidateBundleID
		if bundleID == "" {
			bundleID = "__eval_run_candidate__"
		}
		working[bundleID] = map[string]any{
			"content": req.CandidateContent,
			"enabled": true,
		}
	}
	policy, snapshot, err := policybundles.BuildPolicyFromBundles(working)
	if err != nil {
		return nil, "", fmt.Errorf("candidate policy invalid: %w", err)
	}
	return policy, snapshot, nil
}

func effectiveEvalRunEntryCount(datasetEntryCount, actualEntryCount, maxEntries int) int {
	count := datasetEntryCount
	if count <= 0 {
		count = actualEntryCount
	}
	if count <= 0 {
		return 0
	}
	if maxEntries > 0 && maxEntries < count {
		count = maxEntries
	}
	if count > runner.HardMaxEntries {
		count = runner.HardMaxEntries
	}
	return count
}

func evalRunLockResource(tenant, datasetID string) string {
	return evalRunLockResourcePrefix + strings.TrimSpace(tenant) + ":" + strings.TrimSpace(datasetID)
}

func evalRunPendingKey(tenant, runID string) string {
	return evalRunPendingKeyPrefix + strings.TrimSpace(tenant) + ":" + strings.TrimSpace(runID)
}

func (s *server) saveEvalRunPending(ctx context.Context, pending evalRunPendingRecord) error {
	if s == nil || s.jobStore == nil {
		return fmt.Errorf("job store unavailable")
	}
	payload, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("marshal pending eval run: %w", err)
	}
	if err := s.jobStore.Client().Set(ctx, evalRunPendingKey(pending.Tenant, pending.RunID), payload, evalRunPendingTTL).Err(); err != nil {
		return fmt.Errorf("set pending eval run: %w", err)
	}
	return nil
}

func (s *server) getEvalRunPending(ctx context.Context, tenant, runID string) (evalRunPendingRecord, bool, error) {
	if s == nil || s.jobStore == nil {
		return evalRunPendingRecord{}, false, fmt.Errorf("job store unavailable")
	}
	raw, err := s.jobStore.Client().Get(ctx, evalRunPendingKey(tenant, runID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return evalRunPendingRecord{}, false, nil
		}
		return evalRunPendingRecord{}, false, fmt.Errorf("get pending eval run: %w", err)
	}
	var pending evalRunPendingRecord
	if err := json.Unmarshal(raw, &pending); err != nil {
		return evalRunPendingRecord{}, false, fmt.Errorf("decode pending eval run: %w", err)
	}
	if pending.Tenant != tenant {
		return evalRunPendingRecord{}, false, nil
	}
	return pending, true, nil
}

func (s *server) deleteEvalRunPending(ctx context.Context, tenant, runID string) error {
	if s == nil || s.jobStore == nil {
		return nil
	}
	return s.jobStore.Client().Del(ctx, evalRunPendingKey(tenant, runID)).Err()
}
