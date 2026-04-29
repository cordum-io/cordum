package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/model"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Key layout under the tenant-scoped `eval:dataset:<tenant>:` prefix:
//
//	rec:<id>             HASH  {json: <marshaled EvalDataset>}
//	byname:<name>:<ver>  STRING <id>                    (SETNX for uniqueness)
//	name:<name>          ZSET  score=version, member=id (ListEvalDatasetVersions)
//	index                ZSET  score=created_at_ms, member=id (paginated list)
//
// Tenant isolation is structural — every key in the store carries the
// tenant in its prefix and there is no cross-tenant query helper. Callers
// must therefore pass the correct tenant on every invocation.
//
// TTL is intentionally NONE: datasets are durable by design. The only way
// to remove a dataset is DeleteEvalDataset, which the gateway handler
// gates behind the explicit `force=true` admin escape hatch.
const (
	evalDatasetPrefix        = "eval:dataset:"
	evalDatasetRecField      = "json"
	defaultEvalDatasetLimit  = 50
	maxEvalDatasetLimit      = 200
	evalDatasetFetchBatchMax = 512
)

var _ model.EvalDatasetStore = (*EvalDatasetStore)(nil)

// EvalDatasetStore is the Redis-backed implementation of
// model.EvalDatasetStore. Concrete callers should use this type directly
// so the gateway can expose the underlying redis client for readiness
// probes, while holding a pointer through the model interface in tests.
type EvalDatasetStore struct {
	client redis.UniversalClient
}

// NewEvalDatasetStore opens its own Redis connection and validates
// reachability. Returns an error when the URL is malformed or the server
// is unreachable within a 2s ping.
func NewEvalDatasetStore(url string) (*EvalDatasetStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("eval dataset store: parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("eval dataset store: connect redis: %w", err)
	}
	slog.Debug("eval dataset store connected", "component", "store")
	return &EvalDatasetStore{client: client}, nil
}

// NewEvalDatasetStoreFromClient wraps an existing Redis client so multiple
// stores can share a connection pool — the gateway already owns a job-
// store client, which this layer piggy-backs on.
func NewEvalDatasetStoreFromClient(client redis.UniversalClient) *EvalDatasetStore {
	if client == nil {
		return nil
	}
	return &EvalDatasetStore{client: client}
}

