package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

const agentIdentityFeature = "agent_identity"

type createAgentRequest struct {
	Name string `json:"name"`
	// AgentID, when set, registers this identity for an existing worker. The
	// server links worker_id -> identity (LinkWorker) so audit/scheduler
	// resolution via GetByWorkerID surfaces the human label instead of the raw
	// agent_id (#314). Empty preserves the prior behaviour (unlinked identity).
	AgentID                  string   `json:"agent_id,omitempty"`
	Description              string   `json:"description,omitempty"`
	Owner                    string   `json:"owner"`
	Team                     string   `json:"team,omitempty"`
	RiskTier                 string   `json:"risk_tier"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools,omitempty"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
}

type updateAgentRequest struct {
	Name                     string   `json:"name,omitempty"`
	Description              string   `json:"description,omitempty"`
	Owner                    string   `json:"owner,omitempty"`
	Team                     string   `json:"team,omitempty"`
	RiskTier                 string   `json:"risk_tier,omitempty"`
	Status                   string   `json:"status,omitempty"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools,omitempty"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
}

type agentResponse struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	Description              string   `json:"description,omitempty"`
	Owner                    string   `json:"owner"`
	Team                     string   `json:"team,omitempty"`
	RiskTier                 string   `json:"risk_tier"`
	AllowedTopics            []string `json:"allowed_topics,omitempty"`
	AllowedPools             []string `json:"allowed_pools,omitempty"`
	AllowedServers           []string `json:"allowed_servers,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	AllowedResources         []string `json:"allowed_resources,omitempty"`
	Entitlements             []string `json:"entitlements,omitempty"`
	PreapprovedMutatingTools []string `json:"preapproved_mutating_tools"`
	DataClassifications      []string `json:"data_classifications,omitempty"`
	Status                   string   `json:"status"`
	CreatedAt                string   `json:"created_at"`
	UpdatedAt                string   `json:"updated_at"`
	LastActive               int64    `json:"last_active,omitempty"`
}

func agentResponseFromIdentity(a *store.AgentIdentity) agentResponse {
	return agentResponse{
		ID:                       a.ID,
		Name:                     a.Name,
		Description:              a.Description,
		Owner:                    a.Owner,
		Team:                     a.Team,
		RiskTier:                 a.RiskTier,
		AllowedTopics:            cloneAgentStrings(a.AllowedTopics),
		AllowedPools:             cloneAgentStrings(a.AllowedPools),
		AllowedServers:           cloneAgentStrings(a.AllowedServers),
		AllowedTools:             cloneAgentStrings(a.AllowedTools),
		AllowedResources:         cloneAgentStrings(a.AllowedResources),
		Entitlements:             cloneAgentStrings(a.Entitlements),
		PreapprovedMutatingTools: cloneAgentStrings(a.PreapprovedMutatingTools),
		DataClassifications:      cloneAgentStrings(a.DataClassifications),
		Status:                   a.Status,
		CreatedAt:                a.CreatedAt,
		UpdatedAt:                a.UpdatedAt,
	}
}

func cloneAgentStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (s *server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsWrite, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	var req createAgentRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	// When registering an identity for an existing worker, refuse if that
	// worker is already linked — otherwise LinkWorker below would overwrite
	// the global worker->identity mapping and orphan the prior identity.
	workerID := strings.TrimSpace(req.AgentID)
	if workerID != "" {
		if existing, lookupErr := s.agentIdentityStore.GetByWorkerID(r.Context(), workerID); lookupErr == nil && existing != nil {
			writeJSONError(w, http.StatusConflict, errorCodeAgentWorkerConflict, "worker is already linked to an agent identity")
			return
		}
	}

	identity := store.AgentIdentity{
		TenantID:                 tenant,
		Name:                     strings.TrimSpace(req.Name),
		Description:              strings.TrimSpace(req.Description),
		Owner:                    strings.TrimSpace(req.Owner),
		Team:                     strings.TrimSpace(req.Team),
		RiskTier:                 strings.TrimSpace(req.RiskTier),
		AllowedTopics:            req.AllowedTopics,
		AllowedPools:             req.AllowedPools,
		AllowedServers:           req.AllowedServers,
		AllowedTools:             req.AllowedTools,
		AllowedResources:         req.AllowedResources,
		Entitlements:             req.Entitlements,
		PreapprovedMutatingTools: req.PreapprovedMutatingTools,
		DataClassifications:      req.DataClassifications,
	}

	created, err := s.agentIdentityStore.Create(r.Context(), identity)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	// Link the worker so GetByWorkerID-based resolution (audit enrichment,
	// scheduler, MCP/job principals) maps this worker's agent_id to the new
	// identity. Without this the identity exists but audit rows still fall
	// back to the raw agent_id — the exact bug #314 set out to fix. The
	// identity is already persisted, so surface a link failure rather than
	// reporting a misleading success.
	if workerID != "" {
		if linkErr := s.agentIdentityStore.LinkWorker(r.Context(), created.ID, workerID); linkErr != nil {
			writeInternalError(w, r, "link worker to agent identity", linkErr)
			return
		}
	}

	slog.Info("agent identity created",
		"agent_id", created.ID,
		"name", created.Name,
		"risk_tier", created.RiskTier,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "create", "agent_identity", created.ID, created.Name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "create agent identity "+created.Name)

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, agentResponseFromIdentity(created))
}

func (s *server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsRead, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, "invalid limit")
			return
		}
		limit = parsed
	}

	filter := store.AgentIdentityFilter{
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
		RiskTier: strings.TrimSpace(r.URL.Query().Get("risk_tier")),
		Team:     strings.TrimSpace(r.URL.Query().Get("team")),
	}

	identities, nextCursor, err := s.agentIdentityStore.List(r.Context(), tenant, cursor, limit, filter)
	if err != nil {
		writeInternalError(w, r, "list agent identities", err)
		return
	}

	// Collect agent IDs for batch last-active lookup.
	agentIDs := make([]string, 0, len(identities))
	for _, a := range identities {
		agentIDs = append(agentIDs, a.ID)
	}
	lastActiveMap := s.computeAgentLastActive(r.Context(), agentIDs)

	items := make([]agentResponse, 0, len(identities))
	for _, a := range identities {
		resp := agentResponseFromIdentity(a)
		if ts, ok := lastActiveMap[a.ID]; ok {
			resp.LastActive = ts
		}
		items = append(items, resp)
	}

	resp := map[string]any{"items": items}
	if nextCursor != "" {
		resp["cursor"] = nextCursor
	}
	writeJSON(w, resp)
}

// computeAgentLastActive scans recent jobs and returns the most recent job
// timestamp (UnixMicro) for each of the requested agent IDs.
func (s *server) computeAgentLastActive(ctx context.Context, agentIDs []string) map[string]int64 {
	result := make(map[string]int64, len(agentIDs))
	if s.jobStore == nil || len(agentIDs) == 0 {
		return result
	}

	idSet := make(map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		idSet[id] = true
	}

	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour).UnixMicro()
	offset := 0
	batchSize := int64(500)
	for {
		jobs, err := s.jobStore.ListRecentJobs(ctx, batchSize)
		if err != nil {
			slog.Warn("computeAgentLastActive: failed to list recent jobs", "error", err)
			break
		}
		if len(jobs) <= offset {
			break
		}
		batch := jobs[offset:]
		inWindow := false
		for _, job := range batch {
			if job.UpdatedAt < sevenDaysAgo {
				continue
			}
			inWindow = true
			labelsJSON, lErr := s.jobStore.Client().HGet(ctx, "job:meta:"+job.ID, "labels").Result()
			if lErr != nil {
				continue
			}
			var labels map[string]string
			if jErr := json.Unmarshal([]byte(labelsJSON), &labels); jErr != nil {
				continue
			}
			agentID := strings.TrimSpace(labels["agent_id"])
			if !idSet[agentID] {
				continue
			}
			if job.UpdatedAt > result[agentID] {
				result[agentID] = job.UpdatedAt
			}
		}
		if len(jobs) < int(batchSize) || !inWindow {
			break
		}
		offset = len(jobs)
		batchSize = min(batchSize*2, 5000)
	}
	return result
}

func (s *server) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsRead, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, "agent id required")
		return
	}

	// Get returns nil when the stored identity's TenantID does not match
	// (existence-oracle hardening — cross-tenant ID probes look identical
	// to misses).
	identity, err := s.agentIdentityStore.Get(r.Context(), tenant, id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if identity == nil {
		// Fallback: `id` may be a worker id linked to an identity (e.g. a
		// heartbeating worker registered via the dashboard empty-state flow,
		// #314). Resolve through the worker link, then re-apply the tenant
		// scope that GetByWorkerID does not enforce — preserving the
		// existence-oracle hardening of the direct lookup above.
		if linked, linkErr := s.agentIdentityStore.GetByWorkerID(r.Context(), id); linkErr == nil && linked != nil {
			if tenant == "" || strings.TrimSpace(linked.TenantID) == tenant {
				identity = linked
			}
		}
	}
	if identity == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeAgentNotFound, "agent identity not found")
		return
	}

	writeJSON(w, agentResponseFromIdentity(identity))
}

func (s *server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsWrite, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, "agent id required")
		return
	}

	var req updateAgentRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	updates := store.AgentIdentity{
		Name:                     strings.TrimSpace(req.Name),
		Description:              strings.TrimSpace(req.Description),
		Owner:                    strings.TrimSpace(req.Owner),
		Team:                     strings.TrimSpace(req.Team),
		RiskTier:                 strings.TrimSpace(req.RiskTier),
		Status:                   strings.TrimSpace(req.Status),
		AllowedTopics:            req.AllowedTopics,
		AllowedPools:             req.AllowedPools,
		AllowedServers:           req.AllowedServers,
		AllowedTools:             req.AllowedTools,
		AllowedResources:         req.AllowedResources,
		Entitlements:             req.Entitlements,
		PreapprovedMutatingTools: req.PreapprovedMutatingTools,
		DataClassifications:      req.DataClassifications,
	}

	// Update returns "not found" when the stored identity's TenantID does
	// not match (existence-oracle hardening — cross-tenant writes look
	// identical to misses; no leaked existence signal).
	updated, err := s.agentIdentityStore.Update(r.Context(), tenant, id, updates)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSONError(w, http.StatusNotFound, errorCodeAgentNotFound, "agent identity not found")
			return
		}
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	slog.Info("agent identity updated",
		"agent_id", updated.ID,
		"name", updated.Name,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "update", "agent_identity", updated.ID, updated.Name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "update agent identity "+updated.Name)

	writeJSON(w, agentResponseFromIdentity(updated))
}

func (s *server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsWrite, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, "agent id required")
		return
	}

	// Tenant-scoped Get returns nil when the stored identity's TenantID
	// does not match (existence-oracle hardening — cross-tenant ID probes
	// look like misses).
	existing, err := s.agentIdentityStore.Get(r.Context(), tenant, id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeAgentNotFound, "agent identity not found")
		return
	}

	if err := s.agentIdentityStore.Delete(r.Context(), tenant, id); err != nil {
		writeInternalError(w, r, "delete agent identity", err)
		return
	}

	slog.Warn("agent identity revoked",
		"agent_id", id,
		"name", existing.Name,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "revoke", "agent_identity", id, existing.Name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "revoke agent identity "+existing.Name)

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsRead, "admin") {
		return
	}
	if !s.requireFeatureEntitlement(w, agentIdentityFeature, "agent identity requires an Enterprise license") {
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, err.Error())
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeAgentRequestInvalid, "agent id required")
		return
	}

	// Tenant-scoped Get prevents cross-tenant agents from showing stats
	// (existence-oracle hardening — cross-tenant ID probes look like misses).
	identity, err := s.agentIdentityStore.Get(r.Context(), tenant, id)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if identity == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeAgentNotFound, "agent identity not found")
		return
	}

	// Compute 7-day stats by scanning recent jobs.
	var totalJobs, deniedCount int
	var lastActive int64

	if s.jobStore != nil {
		sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour).UnixMicro()
		// Scan all recent jobs in batches to avoid undercounting for active tenants.
		offset := 0
		batchSize := int64(500)
		for {
			jobs, err := s.jobStore.ListRecentJobs(r.Context(), batchSize)
			if err != nil {
				slog.Warn("agent stats: failed to list recent jobs", "agent_id", id, "error", err)
				break
			}
			if len(jobs) <= offset {
				break
			}
			batch := jobs[offset:]
			inWindow := false
			for _, job := range batch {
				if job.UpdatedAt < sevenDaysAgo {
					continue
				}
				inWindow = true
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
			if len(jobs) < int(batchSize) || !inWindow {
				break
			}
			offset = len(jobs)
			batchSize = min(batchSize*2, 5000)
		}
	}

	writeJSON(w, map[string]any{
		"agent_id":      id,
		"total_jobs_7d": totalJobs,
		"denied_7d":     deniedCount,
		"last_active":   lastActive,
	})
}

// resolveAgentForAudit looks up agent identity from the agent store by ID.
// Returns (agentID, agentName, agentRiskTier). For unlinked or missing agents,
// returns ("unlinked", "unlinked", "").
func (s *server) resolveAgentForAudit(ctx context.Context, agentID string) (string, string, string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "unlinked", "unlinked", ""
	}
	if s.agentIdentityStore == nil {
		return agentID, agentID, ""
	}
	// Audit enrichment is a system-internal lookup — tenant scoping is
	// enforced at the calling handler boundary, not here. Pass empty
	// tenantID to skip the store-level tenant check.
	agent, err := s.agentIdentityStore.Get(ctx, "", agentID)
	if err != nil || agent == nil {
		return agentID, agentID, ""
	}
	return agent.ID, agent.Name, agent.RiskTier
}
