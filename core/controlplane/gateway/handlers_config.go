package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/licensing"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config handlers

func (s *server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermConfigWrite, []string{"admin"}, s.configSvc) {
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
	if scope == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, scopeID)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, errorCodeConfigKeyForbidden, "tenant access denied")
			return
		}
		scopeID = tenant
	}
	if scope == configsvc.ScopeSystem && (scopeID == "" || scopeID == "default") {
		if _, hasBundles := data[packs.PolicyConfigKey]; hasBundles {
			writeJSONError(w, http.StatusBadRequest, errorCodeConfigKeyForbidden, "bundles must be written to system/policy scope, not system/default")
			return
		}
	}
	err := s.configSvc.SetWithRetry(r.Context(), scope, scopeID, 3, func(doc *configsvc.Document) error {
		for k, v := range data {
			doc.Data[k] = v
		}
		for k, v := range meta {
			if doc.Meta == nil {
				doc.Meta = map[string]string{}
			}
			doc.Meta[k] = v
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			writeJSONError(w, http.StatusConflict, errorCodeConfigVersionConflict, "config update conflict — retry")
		} else {
			slog.Error("config set failed", "error", err, "scope", scopeStr, "scope_id", scopeID)
			writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, "config update failed")
		}
		return
	}

	// Broadcast config-changed notification so all replicas reload immediately.
	s.publishConfigChanged(scopeStr, scopeID)

	if mcpConfigTouched(data) {
		s.reloadMCPConfig(r.Context())
	}
	s.appendAuditEntryNamed(r.Context(), "set", "config", scopeStr+"/"+scopeID, scopeStr, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "set config "+scopeStr+"/"+scopeID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndRole(w, r, nil, s.configSvc) {
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = string(configsvc.ScopeSystem)
	}
	// Reject unknown scopes before any data access.
	switch configsvc.Scope(scope) {
	case configsvc.ScopeSystem, configsvc.ScopeOrg, configsvc.ScopeTeam, configsvc.ScopeWorkflow, configsvc.ScopeStep:
		// valid
	default:
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigRequestInvalid, "invalid scope")
		return
	}
	scopeID := strings.TrimSpace(r.URL.Query().Get("scope_id"))
	if scope == string(configsvc.ScopeSystem) && scopeID == "" {
		scopeID = "default"
	}
	// System config requires admin; all other scopes require at least operator.
	if configsvc.Scope(scope) == configsvc.ScopeSystem {
		if !s.requirePermissionOrRole(w, r, auth.PermConfigRead, "admin") {
			return
		}
	} else {
		if !s.requirePermissionOrRole(w, r, auth.PermConfigRead, "admin", "operator") {
			return
		}
	}
	if configsvc.Scope(scope) == configsvc.ScopeOrg {
		tenant, err := s.resolveTenant(r, scopeID)
		if err != nil {
			writeJSONError(w, http.StatusForbidden, errorCodeConfigKeyForbidden, "tenant access denied")
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
			writeJSONError(w, http.StatusNotFound, errorCodeConfigNotFound, "config not found")
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermConfigRead, []string{"admin", "operator"}, s.configSvc) {
		return
	}
	orgID, err := s.resolveTenant(r, r.URL.Query().Get("org_id"))
	if err != nil {
		writeJSONError(w, http.StatusForbidden, errorCodeConfigKeyForbidden, "tenant access denied")
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermSchemasWrite, []string{"admin"}, s.schemaRegistry) {
		return
	}
	var req schemaRegisterRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, "schema id required")
		return
	}
	data, err := json.Marshal(req.Schema)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, "invalid schema")
		return
	}
	_, err = s.schemaRegistry.Get(r.Context(), req.ID)
	schemaExists := err == nil
	if err != nil && !errors.Is(err, redis.Nil) {
		writeInternalError(w, r, "schema lookup", err)
		return
	}
	if !schemaExists {
		count, err := s.schemaCount(r.Context())
		if err != nil {
			writeInternalError(w, r, "schema count", err)
			return
		}
		if limitErr := licensing.CheckSchemaCount(int64(count+1), s.currentEntitlements()); limitErr != nil {
			writeTierLimitJSON(w, limitErr)
			return
		}
	}
	if err := s.schemaRegistry.Register(r.Context(), req.ID, data); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, err.Error())
		return
	}
	s.appendAuditEntryNamed(r.Context(), "register", "schema", req.ID, req.ID, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "register schema "+req.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermSchemasRead, []string{"admin", "operator", "viewer"}, s.schemaRegistry) {
		return
	}
	limit, _ := parsePagination(r, 100)
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermSchemasRead, []string{"admin", "operator", "viewer"}, s.schemaRegistry) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, "schema id required")
		return
	}
	data, err := s.schemaRegistry.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeJSONError(w, http.StatusNotFound, errorCodeConfigSchemaNotFound, "schema not found")
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
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermSchemasWrite, []string{"admin"}, s.schemaRegistry) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeConfigSchemaViolation, "schema id required")
		return
	}
	if err := s.schemaRegistry.Delete(r.Context(), id); err != nil {
		slog.Error("schema delete failed", "error", err, "id", id) // #nosec -- id is validated and used for diagnostics.
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.appendAuditEntryNamed(r.Context(), "delete", "schema", id, id, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "delete schema "+id)
	w.WriteHeader(http.StatusNoContent)
}

// Resource lock handlers
