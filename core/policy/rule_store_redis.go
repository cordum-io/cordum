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

// Redis key constructors for the Rule store. Lives here (not in
// bundle_store_keys.go) to keep Rule-specific surface area together.
//
//	policy:rule:{id}                          STRING (Rule envelope JSON)
//	policy:rule:{id}:version                  STRING (version-only sidecar for cheap CAS)
//	policy:rules:scope:{kind}:{value}         SET    (rule IDs by scope, for fast scope-bound lookups)
//
// The version sidecar lets the UpdateRule Lua script compare the
// expected version against a small string without re-decoding the
// envelope JSON inside the script. miniredis's cjson support is
// inconsistent for nested objects; the sidecar avoids the dependency.
const (
	ruleKeyPrefix       = "policy:rule:"
	ruleVersionSuffix   = ":version"
	rulesScopeKeyPrefix = "policy:rules:scope:"
)

func ruleKey(id string) string {
	return ruleKeyPrefix + id
}

func ruleVersionKey(id string) string {
	return ruleKeyPrefix + id + ruleVersionSuffix
}

func rulesScopeKey(scope RuleScope) string {
	return rulesScopeKeyPrefix + string(scope.Kind) + ":" + scope.Value
}

// RuleRedisStore is the Redis-backed RuleStore implementation. Mirrors
// BundleRedisStore conventions: Lua-atomic CAS for UpdateRule so
// concurrent updates serialize through Redis (not via Go-level WATCH/
// MULTI/EXEC, per memory mem-12f1ceeb).
type RuleRedisStore struct {
	client redis.UniversalClient
	// nowFn is overridable in tests so audit timestamps are deterministic
	// across miniredis-backed test runs.
	nowFn func() time.Time
	// actorFn returns the audit actor for writes. Default returns
	// "system" so unit tests that don't thread an actor get a stable
	// value. Production handlers override via WithActor(...) to
	// propagate the authenticated principal id.
	actorFn func() string
}

// NewRedisRuleStore builds a Redis-backed RuleStore from a connection
// URL.
func NewRedisRuleStore(url string) (*RuleRedisStore, error) {
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
	return &RuleRedisStore{
		client:  client,
		nowFn:   time.Now,
		actorFn: func() string { return "system" },
	}, nil
}

// NewRedisRuleStoreFromClient wraps an existing client for tests that
// share a miniredis instance with BundleStore.
func NewRedisRuleStoreFromClient(client redis.UniversalClient) *RuleRedisStore {
	return &RuleRedisStore{
		client:  client,
		nowFn:   time.Now,
		actorFn: func() string { return "system" },
	}
}

// WithNow overrides the clock used for audit timestamps. Tests use this
// to lock CreatedAt / UpdatedAt to a known value so equality assertions
// don't race against wall-clock drift.
func (s *RuleRedisStore) WithNow(now func() time.Time) *RuleRedisStore {
	if now == nil {
		return s
	}
	s.nowFn = now
	return s
}

// WithActor overrides the audit actor function. Production handlers
// pass a per-request closure that resolves the authenticated principal
// id; unit tests can pass a constant.
func (s *RuleRedisStore) WithActor(actor func() string) *RuleRedisStore {
	if actor == nil {
		return s
	}
	s.actorFn = actor
	return s
}

