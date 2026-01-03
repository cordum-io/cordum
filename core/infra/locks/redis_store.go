package locks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisURL = "redis://localhost:6379"
	defaultTTL      = 30 * time.Second
)

type RedisStore struct {
	client *redis.Client
}

// NewRedisStore constructs a Redis-backed lock store.
func NewRedisStore(url string) (*RedisStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &RedisStore{client: client}, nil
}

// Close shuts down the Redis client.
func (s *RedisStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Acquire attempts to acquire a shared or exclusive lock.
func (s *RedisStore) Acquire(ctx context.Context, resource, owner string, mode Mode, ttl time.Duration) (*Lock, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, fmt.Errorf("lock store unavailable")
	}
	resource = strings.TrimSpace(resource)
	owner = strings.TrimSpace(owner)
	if resource == "" || owner == "" {
		return nil, false, fmt.Errorf("resource and owner required")
	}
	mode = normalizeMode(mode)
	ttl = normalizeTTL(ttl)

	now := time.Now().UTC()
	res, err := s.client.Eval(ctx, acquireScript, []string{lockKey(resource)},
		string(mode),
		owner,
		ttl.Milliseconds(),
		now.Unix(),
	).Result()
	if err != nil {
		return nil, false, err
	}
	payload, ok := res.(string)
	if !ok || payload == "" {
		return nil, false, nil
	}
	lock, err := parseLock(payload, resource)
	if err != nil {
		return nil, false, err
	}
	return lock, true, nil
}

// Release removes the caller from a lock.
func (s *RedisStore) Release(ctx context.Context, resource, owner string) (*Lock, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, fmt.Errorf("lock store unavailable")
	}
	resource = strings.TrimSpace(resource)
	owner = strings.TrimSpace(owner)
	if resource == "" || owner == "" {
		return nil, false, fmt.Errorf("resource and owner required")
	}
	now := time.Now().UTC()
	res, err := s.client.Eval(ctx, releaseScript, []string{lockKey(resource)},
		owner,
		now.Unix(),
	).Result()
	if err != nil {
		return nil, false, err
	}
	payload, _ := res.(string)
	if payload == "" {
		return nil, true, nil
	}
	lock, err := parseLock(payload, resource)
	if err != nil {
		return nil, false, err
	}
	return lock, true, nil
}

// Renew extends a lock TTL if the owner is present.
func (s *RedisStore) Renew(ctx context.Context, resource, owner string, ttl time.Duration) (*Lock, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, fmt.Errorf("lock store unavailable")
	}
	resource = strings.TrimSpace(resource)
	owner = strings.TrimSpace(owner)
	if resource == "" || owner == "" {
		return nil, false, fmt.Errorf("resource and owner required")
	}
	ttl = normalizeTTL(ttl)
	now := time.Now().UTC()
	res, err := s.client.Eval(ctx, renewScript, []string{lockKey(resource)},
		owner,
		ttl.Milliseconds(),
		now.Unix(),
	).Result()
	if err != nil {
		return nil, false, err
	}
	payload, _ := res.(string)
	if payload == "" {
		return nil, false, nil
	}
	lock, err := parseLock(payload, resource)
	if err != nil {
		return nil, false, err
	}
	return lock, true, nil
}

// Get returns the current lock state.
func (s *RedisStore) Get(ctx context.Context, resource string) (*Lock, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("lock store unavailable")
	}
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return nil, fmt.Errorf("resource required")
	}
	payload, err := s.client.Get(ctx, lockKey(resource)).Result()
	if err != nil {
		return nil, err
	}
	return parseLock(payload, resource)
}

func normalizeTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTTL
	}
	return ttl
}

func normalizeMode(mode Mode) Mode {
	switch mode {
	case ModeShared:
		return ModeShared
	case ModeExclusive:
		return ModeExclusive
	default:
		return ModeExclusive
	}
}

