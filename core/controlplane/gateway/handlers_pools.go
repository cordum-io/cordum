package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

var ErrPoolNotFound = errors.New("pool not found")
var ErrTopicNotFound = errors.New("topic not found")
var ErrTopicPoolMappingNotFound = errors.New("topic pool mapping not found")

type poolNotFoundError struct {
	pool string
}

func (e poolNotFoundError) Error() string {
	return fmt.Sprintf("pool %q not found", e.pool)
}

func (e poolNotFoundError) Is(target error) bool {
	return target == ErrPoolNotFound
}

type topicNotFoundError struct {
	topic string
}

func (e topicNotFoundError) Error() string {
	return fmt.Sprintf("topic %q not found", e.topic)
}

func (e topicNotFoundError) Is(target error) bool {
	return target == ErrTopicNotFound
}

type topicPoolMappingNotFoundError struct {
	pool  string
	topic string
}

func (e topicPoolMappingNotFoundError) Error() string {
	return fmt.Sprintf("pool %q not mapped to topic %q", e.pool, e.topic)
}

func (e topicPoolMappingNotFoundError) Is(target error) bool {
	return target == ErrTopicPoolMappingNotFound
}

type poolInUseError struct {
	pool       string
	status     string
	queuedJobs int
	agentIDs   []string
}

func (e *poolInUseError) Error() string {
	if e == nil {
		return "pool in use"
	}
	return fmt.Sprintf("pool %q is in use", e.pool)
}

// ---------------------------------------------------------------------------
// Helpers: extract / write pools from the config document
// ---------------------------------------------------------------------------

// extractPoolsFromConfig safely extracts the pools section from a config doc.
// Returns topics and pools maps. Missing keys return empty maps.
func extractPoolsFromConfig(doc *configsvc.Document) (topics map[string][]string, poolMap map[string]config.PoolConfig, err error) {
	if doc.Data == nil {
		return map[string][]string{}, map[string]config.PoolConfig{}, nil
	}
	raw, ok := doc.Data["pools"]
	if !ok || raw == nil {
		return map[string][]string{}, map[string]config.PoolConfig{}, nil
	}
	if rawMap, ok := raw.(map[string]any); ok {
		if rawTopics, ok := rawMap["topics"].(map[string]any); (!ok || len(rawTopics) == 0) && rawMap["pools"] != nil {
			poolData, err := json.Marshal(rawMap["pools"])
			if err != nil {
				return nil, nil, fmt.Errorf("marshal pools map: %w", err)
			}
			poolMap := map[string]config.PoolConfig{}
			if err := json.Unmarshal(poolData, &poolMap); err != nil {
				return nil, nil, fmt.Errorf("unmarshal pools map: %w", err)
			}
			return map[string][]string{}, poolMap, nil
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal pools config: %w", err)
	}
	cfg, err := config.ParsePoolsConfig(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse pools config: %w", err)
	}
	if cfg == nil {
		return map[string][]string{}, map[string]config.PoolConfig{}, nil
	}
	if cfg.Pools == nil {
		cfg.Pools = map[string]config.PoolConfig{}
	}
	return cfg.Topics, cfg.Pools, nil
}

// writePoolsToConfig writes the pools section back into the config document.
func writePoolsToConfig(doc *configsvc.Document, topics map[string][]string, poolMap map[string]config.PoolConfig) {
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	// Build a clean map for serialization. Topic values are either a single
	// string or an array depending on length — this matches the YAML pattern
	// the parser expects.
	topicOut := make(map[string]any, len(topics))
	for t, plist := range topics {
		if len(plist) == 1 {
			topicOut[t] = plist[0]
		} else {
			topicOut[t] = plist
		}
	}
	doc.Data["pools"] = map[string]any{
		"topics": topicOut,
		"pools":  poolMap,
	}
}

// ---------------------------------------------------------------------------
// PUT /api/v1/pools/{name} — create pool
// ---------------------------------------------------------------------------

type createPoolRequest struct {
	Requires    []string `json:"requires"`
	Description string   `json:"description"`
}

func (s *server) handleCreatePool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	var req createPoolRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	newPool := config.PoolConfig{
		Status:      config.PoolStatusActive,
		Requires:    req.Requires,
		Description: req.Description,
	}
	if err := pools.ValidatePoolConfig(newPool); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		_, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		if err := pools.ValidatePoolCreate(name, newPool, poolMap); err != nil {
			return err
		}
		topics, _, _ := extractPoolsFromConfig(doc)
		poolMap[name] = newPool
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			writeJSONError(w, http.StatusConflict, errorCodePoolNameConflict, err.Error())
			return
		}
		slog.Error("create pool failed", "pool", name, "error", err)
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "create", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "create pool "+name)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"name":        name,
		"status":      newPool.EffectiveStatus(),
		"requires":    newPool.Requires,
		"description": newPool.Description,
	})
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/pools/{name} — update pool
// ---------------------------------------------------------------------------

