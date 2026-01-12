package configsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

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

func (s *Service) Close() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

// Set stores/overwrites a config document.
func (s *Service) Set(ctx context.Context, doc *Document) error {
	if doc == nil || doc.Scope == "" {
		return fmt.Errorf("scope required")
	}
	if doc.Scope != ScopeSystem && doc.ScopeID == "" {
		return fmt.Errorf("scope_id required for non-system scope")
	}
	doc.Revision++
	doc.Updated = time.Now().UTC()
	payload, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal doc: %w", err)
	}
	return s.client.Set(ctx, cfgKey(doc.Scope, doc.ScopeID), payload, 0).Err()
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
			// ignore missing
			continue
		}
		revisions[item.scope] = doc.Revision
		mergeShallow(result, doc.Data)
	}
	version := snapshotVersion(revisions)
	hash, _ := snapshotHash(result)
	return &EffectiveSnapshot{
		Version: version,
		Hash:    hash,
		Data:    result,
	}, nil
}

// mergeShallow overwrites keys in dst with src values.
func mergeShallow(dst, src map[string]any) {
	if len(src) == 0 {
		return
	}
	for k, v := range src {
		dst[k] = v
	}
}

func cfgKey(scope Scope, id string) string {
	if scope == ScopeSystem {
		if id == "" {
			id = "default"
		}
	}
	return fmt.Sprintf("cfg:%s:%s", scope, id)
}
