package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

const (
	defaultDecisionLogTTL      = 30 * 24 * time.Hour
	decisionLogTTLSecondsEnv   = "CORDUM_DECISION_LOG_TTL_SECONDS"
	decisionLogPrefix          = "gov:dec:"
	decisionLogRecordFieldJSON = "json"
	decisionLogQueryWorkFactor = 10
)

var _ model.DecisionLogStore = (*RedisDecisionLogStore)(nil)

// RedisDecisionLogStore persists governance decisions in Redis for the Policy
// Decision Log / Governance Timeline APIs.
type RedisDecisionLogStore struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func NewRedisDecisionLogStore(url string) (*RedisDecisionLogStore, error) {
	if url == "" {
		url = defaultRedisURL
	}

	ttl := redisDecisionLogTTL()
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	slog.Debug("decision log store connected", "component", "store", "ttl", ttl.String())
	return &RedisDecisionLogStore{client: client, ttl: ttl}, nil
}

// NewRedisDecisionLogStoreFromClient constructs a decision-log store from a shared Redis client.
func NewRedisDecisionLogStoreFromClient(client redis.UniversalClient) *RedisDecisionLogStore {
	return &RedisDecisionLogStore{client: client, ttl: redisDecisionLogTTL()}
}

func redisDecisionLogTTL() time.Duration {
	ttl := defaultDecisionLogTTL
	if raw := strings.TrimSpace(os.Getenv(decisionLogTTLSecondsEnv)); raw != "" {
		secs, err := strconv.Atoi(raw)
		if err != nil {
			slog.Warn("invalid "+decisionLogTTLSecondsEnv+", using default", "value", sanitizeLogValue(raw), "error", sanitizeLogValue(err.Error()), "default", defaultDecisionLogTTL)
		} else if secs <= 0 {
			slog.Warn("non-positive "+decisionLogTTLSecondsEnv+", using default", "value", secs, "default", defaultDecisionLogTTL)
		} else {
			ttl = time.Duration(secs) * time.Second
		}
	}
	return ttl
}

func (s *RedisDecisionLogStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *RedisDecisionLogStore) AppendDecision(ctx context.Context, record model.DecisionLogRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("decision log store client is nil")
	}

	record = normalizeDecisionLogRecord(record)
	if err := validateDecisionLogRecord(record); err != nil {
		return err
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal decision log record: %w", err)
	}

	recordID := decisionRecordID(record)
	recordKey := decisionRecordKey(record.Tenant, recordID)
	verdict, err := record.Verdict.DecisionLogWireValue()
	if err != nil {
		return err
	}

	// The record and each index sit in different Redis Cluster slots, so they
	// cannot share a MULTI. The record goes first, with its TTL.
	if _, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, recordKey, decisionLogRecordFieldJSON, payload)
		if s.ttl > 0 {
			pipe.Expire(ctx, recordKey, s.ttl)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("append decision log record: %w", err)
	}

	if err := s.appendDecisionIndexes(ctx, decisionIndexKeys(record, verdict), recordID, float64(record.Timestamp)); err != nil {
		// backfill-decisions skips a decision whose record exists, so an
		// unindexed record would never be repaired: remove it instead.
		undoErr := s.client.Del(context.WithoutCancel(ctx), recordKey).Err()
		return fmt.Errorf("append decision log record: %w", errors.Join(err, undoErr))
	}
	return nil
}

// decisionIndexKeys lists every index that holds a record: the primary index,
// the rule, agent and topic indexes when those are set, and the verdict index.
func decisionIndexKeys(record model.DecisionLogRecord, verdict string) []string {
	keys := []string{decisionPrimaryIndexKey(record.Tenant)}
	if record.RuleID != "" {
		keys = append(keys, decisionRuleIndexKey(record.Tenant, record.RuleID))
	}
	if record.AgentID != "" {
		keys = append(keys, decisionAgentIndexKey(record.Tenant, record.AgentID))
	}
	if record.Topic != "" {
		keys = append(keys, decisionTopicIndexKey(record.Tenant, record.Topic))
	}
	return append(keys, decisionVerdictIndexKey(record.Tenant, verdict))
}

