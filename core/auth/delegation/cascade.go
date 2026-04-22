package delegation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	delegationCascadeDepthLimit = 256
	delegationRevocationTTL     = 24 * time.Hour
)

type CascadeRevocationResult struct {
	RootJTI       string
	RevokedJTIs   []string
	CascadedCount int
}

var cascadeRevocationScript = redis.NewScript(`
local root = ARGV[1]
local revokedAt = ARGV[2]
local reason = ARGV[3]
local ttlSeconds = tonumber(ARGV[4])
local maxDepth = tonumber(ARGV[5])
local cascade = ARGV[6] == "1"

local tokenPrefix = KEYS[1]
local childrenPrefix = KEYS[2]
local revokedPrefix = KEYS[3]
local activePrefix = KEYS[4]

if redis.call("EXISTS", tokenPrefix .. root) == 0 then
  return {}
end

local queue = {root}
local depthQueue = {0}
local head = 1
local seen = {}
local revoked = {}

while head <= #queue do
  local current = queue[head]
  local depth = depthQueue[head]
  head = head + 1

  if not seen[current] then
    seen[current] = true
    local tokenKey = tokenPrefix .. current
    local tenant = redis.call("HGET", tokenKey, "tenant")

    redis.call("SET", revokedPrefix .. current, "1", "EX", ttlSeconds)
    redis.call("HSET", tokenKey,
      "revoked", "1",
      "revoked_at", revokedAt,
      "revoked_reason", reason
    )
    if tenant and tenant ~= "" then
      redis.call("ZREM", activePrefix .. tenant, current)
    end
    table.insert(revoked, current)

    if cascade then
      local children = redis.call("SMEMBERS", childrenPrefix .. current)
      table.sort(children)
      if depth >= maxDepth and #children > 0 then
        return redis.error_reply("cascade depth exceeded")
      end
      for _, child in ipairs(children) do
        if not seen[child] then
          table.insert(queue, child)
          table.insert(depthQueue, depth + 1)
        end
      end
    end
  end
end

return revoked
`)

func (s *RedisRevocationStore) CascadeRevoke(ctx context.Context, rootJTI, reason string, revokedAt time.Time, cascade bool) (CascadeRevocationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return CascadeRevocationResult{}, fmt.Errorf("delegation revocation store unavailable")
	}
	rootJTI = strings.TrimSpace(rootJTI)
	if rootJTI == "" {
		return CascadeRevocationResult{}, fmt.Errorf("delegation jti required")
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now().UTC()
	}
	result, err := cascadeRevocationScript.Eval(ctx, s.client, []string{
		delegationTokenKeyPrefix,
		delegationChildrenKeyPrefix,
		delegationRevocationPrefix,
		delegationActiveKeyPrefix,
	}, rootJTI, revokedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(reason), int(delegationRevocationTTL/time.Second), delegationCascadeDepthLimit, boolToLua(cascade)).Result()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cascade depth exceeded") {
			return CascadeRevocationResult{}, ErrCascadeTooDeep
		}
		return CascadeRevocationResult{}, fmt.Errorf("cascade revoke delegation token: %w", err)
	}
	revokedJTIs, err := cascadeRevocationJTIs(result)
	if err != nil {
		return CascadeRevocationResult{}, err
	}
	if len(revokedJTIs) == 0 {
		return CascadeRevocationResult{}, ErrNotFound
	}
	return CascadeRevocationResult{
		RootJTI:       rootJTI,
		RevokedJTIs:   revokedJTIs,
		CascadedCount: max(0, len(revokedJTIs)-1),
	}, nil
}

func cascadeRevocationJTIs(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			switch value := item.(type) {
			case string:
				if trim := strings.TrimSpace(value); trim != "" {
					out = append(out, trim)
				}
			case []byte:
				if trim := strings.TrimSpace(string(value)); trim != "" {
					out = append(out, trim)
				}
			default:
				return nil, fmt.Errorf("unexpected cascade revoke result type %T", item)
			}
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trim := strings.TrimSpace(item); trim != "" {
				out = append(out, trim)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected cascade revoke result type %T", value)
	}
}

func boolToLua(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func (s *RedisRevocationStore) RecordChildDelegation(ctx context.Context, parentJTI, childJTI string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("delegation revocation store unavailable")
	}
	parentJTI = strings.TrimSpace(parentJTI)
	childJTI = strings.TrimSpace(childJTI)
	if parentJTI == "" || childJTI == "" {
		return nil
	}
	return s.client.SAdd(ctx, delegationChildrenKey(parentJTI), childJTI).Err()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