// Close releases the underlying Redis client.
func (s *RuleRedisStore) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// CreateRule persists a new Rule. Returns ErrRuleExists when the ID
// is already taken. Server-side metadata (Audit, Version, Status if
// empty) is overwritten. The caller's Audit/Version/Status fields are
// silently discarded — clients cannot fake history.
func (s *RuleRedisStore) CreateRule(ctx context.Context, r *Rule) (*Rule, error) {
	if r == nil {
		return nil, fmt.Errorf("rule: nil rule")
	}
	if strings.TrimSpace(r.ID) == "" {
		return nil, fmt.Errorf("rule: id required")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	now := s.nowFn()
	actor := s.actorFn()
	persisted := *r
	applyServerSideCreateMetadata(&persisted, now, actor)
	payload, err := json.Marshal(&persisted)
	if err != nil {
		return nil, fmt.Errorf("marshal rule: %w", err)
	}
	const script = `
local current = redis.call('GET', KEYS[1])
if current ~= false then
  return {err = 'EXISTS'}
end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], ARGV[2])
redis.call('SADD', KEYS[3], ARGV[3])
return 'OK'
`
	res, err := s.client.Eval(
		ctx, script,
		[]string{
			ruleKey(persisted.ID),
			ruleVersionKey(persisted.ID),
			rulesScopeKey(persisted.Scope),
		},
		payload, persisted.Version, persisted.ID,
	).Result()
	if err != nil {
		if isRedisErrEXISTS(err) {
			return nil, ErrRuleExists
		}
		return nil, fmt.Errorf("create rule: %w", err)
	}
	if str, ok := res.(string); !ok || str != "OK" {
		return nil, fmt.Errorf("create rule: unexpected redis response %v", res)
	}
	return &persisted, nil
}

// GetRule returns a Rule by ID. Returns ErrRuleNotFound on miss.
func (s *RuleRedisStore) GetRule(ctx context.Context, id string) (*Rule, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("rule: id required")
	}
	data, err := s.client.Get(ctx, ruleKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrRuleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get rule %s: %w", id, err)
	}
	var rule Rule
	if err := json.Unmarshal(data, &rule); err != nil {
		return nil, fmt.Errorf("decode rule %s: %w", id, err)
	}
	return &rule, nil
}

// UpdateRule writes the Rule iff the stored Version equals
// expectedVersion. On stale, returns *ErrRuleStaleVersion carrying the
// current version + audit hash so the caller can render a reload
// banner without an extra fetch.
//
// Implementation detail: read the current Rule, compare versions in Go
// (string equality), then write atomically via a Lua script that
// re-checks the version inside Redis. The Go-side check returns the
// stale-version error early; the Lua-side check guards against a race
// between our read and our write where another writer slipped a new
// version in.
func (s *RuleRedisStore) UpdateRule(
	ctx context.Context,
	r *Rule,
	expectedVersion string,
) (*Rule, error) {
	if r == nil {
		return nil, fmt.Errorf("rule: nil rule")
	}
	if strings.TrimSpace(r.ID) == "" {
		return nil, fmt.Errorf("rule: id required")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	prior, err := s.GetRule(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if prior.Version != expectedVersion {
		return nil, &ErrRuleStaleVersion{
			CurrentVersion:   prior.Version,
			CurrentAuditHash: computeRuleAuditHash(prior),
		}
	}
	now := s.nowFn()
	actor := s.actorFn()
	persisted := *r
	applyServerSideUpdateMetadata(&persisted, prior, now, actor)
	payload, err := json.Marshal(&persisted)
	if err != nil {
		return nil, fmt.Errorf("marshal rule: %w", err)
	}
	const script = `
local current = redis.call('GET', KEYS[1])
if current == false then
  return {err = 'NOTFOUND'}
end
local stored_version = redis.call('GET', KEYS[2])
if stored_version == false or stored_version ~= ARGV[2] then
  return {err = 'STALE'}
end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], ARGV[3])
return 'OK'
`
	res, err := s.client.Eval(
		ctx, script,
		[]string{ruleKey(r.ID), ruleVersionKey(r.ID)},
		payload, expectedVersion, persisted.Version,
	).Result()
	if err != nil {
		if isRedisErrNOTFOUND(err) {
			return nil, ErrRuleNotFound
		}
		if isRedisErrSTALE(err) {
			// A concurrent writer landed between our Go-side read and
			// the Lua-side compare. Re-fetch to give the caller the
			// freshest current_version + current_audit_hash.
			latest, fetchErr := s.GetRule(ctx, r.ID)
			if fetchErr != nil {
				return nil, fmt.Errorf("update rule (post-stale fetch): %w", fetchErr)
			}
			return nil, &ErrRuleStaleVersion{
				CurrentVersion:   latest.Version,
				CurrentAuditHash: computeRuleAuditHash(latest),
			}
		}
		return nil, fmt.Errorf("update rule %s: %w", r.ID, err)
	}
	if str, ok := res.(string); !ok || str != "OK" {
		return nil, fmt.Errorf("update rule %s: unexpected redis response %v", r.ID, res)
	}
	// If scope changed, rebalance the per-scope index. Done outside the
	// Lua script because cross-key SREM/SADD on different scope keys
	// requires the keys to be available at script-eval time; the older
	// scope's value isn't known to the Lua block. Race-acceptable: a
	// concurrent reader during the rebalance might transiently see the
	// rule in both scopes, never in neither.
	if !rulesScopesEqual(prior.Scope, persisted.Scope) {
		s.client.SRem(ctx, rulesScopeKey(prior.Scope), persisted.ID)
		s.client.SAdd(ctx, rulesScopeKey(persisted.Scope), persisted.ID)
	}
	return &persisted, nil
}

// DeleteRule removes the Rule + drops its scope-index membership.
// Returns ErrRuleNotFound when the rule doesn't exist.
func (s *RuleRedisStore) DeleteRule(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("rule: id required")
	}
	prior, err := s.GetRule(ctx, id)
	if err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, ruleKey(id))
	pipe.Del(ctx, ruleVersionKey(id))
	pipe.SRem(ctx, rulesScopeKey(prior.Scope), id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("delete rule %s: %w", id, err)
	}
	return nil
}

