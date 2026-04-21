package gateway

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

type delegationListResponse struct {
	Items      []delegation.DelegationView `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

func (s *server) handleListAgentDelegations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermDelegationRead, "admin") {
		return
	}
	agentID, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}
	tenant := tenantFromRequest(r)
	filter, limit, cursor, ok := parseDelegationListParams(w, r)
	if !ok {
		return
	}
	store := s.delegationListStore()
	if store == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	page, err := store.ListByAgent(r.Context(), tenant, agentID, filter, cursor, limit)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, delegationListResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleListDelegations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermDelegationRead, "admin") {
		return
	}
	tenant := tenantFromRequest(r)
	filter, limit, cursor, ok := parseDelegationListParams(w, r)
	if !ok {
		return
	}
	store := s.delegationListStore()
	if store == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	page, err := store.ListAll(r.Context(), tenant, filter, cursor, limit)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, delegationListResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func parseDelegationListParams(w http.ResponseWriter, r *http.Request) (delegation.DelegationListFilter, int, string, bool) {
	q := r.URL.Query()
	filter := delegation.DelegationListFilter{
		Status: strings.TrimSpace(q.Get("status")),
		Scope:  strings.TrimSpace(q.Get("scope")),
	}
	if raw := strings.TrimSpace(q.Get("before_expiry")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "before_expiry must be RFC3339")
			return delegation.DelegationListFilter{}, 0, "", false
		}
		filter.BeforeExpiry = value.UTC()
	}
	if raw := strings.TrimSpace(q.Get("since_issued")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "since_issued must be RFC3339")
			return delegation.DelegationListFilter{}, 0, "", false
		}
		filter.SinceIssued = value.UTC()
	}
	if raw := strings.TrimSpace(q.Get("until_issued")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "until_issued must be RFC3339")
			return delegation.DelegationListFilter{}, 0, "", false
		}
		filter.UntilIssued = value.UTC()
	}
	limit := 50
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 200 {
			writeErrorJSON(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return delegation.DelegationListFilter{}, 0, "", false
		}
		limit = parsed
	}
	return filter, limit, strings.TrimSpace(q.Get("cursor")), true
}
