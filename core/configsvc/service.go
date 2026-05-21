package configsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

// ErrRevisionConflict is returned when a concurrent writer modified the
// document between Get and Set. Callers should re-read and retry.
var ErrRevisionConflict = errors.New("config revision conflict: document was modified by another writer")

// Scope levels for configuration inheritance.
type Scope string

const (
	ScopeSystem   Scope = "system"
	ScopeOrg      Scope = "org"
	ScopeTeam     Scope = "team"
	ScopeWorkflow Scope = "workflow"
	ScopeStep     Scope = "step"
)

// Document is a config fragment at a given scope.
type Document struct {
	Scope    Scope             `json:"scope"`
	ScopeID  string            `json:"scope_id"` // system may use "default"
	Data     map[string]any    `json:"data"`
	Revision int64             `json:"revision"`
	Updated  time.Time         `json:"updated_at"`
	Meta     map[string]string `json:"meta,omitempty"`
}

// Service persists config documents and resolves effective config with simple override semantics.
type Service struct {
	client redis.UniversalClient
}

// EffectiveSnapshot includes the merged config plus version/hash metadata.
type EffectiveSnapshot struct {
	Version string         `json:"version"`
	Hash    string         `json:"hash"`
	Data    map[string]any `json:"data"`
}

// New creates a config service backed by Redis.
func New(url string) (*Service, error) {
	if url == "" {
		url = "redis://localhost:6379"
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &Service{client: client}, nil
}

// NewFromClient constructs a config service from a shared Redis client.
func NewFromClient(client redis.UniversalClient) *Service {
	return &Service{client: client}
}

func (s *Service) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Set stores a config document with optimistic locking. If another writer
// modified the document since it was read, ErrRevisionConflict is returned.
// The caller should re-read with Get and retry.
func (s *Service) Set(ctx context.Context, doc *Document) error {
	if doc == nil || doc.Scope == "" {
		return fmt.Errorf("scope required")
	}
	if doc.Scope != ScopeSystem && doc.ScopeID == "" {
		return fmt.Errorf("scope_id required for non-system scope")
	}
	key := cfgKey(doc.Scope, doc.ScopeID)
	expectedRevision := doc.Revision

	txFn := func(tx *redis.Tx) error {
		// Read current revision under WATCH
		stored, err := tx.Get(ctx, key).Bytes()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("read config for lock: %w", err)
		}
		if stored != nil {
			var existing Document
			if err := json.Unmarshal(stored, &existing); err != nil {
				return fmt.Errorf("unmarshal existing config: %w", err)
			}
			if existing.Revision != expectedRevision {
				return ErrRevisionConflict
			}
		}

		// Prepare new document — increment revision only inside transaction
		newRevision := expectedRevision + 1
		doc.Revision = newRevision
		doc.Updated = time.Now().UTC()
		payload, err := json.Marshal(doc)
		if err != nil {
			doc.Revision = expectedRevision // roll back on marshal error
			return fmt.Errorf("marshal doc: %w", err)
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, 0)
			return nil
		})
		if err != nil {
			doc.Revision = expectedRevision // roll back revision on tx failure
			return err
		}
		return nil
	}

	err := s.client.Watch(ctx, txFn, key)
	if err != nil {
		if errors.Is(err, redis.TxFailedErr) {
			doc.Revision = expectedRevision // roll back revision
			slog.Warn("config set: optimistic lock failed (key changed during tx)",
				"scope", doc.Scope, "scope_id", doc.ScopeID)
			return ErrRevisionConflict
		}
		if errors.Is(err, ErrRevisionConflict) {
			doc.Revision = expectedRevision // roll back revision
			return err
		}
		doc.Revision = expectedRevision // roll back on any failure
		return err
	}
	return nil
}

// Get fetches a config document at a given scope/id.
func (s *Service) Get(ctx context.Context, scope Scope, id string) (*Document, error) {
	if scope == "" {
		return nil, fmt.Errorf("scope required")
	}
	data, err := s.client.Get(ctx, cfgKey(scope, id)).Bytes()
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal doc: %w", err)
	}
	return &doc, nil
}

// Effective merges configs in order: system -> org -> team -> workflow -> step.
// Later scopes override earlier keys shallowly.
func (s *Service) Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error) {
	snap, err := s.EffectiveSnapshot(ctx, orgID, teamID, workflowID, stepID)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return map[string]any{}, nil
	}
	return snap.Data, nil
}