// appendDecisionIndexes adds recordID to each index and trims the entries past
// the retention window. Every command names one key, so a cluster client sends
// each to its own slot.
func (s *RedisDecisionLogStore) appendDecisionIndexes(ctx context.Context, keys []string, recordID string, score float64) error {
	cutoff := strconv.FormatInt(time.Now().UTC().Add(-s.ttl).UnixMilli(), 10)
	pipe := s.client.Pipeline()
	for _, key := range keys {
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: recordID})
		if s.ttl > 0 {
			pipe.ZRemRangeByScore(ctx, key, "-inf", cutoff)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisDecisionLogStore) QueryDecisions(ctx context.Context, query model.DecisionQuery) (model.DecisionPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return model.DecisionPage{}, fmt.Errorf("decision log store client is nil")
	}

	query, err := query.Normalize(time.Now().UTC())
	if err != nil {
		return model.DecisionPage{}, err
	}

	sourceKey, filterKeys, err := s.querySourceKey(ctx, query)
	if err != nil {
		return model.DecisionPage{}, err
	}

	cursorTS, cursorID, hasCursor, err := decodeDecisionCursor(query.Cursor)
	if err != nil {
		return model.DecisionPage{}, err
	}

	limitPlusOne := query.Limit + 1
	candidateCap := query.Limit * decisionLogQueryWorkFactor
	if candidateCap < limitPlusOne {
		candidateCap = limitPlusOne
	}
	chunkSize := query.Limit * 2
	if chunkSize < 32 {
		chunkSize = 32
	}
	if chunkSize > 256 {
		chunkSize = 256
	}
	if chunkSize > candidateCap {
		chunkSize = candidateCap
	}

	var (
		offset     int64
		scanned    int
		items      []model.DecisionLogRecord
		nextCursor string
	)

	for scanned < candidateCap && len(items) < limitPlusOne {
		remaining := candidateCap - scanned
		count := chunkSize
		if count > remaining {
			count = remaining
		}
		if count <= 0 {
			break
		}

		zs, err := s.client.ZRevRangeByScoreWithScores(ctx, sourceKey, &redis.ZRangeBy{
			Max:    strconv.FormatInt(query.Until, 10),
			Min:    strconv.FormatInt(query.Since, 10),
			Offset: offset,
			Count:  int64(count),
		}).Result()
		if err != nil {
			return model.DecisionPage{}, fmt.Errorf("query decision index: %w", err)
		}
		if len(zs) == 0 {
			break
		}
		offset += int64(len(zs))
		zs, err = s.filterDecisionMembers(ctx, zs, filterKeys)
		if err != nil {
			return model.DecisionPage{}, err
		}
		scanned += len(zs)

		ids := make([]string, 0, len(zs))
		for _, z := range zs {
			id := fmt.Sprint(z.Member)
			ts := int64(z.Score)
			if hasCursor && !afterDecisionCursor(ts, id, cursorTS, cursorID) {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			continue
		}

		records, staleIDs, err := s.fetchDecisionRecords(ctx, query.Tenant, ids)
		if err != nil {
			return model.DecisionPage{}, err
		}
		if len(staleIDs) > 0 {
			s.cleanupStaleDecisionIDs(ctx, query, sourceKey, staleIDs)
		}

		for _, record := range records {
			if !matchesDecisionQuery(record, query) {
				continue
			}
			items = append(items, record)
			if len(items) == limitPlusOne {
				break
			}
		}
	}

	if len(items) > query.Limit {
		lastVisible := items[query.Limit-1]
		nextCursor = model.EncodeDecisionCursor(lastVisible.Timestamp, decisionRecordID(lastVisible))
		items = items[:query.Limit]
	}

	return model.DecisionPage{Items: items, NextCursor: nextCursor}, nil
}

