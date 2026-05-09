package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

// BundleRedisStore is the Redis-backed BundleStore implementation. All
// multi-key mutations execute inside Lua scripts so concurrent
// deploy/rollback operations serialize through Redis itself, not via
// Go-level WATCH/MULTI/EXEC (per memory mem-12f1ceeb — go-redis
// WATCH+TxPipelined corrupts the connection pool when miniredis returns
// errors).
type BundleRedisStore struct {
	client redis.UniversalClient
}

// NewRedisBundleStore constructs a Redis-backed BundleStore.
func NewRedisBundleStore(url string) (*BundleRedisStore, error) {
	if url == "" {
		return nil, fmt.Errorf("redis url required")
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
	return &BundleRedisStore{client: client}, nil
}

// NewRedisBundleStoreFromClient wraps an existing Redis client. Used by
// tests to share a miniredis instance with other stores.
func NewRedisBundleStoreFromClient(client redis.UniversalClient) *BundleRedisStore {
	return &BundleRedisStore{client: client}
}

// Close releases the underlying Redis client.
func (s *BundleRedisStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// ---------------------------------------------------------------------------
// Bundle CRUD
// ---------------------------------------------------------------------------

// CreateBundle persists a new Bundle. Returns ErrBundleExists when a
// bundle with the same ID already exists. Versions are NOT embedded in
// the envelope payload — they live in their own keys per the schema.
func (s *BundleRedisStore) CreateBundle(ctx context.Context, b *Bundle) error {
	if b == nil {
		return fmt.Errorf("bundle: nil bundle")
	}
	if strings.TrimSpace(b.ID) == "" {
		return fmt.Errorf("bundle: id required")
	}
	envelope := *b
	envelope.Versions = nil
	payload, err := json.Marshal(&envelope)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	ok, err := s.client.SetNX(ctx, bundleKey(b.ID), payload, 0).Result()
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	if !ok {
		return ErrBundleExists
	}
	return nil
}

// GetBundle returns a Bundle envelope by ID. Returns ErrBundleNotFound
// when no such bundle. The Versions field is left empty; callers fetch
// versions explicitly via ListBundleVersions / GetBundleVersion.
func (s *BundleRedisStore) GetBundle(ctx context.Context, id string) (*Bundle, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("bundle: id required")
	}
	data, err := s.client.Get(ctx, bundleKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrBundleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get bundle %s: %w", id, err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("unmarshal bundle %s: %w", id, err)
	}
	return &b, nil
}

// ListBundlesByScope returns every bundle whose ScopeBinding matches the
// supplied scope. Implementation uses SCAN over the `policy:bundle:*`
// prefix and filters in-process; this is acceptable for the early
// adopter cohort but Phase 7 / v3 should add a per-scope index.
func (s *BundleRedisStore) ListBundlesByScope(ctx context.Context, scope RuleScope) ([]*Bundle, error) {
	out := make([]*Bundle, 0)
	var cursor uint64
	versionInfix := bundleVersionInfix
	for {
		keys, next, err := s.client.Scan(ctx, cursor, bundleKeyPrefix+"*", 256).Result()
		if err != nil {
			return nil, fmt.Errorf("scan bundles: %w", err)
		}
		for _, k := range keys {
			// Skip per-version + version-index keys; we only want the envelope.
			if strings.Contains(k, versionInfix) || strings.HasSuffix(k, bundleVersionsSuffix) {
				continue
			}
			data, err := s.client.Get(ctx, k).Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read bundle %s: %w", k, err)
			}
			var b Bundle
			if err := json.Unmarshal(data, &b); err != nil {
				return nil, fmt.Errorf("unmarshal bundle %s: %w", k, err)
			}
			if scopeBindingMatches(b.ScopeBinding, scope) {
				bb := b
				out = append(out, &bb)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return out, nil
}

func scopeBindingMatches(have, want RuleScope) bool {
	if have.Kind != want.Kind {
		return false
	}
	if want.Kind == RuleScopeGlobal {
		return true
	}
	return have.Value == want.Value
}

// ---------------------------------------------------------------------------
// Bundle versioning
// ---------------------------------------------------------------------------

// CreateBundleVersion appends a new immutable version to bundleID.
// SETNX on the version blob + ZADD on the index. Idempotent on
// duplicate version numbers — returns ErrBundleVersionExists.
func (s *BundleRedisStore) CreateBundleVersion(ctx context.Context, bundleID string, v *BundleVersion) error {
	if v == nil {
		return fmt.Errorf("bundle version: nil version")
	}
	if strings.TrimSpace(bundleID) == "" || strings.TrimSpace(v.Version) == "" {
		return fmt.Errorf("bundle version: bundle id and version required")
	}
	if v.DeployedAt.IsZero() {
		v.DeployedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal bundle version: %w", err)
	}
	ok, err := s.client.SetNX(ctx, bundleVersionKey(bundleID, v.Version), payload, 0).Result()
	if err != nil {
		return fmt.Errorf("create bundle version: %w", err)
	}
	if !ok {
		return ErrBundleVersionExists
	}
	score := float64(v.DeployedAt.UnixNano())
	if err := s.client.ZAdd(ctx, bundleVersionsIndexKey(bundleID), redis.Z{Score: score, Member: v.Version}).Err(); err != nil {
		return fmt.Errorf("index bundle version: %w", err)
	}
	return nil
}

// ListBundleVersions returns all versions for a bundle, oldest first
// (ascending DeployedAt).
func (s *BundleRedisStore) ListBundleVersions(ctx context.Context, bundleID string) ([]*BundleVersion, error) {
	if strings.TrimSpace(bundleID) == "" {
		return nil, fmt.Errorf("bundle version: bundle id required")
	}
	versions, err := s.client.ZRange(ctx, bundleVersionsIndexKey(bundleID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, nil
	}
	keys := make([]string, len(versions))
	for i, v := range versions {
		keys[i] = bundleVersionKey(bundleID, v)
	}
	raw, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("mget versions: %w", err)
	}
	out := make([]*BundleVersion, 0, len(raw))
	for i, item := range raw {
		if item == nil {
			continue
		}
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected version payload type for %s", versions[i])
		}
		var v BundleVersion
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("unmarshal version %s: %w", versions[i], err)
		}
		vv := v
		out = append(out, &vv)
	}
	return out, nil
}

// GetBundleVersion returns one version. Returns
// ErrBundleVersionNotFound for unknown (bundleID, version) pairs.
func (s *BundleRedisStore) GetBundleVersion(ctx context.Context, bundleID, version string) (*BundleVersion, error) {
	if strings.TrimSpace(bundleID) == "" || strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("bundle version: bundle id and version required")
	}
	data, err := s.client.Get(ctx, bundleVersionKey(bundleID, version)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrBundleVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get bundle version: %w", err)
	}
	var v BundleVersion
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("unmarshal bundle version: %w", err)
	}
	return &v, nil
}

