package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
)

// Config handlers
type configUpsertRequest struct {
	Scope   string            `json:"scope"`
	ScopeID string            `json:"scope_id"`
	Data    map[string]any    `json:"data"`
	Meta    map[string]string `json:"meta,omitempty"`
}

func (s *server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req configUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	scope := configsvc.Scope(req.Scope)
	if scope == configsvc.ScopeSystem {
		if err := s.requireRole(r, "admin"); err != nil {
			writeErrorJSON(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if scope == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, req.ScopeID)
		if err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		req.ScopeID = tenant
	}
	doc := &configsvc.Document{
		Scope:   scope,
		ScopeID: req.ScopeID,
		Data:    req.Data,
		Meta:    req.Meta,
	}
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	s.appendAuditEntry(r.Context(), "set", "config", req.Scope+"/"+req.ScopeID, policyActorID(r), policyRole(r), "set config "+req.Scope+"/"+req.ScopeID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = string(configsvc.ScopeSystem)
	}
	scopeID := strings.TrimSpace(r.URL.Query().Get("scope_id"))
	if scope == string(configsvc.ScopeSystem) && scopeID == "" {
		scopeID = "default"
	}
	if configsvc.Scope(scope) == configsvc.ScopeSystem {
		if err := s.requireRole(r, "admin"); err != nil {
			writeErrorJSON(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if configsvc.Scope(scope) == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, scopeID)
		if err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		scopeID = tenant
	}
	doc, err := s.configSvc.Get(r.Context(), configsvc.Scope(scope), scopeID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "config not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, doc)
}

func (s *server) handleGetEffectiveConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	teamID := r.URL.Query().Get("team_id")
	wfID := r.URL.Query().Get("workflow_id")
	stepID := r.URL.Query().Get("step_id")

	snap, err := s.configSvc.EffectiveSnapshot(r.Context(), orgID, teamID, wfID, stepID)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if snap == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, snap)
}

// Schema handlers
type schemaRegisterRequest struct {
	ID     string         `json:"id"`
	Schema map[string]any `json:"schema"`
}

func (s *server) handleRegisterSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "schema registry unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req schemaRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	data, err := json.Marshal(req.Schema)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid schema")
		return
	}
	if err := s.schemaRegistry.Register(r.Context(), req.ID, data); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	s.appendAuditEntry(r.Context(), "register", "schema", req.ID, policyActorID(r), policyRole(r), "register schema "+req.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "schema registry unavailable")
		return
	}
	limit := int64(100)
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	ids, err := s.schemaRegistry.List(r.Context(), limit)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"schemas": ids})
}

func (s *server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "schema registry unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "schema id required")
		return
	}
	data, err := s.schemaRegistry.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "schema not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to decode schema")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"id": id, "schema": payload})
}

func (s *server) handleDeleteSchema(w http.ResponseWriter, r *http.Request) {
	if s.schemaRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "schema registry unavailable")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "schema id required")
		return
	}
	if err := s.schemaRegistry.Delete(r.Context(), id); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendAuditEntry(r.Context(), "delete", "schema", id, policyActorID(r), policyRole(r), "delete schema "+id)
	w.WriteHeader(http.StatusNoContent)
}

// Resource lock handlers
