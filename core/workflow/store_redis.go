package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	defaultWorkflowRedisURL = "redis://localhost:6379"
	timelineMaxEntries      = 1000
	pendingAuditHashTTL     = 24 * time.Hour
)

type runJobRef struct {
	RunID    string   `json:"run_id"`
	StepPath []string `json:"step_path,omitempty"`
}

type auditHashUpdateResult struct {
	matched    bool
	changed    bool
	missingRun bool
}

// RedisStore persists workflow definitions and runs in Redis.
type RedisStore struct {
	client redis.UniversalClient
}

// NewRedisWorkflowStore constructs a Redis-backed workflow store.
func NewRedisWorkflowStore(url string) (*RedisStore, error) {
	if url == "" {
		url = defaultWorkflowRedisURL
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &RedisStore{client: client}, nil
}

// NewRedisWorkflowStoreFromClient constructs a workflow store from a shared Redis client.
func NewRedisWorkflowStoreFromClient(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client}
}

// Close closes the underlying Redis client.
func (s *RedisStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// SaveWorkflow upserts a workflow definition and updates org index.
func (s *RedisStore) SaveWorkflow(ctx context.Context, wf *Workflow) error {
	if wf == nil || wf.ID == "" {
		return fmt.Errorf("workflow id required")
	}
	now := time.Now().UTC()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = now
	}
	wf.UpdatedAt = now

	payload, err := json.Marshal(wf)
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, workflowKey(wf.ID), payload, 0)
	if wf.OrgID != "" {
		pipe.ZAdd(ctx, workflowOrgIndexKey(wf.OrgID), redis.Z{Score: float64(now.Unix()), Member: wf.ID})
	}
	pipe.ZAdd(ctx, workflowAllIndexKey(), redis.Z{Score: float64(now.Unix()), Member: wf.ID})
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("save workflow: %w", err)
	}
	return nil
}

// GetWorkflow returns a workflow definition by ID.
func (s *RedisStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	if id == "" {
		return nil, fmt.Errorf("id required")
	}
	data, err := s.client.Get(ctx, workflowKey(id)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get workflow %s: %w", id, err)
	}
	var wf Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("unmarshal workflow: %w", err)
	}
	return &wf, nil
}

