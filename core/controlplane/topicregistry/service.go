package topicregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/redis/go-redis/v9"
)

const (
	scopeIDTopics  = "topics"
	scopeIDDefault = "default"

	StatusActive     = "active"
	StatusDeprecated = "deprecated"
	StatusDisabled   = "disabled"
)

var validStatuses = map[string]bool{
	StatusActive:     true,
	StatusDeprecated: true,
	StatusDisabled:   true,
}

// Registration is the canonical topic authority record persisted in cfg:system:topics.
type Registration struct {
	Name           string   `json:"name"`
	Pool           string   `json:"pool"`
	InputSchemaID  string   `json:"input_schema_id,omitempty"`
	OutputSchemaID string   `json:"output_schema_id,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	Requires       []string `json:"requires,omitempty"`
	RiskTags       []string `json:"risk_tags,omitempty"`
	Status         string   `json:"status"`
}

// Snapshot is the current topic registry view.
type Snapshot struct {
	Items         []Registration
	RegistryEmpty bool
}

// Service reads and writes topic registrations backed by configsvc.
type Service struct {
	config *configsvc.Service
}

func NewService(cfg *configsvc.Service) *Service {
	if cfg == nil {
		return nil
	}
	return &Service{config: cfg}
}

// Get resolves a topic registration by name. RegistryEmpty is true only when no
// canonical registrations exist and migration found no legacy topic mappings.
func (s *Service) Get(ctx context.Context, name string) (*Registration, bool, error) {
	if s == nil || s.config == nil {
		return nil, true, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false, fmt.Errorf("topic name required")
	}
	records, _, registryEmpty, err := s.loadRecords(ctx)
	if err != nil {
		return nil, false, err
	}
	rec, ok := records[name]
	if !ok {
		return nil, registryEmpty, nil
	}
	out := rec
	return &out, registryEmpty, nil
}

// List returns all registrations sorted by topic name. RegistryEmpty is true
// only when no canonical registrations exist and no legacy topic mappings were migrated.
func (s *Service) List(ctx context.Context) (Snapshot, error) {
	if s == nil || s.config == nil {
		return Snapshot{RegistryEmpty: true}, nil
	}
	records, _, registryEmpty, err := s.loadRecords(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	items := make([]Registration, 0, len(records))
	for _, rec := range records {
		items = append(items, normalizeRegistration(rec))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return Snapshot{Items: items, RegistryEmpty: registryEmpty}, nil
}

// Set inserts or updates one topic registration.
func (s *Service) Set(ctx context.Context, reg Registration) error {
	return s.SetMany(ctx, []Registration{reg})
}

// SetMany inserts or updates multiple topic registrations atomically.
func (s *Service) SetMany(ctx context.Context, regs []Registration) error {
	if s == nil || s.config == nil {
		return fmt.Errorf("topic registry unavailable")
	}
	if len(regs) == 0 {
		return nil
	}
	normalized := make([]Registration, 0, len(regs))
	for _, reg := range regs {
		norm, err := validateRegistration(reg)
		if err != nil {
			return err
		}
		normalized = append(normalized, norm)
	}
	return s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDTopics, 3, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
		}
		for _, reg := range normalized {
			existing[reg.Name] = reg
		}
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDTopics
		doc.Data = encodeDocument(existing)
		return nil
	})
}

// Delete removes one topic registration.
func (s *Service) Delete(ctx context.Context, name string) error {
	return s.DeleteMany(ctx, []string{name})
}

// DeleteMany removes the named topic registrations.
func (s *Service) DeleteMany(ctx context.Context, names []string) error {
	if s == nil || s.config == nil {
		return fmt.Errorf("topic registry unavailable")
	}
	if len(names) == 0 {
		return nil
	}
	targets := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			targets[name] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDTopics, 3, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
		}
		for name := range targets {
			delete(existing, name)
		}
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDTopics
		doc.Data = encodeDocument(existing)
		return nil
	})
}

func (s *Service) loadRecords(ctx context.Context) (map[string]Registration, *configsvc.Document, bool, error) {
	doc, err := s.config.Get(ctx, configsvc.ScopeSystem, scopeIDTopics)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, false, fmt.Errorf("get topic registry: %w", err)
	}
	if errors.Is(err, redis.Nil) {
		doc = &configsvc.Document{
			Scope:   configsvc.ScopeSystem,
			ScopeID: scopeIDTopics,
			Data:    map[string]any{},
		}
	}

	records, err := decodeDocument(doc)
	if err != nil {
		return nil, nil, false, err
	}
	if len(records) > 0 {
		return records, doc, false, nil
	}

	migrated, err := s.migrateFromLegacyPools(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	if migrated {
		doc, err = s.config.Get(ctx, configsvc.ScopeSystem, scopeIDTopics)
		if err != nil {
			return nil, nil, false, fmt.Errorf("reload topic registry: %w", err)
		}
		records, err = decodeDocument(doc)
		if err != nil {
			return nil, nil, false, err
		}
		return records, doc, len(records) == 0, nil
	}
	return map[string]Registration{}, doc, true, nil
}

func (s *Service) migrateFromLegacyPools(ctx context.Context) (bool, error) {
	legacy, err := s.legacyTopicRegistrations(ctx)
	if err != nil {
		return false, err
	}
	if len(legacy) == 0 {
		return false, nil
	}
	err = s.config.SetWithRetry(ctx, configsvc.ScopeSystem, scopeIDTopics, 3, func(doc *configsvc.Document) error {
		existing, err := decodeDocument(doc)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return nil
		}
		doc.Scope = configsvc.ScopeSystem
		doc.ScopeID = scopeIDTopics
		doc.Data = encodeDocument(legacy)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("migrate topic registry: %w", err)
	}
	return true, nil
}

func (s *Service) legacyTopicRegistrations(ctx context.Context) (map[string]Registration, error) {
	defaultDoc, err := s.config.Get(ctx, configsvc.ScopeSystem, scopeIDDefault)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return map[string]Registration{}, nil
		}
		return nil, fmt.Errorf("get default config: %w", err)
	}
	if defaultDoc == nil || defaultDoc.Data == nil {
		return map[string]Registration{}, nil
	}
	rawPools, ok := defaultDoc.Data["pools"]
	if !ok || rawPools == nil {
		return map[string]Registration{}, nil
	}
	if rawMap, ok := rawPools.(map[string]any); ok {
		if rawTopics, ok := rawMap["topics"].(map[string]any); !ok || len(rawTopics) == 0 {
			return map[string]Registration{}, nil
		}
	}
	payload, err := json.Marshal(rawPools)
	if err != nil {
		return nil, fmt.Errorf("marshal pools config: %w", err)
	}
	poolsCfg, err := config.ParsePoolsConfig(payload)
	if err != nil {
		return nil, fmt.Errorf("parse pools config: %w", err)
	}
	if poolsCfg == nil || len(poolsCfg.Topics) == 0 {
		return map[string]Registration{}, nil
	}
	out := make(map[string]Registration, len(poolsCfg.Topics))
	for topic, pools := range poolsCfg.Topics {
		pool := firstPool(pools)
		if pool == "" {
			continue
		}
		out[topic] = Registration{
			Name:     strings.TrimSpace(topic),
			Pool:     pool,
			Requires: []string{},
			RiskTags: []string{},
			Status:   StatusActive,
		}
	}
	return out, nil
}

// ActiveWorkerCountsByTopic derives active worker counts from the scheduler snapshot using
// the topic's configured pool. Missing pools/topics report zero.
func ActiveWorkerCountsByTopic(snap *registry.Snapshot, items []Registration) map[string]int {
	counts := make(map[string]int, len(items))
	if len(items) == 0 {
		return counts
	}
	workersByPool := map[string]int{}
	if snap != nil {
		for _, worker := range snap.Workers {
			pool := strings.TrimSpace(worker.Pool)
			if pool != "" {
				workersByPool[pool]++
			}
		}
	}
	for _, item := range items {
		counts[item.Name] = workersByPool[item.Pool]
	}
	return counts
}

func decodeDocument(doc *configsvc.Document) (map[string]Registration, error) {
	if doc == nil || len(doc.Data) == 0 {
		return map[string]Registration{}, nil
	}
	out := make(map[string]Registration, len(doc.Data))
	for topic, raw := range doc.Data {
		bytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal topic %s: %w", topic, err)
		}
		var reg Registration
		if err := json.Unmarshal(bytes, &reg); err != nil {
			return nil, fmt.Errorf("decode topic %s: %w", topic, err)
		}
		if strings.TrimSpace(reg.Name) == "" {
			reg.Name = topic
		}
		norm, err := validateRegistration(reg)
		if err != nil {
			return nil, fmt.Errorf("decode topic %s: %w", topic, err)
		}
		out[norm.Name] = norm
	}
	return out, nil
}

func encodeDocument(records map[string]Registration) map[string]any {
	if len(records) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(records))
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out[name] = normalizeRegistration(records[name])
	}
	return out
}

func validateRegistration(reg Registration) (Registration, error) {
	reg = normalizeRegistration(reg)
	if reg.Name == "" {
		return Registration{}, fmt.Errorf("topic name required")
	}
	if !strings.HasPrefix(reg.Name, "job.") {
		return Registration{}, fmt.Errorf("topic must match job.* pattern: %q", reg.Name)
	}
	if !validStatuses[reg.Status] {
		return Registration{}, fmt.Errorf("invalid topic status %q", reg.Status)
	}
	if reg.Pool == "" && reg.Status != StatusDisabled {
		return Registration{}, fmt.Errorf("topic pool required for %q", reg.Name)
	}
	return reg, nil
}

func normalizeRegistration(reg Registration) Registration {
	reg.Name = strings.TrimSpace(reg.Name)
	reg.Pool = strings.TrimSpace(reg.Pool)
	reg.InputSchemaID = strings.TrimSpace(reg.InputSchemaID)
	reg.OutputSchemaID = strings.TrimSpace(reg.OutputSchemaID)
	reg.PackID = strings.TrimSpace(reg.PackID)
	reg.Requires = normalizeStrings(reg.Requires)
	reg.RiskTags = normalizeStrings(reg.RiskTags)
	reg.Status = strings.ToLower(strings.TrimSpace(reg.Status))
	if reg.Status == "" {
		reg.Status = StatusActive
	}
	return reg
}

func normalizeStrings(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func firstPool(pools []string) string {
	for _, pool := range pools {
		pool = strings.TrimSpace(pool)
		if pool != "" {
			return pool
		}
	}
	return ""
}
