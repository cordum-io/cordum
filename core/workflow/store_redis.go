package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	defaultWorkflowRedisURL = "redis://localhost:6379"
	timelineMaxEntries      = 1000
)

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
	_, _ = pipe.Exec(ctx)

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
			continue
		}
		out = append(out, &wf)
	}
	return out, nil
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

	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, runKey(run.ID), payload, 0)
	pipe.ZAdd(ctx, runIndexKey(run.WorkflowID), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runAllIndexKey(), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runStatusIndexKey(run.Status), redis.Z{Score: float64(now.Unix()), Member: run.ID})
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
		_, _ = s.TrySetRunIdempotencyKey(ctx, run.IdempotencyKey, run.ID)
	}
	return nil
}

// UpdateRun overwrites an existing run document and bumps the index score.
func (s *RedisStore) UpdateRun(ctx context.Context, run *WorkflowRun) error {
	if run == nil || run.ID == "" || run.WorkflowID == "" {
		return fmt.Errorf("run id and workflow id required")
	}
	prevStatus := RunStatus("")
	if data, err := s.client.Get(ctx, runKey(run.ID)).Bytes(); err == nil {
		var prev WorkflowRun
		if err := json.Unmarshal(data, &prev); err == nil {
			prevStatus = prev.Status
		}
	}
	now := time.Now().UTC()
	run.UpdatedAt = now

	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, runKey(run.ID), payload, 0)
	pipe.ZAdd(ctx, runIndexKey(run.WorkflowID), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runAllIndexKey(), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	pipe.ZAdd(ctx, runStatusIndexKey(run.Status), redis.Z{Score: float64(now.Unix()), Member: run.ID})
	if prevStatus != "" && prevStatus != run.Status {
		pipe.ZRem(ctx, runStatusIndexKey(prevStatus), run.ID)
	}
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
		return fmt.Errorf("update run: %w", err)
	}
	return nil
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
	_, _ = pipe.Exec(ctx)

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
	_, _ = pipe.Exec(ctx)

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

func isActiveRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return false
	default:
		return status != ""
	}
}