type lockPayload struct {
	Mode      string         `json:"mode"`
	Owners    map[string]int `json:"owners"`
	UpdatedAt int64          `json:"updated_at"`
	ExpiresAt int64          `json:"expires_at"`
}

func parseLock(payload, resource string) (*Lock, error) {
	var decoded lockPayload
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, fmt.Errorf("decode lock: %w", err)
	}
	lock := &Lock{
		Resource: resource,
		Mode:     Mode(decoded.Mode),
		Owners:   decoded.Owners,
	}
	if decoded.UpdatedAt > 0 {
		lock.UpdatedAt = time.Unix(decoded.UpdatedAt, 0).UTC()
	}
	if decoded.ExpiresAt > 0 {
		lock.ExpiresAt = time.Unix(decoded.ExpiresAt, 0).UTC()
	}
	return lock, nil
}

func lockKey(resource string) string {
	return "lock:" + resource
}

const acquireScript = `
local key = KEYS[1]
local mode = ARGV[1]
local owner = ARGV[2]
local ttl = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local payload = redis.call("GET", key)
if not payload then
  local lock = {mode = mode, owners = {[owner] = 1}, updated_at = now, expires_at = now + math.floor(ttl/1000)}
  local encoded = cjson.encode(lock)
  redis.call("SET", key, encoded, "PX", ttl)
  return encoded
end
local lock = cjson.decode(payload)
local owners = lock["owners"] or {}
local currentMode = lock["mode"] or "exclusive"
if currentMode == "exclusive" then
  if owners[owner] then
    owners[owner] = owners[owner] + 1
    lock["owners"] = owners
    lock["updated_at"] = now
    lock["expires_at"] = now + math.floor(ttl/1000)
    local encoded = cjson.encode(lock)
    redis.call("SET", key, encoded, "PX", ttl)
    return encoded
  end
  return ""
end
if mode == "shared" then
  owners[owner] = (owners[owner] or 0) + 1
  lock["owners"] = owners
  lock["updated_at"] = now
  lock["expires_at"] = now + math.floor(ttl/1000)
  local encoded = cjson.encode(lock)
  redis.call("SET", key, encoded, "PX", ttl)
  return encoded
end
local count = 0
local onlyOwner = true
for k, _ in pairs(owners) do
  count = count + 1
  if k ~= owner then
    onlyOwner = false
  end
end
if onlyOwner and count > 0 then
  lock["mode"] = "exclusive"
  owners[owner] = (owners[owner] or 0) + 1
  lock["owners"] = owners
  lock["updated_at"] = now
  lock["expires_at"] = now + math.floor(ttl/1000)
  local encoded = cjson.encode(lock)
  redis.call("SET", key, encoded, "PX", ttl)
  return encoded
end
return ""
`

const releaseScript = `
local key = KEYS[1]
local owner = ARGV[1]
local now = tonumber(ARGV[2])
local payload = redis.call("GET", key)
if not payload then
  return ""
end
local lock = cjson.decode(payload)
local owners = lock["owners"] or {}
local count = owners[owner]
if not count then
  return payload
end
if count <= 1 then
  owners[owner] = nil
else
  owners[owner] = count - 1
end
if next(owners) == nil then
  redis.call("DEL", key)
  return ""
end
lock["owners"] = owners
lock["updated_at"] = now
local ttl = redis.call("PTTL", key)
if ttl > 0 then
  local encoded = cjson.encode(lock)
  redis.call("SET", key, encoded, "PX", ttl)
  return encoded
end
local encoded = cjson.encode(lock)
redis.call("SET", key, encoded)
return encoded
`

const renewScript = `
local key = KEYS[1]
local owner = ARGV[1]
local ttl = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local payload = redis.call("GET", key)
if not payload then
  return ""
end
local lock = cjson.decode(payload)
local owners = lock["owners"] or {}
if not owners[owner] then
  return ""
end
lock["updated_at"] = now
lock["expires_at"] = now + math.floor(ttl/1000)
local encoded = cjson.encode(lock)
redis.call("SET", key, encoded, "PX", ttl)
return encoded
`
