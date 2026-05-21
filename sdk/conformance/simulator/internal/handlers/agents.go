package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Agents registers the agent CRUD surface on r. Routes:
//
//	GET    /api/v1/agents
//	POST   /api/v1/agents
//	GET    /api/v1/agents/{id}
//	PUT    /api/v1/agents/{id}
//	DELETE /api/v1/agents/{id}
func Agents(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			engine.WriteJSON(w, http.StatusOK, map[string]any{
				"items":       eng.ListAgents(),
				"next_cursor": "",
			})
		case http.MethodPost:
			var req struct {
				Name                string            `json:"name"`
				Owner               string            `json:"owner"`
				RiskTier            string            `json:"risk_tier"`
				Description         string            `json:"description"`
				AllowedServers      []string          `json:"allowed_servers"`
				AllowedTools        []string          `json:"allowed_tools"`
				AllowedResources    []string          `json:"allowed_resources"`
				Entitlements        []string          `json:"entitlements"`
				AllowedTopics       []string          `json:"allowed_topics"`
				AllowedPools        []string          `json:"allowed_pools"`
				DataClassifications []string          `json:"data_classifications"`
				Labels              map[string]string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				engine.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not JSON", nil)
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				engine.WriteError(w, http.StatusBadRequest, "validation_failed", "name is required", map[string]any{
					"field_errors": map[string][]string{
						"name": {"required"},
					},
				})
				return
			}
			id := eng.NextID("agent")
			ts := eng.Timestamp(0)
			agent := &engine.Agent{
				ID:                  id,
				Name:                req.Name,
				Owner:               req.Owner,
				RiskTier:            req.RiskTier,
				Description:         req.Description,
				Status:              "active",
				AllowedServers:      cloneStrings(req.AllowedServers),
				AllowedTools:        cloneStrings(req.AllowedTools),
				AllowedResources:    cloneStrings(req.AllowedResources),
				Entitlements:        cloneStrings(req.Entitlements),
				AllowedTopics:       cloneStrings(req.AllowedTopics),
				AllowedPools:        cloneStrings(req.AllowedPools),
				DataClassifications: cloneStrings(req.DataClassifications),
				Labels:              req.Labels,
				CreatedAt:           ts,
				UpdatedAt:           ts,
			}
			eng.Agents[id] = agent
			engine.WriteJSON(w, http.StatusCreated, agent)
		default:
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
		}
	})
	mux.HandleFunc("/api/v1/agents/", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
		if id == "" {
			engine.WriteError(w, http.StatusBadRequest, "missing_id", "agent id required", nil)
			return
		}
		eng.Mu().Lock()
		agent, ok := eng.Agents[id]
		eng.Mu().Unlock()
		if !ok {
			engine.WriteError(w, http.StatusNotFound, "not_found", "agent not found: "+id, map[string]any{
				"resource": "agent",
			})
			return
		}
		switch r.Method {
		case http.MethodGet:
			engine.WriteJSON(w, http.StatusOK, agent)
		case http.MethodPut:
			var req struct {
				Name                string            `json:"name"`
				Owner               string            `json:"owner"`
				RiskTier            string            `json:"risk_tier"`
				Description         string            `json:"description"`
				AllowedServers      []string          `json:"allowed_servers"`
				AllowedTools        []string          `json:"allowed_tools"`
				AllowedResources    []string          `json:"allowed_resources"`
				Entitlements        []string          `json:"entitlements"`
				AllowedTopics       []string          `json:"allowed_topics"`
				AllowedPools        []string          `json:"allowed_pools"`
				DataClassifications []string          `json:"data_classifications"`
				Labels              map[string]string `json:"labels"`
				Status              string            `json:"status"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				engine.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not JSON", nil)
				return
			}
			if req.Name != "" {
				agent.Name = req.Name
			}
			if req.Owner != "" {
				agent.Owner = req.Owner
			}
			if req.RiskTier != "" {
				agent.RiskTier = req.RiskTier
			}
			if req.Description != "" {
				agent.Description = req.Description
			}
			if req.Labels != nil {
				agent.Labels = req.Labels
			}
			if req.AllowedServers != nil {
				agent.AllowedServers = cloneStrings(req.AllowedServers)
			}
			if req.AllowedTools != nil {
				agent.AllowedTools = cloneStrings(req.AllowedTools)
			}
			if req.AllowedResources != nil {
				agent.AllowedResources = cloneStrings(req.AllowedResources)
			}
			if req.Entitlements != nil {
				agent.Entitlements = cloneStrings(req.Entitlements)
			}
			if req.AllowedTopics != nil {
				agent.AllowedTopics = cloneStrings(req.AllowedTopics)
			}
			if req.AllowedPools != nil {
				agent.AllowedPools = cloneStrings(req.AllowedPools)
			}
			if req.DataClassifications != nil {
				agent.DataClassifications = cloneStrings(req.DataClassifications)
			}
			if req.Status != "" {
				agent.Status = req.Status
			}
			agent.UpdatedAt = eng.Timestamp(1)
			engine.WriteJSON(w, http.StatusOK, agent)
		case http.MethodDelete:
			eng.Mu().Lock()
			delete(eng.Agents, id)
			eng.Mu().Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
		}
	})
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