// ListRulesByScope returns every Rule whose Scope matches the supplied
// scope, sorted by ID ascending for determinism.
func (s *RuleRedisStore) ListRulesByScope(
	ctx context.Context,
	scope RuleScope,
) ([]*Rule, error) {
	ids, err := s.client.SMembers(ctx, rulesScopeKey(scope)).Result()
	if err != nil {
		return nil, fmt.Errorf("list rules by scope: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = ruleKey(id)
	}
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("list rules by scope mget: %w", err)
	}
	rules := make([]*Rule, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var rule Rule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			continue
		}
		rules = append(rules, &rule)
	}
	sortRulesByID(rules)
	return rules, nil
}

// ListRules returns every Rule across all scopes via SCAN of the
// `policy:rule:*` key namespace, filtering out the `:version` sidecar
// keys. Used by Backend 5d's unified GET /api/v1/policy/rules. Sorted
// by ID ascending for deterministic cursor pagination across calls.
func (s *RuleRedisStore) ListRules(ctx context.Context) ([]*Rule, error) {
	var ids []string
	var cursor uint64
	for {
		batch, next, err := s.client.Scan(ctx, cursor, ruleKeyPrefix+"*", 256).Result()
		if err != nil {
			return nil, fmt.Errorf("scan rules: %w", err)
		}
		for _, key := range batch {
			// Filter out the `:version` sidecar keys; only the envelope
			// keys carry the rule id and JSON payload.
			if strings.HasSuffix(key, ruleVersionSuffix) {
				continue
			}
			id := strings.TrimPrefix(key, ruleKeyPrefix)
			if id == "" {
				continue
			}
			ids = append(ids, id)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = ruleKey(id)
	}
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("list rules mget: %w", err)
	}
	rules := make([]*Rule, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var rule Rule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			continue
		}
		rules = append(rules, &rule)
	}
	sortRulesByID(rules)
	return rules, nil
}

func rulesScopesEqual(a, b RuleScope) bool {
	return a.Kind == b.Kind && a.Value == b.Value
}

func sortRulesByID(rules []*Rule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0 && rules[j-1].ID > rules[j].ID; j-- {
			rules[j-1], rules[j] = rules[j], rules[j-1]
		}
	}
}

func isRedisErrEXISTS(err error) bool {
	return err != nil && strings.Contains(err.Error(), "EXISTS")
}

func isRedisErrNOTFOUND(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOTFOUND")
}

func isRedisErrSTALE(err error) bool {
	return err != nil && strings.Contains(err.Error(), "STALE")
}
