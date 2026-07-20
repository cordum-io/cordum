//go:build handshakeinterop

package handshakeinterop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
)

const workerConfigRedisKey = "cfg:system:workers"

var errWorkerConfigChanged = errors.New("worker config changed during interop run")

func (s *interopServer) captureWorkerConfigState() {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := s.redis.Get(ctx, workerConfigRedisKey).Bytes()
	if errors.Is(err, redis.Nil) {
		s.workerConfigCaptured = true
		return
	}
	if err != nil {
		s.t.Fatalf("snapshot worker config: %v", err)
	}
	ttl, err := s.redis.PTTL(ctx, workerConfigRedisKey).Result()
	if err != nil {
		s.t.Fatalf("snapshot worker config TTL: %v", err)
	}
	s.workerConfig = append([]byte(nil), value...)
	s.workerConfigTTL = ttl
	s.workerConfigExists = true
	s.workerConfigCaptured = true
}

func (s *interopServer) captureOwnedWorkerConfigState() {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := s.redis.Get(ctx, workerConfigRedisKey).Bytes()
	if err != nil {
		s.t.Fatalf("snapshot run-owned worker config: %v", err)
	}
	s.ownedWorkerConfig = append([]byte(nil), value...)
	s.ownedWorkerConfigCaptured = true
}

func (s *interopServer) restoreWorkerConfigState(ctx context.Context) {
	if s == nil || s.redis == nil || !s.workerConfigCaptured {
		return
	}
	restored, err := s.tryRestoreExactWorkerConfig(ctx)
	if err != nil {
		s.t.Errorf("restore exact worker config: %v", err)
		return
	}
	if restored {
		return
	}
	if err := s.removeOwnedWorkerConfig(ctx); err != nil {
		s.t.Errorf("remove owned worker config: %v", err)
	}
}

func (s *interopServer) tryRestoreExactWorkerConfig(ctx context.Context) (bool, error) {
	if !s.ownedWorkerConfigCaptured {
		return false, nil
	}
	err := s.redis.Watch(ctx, func(tx *redis.Tx) error {
		current, getErr := tx.Get(ctx, workerConfigRedisKey).Bytes()
		if errors.Is(getErr, redis.Nil) {
			return errWorkerConfigChanged
		}
		if getErr != nil {
			return getErr
		}
		if !bytes.Equal(current, s.ownedWorkerConfig) {
			return errWorkerConfigChanged
		}
		_, txErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			if !s.workerConfigExists {
				pipe.Del(ctx, workerConfigRedisKey)
				return nil
			}
			ttl := s.workerConfigTTL
			if ttl < 0 {
				ttl = 0
			}
			pipe.Set(ctx, workerConfigRedisKey, s.workerConfig, ttl)
			return nil
		})
		return txErr
	}, workerConfigRedisKey)
	if errors.Is(err, errWorkerConfigChanged) {
		return false, nil
	}
	return err == nil, err
}

func (s *interopServer) removeOwnedWorkerConfig(ctx context.Context) error {
	service := s.config
	if service == nil {
		service = configsvc.NewFromClient(s.redis)
	}
	err := service.SetWithRetry(ctx, configsvc.ScopeSystem, "workers", 5, func(doc *configsvc.Document) error {
		for _, identity := range s.identities {
			delete(doc.Data, identity.workerID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !s.workerConfigExists {
		return s.deleteEmptyWorkerConfig(ctx)
	}
	return nil
}

func (s *interopServer) deleteEmptyWorkerConfig(ctx context.Context) error {
	return s.redis.Watch(ctx, func(tx *redis.Tx) error {
		value, err := tx.Get(ctx, workerConfigRedisKey).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		doc := &configsvc.Document{}
		if err := json.Unmarshal(value, doc); err != nil || len(doc.Data) != 0 {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, workerConfigRedisKey)
			return nil
		})
		return err
	}, workerConfigRedisKey)
}