// DeleteWorkflow removes a workflow definition and its indexes.
func (s *RedisStore) DeleteWorkflow(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id required")
	}
	wf, err := s.GetWorkflow(ctx, id)
	if err != nil {
		return fmt.Errorf("load workflow for delete: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, workflowKey(id))
	pipe.ZRem(ctx, workflowAllIndexKey(), id)
	if wf.OrgID != "" {
		pipe.ZRem(ctx, workflowOrgIndexKey(wf.OrgID), id)
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	return nil
}

// ListWorkflows returns recent workflows, optionally scoped by org.
func (s *RedisStore) ListWorkflows(ctx context.Context, orgID string, limit int64) ([]*Workflow, error) {
	if limit <= 0 {
		limit = 50
	}
	index := workflowAllIndexKey()
	if orgID != "" {
		index = workflowOrgIndexKey(orgID)
	}
	ids, err := s.client.ZRevRange(ctx, index, 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	if len(ids) == 0 {
		return []*Workflow{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, workflowKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "workflow_store_batch_get", "error", err)
	}

	out := make([]*Workflow, 0, len(ids))
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var wf Workflow
		if err := json.Unmarshal(data, &wf); err != nil {
			slog.Warn("workflow-store: corrupt workflow skipped", "id", id, "error", err)
			continue
		}
		out = append(out, &wf)
	}
	return out, nil
}

// CountWorkflows returns the number of workflow definitions, optionally scoped
// by org.
func (s *RedisStore) CountWorkflows(ctx context.Context, orgID string) (int64, error) {
	index := workflowAllIndexKey()
	if orgID != "" {
		index = workflowOrgIndexKey(orgID)
	}
	count, err := s.client.ZCard(ctx, index).Result()
	if err != nil {
		return 0, fmt.Errorf("count workflows: %w", err)
	}
	return count, nil
}

// CreateRun persists a new workflow run and indexes it by workflow.
func (s *RedisStore) CreateRun(ctx context.Context, run *WorkflowRun) error {
	if run == nil || run.ID == "" || run.WorkflowID == "" {
		return fmt.Errorf("run id and workflow id required")
	}
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	if run.Status == "" {
		run.Status = RunStatusPending
	}
	pendingDeletes := s.applyPendingAuditHashes(ctx, run)

	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	jobRefs := collectRunJobRefs(run)
	jobRefPayloads, err := marshalJobRefs(jobRefs)
	if err != nil {
		return fmt.Errorf("marshal run job index: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, runKey(run.ID), payload, 0)
	pipe.ZAdd(ctx, runIndexKey(run.WorkflowID), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runAllIndexKey(), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runStatusIndexKey(run.Status), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	enqueueRunJobIndexWrites(ctx, pipe, jobRefPayloads)
	enqueuePendingAuditDeletes(ctx, pipe, pendingDeletes)
	if run.OrgID != "" {
		activeKey := runOrgActiveKey(run.OrgID)
		if isActiveRunStatus(run.Status) {
			pipe.SAdd(ctx, activeKey, run.ID)
		} else {
			pipe.SRem(ctx, activeKey, run.ID)
		}
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	if run.IdempotencyKey != "" {
		if _, err := s.TrySetRunIdempotencyKey(ctx, run.IdempotencyKey, run.ID); err != nil {
			slog.Warn("workflow: idempotency key set failed", "key", run.IdempotencyKey, "run_id", run.ID, "error", err)
		}
	}
	return nil
}

// updateRunScript atomically:
//  1. Reads the persisted run and extracts its previous status (so the index
//     update outside the script can ZREM the stale status set when status flips).
//  2. Merges any populated `audit_hash` from persisted-side StepRuns forward
//     into the new payload's StepRuns whose `audit_hash` is empty for the same
//     `job_id` (recursively walks `children`). This is the load-bearing
//     atomicity guarantee: a concurrent UpdateAuditHash that wrote a hash
//     between the caller's marshal and this SET cannot be lost because the
//     GET-merge-SET runs as a single Redis command, eliminating the Go-level
//     race window the previous Lua-then-Go-merge implementation had.
//  3. Writes the (possibly merged) payload back.
//
// Only touches a single key (KEYS[1] = runKey) so the script is cluster-safe.
// Index updates (ZADD/ZREM/SADD/SREM) are issued in a separate Go pipeline
// after this script returns; they are idempotent and eventual-consistency safe.
//
// KEYS: [1]=runKey
// ARGV: [1]=payload (JSON-encoded WorkflowRun)
//
// Returns: previous status string ("" if the key did not exist).
var updateRunScript = redis.NewScript(`
local prev_raw = redis.call('GET', KEYS[1])
local prev_status = ''
local final_payload = ARGV[1]

if prev_raw then
  local ok_prev, prev_doc = pcall(cjson.decode, prev_raw)
  if ok_prev and type(prev_doc) == 'table' then
    if prev_doc.status then
      prev_status = prev_doc.status
    end

    local hashes = {}
    local has_hashes = false
    local collect
    collect = function(steps)
      if type(steps) ~= 'table' then return end
      for _, sr in pairs(steps) do
        if type(sr) == 'table' then
          if sr.job_id and sr.job_id ~= '' and sr.audit_hash and sr.audit_hash ~= '' then
            hashes[sr.job_id] = sr.audit_hash
            has_hashes = true
          end
          if sr.children then
            collect(sr.children)
          end
        end
      end
    end
    collect(prev_doc.steps)

    if has_hashes then
      local ok_next, next_doc = pcall(cjson.decode, ARGV[1])
      if ok_next and type(next_doc) == 'table' then
        local merged = false
        local apply
        apply = function(steps)
          if type(steps) ~= 'table' then return end
          for _, sr in pairs(steps) do
            if type(sr) == 'table' then
              if sr.job_id and sr.job_id ~= '' and (not sr.audit_hash or sr.audit_hash == '') then
                local h = hashes[sr.job_id]
                if h then
                  sr.audit_hash = h
                  merged = true
                end
              end
              if sr.children then
                apply(sr.children)
              end
            end
          end
        end
        apply(next_doc.steps)

        if merged then
          final_payload = cjson.encode(next_doc)
        end
      end
    end
  end
end

redis.call('SET', KEYS[1], final_payload)
return prev_status
`)

// UpdateRun atomically overwrites an existing run document and updates all indexes.
//
// The Lua script runs GET-merge-SET as a single atomic Redis command. The merge
// step copies any `audit_hash` from the persisted run forward into the new
// payload for matching `job_id`s where the new payload's `audit_hash` is empty.
// This closes the lost-update race the previous implementation had: when a
// concurrent UpdateAuditHash wrote an audit hash between the caller's marshal
// and this SET, that hash is now seen by the script's GET and merged forward
// into the SET payload. Index updates run in a separate idempotent pipeline
// after the script. Pending audit-hash recovery (a separate key set by
// UpdateAuditHash when the run/step was not yet persisted) is applied Go-side
// before the script — it operates on the wf:run:pending_audit_hash:<jobID> key
// space, which the Lua merge does not touch.
func (s *RedisStore) UpdateRun(ctx context.Context, run *WorkflowRun) error {
	if run == nil || run.ID == "" || run.WorkflowID == "" {
		return fmt.Errorf("run id and workflow id required")
	}
	now := time.Now().UTC()
	run.UpdatedAt = now
	pendingDeletes := s.applyPendingAuditHashes(ctx, run)
	oldJobRefs := s.loadOldJobRefs(ctx, run.ID)

	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	jobRefs := collectRunJobRefs(run)
	jobRefPayloads, err := marshalJobRefs(jobRefs)
	if err != nil {
		return fmt.Errorf("marshal run job index: %w", err)
	}

	prevStatus, err := updateRunScript.Run(ctx, s.client, []string{runKey(run.ID)}, string(payload)).Text()
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}

	score := float64(now.Unix())
	pipe := s.client.TxPipeline()
	pipe.ZAdd(ctx, runIndexKey(run.WorkflowID), redis.Z{Score: score, Member: run.ID})
	pipe.ZAdd(ctx, runAllIndexKey(), redis.Z{Score: score, Member: run.ID})
	pipe.ZAdd(ctx, runStatusIndexKey(run.Status), redis.Z{Score: score, Member: run.ID})
	enqueueRunJobIndexWrites(ctx, pipe, jobRefPayloads)
	enqueueRunJobIndexDeletes(ctx, pipe, oldJobRefs, jobRefs)
	enqueuePendingAuditDeletes(ctx, pipe, pendingDeletes)

	if prevStatus != "" && prevStatus != string(run.Status) {
		pipe.ZRem(ctx, runStatusIndexKey(RunStatus(prevStatus)), run.ID)
	}

	if run.OrgID != "" {
		orgKey := runOrgActiveKey(run.OrgID)
		if isActiveRunStatus(run.Status) {
			pipe.SAdd(ctx, orgKey, run.ID)
		} else {
			pipe.SRem(ctx, orgKey, run.ID)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("update run: index pipeline failed (idempotent, will self-heal)", "run_id", run.ID, "error", err)
	}
	return nil
}

// loadOldJobRefs returns the run's persisted job-ref set so the post-script
// pipeline can DEL job-index entries that the new payload no longer carries.
// Returns an empty map if the key does not exist or fails to decode — both
// mean "nothing to clean up".
func (s *RedisStore) loadOldJobRefs(ctx context.Context, runID string) map[string]runJobRef {
	data, err := s.client.Get(ctx, runKey(runID)).Bytes()
	if err != nil {
		return map[string]runJobRef{}
	}
	var prev WorkflowRun
	if err := json.Unmarshal(data, &prev); err != nil {
		slog.Warn("workflow: corrupt run snapshot skipped", "run_id", runID, "error", err)
		return map[string]runJobRef{}
	}
	return collectRunJobRefs(&prev)
}

// GetRun fetches a run by ID.
func (s *RedisStore) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	data, err := s.client.Get(ctx, runKey(runID)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get run %s: %w", runID, err)
	}
	var run WorkflowRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("unmarshal run: %w", err)
	}
	return &run, nil
}

// DeleteRun removes a workflow run and its indexes.
func (s *RedisStore) DeleteRun(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("run id required")
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("load run for delete: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, runKey(runID))
	pipe.ZRem(ctx, runAllIndexKey(), runID)
	enqueueRunJobIndexDeletes(ctx, pipe, collectRunJobRefs(run), nil)
	if run.WorkflowID != "" {
		pipe.ZRem(ctx, runIndexKey(run.WorkflowID), runID)
	}
	if run.Status != "" {
		pipe.ZRem(ctx, runStatusIndexKey(run.Status), runID)
	}
	if run.OrgID != "" {
		pipe.SRem(ctx, runOrgActiveKey(run.OrgID), runID)
	}
	pipe.Del(ctx, runTimelineKey(runID))
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	return nil
}

// CountActiveRuns returns the number of active runs for an org.
func (s *RedisStore) CountActiveRuns(ctx context.Context, orgID string) (int, error) {
	if orgID == "" {
		return 0, fmt.Errorf("org id required")
	}
	count, err := s.client.SCard(ctx, runOrgActiveKey(orgID)).Result()
	if err != nil {
		return 0, fmt.Errorf("count active runs: %w", err)
	}
	return int(count), nil
}

// CountRunsSince returns the number of workflow runs updated on or after the
// provided time.
func (s *RedisStore) CountRunsSince(ctx context.Context, since time.Time) (int64, error) {
	count, err := s.client.ZCount(ctx, runAllIndexKey(), fmt.Sprintf("%d", since.UTC().Unix()), "+inf").Result()
	if err != nil {
		return 0, fmt.Errorf("count runs since: %w", err)
	}
	return count, nil
}

// ListRunsByWorkflow returns recent runs for a workflow.
func (s *RedisStore) ListRunsByWorkflow(ctx context.Context, workflowID string, limit int64) ([]*WorkflowRun, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("workflow id required")
	}
	if limit <= 0 {
		limit = 50
	}
	ids, err := s.client.ZRevRange(ctx, runIndexKey(workflowID), 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("list runs for workflow %s: %w", workflowID, err)
	}
	if len(ids) == 0 {
		return []*WorkflowRun{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, runKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "workflow_store_batch_get", "error", err)
	}

	out := make([]*WorkflowRun, 0, len(ids))
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var run WorkflowRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		out = append(out, &run)
	}
	return out, nil
}

// ListRuns returns recent runs across all workflows, ordered by updated time.
func (s *RedisStore) ListRuns(ctx context.Context, cursorUnix int64, limit int64) ([]*WorkflowRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if cursorUnix <= 0 {
		cursorUnix = time.Now().UTC().Unix()
	}
	ids, err := s.client.ZRevRangeByScore(ctx, runAllIndexKey(), &redis.ZRangeBy{
		Max:    fmt.Sprintf("%d", cursorUnix),
		Min:    "-inf",
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	if len(ids) == 0 {
		return []*WorkflowRun{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(ids))
	for _, id := range ids {
		cmds[id] = pipe.Get(ctx, runKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "workflow_store_batch_get", "error", err)
	}

	out := make([]*WorkflowRun, 0, len(ids))
	for _, id := range ids {
		cmd := cmds[id]
		if cmd == nil {
			continue
		}
		data, err := cmd.Bytes()
		if err != nil {
			continue
		}
		var run WorkflowRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		out = append(out, &run)
	}
	return out, nil
}

// AppendTimelineEvent records a workflow run event in append-only order.
func (s *RedisStore) AppendTimelineEvent(ctx context.Context, runID string, event *TimelineEvent) error {
	if runID == "" {
		return fmt.Errorf("run id required")
	}
	if event == nil {
		return fmt.Errorf("event required")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal timeline event: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, runTimelineKey(runID), data)
	pipe.LTrim(ctx, runTimelineKey(runID), -timelineMaxEntries, -1)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("append timeline event: %w", err)
	}
	return nil
}

// ListTimelineEvents returns timeline events for a run in chronological order.
func (s *RedisStore) ListTimelineEvents(ctx context.Context, runID string, limit int64) ([]TimelineEvent, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	if limit <= 0 {
		limit = 100
	}
	raw, err := s.client.LRange(ctx, runTimelineKey(runID), 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("list timeline events: %w", err)
	}
	out := make([]TimelineEvent, 0, len(raw))
	for _, item := range raw {
		var evt TimelineEvent
		if err := json.Unmarshal([]byte(item), &evt); err != nil {
			continue
		}
		out = append(out, evt)
	}
	return out, nil
}

// ListRunIDsByStatus returns recent run IDs filtered by status.
func (s *RedisStore) ListRunIDsByStatus(ctx context.Context, status RunStatus, limit int64) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	if status == "" {
		return nil, fmt.Errorf("status required")
	}
	ids, err := s.client.ZRevRange(ctx, runStatusIndexKey(status), 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []string{}, nil
	}
	return ids, nil
}

// UpdateAuditHash implements audit.StepHashSink for workflow runs. It stores
// auditHash on every StepRun whose JobID matches jobID. Missing workflow jobs
// are a no-op; if the job looks like a workflow job but the step has not been
// persisted yet, the hash is held briefly and applied by the next Create/UpdateRun.
func (s *RedisStore) UpdateAuditHash(ctx context.Context, jobID, auditHash string) error {
	jobID = strings.TrimSpace(jobID)
	auditHash = strings.TrimSpace(auditHash)
	if jobID == "" || auditHash == "" {
		return nil
	}
	if !isAuditHashHex(auditHash) {
		return fmt.Errorf("audit hash must be a 64-character hex SHA-256 digest")
	}

	ref, found, err := s.lookupRunJobRef(ctx, jobID)
	if err != nil {
		return err
	}
	runID := ref.RunID
	parsedRunID, _ := splitJobID(jobID)
	if runID == "" {
		runID = parsedRunID
	}
	if runID == "" {
		return nil
	}

	res, err := s.updateAuditHashInRun(ctx, runID, jobID, auditHash)
	if err != nil {
		return err
	}
	if res.matched {
		_ = s.client.Del(ctx, pendingAuditHashKey(jobID)).Err()
		if !found {
			_ = s.indexSingleRunJobRef(ctx, jobID, runJobRef{RunID: runID})
		}
		return nil
	}
	if found || parsedRunID != "" || res.missingRun {
		if err := s.storePendingAuditHash(ctx, jobID, auditHash); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisStore) lookupRunJobRef(ctx context.Context, jobID string) (runJobRef, bool, error) {
	raw, err := s.client.Get(ctx, runJobIndexKey(jobID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return runJobRef{}, false, nil
	}
	if err != nil {
		return runJobRef{}, false, fmt.Errorf("lookup workflow job index: %w", err)
	}
	var ref runJobRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return runJobRef{}, false, fmt.Errorf("unmarshal workflow job index: %w", err)
	}
	return ref, ref.RunID != "", nil
}

func (s *RedisStore) updateAuditHashInRun(ctx context.Context, runID, jobID, auditHash string) (auditHashUpdateResult, error) {
	key := runKey(runID)
	var result auditHashUpdateResult
	for attempt := 0; attempt < 5; attempt++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			next, loadErr := loadRunForAuditHash(ctx, tx, key)
			if errors.Is(loadErr, redis.Nil) {
				result = auditHashUpdateResult{missingRun: true}
				return nil
			}
			if loadErr != nil {
				return loadErr
			}
			result = mutateRunAuditHash(next, jobID, auditHash)
			if !result.changed {
				return nil
			}
			payload, err := json.Marshal(next)
			if err != nil {
				return fmt.Errorf("marshal run audit hash update: %w", err)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, 0)
				return nil
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return result, err
	}
	return result, fmt.Errorf("update audit hash for job %s: redis transaction retries exhausted", jobID)
}

func loadRunForAuditHash(ctx context.Context, tx *redis.Tx, key string) (*WorkflowRun, error) {
	data, err := tx.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var run WorkflowRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("unmarshal run for audit hash update: %w", err)
	}
	return &run, nil
}

func mutateRunAuditHash(run *WorkflowRun, jobID, auditHash string) auditHashUpdateResult {
	if run == nil || run.Steps == nil {
		return auditHashUpdateResult{}
	}
	matches, changed := setStepAuditHashForJob(run.Steps, jobID, auditHash)
	return auditHashUpdateResult{
		matched: matches > 0,
		changed: changed > 0,
	}
}

func setStepAuditHashForJob(steps map[string]*StepRun, jobID, auditHash string) (matches, changed int) {
	for _, sr := range steps {
		m, c := setStepRunAuditHashForJob(sr, jobID, auditHash)
		matches += m
		changed += c
	}
	return matches, changed
}

func setStepRunAuditHashForJob(sr *StepRun, jobID, auditHash string) (matches, changed int) {
	if sr == nil {
		return 0, 0
	}
	if sr.JobID == jobID {
		matches++
		if sr.AuditHash == "" {
			sr.AuditHash = auditHash
			changed++
		}
	}
	if len(sr.Children) > 0 {
		m, c := setStepAuditHashForJob(sr.Children, jobID, auditHash)
		matches += m
		changed += c
	}
	return matches, changed
}

func isAuditHashHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func (s *RedisStore) applyPendingAuditHashes(ctx context.Context, run *WorkflowRun) []string {
	if run == nil || run.Steps == nil {
		return nil
	}
	refs := collectRunJobRefs(run)
	deletes := make([]string, 0)
	for jobID := range refs {
		hash, err := s.client.Get(ctx, pendingAuditHashKey(jobID)).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			slog.Warn("workflow: pending audit hash lookup failed", "run_id", run.ID, "job_id", jobID, "error", err)
			continue
		}
		if strings.TrimSpace(hash) == "" {
			continue
		}
		res := mutateRunAuditHash(run, jobID, hash)
		if res.matched {
			deletes = append(deletes, pendingAuditHashKey(jobID))
		}
	}
	return deletes
}

func (s *RedisStore) storePendingAuditHash(ctx context.Context, jobID, auditHash string) error {
	return s.client.Set(ctx, pendingAuditHashKey(jobID), auditHash, pendingAuditHashTTL).Err()
}

func (s *RedisStore) indexSingleRunJobRef(ctx context.Context, jobID string, ref runJobRef) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return fmt.Errorf("marshal workflow job index: %w", err)
	}
	return s.client.Set(ctx, runJobIndexKey(jobID), raw, 0).Err()
}

