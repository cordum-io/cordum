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

-- Phase 1: pre-walk the cascade graph depth-first to determine the
-- full set of descendants to revoke and verify none exceeds maxDepth.
-- This is a separate pass from the mutation loop so a depth violation
-- fails CLOSED — no partial revocations — instead of revoking the
-- first maxDepth levels and then erroring out mid-walk.
local toRevoke = {root}
local seenPre = {[root] = true}

if cascade then
  local preQueue = {root}
  local preDepth = {0}
  local preHead = 1
  while preHead <= #preQueue do
    local node = preQueue[preHead]
    local depth = preDepth[preHead]
    preHead = preHead + 1
    if depth >= maxDepth then
      local children = redis.call("SMEMBERS", childrenPrefix .. node)
      if #children > 0 then
        return redis.error_reply("cascade depth exceeded")
      end
    else
      local children = redis.call("SMEMBERS", childrenPrefix .. node)
      table.sort(children)
      for _, child in ipairs(children) do
        if not seenPre[child] then
          seenPre[child] = true
          table.insert(toRevoke, child)
          table.insert(preQueue, child)
          table.insert(preDepth, depth + 1)
        end
      end
    end
  end
end

-- Phase 2: the cascade fits under maxDepth — revoke every node.
local revoked = {}
for _, current in ipairs(toRevoke) do
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