// ---------------------------------------------------------------------------
// Deployment lifecycle (atomic via Lua scripts)
// ---------------------------------------------------------------------------

// deployScript atomically:
//  1. Validates the requested (bundle_id, version) version blob exists.
//  2. SETs `policy:scope:{kind}:{value}:active` to "{bundleID}:{version}".
//  3. LPUSH a new Deployment{Action:deploy} JSON into the history list.
//  4. LTRIM the history to the last 100 entries.
//
// KEYS: [1]=scopeActiveKey [2]=scopeHistoryKey [3]=bundleVersionKey
// ARGV: [1]=bundleID [2]=version [3]=deploymentJSON
//
// Returns: empty string on success; "ERR_VERSION_NOT_FOUND" if KEYS[3]
// doesn't exist (caller maps to ErrBundleVersionNotFound).
var deployScript = redis.NewScript(`
local exists = redis.call('EXISTS', KEYS[3])
if exists == 0 then
  return 'ERR_VERSION_NOT_FOUND'
end
redis.call('SET', KEYS[1], ARGV[1] .. ':' .. ARGV[2])
redis.call('LPUSH', KEYS[2], ARGV[3])
redis.call('LTRIM', KEYS[2], 0, 99)
return ''
`)

// rollbackScript atomically:
//  1. Reads the current active (bundle_id, version) from KEYS[1].
//  2. Walks history newest-first to find the most-recent deploy whose
//     (bundle_id, version) differs from the current active. This skips
//     any rollback markers AND skips the deploy entry that established
//     the current active, so chained rollbacks unwind one step per call.
//  3. SETs active to that prior deploy's (bundle_id, version).
//  4. LPUSH a Deployment{Action:rollback} entry pointing at that prior pair.
//  5. LTRIM history to last 100.
//
// KEYS: [1]=scopeActiveKey [2]=scopeHistoryKey
// ARGV: [1]=deployedAtRFC3339Nano [2]=deployedBy [3]=auditHash
//
// Returns: a 3-element array [bundleID, version, deploymentJSON] on
// success, or "ERR_NO_ROLLBACK_TARGET" when no prior deploy with a
// different (bundle_id, version) exists.
var rollbackScript = redis.NewScript(`
local active = redis.call('GET', KEYS[1])
if not active or active == false or active == '' then
  return 'ERR_NO_ROLLBACK_TARGET'
end
local sep = string.find(active, ':')
if not sep then
  return 'ERR_NO_ROLLBACK_TARGET'
end
local cur_bid = string.sub(active, 1, sep - 1)
local cur_ver = string.sub(active, sep + 1)

local hist = redis.call('LRANGE', KEYS[2], 0, -1)

-- Locate the most-recent deploy entry matching the current active.
-- This is the original deploy that introduced the current binding;
-- we walk backwards from there to find the immediately-prior deploy.
local cur_deploy_idx = -1
for i, raw in ipairs(hist) do
  local ok, dec = pcall(cjson.decode, raw)
  if ok and type(dec) == 'table' and dec.action == 'deploy' then
    if dec.bundle_id == cur_bid and dec.version == cur_ver then
      cur_deploy_idx = i
      break
    end
  end
end
if cur_deploy_idx == -1 then
  return 'ERR_NO_ROLLBACK_TARGET'
end

-- Find the next deploy entry strictly older than the current binding.
local prior_idx = -1
for i = cur_deploy_idx + 1, #hist do
  local ok, dec = pcall(cjson.decode, hist[i])
  if ok and type(dec) == 'table' and dec.action == 'deploy' then
    prior_idx = i
    break
  end
end
if prior_idx == -1 then
  return 'ERR_NO_ROLLBACK_TARGET'
end
local prior = cjson.decode(hist[prior_idx])
local bundle_id = prior.bundle_id
local version = prior.version
redis.call('SET', KEYS[1], bundle_id .. ':' .. version)
local rollback_json = cjson.encode({
  bundle_id = bundle_id,
  version = version,
  scope = prior.scope,
  deployed_at = ARGV[1],
  deployed_by = ARGV[2],
  audit_hash = ARGV[3],
  action = 'rollback'
})
redis.call('LPUSH', KEYS[2], rollback_json)
redis.call('LTRIM', KEYS[2], 0, 99)
return {bundle_id, version, rollback_json}
`)