func marshalJobRefs(refs map[string]runJobRef) (map[string][]byte, error) {
	out := make(map[string][]byte, len(refs))
	for jobID, ref := range refs {
		raw, err := json.Marshal(ref)
		if err != nil {
			return nil, err
		}
		out[jobID] = raw
	}
	return out, nil
}

func collectRunJobRefs(run *WorkflowRun) map[string]runJobRef {
	refs := map[string]runJobRef{}
	if run == nil || run.Steps == nil {
		return refs
	}
	for mapID, sr := range run.Steps {
		stepID := mapID
		if sr != nil && sr.StepID != "" {
			stepID = sr.StepID
		}
		collectStepRunJobRefs(refs, run.ID, []string{stepID}, sr)
	}
	return refs
}

func collectStepRunJobRefs(refs map[string]runJobRef, runID string, path []string, sr *StepRun) {
	if sr == nil {
		return
	}
	if sr.JobID != "" {
		refs[sr.JobID] = runJobRef{RunID: runID, StepPath: append([]string(nil), path...)}
	}
	for childID, child := range sr.Children {
		nextPath := append(append([]string(nil), path...), childID)
		collectStepRunJobRefs(refs, runID, nextPath, child)
	}
}

func enqueueRunJobIndexWrites(ctx context.Context, pipe redis.Pipeliner, refs map[string][]byte) {
	for jobID, raw := range refs {
		pipe.Set(ctx, runJobIndexKey(jobID), raw, 0)
	}
}

