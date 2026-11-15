package configsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrCrossSlot is returned by SetAll when its documents' keys hash to
// different Redis Cluster slots, which one transaction cannot cover.
var ErrCrossSlot = errors.New("config documents must share one Redis Cluster hash slot")

// SetAll stores every document in one optimistic transaction, so all are
// written or none is. Each document is revision-checked as Set does, and a
// missing one is created. All keys must share one Redis Cluster slot
// (co-locate them with a {hash tag}); otherwise ErrCrossSlot is returned
// before any Redis call. On every failure each document keeps the Revision it
// was passed with; a concurrent change returns ErrRevisionConflict.
func (s *Service) SetAll(ctx context.Context, docs ...*Document) error {
	keys, err := setAllKeys(docs)
	if err != nil {
		return err
	}
	expected := make([]int64, len(docs))
	for i, doc := range docs {
		expected[i] = doc.Revision
	}
	txFn := func(tx *redis.Tx) error {
		if err := setAllCheckRevisions(ctx, tx, keys, expected); err != nil {
			return err
		}
		now := time.Now().UTC()
		payloads := make([][]byte, len(docs))
		for i, doc := range docs {
			doc.Revision = expected[i] + 1
			doc.Updated = now
			payload, err := json.Marshal(doc)
			if err != nil {
				return fmt.Errorf("marshal doc: %w", err)
			}
			payloads[i] = payload
		}
		_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			for i, key := range keys {
				pipe.Set(ctx, key, payloads[i], 0)
			}
			return nil
		})
		return err
	}
	if err := s.client.Watch(ctx, txFn, keys...); err != nil {
		for i, doc := range docs {
			doc.Revision = expected[i]
		}
		if errors.Is(err, redis.TxFailedErr) {
			slog.Warn("config set all: optimistic lock failed (key changed during tx)", "keys", keys)
			return ErrRevisionConflict
		}
		return err
	}
	return nil
}

// setAllKeys validates docs as Set does and returns their keys. It refuses an
// empty list, a repeated key and keys in different cluster slots.
func setAllKeys(docs []*Document) ([]string, error) {
	if len(docs) == 0 {
		return nil, errors.New("at least one config document required")
	}
	keys := make([]string, len(docs))
	seen := make(map[string]bool, len(docs))
	for i, doc := range docs {
		if doc == nil || doc.Scope == "" {
			return nil, fmt.Errorf("scope required")
		}
		if doc.Scope != ScopeSystem && doc.ScopeID == "" {
			return nil, fmt.Errorf("scope_id required for non-system scope")
		}
		keys[i] = cfgKey(doc.Scope, doc.ScopeID)
		if seen[keys[i]] {
			return nil, fmt.Errorf("config document %s given twice", keys[i])
		}
		seen[keys[i]] = true
		if clusterKeySlot(keys[i]) != clusterKeySlot(keys[0]) {
			return nil, fmt.Errorf("%w: %s and %s", ErrCrossSlot, keys[0], keys[i])
		}
	}
	return keys, nil
}

// setAllCheckRevisions reads every watched key and refuses a stored revision
// that differs from the one its document was passed with.
func setAllCheckRevisions(ctx context.Context, tx *redis.Tx, keys []string, expected []int64) error {
	for i, key := range keys {
		stored, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read config for lock: %w", err)
		}
		var existing Document
		if err := json.Unmarshal(stored, &existing); err != nil {
			return fmt.Errorf("unmarshal existing config: %w", err)
		}
		if existing.Revision != expected[i] {
			return ErrRevisionConflict
		}
	}
	return nil
}

// clusterKeySlot returns key's Redis Cluster hash slot: CRC16/XMODEM of the
// key, or of its hash tag when the bytes between the first '{' and the first
// '}' after it are not empty, modulo 16384.
func clusterKeySlot(key string) int {
	if start := strings.IndexByte(key, '{'); start >= 0 {
		if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
			key = key[start+1 : start+1+end]
		}
	}
	var crc uint16
	for i := 0; i < len(key); i++ {
		crc ^= uint16(key[i]) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return int(crc) & 16383
}