// DeployVersionToScope atomically rebinds the active deployment for
// scope to (bundleID, version) and appends the deploy event to history.
func (s *BundleRedisStore) DeployVersionToScope(ctx context.Context, bundleID, version string, scope RuleScope) (*Deployment, error) {
	if strings.TrimSpace(bundleID) == "" || strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("deploy: bundle id and version required")
	}
	dep := Deployment{
		BundleID:   bundleID,
		Version:    version,
		Scope:      scope,
		DeployedAt: time.Now().UTC(),
		Action:     DeploymentActionDeploy,
	}
	depJSON, err := json.Marshal(&dep)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment: %w", err)
	}
	keys := []string{
		scopeActiveKey(scope),
		scopeDeploymentHistoryKey(scope),
		bundleVersionKey(bundleID, version),
	}
	res, err := deployScript.Run(ctx, s.client, keys, bundleID, version, string(depJSON)).Text()
	if err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}
	if res == "ERR_VERSION_NOT_FOUND" {
		return nil, ErrBundleVersionNotFound
	}
	return &dep, nil
}

// RollbackDeployment reverts the active deployment for scope to the
// most-recent prior deploy (skipping rollback entries themselves so
// chained rollbacks unwind cleanly). Returns ErrNoRollbackTarget when
// no prior deploy exists.
func (s *BundleRedisStore) RollbackDeployment(ctx context.Context, scope RuleScope) (*Deployment, error) {
	keys := []string{
		scopeActiveKey(scope),
		scopeDeploymentHistoryKey(scope),
	}
	now := time.Now().UTC()
	res, err := rollbackScript.Run(ctx, s.client, keys, now.Format(time.RFC3339Nano), "", "").Result()
	if err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}
	if errStr, ok := res.(string); ok && errStr == "ERR_NO_ROLLBACK_TARGET" {
		return nil, ErrNoRollbackTarget
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) != 3 {
		return nil, fmt.Errorf("rollback: unexpected script result %T", res)
	}
	rollbackJSON, _ := arr[2].(string)
	var dep Deployment
	if err := json.Unmarshal([]byte(rollbackJSON), &dep); err != nil {
		return nil, fmt.Errorf("unmarshal rollback deployment: %w", err)
	}
	dep.DeployedAt = now
	return &dep, nil
}

