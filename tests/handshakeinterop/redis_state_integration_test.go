//go:build handshakeinterop

package handshakeinterop

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const handshakeRedisPrefix = "session:handshake:"

type redisState map[string][]byte

func (s *interopServer) ownedHandshakeState(ctx context.Context) (redisState, error) {
	if s == nil || s.redis == nil {
		return redisState{}, nil
	}
	state := redisState{}
	for _, pattern := range []string{handshakeRedisPrefix + "*", "session:worker:*", "session:revoked:*"} {
		if err := s.collectOwnedState(ctx, pattern, state); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (s *interopServer) ownedSessionState(ctx context.Context) (redisState, error) {
	all, err := s.ownedHandshakeState(ctx)
	if err != nil {
		return nil, err
	}
	sessions := redisState{}
	for key, value := range all {
		if strings.HasPrefix(key, "session:worker:") || strings.HasPrefix(key, "session:revoked:") {
			sessions[key] = append([]byte(nil), value...)
		}
	}
	return sessions, nil
}

func (s *interopServer) collectOwnedState(ctx context.Context, pattern string, state redisState) error {
	return scanRedisKeys(ctx, s.redis, pattern, func(key string) error {
		value, err := s.redis.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		if s.ownsHandshakeKey(key, value) {
			state[key] = append([]byte(nil), value...)
		}
		return nil
	})
}

func scanRedisKeys(ctx context.Context, client redis.UniversalClient, pattern string, visit func(string) error) error {
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := visit(key); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func (s *interopServer) ownsHandshakeKey(key string, value []byte) bool {
	for _, identity := range s.identities {
		if key == activeSessionKey(identity) || key == "session:worker:"+identity.workerID {
			return true
		}
		if strings.HasPrefix(key, revokedSessionPrefix(identity)) {
			return true
		}
		if strings.HasPrefix(key, handshakeStatePrefix("request", identity.workerID)) ||
			strings.HasPrefix(key, handshakeStatePrefix("nonce", identity.workerID)) {
			return true
		}
	}
	return s.ownsChallengeValue(key, value)
}

func (s *interopServer) ownsChallengeValue(key string, value []byte) bool {
	if !strings.HasPrefix(key, handshakeRedisPrefix+"challenge:") {
		return false
	}
	challenge := &agentv1.WorkerHandshakeChallenge{}
	if proto.Unmarshal(value, challenge) != nil {
		return false
	}
	for _, identity := range s.identities {
		if challenge.GetWorkerId() == identity.workerID && challenge.GetTenantId() == identity.tenantID {
			return true
		}
	}
	return false
}

func handshakeStatePrefix(kind, workerID string) string {
	return handshakeRedisPrefix + kind + ":" + encodeHandshakeStatePart([]byte(workerID)) + ":"
}

func encodeHandshakeStatePart(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func (s *interopServer) cleanupHandshakeState(ctx context.Context) {
	state, err := s.ownedHandshakeState(ctx)
	if err != nil {
		s.t.Errorf("collect owned handshake state: %v", err)
		return
	}
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	if len(keys) > 0 {
		if err := s.redis.Del(ctx, keys...).Err(); err != nil {
			s.t.Errorf("delete owned handshake state: %v", err)
		}
	}
}

func (s *interopServer) assertNoOwnedHandshakeState(ctx context.Context) {
	state, err := s.ownedHandshakeState(ctx)
	if err != nil {
		s.t.Errorf("verify owned handshake cleanup: %v", err)
	} else if len(state) != 0 {
		s.t.Errorf("owned handshake cleanup left %d Redis keys", len(state))
	}
}

func equalRedisState(left, right redisState) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !bytes.Equal(value, right[key]) {
			return false
		}
	}
	return true
}

func describeRedisStateChange(before, after redisState) string {
	return fmt.Sprintf("before_keys=%d after_keys=%d", len(before), len(after))
}
