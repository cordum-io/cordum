package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	redis "github.com/redis/go-redis/v9"
)

func enableAgentIdentityEntitlement(t *testing.T, s *server) {
	t.Helper()
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
}

func TestCreateAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	body := bytes.NewBufferString(`{
		"name": "fraud-detector",
		"owner": "risk-team",
		"risk_tier": "high",
		"team": "risk",
		"description": "Detects fraudulent transactions",
		"allowed_topics": ["job.fraud-detection.process"],
		"data_classifications": ["pii", "financial"]
	}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-user",
	})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCreateAgent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp agentResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected generated ID")
	}
	if resp.Name != "fraud-detector" {
		t.Fatalf("expected name fraud-detector, got %q", resp.Name)
	}
	if resp.RiskTier != "high" {
		t.Fatalf("expected risk_tier high, got %q", resp.RiskTier)
	}
	if resp.Status != "active" {
		t.Fatalf("expected default status active, got %q", resp.Status)
	}
	if resp.Owner != "risk-team" {
		t.Fatalf("expected owner risk-team, got %q", resp.Owner)
	}
	if len(resp.DataClassifications) != 2 {
		t.Fatalf("expected 2 data classifications, got %d", len(resp.DataClassifications))
	}
}

func TestCreateAgentValidation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "missing name",
			body:     `{"owner":"admin","risk_tier":"low"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing owner",
			body:     `{"name":"agent","risk_tier":"low"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid risk_tier",
			body:     `{"name":"agent","owner":"admin","risk_tier":"extreme"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty body",
			body:     `{}`,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(tt.body)), &auth.AuthContext{
				Tenant: "default",
				Role:   "admin",
			})
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			s.handleCreateAgent(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d: %s", tt.wantCode, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAgentIdentityHandlersRequireEntitlement(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanTeam, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = false
	})

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-user",
	})
	rr := httptest.NewRecorder()

	s.handleListAgents(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"code":"tier_limit_exceeded"`)) {
		t.Fatalf("expected tier_limit_exceeded response, got %s", rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"limit":"agent_identity"`)) {
		t.Fatalf("expected agent_identity limit key, got %s", rr.Body.String())
	}
}

func TestListAgents(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	// Create 3 agents
	for _, name := range []string{"agent-a", "agent-b", "agent-c"} {
		body := bytes.NewBufferString(`{"name":"` + name + `","owner":"admin","risk_tier":"low"}`)
		req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
			Tenant: "default",
			Role:   "admin",
		})
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.handleCreateAgent(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s: expected 201, got %d: %s", name, rr.Code, rr.Body.String())
		}
	}

	// List all
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	rr := httptest.NewRecorder()
	s.handleListAgents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var listResp struct {
		Items []agentResponse `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(listResp.Items))
	}
}

func TestGetAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	// Create an agent
	body := bytes.NewBufferString(`{"name":"get-me","owner":"admin","risk_tier":"medium"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	s.handleCreateAgent(createRR, createReq)

	var created agentResponse
	if err := json.NewDecoder(createRR.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// GET by ID
	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.ID, nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	getReq.SetPathValue("id", created.ID)
	getRR := httptest.NewRecorder()
	s.handleGetAgent(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRR.Code, getRR.Body.String())
	}

	var got agentResponse
	if err := json.NewDecoder(getRR.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Name != "get-me" {
		t.Fatalf("expected name get-me, got %q", got.Name)
	}

	// GET nonexistent
	notFoundReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/nonexistent", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	notFoundReq.SetPathValue("id", "nonexistent")
	notFoundRR := httptest.NewRecorder()
	s.handleGetAgent(notFoundRR, notFoundReq)

	if notFoundRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", notFoundRR.Code)
	}
}

func TestDeleteAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	// Create an agent
	body := bytes.NewBufferString(`{"name":"delete-me","owner":"admin","risk_tier":"low"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	s.handleCreateAgent(createRR, createReq)

	var created agentResponse
	if err := json.NewDecoder(createRR.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// DELETE
	delReq := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.ID, nil), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-user",
	})
	delReq.SetPathValue("id", created.ID)
	delRR := httptest.NewRecorder()
	s.handleDeleteAgent(delRR, delReq)

	if delRR.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", delRR.Code, delRR.Body.String())
	}

	// Verify soft-deleted (GET should still return it with status=revoked)
	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.ID, nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	getReq.SetPathValue("id", created.ID)
	getRR := httptest.NewRecorder()
	s.handleGetAgent(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200 for soft-deleted, got %d", getRR.Code)
	}

	var got agentResponse
	if err := json.NewDecoder(getRR.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Status != "revoked" {
		t.Fatalf("expected status revoked, got %q", got.Status)
	}
}

func TestDeleteAgentNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/nonexistent", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("id", "nonexistent")
	rr := httptest.NewRecorder()
	s.handleDeleteAgent(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateAgentNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	body := bytes.NewBufferString(`{"name":"updated"}`)
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/nonexistent", body), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	rr := httptest.NewRecorder()
	s.handleUpdateAgent(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)

	// Create
	body := bytes.NewBufferString(`{"name":"original","owner":"admin","risk_tier":"low","team":"eng"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	s.handleCreateAgent(createRR, createReq)

	var created agentResponse
	if err := json.NewDecoder(createRR.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// Update
	updateBody := bytes.NewBufferString(`{"name":"updated","risk_tier":"critical"}`)
	updateReq := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.ID, updateBody), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-user",
	})
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.SetPathValue("id", created.ID)
	updateRR := httptest.NewRecorder()
	s.handleUpdateAgent(updateRR, updateReq)

	if updateRR.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateRR.Code, updateRR.Body.String())
	}

	var updated agentResponse
	if err := json.NewDecoder(updateRR.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Name != "updated" {
		t.Fatalf("expected name updated, got %q", updated.Name)
	}
	if updated.RiskTier != "critical" {
		t.Fatalf("expected risk_tier critical, got %q", updated.RiskTier)
	}
	if updated.Owner != "admin" {
		t.Fatalf("expected owner preserved, got %q", updated.Owner)
	}
	if updated.Team != "eng" {
		t.Fatalf("expected team preserved, got %q", updated.Team)
	}
}

