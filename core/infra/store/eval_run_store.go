package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

// Key layout under the tenant-scoped `eval:run:<tenant>:` prefix:
//
//	rec:<runID>         HASH  {json: <marshaled runner.RunResult>}
//	idx:t               ZSET  score=started_at_ms, member=runID
//	idx:ds:<datasetID>  ZSET  score=started_at_ms, member=runID (per-dataset history)
//
// Unlike the eval-dataset store, runs are NOT immutable — they are
// historical artifacts, not contracts, so a TTL-based retention policy
// (default 180 days, configurable via `CORDUM_EVAL_RUN_TTL_SECONDS`) is
// safe and desirable. Force-delete is available for admin hygiene
// (e.g. to expunge a tenant's runs during offboarding) but follows the
// same explicit-confirm-required pattern as dataset delete so operators
// can't rm -rf history by accident.
const (
	evalRunPrefix           = "eval:run:"
	evalRunRecField         = "json"
	defaultEvalRunLimit     = 50
	maxEvalRunLimit         = 200
	evalRunFetchBatchMax    = 512
	defaultEvalRunTTL       = 180 * 24 * time.Hour
	evalRunTTLSecondsEnv    = "CORDUM_EVAL_RUN_TTL_SECONDS"
)

// ErrEvalRunAlreadyExists is returned by CreateRun when a run with the
// same id is already in the store. Runs are append-only so a caller
// should NEVER see this in steady state; a hit indicates a runid
// collision and the gateway handler should generate a fresh UUID.
var ErrEvalRunAlreadyExists = errors.New("eval run already exists")

// ErrEvalRunNotFound is returned by GetRun / GetByID when no run
// matches the given tenant + runID.
var ErrEvalRunNotFound = errors.New("eval run not found")

// RunFilter narrows a ListRuns query.
type RunFilter struct {
	DatasetID     string
	SinceMS       int64 // inclusive lower bound on started_at_ms; 0 = open-ended
	UntilMS       int64 // inclusive upper bound on started_at_ms; 0 = open-ended
	MinScore      float64
	MinScoreSet   bool // true when MinScore filter should apply
	HasRegression bool // when true, only runs with Summary.Regressions > 0
}

// RunPage is a single page of ListRuns results.
type RunPage struct {
	Items      []runner.RunResult
	NextCursor string
}

// EvalRunStore is the Redis-backed history of evaluation runs.
type EvalRunStore struct {
	client redis.UniversalClient
	ttl    time.Duration
}

// NewEvalRunStore opens its own Redis connection and validates
// reachability. The store honors `CORDUM_EVAL_RUN_TTL_SECONDS` to
// override the default 180-day retention; values ≤ 0 disable the TTL
// (runs kept forever — useful for on-prem deployments that retain
// audit evidence indefinitely).
func NewEvalRunStore(url string) (*EvalRunStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("eval run store: parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("eval run store: connect redis: %w", err)
	}
	s := &EvalRunStore{client: client, ttl: resolveEvalRunTTL()}
	slog.Debug("eval run store connected", "component", "store", "ttl", s.ttl.String())
	return s, nil
}

// NewEvalRunStoreFromClient wraps an existing Redis client. Used by the
// gateway to share the JobStore's client pool.
func NewEvalRunStoreFromClient(client redis.UniversalClient) *EvalRunStore {
	if client == nil {
		return nil
	}
	return &EvalRunStore{client: client, ttl: resolveEvalRunTTL()}
}