func enqueueRunJobIndexDeletes(ctx context.Context, pipe redis.Pipeliner, oldRefs, newRefs map[string]runJobRef) {
	for jobID := range oldRefs {
		if _, ok := newRefs[jobID]; !ok {
			pipe.Del(ctx, runJobIndexKey(jobID))
		}
	}
}

func enqueuePendingAuditDeletes(ctx context.Context, pipe redis.Pipeliner, keys []string) {
	for _, key := range keys {
		pipe.Del(ctx, key)
	}
}

func workflowKey(id string) string {
	return "wf:def:" + id
}

func workflowOrgIndexKey(orgID string) string {
	return "wf:index:org:" + orgID
}

func workflowAllIndexKey() string {
	return "wf:index:all"
}

func runKey(id string) string {
	return "wf:run:" + id
}

func runIndexKey(workflowID string) string {
	return "wf:runs:" + workflowID
}

func runAllIndexKey() string {
	return "wf:runs:all"
}

func runStatusIndexKey(status RunStatus) string {
	return "wf:runs:status:" + string(status)
}

func runOrgActiveKey(orgID string) string {
	return "wf:runs:active:" + orgID
}

func runTimelineKey(runID string) string {
	return "wf:run:timeline:" + runID
}

func runJobIndexKey(jobID string) string {
	return "wf:run:job:" + jobID
}