// EffectiveSnapshot merges configs in order and returns the merged config plus version/hash metadata.
func (s *Service) EffectiveSnapshot(ctx context.Context, orgID, teamID, workflowID, stepID string) (*EffectiveSnapshot, error) {
	order := []struct {
		scope Scope
		id    string
	}{
		{ScopeSystem, "default"},
		{ScopeOrg, orgID},
		{ScopeTeam, teamID},
		{ScopeWorkflow, workflowID},
		{ScopeStep, stepID},
	}
	result := make(map[string]any)
	revisions := make(map[Scope]int64, len(order))
	for _, item := range order {
		if item.scope != ScopeSystem && item.id == "" {
			continue
		}
		doc, err := s.Get(ctx, item.scope, item.id)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue // key does not exist — skip this scope
			}
			return nil, fmt.Errorf("config %s/%s: %w", item.scope, item.id, err)
		}
		revisions[item.scope] = doc.Revision
		mergeDeep(result, doc.Data)
	}
	version := snapshotVersion(revisions)
	hash, err := snapshotHash(result)
	if err != nil {
		return nil, fmt.Errorf("snapshot hash: %w", err)
	}
	return &EffectiveSnapshot{
		Version: version,
		Hash:    hash,
		Data:    result,
	}, nil
}

// mergeDeep recursively merges src into dst. Nested maps are merged
// recursively; non-map values use last-write-wins semantics.
func mergeDeep(dst, src map[string]any) {
	for k, srcVal := range src {
		if srcMap, ok := srcVal.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				mergeDeep(dstMap, srcMap)
				continue
			}
		}
		dst[k] = srcVal
	}
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = cloneValue(child)
		}
		return out
	default:
		return v
	}
}

func shouldPreserveSystemDefaultKeys(scope Scope, scopeID string) bool {
	return scope == ScopeSystem && (scopeID == "" || scopeID == "default")
}

func mergePreservingKeys(original, updated map[string]any) map[string]any {
	if original == nil {
		return cloneMap(updated)
	}
	merged := cloneMap(original)
	if merged == nil {
		merged = map[string]any{}
	}
	mergeDeep(merged, updated)
	return merged
}

// EnsureDefault creates the system/default config document with minimal sensible
// defaults if one does not already exist. This is idempotent — repeated calls are
// no-ops when the document exists. Called during gateway/scheduler startup to
// guarantee a usable config on fresh installs.
func (s *Service) EnsureDefault(ctx context.Context) error {
	_, err := s.Get(ctx, ScopeSystem, "default")
	if err == nil {
		return nil // already exists
	}
	if !errors.Is(err, redis.Nil) {
		return fmt.Errorf("check default config: %w", err)
	}
	return s.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"safety":      map[string]any{"enabled": true, "mode": "enforce"},
			"rate_limits": map[string]any{"enabled": true},
		},
		Meta: map[string]string{"source": "auto-bootstrap"},
	})
}

// SetWithRetry wraps Set with automatic retry on ErrRevisionConflict.
// On conflict, it re-reads the document, calls applyFn to re-apply the
// caller's changes to the fresh document, and retries up to maxAttempts times.
func (s *Service) SetWithRetry(ctx context.Context, scope Scope, scopeID string, maxAttempts int, applyFn func(doc *Document) error) error {
	for attempt := range maxAttempts {
		doc, err := s.Get(ctx, scope, scopeID)
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				return fmt.Errorf("config get for retry: %w", err)
			}
			doc = &Document{
				Scope:   scope,
				ScopeID: scopeID,
				Data:    map[string]any{},
			}
		}
		originalData := cloneMap(doc.Data)
		if err := applyFn(doc); err != nil {
			return fmt.Errorf("apply config changes: %w", err)
		}
		if shouldPreserveSystemDefaultKeys(scope, scopeID) {
			// Protect the shared system/default document from partial overwrite
			// patterns where a caller replaces doc.Data with a subset map.
			doc.Data = mergePreservingKeys(originalData, doc.Data)
		}
		if err := s.Set(ctx, doc); err != nil {
			if errors.Is(err, ErrRevisionConflict) && attempt < maxAttempts-1 {
				slog.Warn("config set conflict, retrying",
					"scope", scope, "scope_id", scopeID, "attempt", attempt+1)
				continue
			}
			return err
		}
		return nil
	}
	return ErrRevisionConflict
}

func cfgKey(scope Scope, id string) string {
	if scope == ScopeSystem {
		if id == "" {
			id = "default"
		}
	}
	return fmt.Sprintf("cfg:%s:%s", scope, id)
}
