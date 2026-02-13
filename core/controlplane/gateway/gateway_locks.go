package gateway

import (
	"encoding/json"
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

func (s *server) handleGetLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	if resource == "" {
		writeErrorJSON(w, http.StatusBadRequest, "resource required")
		return
	}
	lock, err := s.lockStore.Get(r.Context(), resource)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "lock not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}

func (s *server) handleAcquireLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	mode := parseLockMode(req.Mode)
	lock, ok, err := s.lockStore.Acquire(r.Context(), req.Resource, req.Owner, mode, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusConflict, "lock unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}

func (s *server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	if s.lockStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "lock store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	lock, ok, err := s.lockStore.Release(r.Context(), req.Resource, req.Owner)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusConflict, "lock not held")
		return
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
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json")
		return
	}
	lock, ok, err := s.lockStore.Renew(r.Context(), req.Resource, req.Owner, time.Duration(req.TTLms)*time.Millisecond)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "lock not held")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, lock)
}

