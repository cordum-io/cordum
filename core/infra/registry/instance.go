package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// instanceKeyPrefix is the Redis key prefix for instance registration.
	instanceKeyPrefix = "cordum:instance:"

	// defaultInstanceTTL is the TTL for instance registration keys.
	defaultInstanceTTL = 15 * time.Second
)

// InstanceInfo describes a registered service replica.
type InstanceInfo struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	StartedAt string `json:"started_at"`

	// TTLRemaining is populated by List operations, not stored in Redis.
	TTLRemaining time.Duration `json:"ttl_remaining_ms,omitempty"`
}

// InstanceRegistry manages service replica registration in Redis.
// Each replica writes a key with a TTL; a heartbeat goroutine renews it.
type InstanceRegistry struct {
	rdb     redis.UniversalClient
	service string
	id      string
	ttl     time.Duration
	info    InstanceInfo

	cancel context.CancelFunc
	done   chan struct{}
}

// NewInstanceRegistry creates an instance registry for a service replica.
func NewInstanceRegistry(rdb redis.UniversalClient, service, id, version, commit string) *InstanceRegistry {
	return &InstanceRegistry{
		rdb:     rdb,
		service: service,
		id:      id,
		ttl:     defaultInstanceTTL,
		info: InstanceInfo{
			ID:        id,
			Service:   service,
			Version:   version,
			Commit:    commit,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		},
		done: make(chan struct{}),
	}
}

// Start registers the instance in Redis and starts a heartbeat goroutine
// that renews the key every TTL/2. Safe to call with a nil Redis client
// (becomes a no-op with a warning log).
func (r *InstanceRegistry) Start(ctx context.Context) {
	if r.rdb == nil {
		slog.Warn("instance registry: redis unavailable, registration disabled", "service", r.service, "id", r.id)
		close(r.done)
		return
	}

	if err := r.register(ctx); err != nil {
		slog.Warn("instance registry: initial registration failed", "service", r.service, "id", r.id, "error", err)
	}

	hbCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go r.heartbeat(hbCtx)
}

// Stop deregisters the instance from Redis (best-effort) and stops the
// heartbeat goroutine. Blocks until the goroutine exits.
func (r *InstanceRegistry) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	<-r.done

	if r.rdb == nil {
		return
	}
	// Best-effort delete — if it fails, the TTL will expire.
	delCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.rdb.Del(delCtx, r.key()).Err(); err != nil {
		slog.Debug("instance registry: deregister failed, key will expire via TTL", "key", r.key(), "error", err)
	}
}

// ListInstances returns all registered instances for a service.
func ListInstances(ctx context.Context, rdb redis.UniversalClient, service string) ([]InstanceInfo, error) {
	if rdb == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	pattern := instanceKeyPrefix + service + ":*"
	return scanInstances(ctx, rdb, pattern)
}

// ListAllInstances returns all registered instances grouped by service name.
func ListAllInstances(ctx context.Context, rdb redis.UniversalClient) (map[string][]InstanceInfo, error) {
	if rdb == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	pattern := instanceKeyPrefix + "*"
	instances, err := scanInstances(ctx, rdb, pattern)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]InstanceInfo, 8)
	for _, inst := range instances {
		grouped[inst.Service] = append(grouped[inst.Service], inst)
	}
	return grouped, nil
}

func scanInstances(ctx context.Context, rdb redis.UniversalClient, pattern string) ([]InstanceInfo, error) {
	var keys []string
	iter := rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan instance keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	pipe := rdb.Pipeline()
	getCmds := make([]*redis.StringCmd, len(keys))
	ttlCmds := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		getCmds[i] = pipe.Get(ctx, key)
		ttlCmds[i] = pipe.PTTL(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("pipeline instance reads: %w", err)
	}

	var instances []InstanceInfo
	for i, cmd := range getCmds {
		data, err := cmd.Bytes()
		if err != nil {
			continue // Key expired between SCAN and GET.
		}
		var info InstanceInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue // Corrupt entry — skip.
		}
		if ttl, err := ttlCmds[i].Result(); err == nil && ttl > 0 {
			info.TTLRemaining = ttl
		}
		instances = append(instances, info)
	}
	return instances, nil
}

func (r *InstanceRegistry) register(ctx context.Context) error {
	data, err := json.Marshal(r.info)
	if err != nil {
		return fmt.Errorf("marshal instance info: %w", err)
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.rdb.Set(opCtx, r.key(), data, r.ttl).Err()
}

func (r *InstanceRegistry) heartbeat(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.ttl / 2) // Renew at TTL/2 cadence.
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.register(ctx); err != nil {
				slog.Warn("instance registry: heartbeat renewal failed", "key", r.key(), "error", err)
			}
		}
	}
}

func (r *InstanceRegistry) key() string {
	return instanceKeyPrefix + r.service + ":" + r.id
}

// ResolveInstanceID returns a stable instance identifier.
// Precedence: CORDUM_INSTANCE_ID env → os.Hostname() → "unknown".
func ResolveInstanceID() string {
	if id := strings.TrimSpace(os.Getenv("CORDUM_INSTANCE_ID")); id != "" {
		return id
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}