// Close releases the underlying Redis client. Safe on a nil receiver.
func (s *EvalRunStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// createRunScript is the atomic create path. Uses SETNX on the record
// hash to prevent id collisions (astronomically unlikely with UUIDv4
// but cheap defense-in-depth).
//
// KEYS:
//   1. rec:<runID>
//   2. idx:t
//   3. idx:ds:<datasetID>
//
// ARGV:
//   1. runID (member for both ZADDs)
//   2. marshaled JSON payload
//   3. started_at_ms (score for both ZSETs)
//
// Returns 1 on success, 0 on id collision.
var evalRunCreateScript = redis.NewScript(`
local recExists = redis.call('EXISTS', KEYS[1])
if recExists == 1 then
  return 0
end
redis.call('HSET', KEYS[1], 'json', ARGV[2])
redis.call('ZADD', KEYS[2], tonumber(ARGV[3]), ARGV[1])
redis.call('ZADD', KEYS[3], tonumber(ARGV[3]), ARGV[1])
return 1
`)

// evalRunDeleteScript wipes every key associated with a run.
var evalRunDeleteScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[3], ARGV[1])
return 1
`)

// CreateRun persists a completed run. The caller owns RunID and
// StartedAt/CompletedAt — the store trusts those values and does not
// rewrite them. Returns ErrEvalRunAlreadyExists on id collision so the
// caller can generate a fresh id and retry.
func (s *EvalRunStore) CreateRun(ctx context.Context, result runner.RunResult) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("eval run store unavailable")
	}
	if strings.TrimSpace(result.RunID) == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(result.Tenant) == "" {
		return fmt.Errorf("run tenant is required")
	}
	if strings.TrimSpace(result.DatasetID) == "" {
		return fmt.Errorf("run dataset id is required")
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal eval run: %w", err)
	}

	keys := []string{
		evalRunRecordKey(result.Tenant, result.RunID),
		evalRunIndexKey(result.Tenant),
		evalRunDatasetIndexKey(result.Tenant, result.DatasetID),
	}
	scoreMs := result.StartedAt.UTC().UnixMilli()
	if scoreMs <= 0 {
		scoreMs = time.Now().UTC().UnixMilli()
	}
	argv := []any{result.RunID, payload, scoreMs}

	res, err := evalRunCreateScript.Run(ctx, s.client, keys, argv...).Result()
	if err != nil {
		return fmt.Errorf("create eval run: %w", err)
	}
	ok, _ := res.(int64)
	if ok != 1 {
		return ErrEvalRunAlreadyExists
	}
	// Apply TTL after the atomic create so even on a retry-of-retry the
	// record + index members converge to the same expiry.
	if s.ttl > 0 {
		s.client.Expire(ctx, keys[0], s.ttl)
	}
	return nil
}

// GetRun loads a run by id within the tenant.
func (s *EvalRunStore) GetRun(ctx context.Context, tenant, runID string) (runner.RunResult, error) {
	if s == nil || s.client == nil {
		return runner.RunResult{}, fmt.Errorf("eval run store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	runID = strings.TrimSpace(runID)
	if tenant == "" {
		return runner.RunResult{}, fmt.Errorf("tenant required")
	}
	if runID == "" {
		return runner.RunResult{}, fmt.Errorf("run id required")
	}

	raw, err := s.client.HGet(ctx, evalRunRecordKey(tenant, runID), evalRunRecField).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return runner.RunResult{}, ErrEvalRunNotFound
		}
		return runner.RunResult{}, fmt.Errorf("get eval run %s: %w", runID, err)
	}
	var result runner.RunResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return runner.RunResult{}, fmt.Errorf("decode eval run %s: %w", runID, err)
	}
	if result.Tenant != tenant {
		// Defense in depth against a UUID collision crossing tenant
		// scope — treat as a miss rather than a successful cross-tenant
		// read.
		slog.Warn("eval run tenant mismatch on read",
			"want_tenant", tenant, "got_tenant", result.Tenant, "run_id", runID)
		return runner.RunResult{}, ErrEvalRunNotFound
	}
	return result, nil
}

// ListRuns paginates runs for the tenant, newest-first by StartedAt.
// When filter.DatasetID is set, the per-dataset index is used for
// efficient iteration; otherwise the primary index is used.
func (s *EvalRunStore) ListRuns(ctx context.Context, tenant string, filter RunFilter, cursor string, limit int) (RunPage, error) {
	if s == nil || s.client == nil {
		return RunPage{}, fmt.Errorf("eval run store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return RunPage{}, fmt.Errorf("tenant required")
	}
	if limit <= 0 {
		limit = defaultEvalRunLimit
	}
	if limit > maxEvalRunLimit {
		limit = maxEvalRunLimit
	}

	indexKey := evalRunIndexKey(tenant)
	if strings.TrimSpace(filter.DatasetID) != "" {
		indexKey = evalRunDatasetIndexKey(tenant, filter.DatasetID)
	}

	// Cursor format: "<score>:<runID>" — newest-first means max=score-at-cursor.
	maxStr := "+inf"
	var cursorID string
	if cursor != "" {
		score, id, err := decodeEvalRunCursor(cursor)
		if err != nil {
			return RunPage{}, err
		}
		maxStr = strconv.FormatInt(score, 10)
		cursorID = id
	}
	// Apply time-range narrowing
	sinceStr := "-inf"
	if filter.SinceMS > 0 {
		sinceStr = strconv.FormatInt(filter.SinceMS, 10)
	}
	if filter.UntilMS > 0 {
		// Intersect the upper bound with the cursor's max so paging
		// never leaks above Until.
		if upperFromCursor, err := strconv.ParseInt(maxStr, 10, 64); err == nil {
			if filter.UntilMS < upperFromCursor {
				maxStr = strconv.FormatInt(filter.UntilMS, 10)
			}
		} else {
			maxStr = strconv.FormatInt(filter.UntilMS, 10)
		}
	}

	batchSize := min(max(int64(limit*3), 32), int64(evalRunFetchBatchMax))

	out := make([]runner.RunResult, 0, limit)
	var nextCursor string
	var offset int64

	for len(out) < limit {
		members, err := s.client.ZRevRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
			Max:    maxStr,
			Min:    sinceStr,
			Offset: offset,
			Count:  batchSize,
		}).Result()
		if err != nil {
			return RunPage{}, fmt.Errorf("list eval runs: %w", err)
		}
		if len(members) == 0 {
			break
		}
		offset += int64(len(members))

		ids := make([]string, 0, len(members))
		for _, z := range members {
			id, _ := z.Member.(string)
			if id == "" {
				continue
			}
			if cursor != "" && int64(z.Score) == mustAtoiSigned(maxStr) && id >= cursorID {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			if int64(len(members)) < batchSize {
				break
			}
			continue
		}

		results, err := s.fetchRunsByID(ctx, tenant, ids)
		if err != nil {
			return RunPage{}, err
		}
		for _, r := range results {
			if !matchesRunFilter(r, filter) {
				continue
			}
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
		if int64(len(members)) < batchSize {
			break
		}
	}

	if len(out) >= limit {
		last := out[len(out)-1]
		scoreMs := last.StartedAt.UTC().UnixMilli()
		nextCursor = encodeEvalRunCursor(scoreMs, last.RunID)
	}
	return RunPage{Items: out, NextCursor: nextCursor}, nil
}

// DeleteRun removes every key associated with a run id. Idempotent on
// absent ids. The handler is responsible for the `force=true` confirm
// + admin RBAC gate — the store trusts callers.
func (s *EvalRunStore) DeleteRun(ctx context.Context, tenant, runID string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("eval run store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	runID = strings.TrimSpace(runID)
	if tenant == "" {
		return fmt.Errorf("tenant required")
	}
	if runID == "" {
		return fmt.Errorf("run id required")
	}

	existing, err := s.GetRun(ctx, tenant, runID)
	if err != nil {
		if errors.Is(err, ErrEvalRunNotFound) {
			return nil
		}
		return err
	}

	keys := []string{
		evalRunRecordKey(tenant, runID),
		evalRunIndexKey(tenant),
		evalRunDatasetIndexKey(tenant, existing.DatasetID),
	}
	if _, err := evalRunDeleteScript.Run(ctx, s.client, keys, runID).Result(); err != nil {
		return fmt.Errorf("delete eval run: %w", err)
	}
	return nil
}

// GCExpired walks the primary index and removes dangling members whose
// record hash has already TTL-expired. Intended for periodic cleanup;
// tenants that never accumulate enough runs for TTL to matter will
// see it as a quick no-op.
func (s *EvalRunStore) GCExpired(ctx context.Context, tenant string) (int, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("eval run store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return 0, fmt.Errorf("tenant required")
	}
	indexKey := evalRunIndexKey(tenant)
	ids, err := s.client.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("gc eval runs: %w", err)
	}
	stale := make([]any, 0)
	for _, id := range ids {
		exists, err := s.client.Exists(ctx, evalRunRecordKey(tenant, id)).Result()
		if err != nil {
			return 0, fmt.Errorf("gc eval runs exists: %w", err)
		}
		if exists == 0 {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if err := s.client.ZRem(ctx, indexKey, stale...).Err(); err != nil {
		return 0, fmt.Errorf("gc eval runs prune primary index: %w", err)
	}
	// Best-effort per-dataset index pruning — we don't know which
	// dataset the stale runs belonged to (record is gone), so scan
	// every dataset-index ZSET under the tenant prefix and remove the
	// id from each. This is O(datasets) per GC pass, which is fine for
	// reasonable tenant cardinality and runs infrequently.
	iter := s.client.Scan(ctx, 0, evalRunTenantPrefix(tenant)+"idx:ds:*", 0).Iterator()
	for iter.Next(ctx) {
		if err := s.client.ZRem(ctx, iter.Val(), stale...).Err(); err != nil {
			slog.Warn("eval run GC: dataset-index prune failed",
				"tenant", tenant, "key", iter.Val(), "error", err)
		}
	}
	if err := iter.Err(); err != nil {
		slog.Warn("eval run GC: dataset-index scan failed",
			"tenant", tenant, "error", err)
	}
	slog.Info("eval run GC pruned expired members",
		"tenant", tenant, "count", len(stale))
	return len(stale), nil
}

// --- internal helpers ---

func (s *EvalRunStore) fetchRunsByID(ctx context.Context, tenant string, ids []string) ([]runner.RunResult, error) {
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGet(ctx, evalRunRecordKey(tenant, id), evalRunRecField)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("fetch eval runs: %w", err)
	}
	out := make([]runner.RunResult, 0, len(ids))
	for i, id := range ids {
		raw, err := cmds[i].Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return nil, fmt.Errorf("fetch eval run %s: %w", id, err)
		}
		var r runner.RunResult
		if err := json.Unmarshal(raw, &r); err != nil {
			slog.Warn("eval run: skip undecodable record", "id", id, "tenant", tenant, "error", err)
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func matchesRunFilter(r runner.RunResult, f RunFilter) bool {
	if f.HasRegression && r.Summary.Regressions == 0 {
		return false
	}
	if f.MinScoreSet {
		if r.Summary.ScorePercent == nil {
			return false
		}
		if *r.Summary.ScorePercent < f.MinScore {
			return false
		}
	}
	return true
}

func evalRunTenantPrefix(tenant string) string {
	return evalRunPrefix + tenant + ":"
}

func evalRunRecordKey(tenant, id string) string {
	return evalRunTenantPrefix(tenant) + "rec:" + id
}

func evalRunIndexKey(tenant string) string {
	return evalRunTenantPrefix(tenant) + "idx:t"
}

func evalRunDatasetIndexKey(tenant, datasetID string) string {
	return evalRunTenantPrefix(tenant) + "idx:ds:" + datasetID
}

func encodeEvalRunCursor(ms int64, id string) string {
	return strconv.FormatInt(ms, 10) + ":" + id
}

func decodeEvalRunCursor(cursor string) (int64, string, error) {
	idx := strings.IndexByte(cursor, ':')
	if idx <= 0 || idx == len(cursor)-1 {
		return 0, "", fmt.Errorf("malformed eval run cursor")
	}
	ms, err := strconv.ParseInt(cursor[:idx], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed eval run cursor score: %w", err)
	}
	return ms, cursor[idx+1:], nil
}

func mustAtoiSigned(s string) int64 {
	if s == "+inf" || s == "-inf" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func resolveEvalRunTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(evalRunTTLSecondsEnv))
	if raw == "" {
		return defaultEvalRunTTL
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid "+evalRunTTLSecondsEnv+", using default",
			"value", raw, "default", defaultEvalRunTTL)
		return defaultEvalRunTTL
	}
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
