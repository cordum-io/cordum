package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	mcpUpstreamCASMaxAttempts = 5
	mcpUpstreamBackupTTL      = 30 * 24 * time.Hour
	// DefaultMaxMCPUpstreamsPerTenant bounds tenant/global MCP upstream
	// indexes so create/list operations cannot grow without an operator
	// limit. Tests may lower the value via RedisMCPUpstreamRegistry fields.
	DefaultMaxMCPUpstreamsPerTenant = 1000
	defaultMCPUpstreamListScanCount = 128
)

// RedisMCPUpstreamRegistry stores MCP upstream records in Redis with
// tenant-scoped indexes. It intentionally stores secret references only; it
// never resolves or expands credential material.
type RedisMCPUpstreamRegistry struct {
	client             redis.UniversalClient
	now                func() time.Time
	createMaxPerTenant int64
	listScanBatchSize  int64
	listScanHook       func(scope string, count int64)
}

func NewRedisMCPUpstreamRegistryFromClient(client redis.UniversalClient) *RedisMCPUpstreamRegistry {
	return &RedisMCPUpstreamRegistry{client: client, now: time.Now}
}

func (r *RedisMCPUpstreamRegistry) Create(ctx context.Context, upstream *UpstreamServer) error {
	if err := r.ensureReady(); err != nil {
		return err
	}
	record, err := normalizeMCPUpstream(upstream, r.now().UTC())
	if err != nil {
		return err
	}
	if err := ValidateMCPUpstream(ctx, &record, "", nil); err != nil {
		return err
	}
	key := mcpUpstreamKey(record.TenantID, record.Name)
	indexKey := mcpUpstreamTenantIndexKey(record.TenantID)
	payload, err := marshalMCPUpstream(record)
	if err != nil {
		return err
	}
	return redisutil.Retry(ctx, r.client, func(tx *redis.Tx) error {
		exists, err := tx.Exists(ctx, key).Result()
		if err != nil {
			return err
		}
		if exists > 0 {
			return ErrUpstreamAlreadyExists
		}
		count, err := tx.SCard(ctx, indexKey).Result()
		if err != nil {
			return err
		}
		if count >= r.tenantCreateLimit() {
			return ErrUpstreamLimitExceeded
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HSet(ctx, key, "payload", payload)
			pipe.SAdd(ctx, indexKey, record.Name)
			return nil
		})
		return err
	}, redisutil.WithKeys(key, indexKey), redisutil.WithMaxAttempts(mcpUpstreamCASMaxAttempts))
}

func (r *RedisMCPUpstreamRegistry) tenantCreateLimit() int64 {
	if r != nil && r.createMaxPerTenant > 0 {
		return r.createMaxPerTenant
	}
	return DefaultMaxMCPUpstreamsPerTenant
}

func (r *RedisMCPUpstreamRegistry) listScanLimit() int64 {
	if r != nil && r.listScanBatchSize > 0 {
		return r.listScanBatchSize
	}
	return DefaultMaxMCPUpstreamsPerTenant
}

func (r *RedisMCPUpstreamRegistry) listScanCount(remaining int64) int64 {
	count := int64(defaultMCPUpstreamListScanCount)
	if r != nil && r.listScanBatchSize > 0 && r.listScanBatchSize < count {
		count = r.listScanBatchSize
	}
	if remaining > 0 && remaining < count {
		return remaining
	}
	return count
}

func (r *RedisMCPUpstreamRegistry) Get(ctx context.Context, tenantID, name string) (*UpstreamServer, bool, error) {
	if err := r.ensureReady(); err != nil {
		return nil, false, err
	}
	tenantID = strings.TrimSpace(tenantID)
	name = strings.TrimSpace(name)
	if tenantID == "" || name == "" {
		return nil, false, nil
	}
	if got, ok, err := r.load(ctx, tenantID, name); err != nil || ok || tenantID == "*" {
		return got, ok, err
	}
	return r.load(ctx, "*", name)
}

