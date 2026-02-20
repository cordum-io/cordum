package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config handlers

func (s *server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	// Decode raw JSON to detect wrapped vs flat format.
	var raw map[string]any
	if err := decodeJSONBody(w, r, &raw); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	var scopeStr, scopeID string
	var data map[string]any
	var meta map[string]string

	// If body has "scope" as a string, treat as wrapped Document request (backward compat).
	if sc, ok := raw["scope"].(string); ok && sc != "" {
		scopeStr = sc
		if sid, ok := raw["scope_id"].(string); ok {
			scopeID = sid
		}
		if d, ok := raw["data"].(map[string]any); ok {
			data = d
		}
		if m, ok := raw["meta"].(map[string]any); ok {
			meta = make(map[string]string, len(m))
			for k, v := range m {
				if vs, ok := v.(string); ok {
					meta[k] = vs
				}
			}
		}
	} else {
		// Flat config patch from dashboard — auto-wrap as system/default.
		scopeStr = string(configsvc.ScopeSystem)
		scopeID = "default"
		data = raw
	}

	scope := configsvc.Scope(scopeStr)
	if scope == configsvc.ScopeSystem {
		if err := s.requireRole(r, "admin"); err != nil {
			writeForbidden(w, r, err)
			return
		}
	}
	if scope == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, scopeID)
		if err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		scopeID = tenant
	}
	doc := &configsvc.Document{
		Scope:   scope,
		ScopeID: scopeID,
		Data:    data,
		Meta:    meta,
	}
	if err := s.configSvc.Set(r.Context(), doc); err != nil {
		slog.Error("config set failed", "error", err, "scope", scopeStr, "scope_id", scopeID) // #nosec -- values are validated and used for diagnostics.
		writeErrorJSON(w, http.StatusBadRequest, "config update failed")
		return
	}

	// Broadcast config-changed notification so all replicas reload immediately.
	s.publishConfigChanged(scopeStr, scopeID)

	if mcpConfigTouched(data) {
		s.reloadMCPConfig(r.Context())
	}
	s.appendAuditEntryNamed(r.Context(), "set", "config", scopeStr+"/"+scopeID, scopeStr, policyActorID(r), policyRole(r), "set config "+scopeStr+"/"+scopeID)
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
			writeForbidden(w, r, err)
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
			// System/default config missing on clean install — return empty config
			// so the dashboard can bootstrap without error storms.
			if scope == string(configsvc.ScopeSystem) && scopeID == "default" {
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, map[string]any{})
				return
			}
			writeErrorJSON(w, http.StatusNotFound, "config not found")
			return
		}
		slog.Error("config get failed", "error", err, "scope", scope, "scope_id", scopeID) // #nosec -- values are validated and used for diagnostics.
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Return full Document envelope if explicitly requested (backward compat).
	if r.URL.Query().Get("envelope") == "true" {
		writeJSON(w, doc)
		return
	}
	// Default: return flat data for dashboard compatibility.
	data := doc.Data
	if data == nil {
		data = map[string]any{}
	}
	writeJSON(w, data)
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
		slog.Error("effective config snapshot failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if snap == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, snap)
}

// publishConfigChanged publishes a lightweight NATS notification after a config
// write so that all replicas (gateway, scheduler, workflow-engine) can reload
// immediately instead of waiting for the 30s poll. Fire-and-forget: a publish
// failure is logged but does not fail the config write.
func (s *server) publishConfigChanged(scope, scopeID string) {
	if s.bus == nil {
		return
	}
	packet := &pb.BusPacket{
		TraceId:         uuid.New().String(),
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_Alert{
			Alert: &pb.SystemAlert{
				Level:           "INFO",
				Severity:        pb.AlertSeverity_ALERT_SEVERITY_INFO,
				Message:         "config changed",
				Component:       "api-gateway",
				SourceComponent: "api-gateway",
				Details: map[string]string{
					"scope":      scope,
					"scope_id":   scopeID,
					"changed_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
		},
	}
	if err := s.bus.Publish(capsdk.SubjectConfigChanged, packet); err != nil {
		slog.Warn("config change notification publish failed", "error", err)
	}
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
		writeForbidden(w, r, err)
		return
	}
	var req schemaRegisterRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
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
	s.appendAuditEntryNamed(r.Context(), "register", "schema", req.ID, req.ID, policyActorID(r), policyRole(r), "register schema "+req.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin", "operator", "viewer"); err != nil {
		writeForbidden(w, r, err)
		return
	}
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
		slog.Error("schema list failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"schemas": ids})
}

func (s *server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin", "operator", "viewer"); err != nil {
		writeForbidden(w, r, err)
		return
	}
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
		slog.Error("schema get failed", "error", err, "id", id) // #nosec -- id is validated and used for diagnostics.
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "schema id required")
		return
	}
	if err := s.schemaRegistry.Delete(r.Context(), id); err != nil {
		slog.Error("schema delete failed", "error", err, "id", id) // #nosec -- id is validated and used for diagnostics.
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "schema", id, id, policyActorID(r), policyRole(r), "delete schema "+id)
	w.WriteHeader(http.StatusNoContent)
}

// Resource lock handlers