func pendingAuditHashKey(jobID string) string {
	return "wf:run:pending_audit_hash:" + jobID
}

func (s *RedisStore) TrySetRunIdempotencyKey(ctx context.Context, key, runID string) (bool, error) {
	if key == "" || runID == "" {
		return false, fmt.Errorf("idempotency key and run id required")
	}
	return s.client.SetNX(ctx, runIdempotencyKey(key), runID, 0).Result()
}

func (s *RedisStore) GetRunByIdempotencyKey(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("idempotency key required")
	}
	return s.client.Get(ctx, runIdempotencyKey(key)).Result()
}

func (s *RedisStore) DeleteRunIdempotencyKey(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("idempotency key required")
	}
	return s.client.Del(ctx, runIdempotencyKey(key)).Err()
}

func runIdempotencyKey(key string) string {
	return "wf:run:idempotency:" + key
}

// --- Durable delay timer methods ---

const delayTimerKey = "cordum:wf:delay:timers"

// AddDelayTimer persists a delay timer as a sorted set member with fire-time score.
// Member format: workflowID:runID. Score is Unix seconds of the fire time.
func (s *RedisStore) AddDelayTimer(ctx context.Context, workflowID, runID string, fireAt time.Time) error {
	member := workflowID + ":" + runID
	return s.client.ZAdd(ctx, delayTimerKey, redis.Z{
		Score:  float64(fireAt.Unix()),
		Member: member,
	}).Err()
}

