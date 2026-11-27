package redisutil

import (
	"context"
	"errors"
	"sync"

	"github.com/redis/go-redis/v9"
)

// ErrStopScan lets a ScanKeys callback stop all masters without an error.
var ErrStopScan = errors.New("redisutil: stop scan")

// ScanKeys scans every master of a ClusterClient, or one cursor otherwise.
// Callbacks are serialized and receive only nonempty batches of unseen keys.
// Callback and SCAN errors are returned unchanged, except ErrStopScan from fn.
func ScanKeys(ctx context.Context, client redis.UniversalClient, match string, count int64, fn func(keys []string) error) error {
	s := keyScan{seen: make(map[string]struct{})}
	if cluster, ok := client.(*redis.ClusterClient); ok {
		if err := cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
			s.scan(ctx, node, match, count, fn)
			return nil
		}); err != nil {
			return err
		}
	} else {
		s.scan(ctx, client, match, count, fn)
	}
	return s.err
}

type keyScan struct {
	mu      sync.Mutex
	seen    map[string]struct{}
	stopped bool
	err     error
}

func (s *keyScan) scan(ctx context.Context, client redis.Cmdable, match string, count int64, fn func([]string) error) {
	var cursor uint64
	for s.visit(ctx, nil, nil, fn) {
		keys, next, err := client.Scan(ctx, cursor, match, count).Result()
		if !s.visit(ctx, keys, err, fn) || next == 0 {
			return
		}
		cursor = next
	}
}

// visit owns all shared state, including the callback, but never a Redis
// connection: a callback may issue commands even on a single-connection pool.
func (s *keyScan) visit(ctx context.Context, keys []string, err error, fn func([]string) error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		s.stopped, s.err = true, err
		return false
	}
	unique := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := s.seen[key]; !ok {
			s.seen[key] = struct{}{}
			unique = append(unique, key)
		}
	}
	if len(unique) > 0 {
		if err := fn(unique); err != nil {
			s.stopped = true
			if !errors.Is(err, ErrStopScan) {
				s.err = err
			}
			return false
		}
	}
	return true
}