func (r *RedisMCPUpstreamRegistry) List(ctx context.Context, tenantID string) ([]UpstreamServer, error) {
	if err := r.ensureReady(); err != nil {
		return nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil
	}
	byName := make(map[string]UpstreamServer)
	for _, scope := range []string{"*", tenantID} {
		if err := r.listScope(ctx, scope, byName); err != nil {
			return nil, err
		}
	}
	out := make([]UpstreamServer, 0, len(byName))
	for _, record := range byName {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *RedisMCPUpstreamRegistry) Update(ctx context.Context, upstream *UpstreamServer) error {
	if err := r.ensureReady(); err != nil {
		return err
	}
	record, err := normalizeMCPUpstream(upstream, r.now().UTC())
	if err != nil {
		return err
	}
	if err := ValidateMCPUpstream(ctx, &record, "", nil); err != nil {
		return err
	}
	key := mcpUpstreamKey(record.TenantID, record.Name)
	return r.updateExisting(ctx, key, &record)
}

func (r *RedisMCPUpstreamRegistry) Disable(ctx context.Context, tenantID, name string) error {
	return r.setEnabled(ctx, tenantID, name, false)
}

func (r *RedisMCPUpstreamRegistry) Enable(ctx context.Context, tenantID, name string) error {
	return r.setEnabled(ctx, tenantID, name, true)
}

func (r *RedisMCPUpstreamRegistry) ensureReady() error {
	if r == nil || r.client == nil {
		return fmt.Errorf("mcp upstream registry unavailable")
	}
	if r.now == nil {
		return fmt.Errorf("mcp upstream registry unavailable: clock not configured")
	}
	return nil
}

func (r *RedisMCPUpstreamRegistry) updateExisting(ctx context.Context, key string, record *UpstreamServer) error {
	return redisutil.Retry(ctx, r.client, func(tx *redis.Tx) error {
		old, ok, err := r.loadFromTx(ctx, tx, record.TenantID, record.Name)
		if err != nil || !ok {
			if !ok {
				return ErrUpstreamNotFound
			}
			return err
		}
		record.CreatedAt = old.CreatedAt
		payload, err := marshalMCPUpstream(*record)
		if err != nil {
			return err
		}
		oldPayload, err := marshalMCPUpstream(*old)
		if err != nil {
			return err
		}
		backupKey, err := r.nextBackupKey(ctx, tx, record.TenantID, record.Name)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, backupKey, oldPayload, mcpUpstreamBackupTTL)
			pipe.HSet(ctx, key, "payload", payload)
			pipe.SAdd(ctx, mcpUpstreamTenantIndexKey(record.TenantID), record.Name)
			return nil
		})
		return err
	}, redisutil.WithKeys(key), redisutil.WithMaxAttempts(mcpUpstreamCASMaxAttempts))
}

func (r *RedisMCPUpstreamRegistry) nextBackupKey(ctx context.Context, tx *redis.Tx, tenantID, name string) (string, error) {
	base := r.now().UTC()
	for offset := 0; offset < 1024; offset++ {
		candidate := mcpUpstreamBackupKey(tenantID, name, base.Add(time.Duration(offset)))
		exists, err := tx.Exists(ctx, candidate).Result()
		if err != nil {
			return "", err
		}
		if exists == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("mcp upstream backup key collision")
}

func (r *RedisMCPUpstreamRegistry) setEnabled(ctx context.Context, tenantID, name string, enabled bool) error {
	got, ok, err := r.Get(ctx, tenantID, name)
	if err != nil || !ok {
		if !ok {
			return ErrUpstreamNotFound
		}
		return err
	}
	got.Enabled = enabled
	got.TenantID = strings.TrimSpace(tenantID)
	return r.Update(ctx, got)
}

func (r *RedisMCPUpstreamRegistry) listScope(ctx context.Context, scope string, out map[string]UpstreamServer) error {
	names, err := r.scanScopeNames(ctx, scope)
	if err != nil {
		return err
	}
	for _, name := range names {
		record, ok, err := r.load(ctx, scope, name)
		if err != nil {
			return err
		}
		if ok && record != nil {
			out[record.Name] = *record
		}
	}
	return nil
}

// scanScopeNames uses Redis SSCAN rather than SMEMBERS so a list request
// reads at most a bounded candidate set from each tenant/global scope. The
// public registry interface has no cursor yet, so callers keep the existing
// []UpstreamServer response while the store caps internal memory work.
func (r *RedisMCPUpstreamRegistry) scanScopeNames(ctx context.Context, scope string) ([]string, error) {
	limit := r.listScanLimit()
	key := mcpUpstreamTenantIndexKey(scope)
	names := make([]string, 0, int(limit))
	seen := make(map[string]struct{}, int(limit))
	var cursor uint64
	for int64(len(names)) < limit {
		count := r.listScanCount(limit - int64(len(names)))
		if r.listScanHook != nil {
			r.listScanHook(scope, count)
		}
		batch, next, err := r.client.SScan(ctx, key, cursor, "*", count).Result()
		if err != nil {
			return nil, err
		}
		for _, name := range batch {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
			if int64(len(names)) >= limit {
				break
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(names)
	return names, nil
}

func (r *RedisMCPUpstreamRegistry) load(ctx context.Context, tenantID, name string) (*UpstreamServer, bool, error) {
	payload, err := r.client.HGet(ctx, mcpUpstreamKey(tenantID, name), "payload").Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return unmarshalMCPUpstream(payload)
}

func (r *RedisMCPUpstreamRegistry) loadFromTx(ctx context.Context, tx *redis.Tx, tenantID, name string) (*UpstreamServer, bool, error) {
	payload, err := tx.HGet(ctx, mcpUpstreamKey(tenantID, name), "payload").Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return unmarshalMCPUpstream(payload)
}

func marshalMCPUpstream(record UpstreamServer) (string, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func unmarshalMCPUpstream(payload string) (*UpstreamServer, bool, error) {
	var record UpstreamServer
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		return nil, false, err
	}
	return &record, true, nil
}

func mcpUpstreamKey(tenantID, name string) string {
	return "edge:mcp:upstream:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(name)
}

func mcpUpstreamTenantIndexKey(tenantID string) string {
	return "edge:mcp:upstream:tenant:" + strings.TrimSpace(tenantID)
}

func mcpUpstreamBackupKey(tenantID, name string, ts time.Time) string {
	return fmt.Sprintf("edge:mcp:upstream:bak:%s:%s:%d", strings.TrimSpace(tenantID), strings.TrimSpace(name), ts.UnixNano())
}