// RemoveDelayTimer removes a delay timer from the sorted set.
func (s *RedisStore) RemoveDelayTimer(ctx context.Context, workflowID, runID string) error {
	member := workflowID + ":" + runID
	return s.client.ZRem(ctx, delayTimerKey, member).Err()
}

// DelayTimerInfo describes a pending delay timer for a workflow run.
type DelayTimerInfo struct {
	WorkflowID  string    `json:"workflow_id"`
	RunID       string    `json:"run_id"`
	FiresAt     time.Time `json:"fires_at"`
	RemainingMs int64     `json:"remaining_ms"`
}

// GetDelayTimer returns the delay timer for a specific run, or nil if none exists or it has already fired.
func (s *RedisStore) GetDelayTimer(ctx context.Context, workflowID, runID string) (*DelayTimerInfo, error) {
	member := workflowID + ":" + runID
	score, err := s.client.ZScore(ctx, delayTimerKey, member).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get delay timer: %w", err)
	}
	firesAt := time.Unix(int64(score), 0).UTC()
	now := time.Now()
	if firesAt.Before(now) {
		return nil, nil // Already fired — stale.
	}
	return &DelayTimerInfo{
		WorkflowID:  workflowID,
		RunID:       runID,
		FiresAt:     firesAt,
		RemainingMs: firesAt.Sub(now).Milliseconds(),
	}, nil
}

