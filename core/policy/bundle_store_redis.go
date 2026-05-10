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
// Verifies the parent bundle exists (returns ErrBundleNotFound if not)
// to prevent orphan versions, then SETNX on the version blob + ZADD on
// the index. Idempotent on duplicate version numbers — returns
// ErrBundleVersionExists. There is no DeleteBundle on the interface, so
// the EXISTS-then-SETNX sequence cannot race with a parent removal.
func (s *BundleRedisStore) CreateBundleVersion(ctx context.Context, bundleID string, v *BundleVersion) error {
	if v == nil {
		return fmt.Errorf("bundle version: nil version")
	}
	if strings.TrimSpace(bundleID) == "" || strings.TrimSpace(v.Version) == "" {
		return fmt.Errorf("bundle version: bundle id and version required")
	}
	exists, err := s.client.Exists(ctx, bundleKey(bundleID)).Result()
	if err != nil {
		return fmt.Errorf("check parent bundle: %w", err)
	}
	if exists == 0 {
		return ErrBundleNotFound
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
//  2. Reads the current active "{bundleID}:{version}" pointer (may be empty).
//  3. Decodes the incoming deploy JSON, baking the prior active pair
//     into prev_bundle_id + prev_version so future rollbacks can restore
//     this exact prior state.
//  4. SETs `policy:scope:{kind}:{value}:active` to the new "{bundleID}:{version}".
//  5. LPUSHes the populated deploy JSON into the history list.
//  6. LTRIMs the history to the last deploymentHistoryCap entries.
//
// KEYS: [1]=scopeActiveKey [2]=scopeHistoryKey [3]=bundleVersionKey
// ARGV: [1]=bundleID [2]=version [3]=deploymentJSON [4]=historyCap
//
// Returns: the populated deployment JSON (with prev_bundle_id +
// prev_version filled in) on success, or the literal sentinel string
// "ERR_VERSION_NOT_FOUND" if KEYS[3] doesn't exist (caller maps to
// ErrBundleVersionNotFound). The two cases are distinguishable by
// prefix — JSON starts with '{', the sentinel starts with 'E'.
var deployScript = redis.NewScript(`
local exists = redis.call('EXISTS', KEYS[3])
if exists == 0 then
  return 'ERR_VERSION_NOT_FOUND'
end
local prev = redis.call('GET', KEYS[1])
local prev_bid = ''
local prev_ver = ''
if prev and prev ~= false and prev ~= '' then
  local sep = string.find(prev, ':')
  if sep then
    prev_bid = string.sub(prev, 1, sep - 1)
    prev_ver = string.sub(prev, sep + 1)
  end
end
local dep = cjson.decode(ARGV[3])
dep.prev_bundle_id = prev_bid
dep.prev_version = prev_ver
local dep_json = cjson.encode(dep)
redis.call('SET', KEYS[1], ARGV[1] .. ':' .. ARGV[2])
redis.call('LPUSH', KEYS[2], dep_json)
redis.call('LTRIM', KEYS[2], 0, tonumber(ARGV[4]) - 1)
return dep_json
`)

// rollbackScript atomically:
//  1. Reads the current active "{bundle_id}:{version}" pointer.
//  2. Walks history newest-first to find the most-recent deploy event
//     matching the current active pair (skipping rollback markers).
//  3. Reads that deploy event's prev_bundle_id + prev_version — the
//     pair that was active immediately before this deploy ran.
//  4. SETs active to that prior pair.
//  5. LPUSHes a Deployment{Action:rollback} entry pointing at the
//     restored pair, with prev_* fields capturing the pair we rolled
//     away from (informational; rollback never reads its own prev_*).
//  6. LTRIMs history to deploymentHistoryCap.
//
// KEYS: [1]=scopeActiveKey [2]=scopeHistoryKey
// ARGV: [1]=deployedAtRFC3339Nano [2]=deployedBy [3]=auditHash [4]=scopeJSON [5]=historyCap
//
// Returns: a 3-element array [bundleID, version, deploymentJSON] on
// success, or the literal sentinel string "ERR_NO_ROLLBACK_TARGET"
// when there's no current active OR no matching deploy event OR the
// matching deploy event has empty prev_* fields (i.e. the original
// first deploy with no prior state).
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

-- Find the most-recent deploy event matching the current active pair.
-- That entry's prev_bundle_id + prev_version are the pair to restore.
local prev_bid = ''
local prev_ver = ''
local found = false
for i, raw in ipairs(hist) do
  local ok, dec = pcall(cjson.decode, raw)
  if ok and type(dec) == 'table' and dec.action == 'deploy' then
    if dec.bundle_id == cur_bid and dec.version == cur_ver then
      prev_bid = dec.prev_bundle_id or ''
      prev_ver = dec.prev_version or ''
      found = true
      break
    end
  end
end
if not found then
  return 'ERR_NO_ROLLBACK_TARGET'
end
if prev_bid == '' or prev_ver == '' then
  -- Rolling back the original first deploy: no prior state to restore.
  return 'ERR_NO_ROLLBACK_TARGET'
end

local scope = cjson.decode(ARGV[4])
redis.call('SET', KEYS[1], prev_bid .. ':' .. prev_ver)
local marker = {
  bundle_id = prev_bid,
  version = prev_ver,
  scope = scope,
  deployed_at = ARGV[1],
  deployed_by = ARGV[2],
  audit_hash = ARGV[3],
  action = 'rollback',
  prev_bundle_id = cur_bid,
  prev_version = cur_ver,
}
local marker_json = cjson.encode(marker)
redis.call('LPUSH', KEYS[2], marker_json)
redis.call('LTRIM', KEYS[2], 0, tonumber(ARGV[5]) - 1)
return {prev_bid, prev_ver, marker_json}
`)

// DeployVersionToScope atomically rebinds the active deployment for
// scope to (bundleID, version) and appends the deploy event to history.
// The returned Deployment carries PrevBundleID + PrevVersion populated
// from the active state at script-execution time (read inside Lua so
// concurrent deploys on the same scope serialize correctly).
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
	res, err := deployScript.Run(ctx, s.client, keys, bundleID, version, string(depJSON), deploymentHistoryCap).Text()
	if err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}
	if res == "ERR_VERSION_NOT_FOUND" {
		return nil, ErrBundleVersionNotFound
	}
	var populated Deployment
	if err := json.Unmarshal([]byte(res), &populated); err != nil {
		return nil, fmt.Errorf("unmarshal deploy result: %w", err)
	}
	return &populated, nil
}

// RollbackDeployment reverts the active deployment for scope to the
// (bundle, version) pair that was active immediately before the current
// binding was established. Per the rollbackScript contract, this reads
// the matching deploy event's prev_* fields so rollback after a
// deploy-after-rollback restores the correct prior state. Returns
// ErrNoRollbackTarget when no current active exists, no matching deploy
// event is found in bounded history, or the matching deploy has empty
// prev_* fields (the first-ever deploy).
func (s *BundleRedisStore) RollbackDeployment(ctx context.Context, scope RuleScope) (*Deployment, error) {
	keys := []string{
		scopeActiveKey(scope),
		scopeDeploymentHistoryKey(scope),
	}
	now := time.Now().UTC()
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return nil, fmt.Errorf("marshal rollback scope: %w", err)
	}
	res, err := rollbackScript.Run(ctx, s.client, keys, now.Format(time.RFC3339Nano), "", "", string(scopeJSON), deploymentHistoryCap).Result()
	if err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}
	if errStr, ok := res.(string); ok && errStr == "ERR_NO_ROLLBACK_TARGET" {
		return nil, ErrNoRollbackTarget
	}
	arr, ok := res.([]any)
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

// AddRuleToBundle appends ruleID to the bundle's RuleIDs slice via a
// read-modify-write Lua script. The script is atomic against concurrent
// AddRuleToBundle calls on the same bundle: each invocation reads the
// current envelope, decodes the rule_ids list with a small parser
// (cjson would require nested-object support that miniredis lacks), and
// writes back the updated envelope iff the bundle still exists. Idempotent
// on repeated ruleID — the second call leaves RuleIDs unchanged.
//
// ruleExists is called BEFORE the Lua script, not inside it, because Lua
// needs the keys at script-eval time and the rule's existence isn't
// captured by a single key (the rule's scope membership lives in a
// separate SET). Calling RuleStore.GetRule (or an equivalent) is the
// caller's responsibility — the BundleStore stays decoupled from
// RuleStore. Race-acceptable: a rule deleted between the existence
// check and the bundle update would leave a dangling rule_id; a follow-up
// list/get on the bundle's rules surfaces the gap. The cleanup is the
// dashboard's responsibility.
func (s *BundleRedisStore) AddRuleToBundle(
	ctx context.Context,
	bundleID, ruleID string,
	ruleExists func(ctx context.Context, ruleID string) (bool, error),
) (*Bundle, error) {
	if strings.TrimSpace(bundleID) == "" {
		return nil, fmt.Errorf("bundle: id required")
	}
	if strings.TrimSpace(ruleID) == "" {
		return nil, fmt.Errorf("rule: id required")
	}
	if ruleExists == nil {
		return nil, fmt.Errorf("ruleExists callback required")
	}
	exists, err := ruleExists(ctx, ruleID)
	if err != nil {
		return nil, fmt.Errorf("rule existence check: %w", err)
	}
	if !exists {
		return nil, ErrRuleNotFound
	}
	const script = `
local raw = redis.call('GET', KEYS[1])
if raw == false then
  return {err = 'NOTFOUND'}
end
return raw
`
	res, err := s.client.Eval(ctx, script, []string{bundleKey(bundleID)}).Result()
	if err != nil {
		if isRedisErrNOTFOUND(err) {
			return nil, ErrBundleNotFound
		}
		return nil, fmt.Errorf("read bundle %s: %w", bundleID, err)
	}
	rawStr, ok := res.(string)
	if !ok {
		return nil, fmt.Errorf("read bundle %s: unexpected redis response %v", bundleID, res)
	}
	var b Bundle
	if err := json.Unmarshal([]byte(rawStr), &b); err != nil {
		return nil, fmt.Errorf("unmarshal bundle %s: %w", bundleID, err)
	}
	for _, existing := range b.RuleIDs {
		if existing == ruleID {
			return &b, nil
		}
	}
	b.RuleIDs = append(b.RuleIDs, ruleID)
	updated, err := json.Marshal(&b)
	if err != nil {
		return nil, fmt.Errorf("marshal bundle %s: %w", bundleID, err)
	}
	const writeScript = `
local current = redis.call('GET', KEYS[1])
if current == false then
  return {err = 'NOTFOUND'}
end
if current ~= ARGV[1] then
  return {err = 'CONFLICT'}
end
redis.call('SET', KEYS[1], ARGV[2])
return 'OK'
`
	writeRes, err := s.client.Eval(
		ctx, writeScript,
		[]string{bundleKey(bundleID)},
		rawStr, updated,
	).Result()
	if err != nil {
		if isRedisErrNOTFOUND(err) {
			return nil, ErrBundleNotFound
		}
		if strings.Contains(err.Error(), "CONFLICT") {
			// Another writer landed between our read and our write.
			// Recurse once to retry — concurrent ruleID adds converge.
			return s.AddRuleToBundle(ctx, bundleID, ruleID, ruleExists)
		}
		return nil, fmt.Errorf("update bundle %s rule_ids: %w", bundleID, err)
	}
	if str, ok := writeRes.(string); !ok || str != "OK" {
		return nil, fmt.Errorf("update bundle %s rule_ids: unexpected redis response %v", bundleID, writeRes)
	}
	return &b, nil
}

// Compile-time interface satisfaction check.
var _ BundleStore = (*BundleRedisStore)(nil)