type updatePoolRequest struct {
	Requires    *[]string `json:"requires,omitempty"`
	Description *string   `json:"description,omitempty"`
	Status      *string   `json:"status,omitempty"`
}

func (s *server) handleUpdatePool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	var req updatePoolRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	var updated config.PoolConfig
	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		existing, ok := poolMap[name]
		if !ok {
			return poolNotFoundError{pool: name}
		}
		if req.Requires != nil {
			existing.Requires = *req.Requires
		}
		if req.Description != nil {
			existing.Description = *req.Description
		}
		if req.Status != nil {
			existing.Status = *req.Status
		}
		if err := pools.ValidatePoolConfig(existing); err != nil {
			return err
		}
		poolMap[name] = existing
		updated = existing
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if errors.Is(err, ErrPoolNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, err.Error())
			return
		}
		slog.Error("update pool failed", "pool", name, "error", err)
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "update", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "update pool "+name)
	writeJSON(w, map[string]any{
		"name":        name,
		"status":      updated.EffectiveStatus(),
		"requires":    updated.Requires,
		"description": updated.Description,
	})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/pools/{name} — delete pool
// ---------------------------------------------------------------------------

func (s *server) handleDeletePool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	force := r.URL.Query().Get("force") == "true"
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}
	// Pool config is system-scoped, but agent identity blockers are tenant-scoped;
	// use the authenticated request tenant rather than the store's legacy empty-tenant path.
	err = s.deletePoolFromConfig(r.Context(), tenant, name, force)
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if errors.Is(err, ErrPoolNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, err.Error())
			return
		}
		var inUse *poolInUseError
		if errors.As(err, &inUse) {
			writePoolInUseError(w, inUse)
			return
		}
		if strings.Contains(err.Error(), "active topic mapping") {
			writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
			return
		}
		slog.Error("delete pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "delete pool failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "delete", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "delete pool "+name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deletePoolFromConfig(ctx context.Context, tenantID, name string, force bool) error {
	return s.configSvc.SetWithRetry(ctx, configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		if err := s.validatePoolDelete(ctx, tenantID, name, force, topics, poolMap); err != nil {
			return err
		}
		delete(poolMap, name)
		removePoolFromTopicMappings(name, topics)
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
}

func (s *server) validatePoolDelete(ctx context.Context, tenantID, name string, force bool, topics map[string][]string, poolMap map[string]config.PoolConfig) error {
	existing, ok := poolMap[name]
	if !ok {
		return poolNotFoundError{pool: name}
	}
	status := existing.EffectiveStatus()
	if force && status != config.PoolStatusInactive {
		return &poolInUseError{pool: name, status: status}
	}
	queuedJobs, agentIDs, err := s.poolDeleteBlockers(ctx, tenantID, name, topics)
	if err != nil {
		return err
	}
	if queuedJobs > 0 || len(agentIDs) > 0 {
		return &poolInUseError{pool: name, status: status, queuedJobs: queuedJobs, agentIDs: agentIDs}
	}
	return pools.ValidatePoolDelete(name, poolMap, topics, force)
}

func removePoolFromTopicMappings(name string, topics map[string][]string) {
	for topic, plist := range topics {
		filtered := slices.DeleteFunc(plist, func(p string) bool { return p == name })
		if len(filtered) == 0 {
			delete(topics, topic)
			continue
		}
		topics[topic] = filtered
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/pools/{name}/drain — start draining
// ---------------------------------------------------------------------------

type drainPoolRequest struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

const (
	defaultDrainTimeoutSeconds = 300
	maxDrainTimeoutSeconds     = 3600
)

func (s *server) handleDrainPool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	var req drainPoolRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = defaultDrainTimeoutSeconds
	}
	if req.TimeoutSeconds > maxDrainTimeoutSeconds {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, "timeout_seconds exceeds maximum")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var updated config.PoolConfig

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		existing, ok := poolMap[name]
		if !ok {
			return poolNotFoundError{pool: name}
		}
		if existing.EffectiveStatus() != config.PoolStatusActive {
			return fmt.Errorf("pool %q is %s, not active — cannot drain", name, existing.EffectiveStatus())
		}
		existing.Status = config.PoolStatusDraining
		existing.DrainStartedAt = now
		existing.DrainTimeoutSeconds = req.TimeoutSeconds
		poolMap[name] = existing
		updated = existing
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if errors.Is(err, ErrPoolNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "cannot drain") {
			writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
			return
		}
		slog.Error("drain pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "drain pool failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "drain", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "drain pool "+name)
	writeJSON(w, map[string]any{
		"name":                  name,
		"status":                updated.EffectiveStatus(),
		"drain_started_at":      updated.DrainStartedAt,
		"drain_timeout_seconds": updated.DrainTimeoutSeconds,
	})
}

// ---------------------------------------------------------------------------
// PUT /api/v1/pools/{name}/topics/{topic} — add topic→pool mapping
// ---------------------------------------------------------------------------

func (s *server) handleAddTopicToPool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	topic := strings.TrimSpace(r.PathValue("topic"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}
	if err := pools.ValidateTopicName(topic); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		if _, ok := poolMap[name]; !ok {
			return poolNotFoundError{pool: name}
		}
		plist := topics[topic]
		if slices.Contains(plist, name) {
			return nil // already mapped, idempotent
		}
		topics[topic] = append(plist, name)
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if errors.Is(err, ErrPoolNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, err.Error())
			return
		}
		slog.Error("add topic to pool failed", "pool", name, "topic", topic, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "add topic mapping failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "add_topic", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		fmt.Sprintf("add topic %s to pool %s", topic, name))
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/pools/{name}/topics/{topic} — remove topic→pool mapping
// ---------------------------------------------------------------------------

func (s *server) handleRemoveTopicFromPool(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPoolsWrite, "admin") {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	topic := strings.TrimSpace(r.PathValue("topic"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}
	if err := pools.ValidateTopicName(topic); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePoolInvalidConfig, err.Error())
		return
	}

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		plist, ok := topics[topic]
		if !ok {
			return topicNotFoundError{topic: topic}
		}
		if !slices.Contains(plist, name) {
			return topicPoolMappingNotFoundError{pool: name, topic: topic}
		}
		filtered := slices.DeleteFunc(plist, func(p string) bool { return p == name })
		if len(filtered) == 0 {
			delete(topics, topic)
		} else {
			topics[topic] = filtered
		}
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodePoolVersionConflict, "config update conflict — retry")
			return
		}
		if errors.Is(err, ErrTopicNotFound) || errors.Is(err, ErrTopicPoolMappingNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeTopicNotFound, err.Error())
			return
		}
		slog.Error("remove topic from pool failed", "pool", name, "topic", topic, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "remove topic mapping failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "remove_topic", "pool", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r),
		fmt.Sprintf("remove topic %s from pool %s", topic, name))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) poolDeleteBlockers(ctx context.Context, tenantID, pool string, topics map[string][]string) (int, []string, error) {
	queuedJobs, err := s.countPoolQueuedJobs(ctx, pool, topics)
	if err != nil {
		return 0, nil, err
	}
	agentIDs, err := s.agentIDsReferencingPool(ctx, tenantID, pool)
	if err != nil {
		return 0, nil, err
	}
	return queuedJobs, agentIDs, nil
}