// GetActiveDeployment returns the currently-active deployment for
// scope. The implementation reads the active pointer + the most-recent
// matching history entry to surface the full Deployment record (incl.
// timestamp + action). Returns ErrNoDeploymentForScope when nothing is
// bound.
func (s *BundleRedisStore) GetActiveDeployment(ctx context.Context, scope RuleScope) (*Deployment, error) {
	pointer, err := s.client.Get(ctx, scopeActiveKey(scope)).Result()
	if errors.Is(err, redis.Nil) || pointer == "" {
		return nil, ErrNoDeploymentForScope
	}
	if err != nil {
		return nil, fmt.Errorf("get active deployment: %w", err)
	}
	parts := strings.SplitN(pointer, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed active pointer %q", pointer)
	}
	bundleID, version := parts[0], parts[1]
	// Walk history newest-first to find the deploy/rollback that produced this binding.
	entries, err := s.client.LRange(ctx, scopeDeploymentHistoryKey(scope), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	for _, raw := range entries {
		var dep Deployment
		if err := json.Unmarshal([]byte(raw), &dep); err != nil {
			continue
		}
		if dep.BundleID == bundleID && dep.Version == version {
			return &dep, nil
		}
	}
	// Pointer set but no matching history — synthesize a minimal record.
	return &Deployment{
		BundleID: bundleID,
		Version:  version,
		Scope:    scope,
		Action:   DeploymentActionDeploy,
	}, nil
}

// ListDeploymentHistory returns up to limit history entries for scope,
// newest first. The implementation always reads up to
// deploymentHistoryCap (100) entries since LTRIM bounds it; the caller's
// limit is applied client-side.
func (s *BundleRedisStore) ListDeploymentHistory(ctx context.Context, scope RuleScope, limit int) ([]*Deployment, error) {
	if limit <= 0 || limit > deploymentHistoryCap {
		limit = deploymentHistoryCap
	}
	entries, err := s.client.LRange(ctx, scopeDeploymentHistoryKey(scope), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("list deployment history: %w", err)
	}
	out := make([]*Deployment, 0, len(entries))
	for _, raw := range entries {
		var dep Deployment
		if err := json.Unmarshal([]byte(raw), &dep); err != nil {
			return nil, fmt.Errorf("unmarshal deployment: %w", err)
		}
		dd := dep
		out = append(out, &dd)
	}
	return out, nil
}

// Compile-time interface satisfaction check.
var _ BundleStore = (*BundleRedisStore)(nil)