// ListFutureDelays returns all timers with fire time > now, as (member, score) pairs.
// Members are in "workflowID:runID" format.
func (s *RedisStore) ListFutureDelays(ctx context.Context, now time.Time) ([]redis.Z, error) {
	return s.client.ZRangeByScoreWithScores(ctx, delayTimerKey, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", now.Unix()+1),
		Max: "+inf",
	}).Result()
}

// popFiredDelaysScript atomically fetches and removes all timers with score <= now.
var popFiredDelaysScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local members = redis.call('ZRANGEBYSCORE', key, '-inf', now)
if #members > 0 then
  redis.call('ZREM', key, unpack(members))
end
return members
`)

// PopFiredDelays atomically returns and removes all timers that have fired (score <= now).
func (s *RedisStore) PopFiredDelays(ctx context.Context, now time.Time) ([]string, error) {
	result, err := popFiredDelaysScript.Run(ctx, s.client, []string{delayTimerKey}, now.Unix()).StringSlice()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("pop fired delays: %w", err)
	}
	return result, nil
}

// CleanStaleDelays removes timer entries older than the given cutoff time.
// This prevents unbounded ZSET growth from orphaned entries (e.g. run deleted
// while timer was pending).
func (s *RedisStore) CleanStaleDelays(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.client.ZRemRangeByScore(ctx, delayTimerKey, "-inf", fmt.Sprintf("%d", cutoff.Unix())).Result()
}

func isActiveRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusDenied, RunStatusCancelled, RunStatusTimedOut:
		return false
	default:
		return status != ""
	}
}
