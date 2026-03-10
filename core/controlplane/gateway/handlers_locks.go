package gateway

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Resource lock handlers
type lockRequest struct {
	Resource string `json:"resource"`
	Owner    string `json:"owner"`
	Mode     string `json:"mode"`
	TTLms    int64  `json:"ttl_ms"`
}

// tenantLockResource prefixes a lock resource name with the tenant ID to
// enforce per-tenant isolation. Without this, tenant A could interfere with
// locks owned by tenant B.
func tenantLockResource(tenantID, resource string) string {
	return "tenant:" + tenantID + ":" + resource
}

// stripTenantLockPrefix removes the tenant prefix from a lock resource name
// so clients see only their original resource identifier.
func stripTenantLockPrefix(tenantID, resource string) string {
	prefix := "tenant:" + tenantID + ":"
	return strings.TrimPrefix(resource, prefix)
}

func (s *server) handleGetLock(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin", "operator", "viewer"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	tenantID, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	if resource == "" {
		writeErrorJSON(w, http.StatusBadRequest, "resource required")
		return
	}
	scopedResource := tenantLockResource(tenantID, resource)
	lock, err := s.lockStore.Get(r.Context(), scopedResource)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "lock not found")
			return
		}
		writeInternalError(w, r, "lock operation", err)
		return
	}
	// Strip the tenant prefix from the resource before returning to clients.
	lock.Resource = stripTenantLockPrefix(tenantID, lock.Resource)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}

func validateLockRequest(req lockRequest) error {
	resource := strings.TrimSpace(req.Resource)
	if resource == "" {
		return errors.New("resource required")
	}
	if len(resource) > 512 {
		return errors.New("resource too long (max 512 chars)")
	}
	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		return errors.New("owner required")
	}
	if len(owner) > 256 {
		return errors.New("owner too long (max 256 chars)")
	}
	if req.TTLms < 0 {
		return errors.New("ttl_ms must be non-negative")
	}
	const maxLockTTLMs = 3600000 // 1 hour
	if req.TTLms > maxLockTTLMs {
		return errors.New("ttl_ms too large (max 3600000)")
	}
	return nil
}

func (s *server) handleAcquireLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	tenantID, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	var req lockRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if err := validateLockRequest(req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := parseLockMode(req.Mode)
	scopedResource := tenantLockResource(tenantID, req.Resource)
	lock, ok, err := s.lockStore.Acquire(r.Context(), scopedResource, req.Owner, mode, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		writeInternalError(w, r, "acquire lock", err)
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusConflict, "lock unavailable")
		return
	}
	lock.Resource = stripTenantLockPrefix(tenantID, lock.Resource)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}

func (s *server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	tenantID, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	var req lockRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if err := validateLockRequest(req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	scopedResource := tenantLockResource(tenantID, req.Resource)
	lock, ok, err := s.lockStore.Release(r.Context(), scopedResource, req.Owner)
	if err != nil {
		writeInternalError(w, r, "release lock", err)
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusConflict, "lock not held")
		return
	}
	if lock != nil {
		lock.Resource = stripTenantLockPrefix(tenantID, lock.Resource)
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"lock": lock, "released": true})
}

func (s *server) handleRenewLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	tenantID, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	var req lockRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if err := validateLockRequest(req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	scopedResource := tenantLockResource(tenantID, req.Resource)
	lock, ok, err := s.lockStore.Renew(r.Context(), scopedResource, req.Owner, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		writeInternalError(w, r, "renew lock", err)
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "lock not held")
		return
	}
	lock.Resource = stripTenantLockPrefix(tenantID, lock.Resource)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}
