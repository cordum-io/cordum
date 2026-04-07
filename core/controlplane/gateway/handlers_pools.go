package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/infra/config"
)

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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
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
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
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
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			writeErrorJSON(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("create pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "create", "pool", name, name, policyActorID(r), policyRole(r), "create pool "+name)
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
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
			return fmt.Errorf("pool %q not found", name)
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
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		slog.Error("update pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "update", "pool", name, name, policyActorID(r), policyRole(r), "update pool "+name)
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	force := r.URL.Query().Get("force") == "true"

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		if err := pools.ValidatePoolDelete(name, poolMap, topics, force); err != nil {
			return err
		}
		delete(poolMap, name)
		// Remove pool from all topic mappings
		for topic, plist := range topics {
			filtered := slices.DeleteFunc(plist, func(p string) bool { return p == name })
			if len(filtered) == 0 {
				delete(topics, topic)
			} else {
				topics[topic] = filtered
			}
		}
		writePoolsToConfig(doc, topics, poolMap)
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "active topic mapping") {
			writeErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("delete pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "delete pool failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "delete", "pool", name, name, policyActorID(r), policyRole(r), "delete pool "+name)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// POST /api/v1/pools/{name}/drain — start draining
// ---------------------------------------------------------------------------

type drainPoolRequest struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

func (s *server) handleDrainPool(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	var req drainPoolRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300
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
			return fmt.Errorf("pool %q not found", name)
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
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "cannot drain") {
			writeErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("drain pool failed", "pool", name, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "drain pool failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "drain", "pool", name, name, policyActorID(r), policyRole(r), "drain pool "+name)
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	topic := strings.TrimSpace(r.PathValue("topic"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := pools.ValidateTopicName(topic); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		if _, ok := poolMap[name]; !ok {
			return fmt.Errorf("pool %q not found", name)
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
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		slog.Error("add topic to pool failed", "pool", name, "topic", topic, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "add topic mapping failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "add_topic", "pool", name, name, policyActorID(r), policyRole(r),
		fmt.Sprintf("add topic %s to pool %s", topic, name))
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/pools/{name}/topics/{topic} — remove topic→pool mapping
// ---------------------------------------------------------------------------

func (s *server) handleRemoveTopicFromPool(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	topic := strings.TrimSpace(r.PathValue("topic"))
	if err := pools.ValidatePoolName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	err := s.configSvc.SetWithRetry(r.Context(), configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
		topics, poolMap, err := extractPoolsFromConfig(doc)
		if err != nil {
			return err
		}
		plist, ok := topics[topic]
		if !ok {
			return fmt.Errorf("topic %q not found", topic)
		}
		if !slices.Contains(plist, name) {
			return fmt.Errorf("pool %q not mapped to topic %q", name, topic)
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
			writeErrorJSON(w, http.StatusConflict, "config update conflict — retry")
			return
		}
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not mapped") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		slog.Error("remove topic from pool failed", "pool", name, "topic", topic, "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "remove topic mapping failed")
		return
	}

	s.publishConfigChanged("system", "default")
	s.appendAuditEntryNamed(r.Context(), "remove_topic", "pool", name, name, policyActorID(r), policyRole(r),
		fmt.Sprintf("remove topic %s from pool %s", topic, name))
	w.WriteHeader(http.StatusNoContent)
}
