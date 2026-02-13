package gateway

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Allowed scopes for managed API keys.
var allowedKeyScopes = map[string]struct{}{
	"admin":           {},
	"jobs:read":       {},
	"jobs:write":      {},
	"workflows:read":  {},
	"workflows:write": {},
	"policy:read":     {},
	"policy:write":    {},
}

// apiKeyResponse is the JSON shape returned to the frontend, matching the ApiKey type.
type apiKeyResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	LastUsed   string   `json:"lastUsed,omitempty"`
	UsageCount int64    `json:"usageCount"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
}

type createKeyRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

func managedKeyToResponse(mk *ManagedKey) apiKeyResponse {
	resp := apiKeyResponse{
		ID:         mk.ID,
		Name:       mk.Name,
		Prefix:     mk.Prefix,
		Scopes:     mk.Scopes,
		CreatedAt:  mk.CreatedAt.UTC().Format(time.RFC3339),
		UsageCount: mk.UsageCount,
	}
	if !mk.LastUsed.IsZero() {
		resp.LastUsed = mk.LastUsed.UTC().Format(time.RFC3339)
	}
	if !mk.ExpiresAt.IsZero() {
		resp.ExpiresAt = mk.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if resp.Scopes == nil {
		resp.Scopes = []string{}
	}
	return resp
}

// handleListKeys handles GET /api/v1/auth/keys.
func (s *server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	keys, err := s.keyStore.List(r.Context(), tenant)
	if err != nil {
		slog.Error("list keys failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list keys")
		return
	}

	items := make([]apiKeyResponse, 0, len(keys))
	for _, mk := range keys {
		items = append(items, managedKeyToResponse(mk))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"items": items})
}

// handleCreateKey handles POST /api/v1/auth/keys.
func (s *server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	var req createKeyRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "name is required")
		return
	}

	// Validate scopes
	for _, scope := range req.Scopes {
		if _, ok := allowedKeyScopes[scope]; !ok {
			writeErrorJSON(w, http.StatusBadRequest, "invalid scope: "+scope)
			return
		}
	}

	var expiresAt time.Time
	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid expiresAt format, use RFC3339")
			return
		}
		if parsed.Before(time.Now()) {
			writeErrorJSON(w, http.StatusBadRequest, "expiresAt must be in the future")
			return
		}
		expiresAt = parsed
	}

	rawKey, err := GenerateRawKey()
	if err != nil {
		slog.Error("generate key failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to generate key")
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	scopes := req.Scopes
	if scopes == nil {
		scopes = []string{}
	}

	mk := &ManagedKey{
		Name:      req.Name,
		Tenant:    tenant,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}

	if err := s.keyStore.Create(r.Context(), mk, rawKey); err != nil {
		slog.Error("create key failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "create", "api_key", mk.ID, mk.Name, policyActorID(r), policyRole(r), "create api key: "+mk.Name)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"key":    managedKeyToResponse(mk),
		"secret": rawKey,
	})
}

// handleRevokeKey handles DELETE /api/v1/auth/keys/{id}.
func (s *server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if s.keyStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "key management unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing key id")
		return
	}

	tenant := s.tenant
	if auth := authFromRequest(r); auth != nil && auth.Tenant != "" {
		tenant = auth.Tenant
	}

	if err := s.keyStore.Revoke(r.Context(), id, tenant); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, "key not found")
			return
		}
		slog.Error("revoke key failed", "error", err, "key_id", id) // #nosec -- key id is validated and safe for logs.
		writeErrorJSON(w, http.StatusInternalServerError, "failed to revoke key")
		return
	}

	s.appendAuditEntryNamed(r.Context(), "revoke", "api_key", id, "", policyActorID(r), policyRole(r), "revoke api key: "+id)
	w.WriteHeader(http.StatusNoContent)
}