func (s *server) countPoolQueuedJobs(ctx context.Context, pool string, topics map[string][]string) (int, error) {
	if s.jobStore == nil {
		return 0, nil
	}
	states := []model.JobState{
		model.JobStatePending, model.JobStateApproval, model.JobStateScheduled,
		model.JobStateDispatched, model.JobStateRunning, model.JobStateRetrying,
	}
	total := 0
	before := time.Now().UTC().Add(time.Hour).UnixMicro()
	for _, state := range states {
		records, err := s.jobStore.ListJobsByState(ctx, state, before, 5000)
		if err != nil {
			return 0, err
		}
		for _, record := range records {
			if topicUsesPool(record.Topic, topics, pool) {
				total++
			}
		}
	}
	return total, nil
}

func (s *server) agentIDsReferencingPool(ctx context.Context, tenantID, pool string) ([]string, error) {
	if s.agentIdentityStore == nil {
		return nil, nil
	}
	var ids []string
	cursor := ""
	for {
		agents, next, err := s.agentIdentityStore.List(ctx, tenantID, cursor, 200, store.AgentIdentityFilter{})
		if err != nil {
			return nil, err
		}
		for _, agent := range agents {
			if agent != nil && slices.Contains(agent.AllowedPools, pool) {
				ids = append(ids, agent.ID)
			}
		}
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	sort.Strings(ids)
	return ids, nil
}

func topicUsesPool(topic string, topics map[string][]string, pool string) bool {
	if topic == "" {
		return false
	}
	return slices.Contains(topics[topic], pool)
}

func writePoolInUseError(w http.ResponseWriter, err *poolInUseError) {
	resp := map[string]any{
		"error":       errorCodePoolInUse,
		"code":        errorCodePoolInUse,
		"status":      http.StatusConflict,
		"message":     "pool in use",
		"pool":        "",
		"pool_status": "",
		"queued_jobs": 0,
		"agent_ids":   []string{},
	}
	if err != nil {
		resp["message"] = err.Error()
		resp["pool"] = err.pool
		resp["pool_status"] = err.status
		resp["queued_jobs"] = err.queuedJobs
		resp["agent_ids"] = append([]string{}, err.agentIDs...)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(resp)
}