func (s *RedisDecisionLogStore) querySourceKey(ctx context.Context, query model.DecisionQuery) (string, []string, error) {
	filterKeys := make([]string, 0, 4)
	if query.RuleID != "" {
		filterKeys = append(filterKeys, decisionRuleIndexKey(query.Tenant, query.RuleID))
	}
	if query.AgentID != "" {
		filterKeys = append(filterKeys, decisionAgentIndexKey(query.Tenant, query.AgentID))
	}
	if query.Topic != "" {
		filterKeys = append(filterKeys, decisionTopicIndexKey(query.Tenant, query.Topic))
	}
	if query.Verdict != "" {
		verdict, err := query.Verdict.DecisionLogWireValue()
		if err != nil {
			return "", nil, err
		}
		filterKeys = append(filterKeys, decisionVerdictIndexKey(query.Tenant, verdict))
	}

	if len(filterKeys) == 0 {
		return decisionPrimaryIndexKey(query.Tenant), nil, nil
	}
	if len(filterKeys) == 1 {
		return filterKeys[0], nil, nil
	}

	pipe := s.client.Pipeline()
	counts := make([]*redis.IntCmd, len(filterKeys))
	for i, key := range filterKeys {
		counts[i] = pipe.ZCount(ctx, key, strconv.FormatInt(query.Since, 10), strconv.FormatInt(query.Until, 10))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return "", nil, fmt.Errorf("count decision filter members: %w", err)
	}
	smallest := 0
	for i, count := range counts {
		if count.Val() < counts[smallest].Val() {
			smallest = i
		}
	}
	source := filterKeys[smallest]
	return source, append(filterKeys[:smallest], filterKeys[smallest+1:]...), nil
}

func (s *RedisDecisionLogStore) filterDecisionMembers(ctx context.Context, zs []redis.Z, keys []string) ([]redis.Z, error) {
	if len(keys) == 0 {
		return zs, nil
	}
	pipe := s.client.Pipeline()
	cmds := make([]*redis.FloatCmd, 0, len(zs)*len(keys))
	for _, z := range zs {
		for _, key := range keys {
			cmds = append(cmds, pipe.ZScore(ctx, key, fmt.Sprint(z.Member)))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("query decision filter membership: %w", err)
	}
	kept := zs[:0]
	for i, z := range zs {
		present := true
		for _, cmd := range cmds[i*len(keys) : (i+1)*len(keys)] {
			if err := cmd.Err(); err != nil {
				if !errors.Is(err, redis.Nil) {
					return nil, fmt.Errorf("query decision filter membership: %w", err)
				}
				present = false
			}
		}
		if present {
			kept = append(kept, z)
		}
	}
	return kept, nil
}

func (s *RedisDecisionLogStore) fetchDecisionRecords(ctx context.Context, tenant string, ids []string) ([]model.DecisionLogRecord, []string, error) {
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGet(ctx, decisionRecordKey(tenant, id), decisionLogRecordFieldJSON)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, nil, fmt.Errorf("fetch decision log records: %w", err)
	}

	records := make([]model.DecisionLogRecord, 0, len(ids))
	staleIDs := make([]string, 0)
	for i, id := range ids {
		data, err := cmds[i].Bytes()
		if err != nil {
			if err == redis.Nil {
				staleIDs = append(staleIDs, id)
				continue
			}
			return nil, nil, fmt.Errorf("fetch decision log record %q: %w", id, err)
		}
		var record model.DecisionLogRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, nil, fmt.Errorf("decode decision log record %q: %w", id, err)
		}
		records = append(records, record)
	}
	return records, staleIDs, nil
}