// Close releases the underlying Redis client. Safe on a nil receiver.
func (s *EvalDatasetStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// createScript is the atomic create path. It enforces (tenant, name,
// version) uniqueness by SETNXing the byname key FIRST and aborting the
// rest of the write if that fails. Doing all four writes inside one Lua
// script prevents the partial-write failure mode where the byname
// sentinel lands but the record hash never does — which would permanently
// block that (name, version) slot.
//
// KEYS:
//  1. rec:<id>
//  2. byname:<name>:<version>
//  3. name:<name>
//  4. index
//
// ARGV:
//  1. dataset id (member for both ZADDs)
//  2. marshaled JSON payload
//  3. version (score for name ZSET)
//  4. created_at_ms (score for index ZSET)
//
// Returns 1 on success, 0 on (name, version) collision.
var evalDatasetCreateScript = redis.NewScript(`
local bynameExists = redis.call('EXISTS', KEYS[2])
if bynameExists == 1 then
  return 0
end
redis.call('HSET', KEYS[1], 'json', ARGV[2])
redis.call('SET', KEYS[2], ARGV[1])
redis.call('ZADD', KEYS[3], tonumber(ARGV[3]), ARGV[1])
redis.call('ZADD', KEYS[4], tonumber(ARGV[4]), ARGV[1])
return 1
`)

// deleteScript removes every key associated with a dataset id in one
// atomic operation.
//
// KEYS:
//  1. rec:<id>
//  2. byname:<name>:<version>
//  3. name:<name>
//  4. index
//
// ARGV:
//  1. dataset id
var evalDatasetDeleteScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
redis.call('DEL', KEYS[2])
redis.call('ZREM', KEYS[3], ARGV[1])
redis.call('ZREM', KEYS[4], ARGV[1])
return 1
`)

// CreateEvalDataset persists a new, immutable dataset. The store owns ID,
// CreatedAt, UpdatedAt, EntryCount, and ContentHash — it overwrites
// whatever the caller supplied for those fields so a client cannot dictate
// (for example) ContentHash and silently lie about what got stored.
func (s *EvalDatasetStore) CreateEvalDataset(ctx context.Context, dataset model.EvalDataset) (model.EvalDataset, error) {
	if s == nil || s.client == nil {
		return model.EvalDataset{}, fmt.Errorf("eval dataset store unavailable")
	}

	dataset.Normalize()

	if strings.TrimSpace(dataset.ID) == "" {
		dataset.ID = uuid.NewString()
	}

	now := time.Now().UTC()
	dataset.CreatedAt = now.Format(time.RFC3339Nano)
	dataset.UpdatedAt = dataset.CreatedAt
	dataset.EntryCount = len(dataset.Entries)

	hash, err := dataset.ComputeContentHash()
	if err != nil {
		return model.EvalDataset{}, fmt.Errorf("compute content hash: %w", err)
	}
	dataset.ContentHash = hash

	if err := dataset.Validate(); err != nil {
		return model.EvalDataset{}, err
	}

	payload, err := json.Marshal(dataset)
	if err != nil {
		return model.EvalDataset{}, fmt.Errorf("marshal eval dataset: %w", err)
	}

	keys := []string{
		evalDatasetRecordKey(dataset.Tenant, dataset.ID),
		evalDatasetByNameKey(dataset.Tenant, dataset.Name, dataset.Version),
		evalDatasetNameIndexKey(dataset.Tenant, dataset.Name),
		evalDatasetIndexKey(dataset.Tenant),
	}
	argv := []any{
		dataset.ID,
		payload,
		dataset.Version,
		now.UnixMilli(),
	}

	res, err := evalDatasetCreateScript.Run(ctx, s.client, keys, argv...).Result()
	if err != nil {
		return model.EvalDataset{}, fmt.Errorf("create eval dataset: %w", err)
	}
	ok, _ := res.(int64)
	if ok != 1 {
		return model.EvalDataset{}, ErrEvalDatasetVersionExists
	}

	return dataset, nil
}

// GetEvalDataset loads a dataset by id within the tenant. Returns
// ErrEvalDatasetNotFound when the record key is absent — the byname
// sentinel alone does not satisfy a read because the canonical source of
// truth is the record hash.
func (s *EvalDatasetStore) GetEvalDataset(ctx context.Context, tenant, id string) (model.EvalDataset, error) {
	if s == nil || s.client == nil {
		return model.EvalDataset{}, fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	id = strings.TrimSpace(id)
	if tenant == "" {
		return model.EvalDataset{}, fmt.Errorf("tenant required")
	}
	if id == "" {
		return model.EvalDataset{}, fmt.Errorf("eval dataset id required")
	}

	raw, err := s.client.HGet(ctx, evalDatasetRecordKey(tenant, id), evalDatasetRecField).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return model.EvalDataset{}, ErrEvalDatasetNotFound
		}
		return model.EvalDataset{}, fmt.Errorf("get eval dataset %s: %w", id, err)
	}

	var dataset model.EvalDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return model.EvalDataset{}, fmt.Errorf("decode eval dataset %s: %w", id, err)
	}
	if dataset.Tenant != tenant {
		// Defense in depth: if a UUID collision were ever to cross a
		// tenant boundary via some administrative tooling bug, treat it
		// as a miss rather than a successful read.
		slog.Warn("eval dataset tenant mismatch on read",
			"want_tenant", tenant, "got_tenant", dataset.Tenant, "id", id)
		return model.EvalDataset{}, ErrEvalDatasetNotFound
	}
	return dataset, nil
}

// ListEvalDatasets paginates datasets in the tenant ordered by CreatedAt
// descending. Filtering is post-fetch (the primary index does not support
// prefix scans directly), so pathological filters that match very few
// entries may require multiple scan rounds to fill a page.
func (s *EvalDatasetStore) ListEvalDatasets(ctx context.Context, tenant string, filter model.EvalDatasetFilter, cursor string, limit int) (model.EvalDatasetPage, error) {
	if s == nil || s.client == nil {
		return model.EvalDatasetPage{}, fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return model.EvalDatasetPage{}, fmt.Errorf("tenant required")
	}
	if limit <= 0 {
		limit = defaultEvalDatasetLimit
	}
	if limit > maxEvalDatasetLimit {
		limit = maxEvalDatasetLimit
	}

	// Cursor encodes "<score>:<id>" where score is the created_at_ms of
	// the last emitted item on the prior page. ZRevRangeByScore is called
	// with max=cursorScore inclusive, and ties at the same score are
	// resolved in-batch by filtering out any member id >= cursorID so the
	// cursor entry is never re-emitted.
	cursorScore := "+inf"
	var cursorID string
	if cursor != "" {
		score, id, err := decodeEvalDatasetCursor(cursor)
		if err != nil {
			return model.EvalDatasetPage{}, err
		}
		cursorScore = strconv.FormatInt(score, 10)
		cursorID = id
	}

	out := make([]model.EvalDataset, 0, limit)
	nextCursor := ""
	indexKey := evalDatasetIndexKey(tenant)

	nameLower := strings.ToLower(strings.TrimSpace(filter.NamePrefix))
	createdAfter := filter.CreatedAfterMS
	createdBefore := filter.CreatedBeforeMS
	unfiltered := nameLower == "" && createdAfter == 0 && createdBefore == 0

	// Iterate the sorted set newest-first. We fetch in bounded batches and
	// re-drive the loop until we fill the page target or exhaust the index.
	// Filtered queries intentionally over-sample because filtering happens
	// after fetch; unfiltered queries need only one live lookahead item.
	scanTarget := limit
	batchSize := min(max(int64(limit*3), 32), int64(evalDatasetFetchBatchMax))
	if unfiltered {
		scanTarget = limit + 1
		batchSize = min(int64(scanTarget), int64(evalDatasetFetchBatchMax))
	}

	// scanMax is always inclusive — when a cursor is present we filter out
	// the cursor item itself below via the id tie-break.
	scanMax := cursorScore
	if cursor == "" {
		scanMax = "+inf"
	}

	var offset int64
	staleToPrune := make([]string, 0)
	for len(out) < scanTarget {
		members, err := s.client.ZRevRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
			Max:    scanMax,
			Min:    "-inf",
			Offset: offset,
			Count:  batchSize,
		}).Result()
		if err != nil {
			return model.EvalDatasetPage{}, fmt.Errorf("list eval datasets: %w", err)
		}
		if len(members) == 0 {
			break
		}
		offset += int64(len(members))

		// Build the candidate id list respecting the cursor tie-break.
		ids := make([]string, 0, len(members))
		for _, z := range members {
			id, _ := z.Member.(string)
			if id == "" {
				continue
			}
			if cursor != "" && int64(z.Score) == mustAtoi(cursorScore) && id >= cursorID {
				// Same-score tie: skip equals and anything that would
				// re-emit the cursor's last-seen item.
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

		datasets, stale, err := s.fetchDatasetsByID(ctx, tenant, ids)
		if err != nil {
			return model.EvalDatasetPage{}, err
		}
		if len(stale) > 0 {
			staleToPrune = append(staleToPrune, stale...)
		}

		for _, ds := range datasets {
			if !matchesEvalDatasetFilter(ds, nameLower, createdAfter, createdBefore) {
				continue
			}
			out = append(out, ds)
			if len(out) >= scanTarget {
				break
			}
		}

		if int64(len(members)) < batchSize {
			break
		}
	}
	if len(staleToPrune) > 0 {
		s.pruneStaleIndexEntries(ctx, tenant, staleToPrune)
	}

	if len(out) > limit {
		last := out[limit-1]
		if ms, err := last.CreatedAtMilli(); err == nil {
			nextCursor = encodeEvalDatasetCursor(ms, last.ID)
		}
		out = out[:limit]
	} else if !unfiltered && len(out) >= limit {
		last := out[len(out)-1]
		if ms, err := last.CreatedAtMilli(); err == nil {
			nextCursor = encodeEvalDatasetCursor(ms, last.ID)
		}
	}

	return model.EvalDatasetPage{Items: out, NextCursor: nextCursor}, nil
}

// DeleteEvalDataset wipes every key pointing at the dataset id. The store
// has no soft-delete (the immutability rail plus an explicit destructive
// escape hatch is the contract the gateway enforces). Deleting an absent
// dataset is a no-op so retries stay idempotent.
func (s *EvalDatasetStore) DeleteEvalDataset(ctx context.Context, tenant, id string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	id = strings.TrimSpace(id)
	if tenant == "" {
		return fmt.Errorf("tenant required")
	}
	if id == "" {
		return fmt.Errorf("eval dataset id required")
	}

	existing, err := s.GetEvalDataset(ctx, tenant, id)
	if err != nil {
		if errors.Is(err, ErrEvalDatasetNotFound) {
			return nil
		}
		return err
	}

	keys := []string{
		evalDatasetRecordKey(tenant, id),
		evalDatasetByNameKey(tenant, existing.Name, existing.Version),
		evalDatasetNameIndexKey(tenant, existing.Name),
		evalDatasetIndexKey(tenant),
	}
	if _, err := evalDatasetDeleteScript.Run(ctx, s.client, keys, id).Result(); err != nil {
		return fmt.Errorf("delete eval dataset: %w", err)
	}
	return nil
}

// GetEvalDatasetByNameVersion is a convenience lookup for the gateway's
// `/by-name/{name}/versions/{version}` route.
func (s *EvalDatasetStore) GetEvalDatasetByNameVersion(ctx context.Context, tenant, name string, version int) (model.EvalDataset, error) {
	if s == nil || s.client == nil {
		return model.EvalDataset{}, fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	name = strings.ToLower(strings.TrimSpace(name))
	if tenant == "" {
		return model.EvalDataset{}, fmt.Errorf("tenant required")
	}
	if name == "" {
		return model.EvalDataset{}, fmt.Errorf("eval dataset name required")
	}
	if version < 1 {
		return model.EvalDataset{}, fmt.Errorf("eval dataset version must be >= 1")
	}

	id, err := s.client.Get(ctx, evalDatasetByNameKey(tenant, name, version)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return model.EvalDataset{}, ErrEvalDatasetNotFound
		}
		return model.EvalDataset{}, fmt.Errorf("resolve eval dataset by name: %w", err)
	}
	return s.GetEvalDataset(ctx, tenant, id)
}

// ListEvalDatasetVersions returns every version of a named dataset within
// a tenant, newest-version-first. The output is intended for the gateway's
// `/by-name/{name}` route so an operator can see the full version history
// of a dataset at a glance.
func (s *EvalDatasetStore) ListEvalDatasetVersions(ctx context.Context, tenant, name string) ([]model.EvalDataset, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	name = strings.ToLower(strings.TrimSpace(name))
	if tenant == "" {
		return nil, fmt.Errorf("tenant required")
	}
	if name == "" {
		return nil, fmt.Errorf("eval dataset name required")
	}

	ids, err := s.client.ZRevRangeByScore(ctx, evalDatasetNameIndexKey(tenant, name), &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("list eval dataset versions: %w", err)
	}
	if len(ids) == 0 {
		return []model.EvalDataset{}, nil
	}

	datasets, stale, err := s.fetchDatasetsByID(ctx, tenant, ids)
	if err != nil {
		return nil, err
	}
	if len(stale) > 0 {
		// Stale version-index entries are worth cleaning up too: a
		// dangling member here would make `/by-name/{name}` return a
		// partial history forever.
		members := make([]any, 0, len(stale))
		for _, id := range stale {
			members = append(members, id)
		}
		if err := s.client.ZRem(ctx, evalDatasetNameIndexKey(tenant, name), members...).Err(); err != nil {
			slog.Warn("eval dataset: prune stale name-index member failed",
				"tenant", tenant, "name", name, "count", len(stale), "error", err)
		}
	}
	return datasets, nil
}

// ReconcileIndexes walks every record under the tenant prefix (via SCAN)
// and prunes index members whose target record hash is missing. Intended
// to be called once at gateway startup after a crash recovery.
//
// This is intentionally not called automatically from NewEvalDatasetStore
// because the store may be used across multiple tenants and the reconcile
// semantics are tenant-scoped; the owning service layer gets to decide
// when to invoke it.
func (s *EvalDatasetStore) ReconcileIndexes(ctx context.Context, tenant string) (int, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("eval dataset store unavailable")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return 0, fmt.Errorf("tenant required")
	}

	indexKey := evalDatasetIndexKey(tenant)
	ids, err := s.client.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("reconcile eval dataset index: %w", err)
	}
	stale := make([]any, 0)
	for _, id := range ids {
		exists, err := s.client.Exists(ctx, evalDatasetRecordKey(tenant, id)).Result()
		if err != nil {
			return 0, fmt.Errorf("reconcile eval dataset record: %w", err)
		}
		if exists == 0 {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if err := s.client.ZRem(ctx, indexKey, stale...).Err(); err != nil {
		return 0, fmt.Errorf("reconcile eval dataset index remove: %w", err)
	}
	slog.Info("eval dataset reconcile pruned dangling index members", "tenant", tenant, "count", len(stale))
	return len(stale), nil
}

// fetchDatasetsByID pipelines HGETs for a batch of ids. Missing ids are
// collected into `stale` so the caller can prune index entries that point
// at records that no longer exist (e.g. after an out-of-band DEL).
func (s *EvalDatasetStore) fetchDatasetsByID(ctx context.Context, tenant string, ids []string) ([]model.EvalDataset, []string, error) {
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGet(ctx, evalDatasetRecordKey(tenant, id), evalDatasetRecField)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("fetch eval datasets: %w", err)
	}

	datasets := make([]model.EvalDataset, 0, len(ids))
	stale := make([]string, 0)
	for i, id := range ids {
		raw, err := cmds[i].Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				stale = append(stale, id)
				continue
			}
			return nil, nil, fmt.Errorf("fetch eval dataset %s: %w", id, err)
		}
		var ds model.EvalDataset
		if err := json.Unmarshal(raw, &ds); err != nil {
			slog.Warn("eval dataset: skip undecodable record", "id", id, "tenant", tenant, "error", err)
			continue
		}
		datasets = append(datasets, ds)
	}
	return datasets, stale, nil
}

func (s *EvalDatasetStore) pruneStaleIndexEntries(ctx context.Context, tenant string, staleIDs []string) {
	if len(staleIDs) == 0 {
		return
	}
	members := make([]any, 0, len(staleIDs))
	for _, id := range staleIDs {
		members = append(members, id)
	}
	indexKey := evalDatasetIndexKey(tenant)
	if err := s.client.ZRem(ctx, indexKey, members...).Err(); err != nil {
		slog.Warn("eval dataset: prune stale index members failed",
			"tenant", tenant, "count", len(staleIDs), "error", err)
	}
}

func matchesEvalDatasetFilter(d model.EvalDataset, namePrefix string, afterMS, beforeMS int64) bool {
	if namePrefix != "" && !strings.HasPrefix(d.Name, namePrefix) {
		return false
	}
	if afterMS > 0 || beforeMS > 0 {
		ms, err := d.CreatedAtMilli()
		if err != nil {
			return false
		}
		if afterMS > 0 && ms < afterMS {
			return false
		}
		if beforeMS > 0 && ms > beforeMS {
			return false
		}
	}
	return true
}

// --- key helpers ---

func evalDatasetTenantPrefix(tenant string) string {
	return evalDatasetPrefix + tenant + ":"
}

func evalDatasetRecordKey(tenant, id string) string {
	return evalDatasetTenantPrefix(tenant) + "rec:" + id
}

func evalDatasetByNameKey(tenant, name string, version int) string {
	return evalDatasetTenantPrefix(tenant) + "byname:" + name + ":" + strconv.Itoa(version)
}

func evalDatasetNameIndexKey(tenant, name string) string {
	return evalDatasetTenantPrefix(tenant) + "name:" + name
}

func evalDatasetIndexKey(tenant string) string {
	return evalDatasetTenantPrefix(tenant) + "index"
}

// --- cursor helpers ---

func encodeEvalDatasetCursor(ms int64, id string) string {
	return strconv.FormatInt(ms, 10) + ":" + id
}

func decodeEvalDatasetCursor(cursor string) (int64, string, error) {
	idx := strings.IndexByte(cursor, ':')
	if idx <= 0 || idx == len(cursor)-1 {
		return 0, "", fmt.Errorf("malformed eval dataset cursor")
	}
	ms, err := strconv.ParseInt(cursor[:idx], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed eval dataset cursor score: %w", err)
	}
	return ms, cursor[idx+1:], nil
}

func mustAtoi(s string) int64 {
	if s == "+inf" || s == "-inf" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