func TestAgentStatsHighVolume(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	// Create an agent identity.
	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "high-vol-agent", Owner: "admin", RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Seed 1200 jobs in Redis, spread across the last 7 days.
	// 800 belong to our agent (50 denied), 400 belong to another agent.
	now := time.Now()
	rc := s.jobStore.Client()
	totalOurs := 0
	totalDenied := 0
	var latestTs int64

	for i := 0; i < 1200; i++ {
		jobID := fmt.Sprintf("hvjob-%04d", i)
		ts := now.Add(-time.Duration(i) * 5 * time.Minute).UnixMicro()

		// Add to job:recent sorted set.
		rc.ZAdd(ctx, "job:recent", redis.Z{Score: float64(ts), Member: jobID})

		// Determine ownership and state.
		ownerID := "other-agent"
		state := model.JobStateSucceeded
		if i%3 != 0 {
			// 800 of 1200 belong to our agent (indices where i%3 != 0).
			ownerID = agent.ID
			totalOurs++
			if ts > latestTs {
				latestTs = ts
			}
			if i%16 == 1 {
				state = model.JobStateDenied
				totalDenied++
			}
		}

		labels := fmt.Sprintf(`{"agent_id":"%s"}`, ownerID)
		rc.HSet(ctx, "job:meta:"+jobID, "labels", labels, "state", string(state))
		rc.Set(ctx, "job:state:"+jobID, string(state), 0)
	}

	// Verify: our agent should have > 500 jobs (tests the batch boundary).
	if totalOurs < 500 {
		t.Fatalf("test setup: expected > 500 jobs for our agent, got %d", totalOurs)
	}

	// Call the stats endpoint.
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+agent.ID+"/stats", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("id", agent.ID)
	rr := httptest.NewRecorder()
	s.handleAgentStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var stats struct {
		AgentID    string `json:"agent_id"`
		TotalJobs  int    `json:"total_jobs_7d"`
		Denied     int    `json:"denied_7d"`
		LastActive int64  `json:"last_active"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}

	if stats.TotalJobs != totalOurs {
		t.Fatalf("expected total_jobs_7d=%d (>1000 job pool, agent owns %d), got %d", totalOurs, totalOurs, stats.TotalJobs)
	}
	if stats.Denied != totalDenied {
		t.Fatalf("expected denied_7d=%d, got %d", totalDenied, stats.Denied)
	}
	if stats.LastActive != latestTs {
		t.Fatalf("expected last_active=%d, got %d", latestTs, stats.LastActive)
	}
}

func TestListAgentsIncludesLastActive(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	// Create two agents.
	agentA, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "active-agent", Owner: "admin", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	agentB, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "quiet-agent", Owner: "admin", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}

	// Seed a job for agent A only.
	rc := s.jobStore.Client()
	ts := time.Now().Add(-1 * time.Hour).UnixMicro()
	rc.ZAdd(ctx, "job:recent", redis.Z{Score: float64(ts), Member: "la-job-1"})
	rc.HSet(ctx, "job:meta:la-job-1",
		"labels", fmt.Sprintf(`{"agent_id":"%s"}`, agentA.ID),
		"state", string(model.JobStateSucceeded),
	)
	rc.Set(ctx, "job:state:la-job-1", string(model.JobStateSucceeded), 0)

	// List agents — both should appear, only A should have last_active.
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	rr := httptest.NewRecorder()
	s.handleListAgents(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var listResp struct {
		Items []agentResponse `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(listResp.Items))
	}

	found := map[string]int64{}
	for _, item := range listResp.Items {
		found[item.ID] = item.LastActive
	}

	if found[agentA.ID] != ts {
		t.Fatalf("agent A: expected last_active=%d, got %d", ts, found[agentA.ID])
	}
	if found[agentB.ID] != 0 {
		t.Fatalf("agent B: expected last_active=0 (no jobs), got %d", found[agentB.ID])
	}
}