func (s *RedisDecisionLogStore) cleanupStaleDecisionIDs(ctx context.Context, query model.DecisionQuery, sourceKey string, staleIDs []string) {
	if len(staleIDs) == 0 {
		return
	}
	members := make([]interface{}, len(staleIDs))
	for i, id := range staleIDs {
		members[i] = id
	}
	keys := []string{decisionPrimaryIndexKey(query.Tenant)}
	if sourceKey != "" && sourceKey != keys[0] {
		keys = append(keys, sourceKey)
	}
	if query.RuleID != "" {
		keys = append(keys, decisionRuleIndexKey(query.Tenant, query.RuleID))
	}
	if query.AgentID != "" {
		keys = append(keys, decisionAgentIndexKey(query.Tenant, query.AgentID))
	}
	if query.Topic != "" {
		keys = append(keys, decisionTopicIndexKey(query.Tenant, query.Topic))
	}
	if query.Verdict != "" {
		if verdict, err := query.Verdict.DecisionLogWireValue(); err == nil {
			keys = append(keys, decisionVerdictIndexKey(query.Tenant, verdict))
		}
	}
	for _, key := range uniqueStrings(keys) {
		if err := s.client.ZRem(ctx, key, members...).Err(); err != nil {
			slog.Warn("decision-log: stale index cleanup failed", "key", key, "count", len(staleIDs), "error", err)
		}
	}
}

func normalizeDecisionLogRecord(record model.DecisionLogRecord) model.DecisionLogRecord {
	record.Tenant = strings.TrimSpace(record.Tenant)
	record.JobID = strings.TrimSpace(record.JobID)
	record.AgentID = strings.TrimSpace(record.AgentID)
	record.Topic = strings.TrimSpace(record.Topic)
	record.RuleID = strings.TrimSpace(record.RuleID)
	record.PolicyVersion = strings.TrimSpace(record.PolicyVersion)
	record.Reason = strings.TrimSpace(record.Reason)
	if record.Timestamp == 0 {
		record.Timestamp = time.Now().UTC().UnixMilli()
	}
	return record
}

func validateDecisionLogRecord(record model.DecisionLogRecord) error {
	if record.Tenant == "" {
		return fmt.Errorf("decision log record tenant is required")
	}
	if record.JobID == "" {
		return fmt.Errorf("decision log record job id is required")
	}
	if record.Timestamp <= 0 {
		return fmt.Errorf("decision log record timestamp is required")
	}
	if _, err := record.Verdict.DecisionLogWireValue(); err != nil {
		return err
	}
	return nil
}

func matchesDecisionQuery(record model.DecisionLogRecord, query model.DecisionQuery) bool {
	if record.Tenant != query.Tenant {
		return false
	}
	if record.Timestamp < query.Since || record.Timestamp > query.Until {
		return false
	}
	if query.Topic != "" && record.Topic != query.Topic {
		return false
	}
	if query.RuleID != "" && record.RuleID != query.RuleID {
		return false
	}
	if query.AgentID != "" && record.AgentID != query.AgentID {
		return false
	}
	if query.Verdict != "" && record.Verdict != query.Verdict {
		return false
	}
	return true
}

func decisionTenantPrefix(tenant string) string {
	return decisionLogPrefix + tenant + ":"
}

func decisionPrimaryIndexKey(tenant string) string {
	return decisionTenantPrefix(tenant) + "idx:t"
}

func decisionRecordKey(tenant, id string) string {
	return decisionTenantPrefix(tenant) + "rec:" + id
}

func decisionRuleIndexKey(tenant, ruleID string) string {
	return decisionTenantPrefix(tenant) + "idx:rule:" + ruleID
}

func decisionAgentIndexKey(tenant, agentID string) string {
	return decisionTenantPrefix(tenant) + "idx:agent:" + agentID
}

func decisionTopicIndexKey(tenant, topic string) string {
	return decisionTenantPrefix(tenant) + "idx:topic:" + topic
}

func decisionVerdictIndexKey(tenant, verdict string) string {
	return decisionTenantPrefix(tenant) + "idx:verdict:" + verdict
}

func decisionRecordID(record model.DecisionLogRecord) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		record.Tenant,
		record.JobID,
		strconv.FormatInt(record.Timestamp, 10),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

func decodeDecisionCursor(cursor string) (int64, string, bool, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, "", false, nil
	}
	ts, id, err := model.DecodeDecisionCursor(cursor)
	if err != nil {
		return 0, "", false, err
	}
	return ts, id, true, nil
}

func afterDecisionCursor(timestamp int64, id string, cursorTS int64, cursorID string) bool {
	if timestamp < cursorTS {
		return true
	}
	if timestamp > cursorTS {
		return false
	}
	return id < cursorID
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
