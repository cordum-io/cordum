package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

type createAgentRequest struct {
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Owner               string   `json:"owner"`
	Team                string   `json:"team,omitempty"`
	RiskTier            string   `json:"risk_tier"`
	AllowedTopics       []string `json:"allowed_topics,omitempty"`
	AllowedPools        []string `json:"allowed_pools,omitempty"`
	AllowedTools        []string `json:"allowed_tools,omitempty"`
	DataClassifications []string `json:"data_classifications,omitempty"`
}

type updateAgentRequest struct {
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	Owner               string   `json:"owner,omitempty"`
	Team                string   `json:"team,omitempty"`
	RiskTier            string   `json:"risk_tier,omitempty"`
	Status              string   `json:"status,omitempty"`
	AllowedTopics       []string `json:"allowed_topics,omitempty"`
	AllowedPools        []string `json:"allowed_pools,omitempty"`
	AllowedTools        []string `json:"allowed_tools,omitempty"`
	DataClassifications []string `json:"data_classifications,omitempty"`
}

type agentResponse struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Owner               string   `json:"owner"`
	Team                string   `json:"team,omitempty"`
	RiskTier            string   `json:"risk_tier"`
	AllowedTopics       []string `json:"allowed_topics,omitempty"`
	AllowedPools        []string `json:"allowed_pools,omitempty"`
	AllowedTools        []string `json:"allowed_tools,omitempty"`
	DataClassifications []string `json:"data_classifications,omitempty"`
	Status              string   `json:"status"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

func agentResponseFromIdentity(a *store.AgentIdentity) agentResponse {
	return agentResponse{
		ID:                  a.ID,
		Name:                a.Name,
		Description:         a.Description,
		Owner:               a.Owner,
		Team:                a.Team,
		RiskTier:            a.RiskTier,
		AllowedTopics:       a.AllowedTopics,
		AllowedPools:        a.AllowedPools,
		AllowedTools:        a.AllowedTools,
		DataClassifications: a.DataClassifications,
		Status:              a.Status,
		CreatedAt:           a.CreatedAt,
		UpdatedAt:           a.UpdatedAt,
	}
}

func (s *server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	var req createAgentRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	identity := store.AgentIdentity{
		Name:                strings.TrimSpace(req.Name),
		Description:         strings.TrimSpace(req.Description),
		Owner:               strings.TrimSpace(req.Owner),
		Team:                strings.TrimSpace(req.Team),
		RiskTier:            strings.TrimSpace(req.RiskTier),
		AllowedTopics:       req.AllowedTopics,
		AllowedPools:        req.AllowedPools,
		AllowedTools:        req.AllowedTools,
		DataClassifications: req.DataClassifications,
	}

	created, err := s.agentIdentityStore.Create(r.Context(), identity)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("agent identity created",
		"agent_id", created.ID,
		"name", created.Name,
		"risk_tier", created.RiskTier,
		"actor", policyActorID(r),
		"role", policyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "create", "agent_identity", created.ID, created.Name, policyActorID(r), policyRole(r), "create agent identity "+created.Name)

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, agentResponseFromIdentity(created))
}

func (s *server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			writeErrorJSON(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	filter := store.AgentIdentityFilter{
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
		RiskTier: strings.TrimSpace(r.URL.Query().Get("risk_tier")),
		Team:     strings.TrimSpace(r.URL.Query().Get("team")),
	}

	identities, nextCursor, err := s.agentIdentityStore.List(r.Context(), cursor, limit, filter)
	if err != nil {
		writeInternalError(w, r, "list agent identities", err)
		return
	}

	items := make([]agentResponse, 0, len(identities))
	for _, a := range identities {
		items = append(items, agentResponseFromIdentity(a))
	}

	resp := map[string]any{"items": items}
	if nextCursor != "" {
		resp["cursor"] = nextCursor
	}
	writeJSON(w, resp)
}

func (s *server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "agent id required")
		return
	}

	identity, err := s.agentIdentityStore.Get(r.Context(), id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if identity == nil {
		writeErrorJSON(w, http.StatusNotFound, "agent identity not found")
		return
	}

	writeJSON(w, agentResponseFromIdentity(identity))
}

func (s *server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "agent id required")
		return
	}

	var req updateAgentRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	updates := store.AgentIdentity{
		Name:                strings.TrimSpace(req.Name),
		Description:         strings.TrimSpace(req.Description),
		Owner:               strings.TrimSpace(req.Owner),
		Team:                strings.TrimSpace(req.Team),
		RiskTier:            strings.TrimSpace(req.RiskTier),
		Status:              strings.TrimSpace(req.Status),
		AllowedTopics:       req.AllowedTopics,
		AllowedPools:        req.AllowedPools,
		AllowedTools:        req.AllowedTools,
		DataClassifications: req.DataClassifications,
	}

	updated, err := s.agentIdentityStore.Update(r.Context(), id, updates)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, "agent identity not found")
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("agent identity updated",
		"agent_id", updated.ID,
		"name", updated.Name,
		"actor", policyActorID(r),
		"role", policyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "update", "agent_identity", updated.ID, updated.Name, policyActorID(r), policyRole(r), "update agent identity "+updated.Name)

	writeJSON(w, agentResponseFromIdentity(updated))
}

func (s *server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "agent id required")
		return
	}

	existing, err := s.agentIdentityStore.Get(r.Context(), id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if existing == nil {
		writeErrorJSON(w, http.StatusNotFound, "agent identity not found")
		return
	}

	if err := s.agentIdentityStore.Delete(r.Context(), id); err != nil {
		writeInternalError(w, r, "delete agent identity", err)
		return
	}

	slog.Warn("agent identity revoked",
		"agent_id", id,
		"name", existing.Name,
		"actor", policyActorID(r),
		"role", policyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "revoke", "agent_identity", id, existing.Name, policyActorID(r), policyRole(r), "revoke agent identity "+existing.Name)

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "agent id required")
		return
	}

	identity, err := s.agentIdentityStore.Get(r.Context(), id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if identity == nil {
		writeErrorJSON(w, http.StatusNotFound, "agent identity not found")
		return
	}

	// Compute 7-day stats by scanning recent jobs.
	var totalJobs, deniedCount int
	var lastActive int64

	if s.jobStore != nil {
		// Use ListRecentJobs with a large limit and filter by time + agent_id.
		sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour).UnixMicro()
		jobs, err := s.jobStore.ListRecentJobs(r.Context(), 1000)
		if err != nil {
			slog.Warn("agent stats: failed to list recent jobs", "agent_id", id, "error", err)
		} else {
			for _, job := range jobs {
				if job.UpdatedAt < sevenDaysAgo {
					continue
				}
				// Check if this job belongs to the agent via labels in metadata.
				labelsJSON, lErr := s.jobStore.Client().HGet(r.Context(), "job:meta:"+job.ID, "labels").Result()
				if lErr != nil {
					continue
				}
				var labels map[string]string
				if jErr := json.Unmarshal([]byte(labelsJSON), &labels); jErr != nil {
					continue
				}
				if strings.TrimSpace(labels["agent_id"]) != id {
					continue
				}
				totalJobs++
				if job.State == model.JobStateDenied {
					deniedCount++
				}
				if job.UpdatedAt > lastActive {
					lastActive = job.UpdatedAt
				}
			}
		}
	}

	writeJSON(w, map[string]any{
		"agent_id":      id,
		"total_jobs_7d": totalJobs,
		"denied_7d":     deniedCount,
		"last_active":   lastActive,
	})
}
