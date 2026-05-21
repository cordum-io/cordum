package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/mcp"
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

func TestAgentIdentityAPISurfacesMCPAllowlists(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	created := createAgentWithMCPAllowlists(t, s)
	requireAgentMCPFields(t, created, []string{"prod-mcp"}, []string{"repo.*"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	requireAgentPreapprovedFields(t, created, []string{"cordum_install_pack"})

	stored, err := s.agentIdentityStore.Get(ctx, "default", created.ID)
	if err != nil {
		t.Fatalf("get stored agent: %v", err)
	}
	requireStoreMCPFields(t, stored, []string{"prod-mcp"}, []string{"repo.*"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	requireStorePreapprovedFields(t, stored, []string{"cordum_install_pack"})

	got := getAgentViaHandler(t, s, created.ID)
	requireAgentMCPFields(t, got, []string{"prod-mcp"}, []string{"repo.*"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	requireAgentPreapprovedFields(t, got, []string{"cordum_install_pack"})

	listed := listAgentViaHandler(t, s, created.ID)
	requireAgentMCPFields(t, listed, []string{"prod-mcp"}, []string{"repo.*"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	requireAgentPreapprovedFields(t, listed, []string{"cordum_install_pack"})

	updated := updateAgentMCPAllowlists(t, s, created.ID)
	requireAgentMCPFields(t, updated, []string{"stage-mcp"}, []string{"repo.*"}, nil, []string{"repo.write"})
	requireAgentPreapprovedFields(t, updated, []string{"cordum_publish_release"})
	afterUpdate, err := s.agentIdentityStore.Get(ctx, "default", created.ID)
	if err != nil {
		t.Fatalf("get updated stored agent: %v", err)
	}
	requireStoreMCPFields(t, afterUpdate, []string{"stage-mcp"}, []string{"repo.*"}, []string{}, []string{"repo.write"})
	requireStorePreapprovedFields(t, afterUpdate, []string{"cordum_publish_release"})
	assertStringSlice(t, "AllowedTopics preserved", afterUpdate.AllowedTopics, []string{"job.repo"})
	assertStringSlice(t, "DataClassifications preserved", afterUpdate.DataClassifications, []string{"internal"})

	cleared := clearAgentPreapprovals(t, s, created.ID)
	requireAgentPreapprovedFields(t, cleared, []string{})
	afterClear, err := s.agentIdentityStore.Get(ctx, "default", created.ID)
	if err != nil {
		t.Fatalf("get cleared stored agent: %v", err)
	}
	requireStorePreapprovedFields(t, afterClear, []string{})
}

func TestAgentResponseFromIdentityCopiesAllowlistSlices(t *testing.T) {
	src := &store.AgentIdentity{
		ID: "agent-copy", Name: "copy", Owner: "admin", RiskTier: "high", Status: "active",
		AllowedServers: []string{"prod-mcp"}, AllowedTools: []string{"repo.*"},
		AllowedResources: []string{"cordum://repos/*"}, Entitlements: []string{"repo.read"},
		PreapprovedMutatingTools: []string{"cordum_install_pack"},
	}
	resp := agentResponseFromIdentity(src)
	resp.AllowedServers[0] = "mutated-server"
	resp.AllowedTools[0] = "mutated-tool"
	resp.AllowedResources[0] = "mutated-resource"
	resp.Entitlements[0] = "mutated-entitlement"
	resp.PreapprovedMutatingTools[0] = "mutated-preapproval"
	requireStoreMCPFields(t, src, []string{"prod-mcp"}, []string{"repo.*"}, []string{"cordum://repos/*"}, []string{"repo.read"})
	requireStorePreapprovedFields(t, src, []string{"cordum_install_pack"})
}

func createAgentWithMCPAllowlists(t *testing.T, s *server) agentResponse {
	t.Helper()
	body := bytes.NewBufferString(`{
		"name":"repo-bot","owner":"admin","risk_tier":"high",
		"allowed_topics":["job.repo"],"allowed_tools":["repo.*"],
		"allowed_servers":["prod-mcp"],"allowed_resources":["cordum://repos/*"],
		"entitlements":["repo.read"],"preapproved_mutating_tools":["cordum_install_pack"],
		"data_classifications":["internal"]
	}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin-user",
	})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleCreateAgent(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create agent: got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeAgentResponse(t, rr)
}

func getAgentViaHandler(t *testing.T, s *server, id string) agentResponse {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin-user",
	})
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.handleGetAgent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get agent: got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeAgentResponse(t, rr)
}

func listAgentViaHandler(t *testing.T, s *server, id string) agentResponse {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin-user",
	})
	rr := httptest.NewRecorder()
	s.handleListAgents(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list agents: got %d: %s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Items []agentResponse `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, item := range listResp.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("agent %s not found in list response", id)
	return agentResponse{}
}

func updateAgentMCPAllowlists(t *testing.T, s *server, id string) agentResponse {
	t.Helper()
	body := bytes.NewBufferString(`{
		"allowed_servers":["stage-mcp"],
		"allowed_resources":[],
		"entitlements":["repo.write"],
		"preapproved_mutating_tools":["cordum_publish_release"]
	}`)
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, body), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin-user",
	})
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.handleUpdateAgent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update agent: got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeAgentResponse(t, rr)
}

func clearAgentPreapprovals(t *testing.T, s *server, id string) agentResponse {
	t.Helper()
	body := bytes.NewBufferString(`{"preapproved_mutating_tools":[]}`)
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, body), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin-user",
	})
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.handleUpdateAgent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear preapprovals: got %d: %s", rr.Code, rr.Body.String())
	}
	return decodeAgentResponse(t, rr)
}

func decodeAgentResponse(t *testing.T, rr *httptest.ResponseRecorder) agentResponse {
	t.Helper()
	var resp agentResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode agent response: %v", err)
	}
	return resp
}

func requireAgentMCPFields(
	t *testing.T,
	got agentResponse,
	servers, tools, resources, entitlements []string,
) {
	t.Helper()
	assertStringSlice(t, "response AllowedServers", got.AllowedServers, servers)
	assertStringSlice(t, "response AllowedTools", got.AllowedTools, tools)
	assertStringSlice(t, "response AllowedResources", got.AllowedResources, resources)
	assertStringSlice(t, "response Entitlements", got.Entitlements, entitlements)
}

func requireAgentPreapprovedFields(t *testing.T, got agentResponse, preapproved []string) {
	t.Helper()
	assertStringSlice(t, "response PreapprovedMutatingTools", got.PreapprovedMutatingTools, preapproved)
}

func requireStoreMCPFields(
	t *testing.T,
	got *store.AgentIdentity,
	servers, tools, resources, entitlements []string,
) {
	t.Helper()
	if got == nil {
		t.Fatal("stored identity is nil")
	}
	assertStringSlice(t, "store AllowedServers", got.AllowedServers, servers)
	assertStringSlice(t, "store AllowedTools", got.AllowedTools, tools)
	assertStringSlice(t, "store AllowedResources", got.AllowedResources, resources)
	assertStringSlice(t, "store Entitlements", got.Entitlements, entitlements)
}

func requireStorePreapprovedFields(t *testing.T, got *store.AgentIdentity, preapproved []string) {
	t.Helper()
	if got == nil {
		t.Fatal("stored identity is nil")
	}
	assertStringSlice(t, "store PreapprovedMutatingTools", got.PreapprovedMutatingTools, preapproved)
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
			requireStableErrorCode(t, rr, tt.wantCode, "AGENT_REQUEST_INVALID")
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
	requireStableErrorCode(t, notFoundRR, http.StatusNotFound, "AGENT_NOT_FOUND")
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
	requireStableErrorCode(t, rr, http.StatusNotFound, "AGENT_NOT_FOUND")
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
	requireStableErrorCode(t, rr, http.StatusNotFound, "AGENT_NOT_FOUND")
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
		TenantID: "default", Name: "high-vol-agent", Owner: "admin", RiskTier: "high",
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

// TestHandleCreateAgent_PersistsTenantID verifies that handleCreateAgent
// stamps the caller tenant onto the persisted AgentIdentity instead of
// writing a global tenant_id="" entry that any tenant can read.
// Regression for PR #276 audit finding (CRITICAL, handlers_agents.go:80-127).
func TestHandleCreateAgent_PersistsTenantID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	body := bytes.NewBufferString(`{"name":"tenant-a-bot","owner":"admin","risk_tier":"low"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &auth.AuthContext{
		Tenant: "tenant-a", Role: "admin", PrincipalID: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleCreateAgent(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp agentResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Persisted record MUST carry TenantID = caller tenant, not "".
	stored, err := s.agentIdentityStore.Get(ctx, "tenant-a", resp.ID)
	if err != nil {
		t.Fatalf("get persisted agent: %v", err)
	}
	if stored == nil {
		t.Fatalf("persisted agent not found under tenant-a — likely written with empty TenantID")
	}
	if stored.TenantID != "tenant-a" {
		t.Fatalf("expected TenantID=tenant-a on persisted record, got %q — record is GLOBAL and visible to every tenant", stored.TenantID)
	}
}

// TestHandleAgentByID_RejectsCrossTenant verifies that handleGetAgent,
// handleUpdateAgent, handleDeleteAgent, and handleAgentStats all reject
// access to another tenant's agent identity by UUID guess with 404 (not 403)
// to avoid existence-oracle leakage.
// Regression for PR #276 audit finding (CRITICAL, handlers_agents.go:244-403).
func TestHandleAgentByID_RejectsCrossTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	// Seed tenant-b's agent directly via store.
	victim, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		TenantID: "tenant-b", Name: "victim", Owner: "admin", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("seed victim agent: %v", err)
	}

	cases := []struct {
		op                     string
		exec                   func() *httptest.ResponseRecorder
		wantStatusOnSoftDelete int
	}{
		{
			op: "GET",
			exec: func() *httptest.ResponseRecorder {
				req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+victim.ID, nil), &auth.AuthContext{
					Tenant: "tenant-a", Role: "admin",
				})
				req.SetPathValue("id", victim.ID)
				rr := httptest.NewRecorder()
				s.handleGetAgent(rr, req)
				return rr
			},
		},
		{
			op: "UPDATE",
			exec: func() *httptest.ResponseRecorder {
				body := bytes.NewBufferString(`{"risk_tier":"critical"}`)
				req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+victim.ID, body), &auth.AuthContext{
					Tenant: "tenant-a", Role: "admin",
				})
				req.Header.Set("Content-Type", "application/json")
				req.SetPathValue("id", victim.ID)
				rr := httptest.NewRecorder()
				s.handleUpdateAgent(rr, req)
				return rr
			},
		},
		{
			op: "DELETE",
			exec: func() *httptest.ResponseRecorder {
				req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+victim.ID, nil), &auth.AuthContext{
					Tenant: "tenant-a", Role: "admin",
				})
				req.SetPathValue("id", victim.ID)
				rr := httptest.NewRecorder()
				s.handleDeleteAgent(rr, req)
				return rr
			},
		},
		{
			op: "STATS",
			exec: func() *httptest.ResponseRecorder {
				req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+victim.ID+"/stats", nil), &auth.AuthContext{
					Tenant: "tenant-a", Role: "admin",
				})
				req.SetPathValue("id", victim.ID)
				rr := httptest.NewRecorder()
				s.handleAgentStats(rr, req)
				return rr
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			rr := tc.exec()
			if rr.Code != http.StatusNotFound {
				t.Fatalf("CROSS-TENANT %s ALLOWED: tenant-a accessed tenant-b's agent; expected 404, got %d (body=%s)", tc.op, rr.Code, rr.Body.String())
			}
			// Existence-oracle hardening: must return AGENT_NOT_FOUND, not a
			// FORBIDDEN-style code (which would leak that the ID exists).
			if !bytes.Contains(rr.Body.Bytes(), []byte("AGENT_NOT_FOUND")) {
				t.Fatalf("expected AGENT_NOT_FOUND (existence-oracle hardening), got: %s", rr.Body.String())
			}
		})
	}

	// Victim record must be intact: same TenantID, original Name, unmodified RiskTier, not revoked.
	after, err := s.agentIdentityStore.Get(ctx, "tenant-b", victim.ID)
	if err != nil {
		t.Fatalf("get victim after cross-tenant attempts: %v", err)
	}
	if after == nil {
		t.Fatalf("REGRESSION: victim agent was deleted by cross-tenant DELETE")
	}
	if after.RiskTier != "low" {
		t.Fatalf("REGRESSION: victim risk_tier mutated by cross-tenant UPDATE (got %q, want low)", after.RiskTier)
	}
	if after.Status == "revoked" {
		t.Fatalf("REGRESSION: victim status set to revoked by cross-tenant DELETE")
	}
}

func TestHandleListAgentsTenantScoped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	seeded := []store.AgentIdentity{
		{
			TenantID:                 "tenant-a",
			ID:                       "agent-tenant-a",
			Name:                     "tenant-a-agent",
			Owner:                    "tenant-a-owner",
			Team:                     "red",
			RiskTier:                 "high",
			AllowedServers:           []string{"tenant-a-mcp"},
			AllowedTools:             []string{"tenant-a.tool"},
			AllowedResources:         []string{"cordum://tenant-a/*"},
			Entitlements:             []string{"tenant-a.entitlement"},
			PreapprovedMutatingTools: []string{"tenant-a.mutate"},
		},
		{
			TenantID:                 "tenant-b",
			ID:                       "agent-tenant-b",
			Name:                     "tenant-b-agent",
			Owner:                    "tenant-b-owner",
			Team:                     "blue",
			RiskTier:                 "critical",
			AllowedServers:           []string{"tenant-b-mcp"},
			AllowedTools:             []string{"tenant-b.tool"},
			AllowedResources:         []string{"cordum://tenant-b/*"},
			Entitlements:             []string{"tenant-b.entitlement"},
			PreapprovedMutatingTools: []string{"tenant-b.mutate"},
		},
	}
	for _, identity := range seeded {
		if _, err := s.agentIdentityStore.Create(ctx, identity); err != nil {
			t.Fatalf("seed %s: %v", identity.ID, err)
		}
	}

	tenantA := listAgentsForTenant(t, s, "tenant-a")
	requireSingleTenantAgent(t, tenantA, "agent-tenant-a", "tenant-a-mcp", "tenant-a.tool", "cordum://tenant-a/*", "tenant-a.entitlement", "tenant-a.mutate")
	tenantB := listAgentsForTenant(t, s, "tenant-b")
	requireSingleTenantAgent(t, tenantB, "agent-tenant-b", "tenant-b-mcp", "tenant-b.tool", "cordum://tenant-b/*", "tenant-b.entitlement", "tenant-b.mutate")
}

func TestMCPSetAgentScopeRoundTripPreapprovedMutatingTools(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()
	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		TenantID: "default",
		ID:       "mcp-scope-agent",
		Name:     "MCP Scope Agent",
		Owner:    "ops",
		RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/api/v1/agents/") {
			t.Errorf("unexpected bridge request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		r.SetPathValue("id", strings.TrimPrefix(r.URL.Path, "/api/v1/agents/"))
		authed := withAuth(r, &auth.AuthContext{
			Tenant:      "default",
			Role:        "admin",
			PrincipalID: "mcp-operator",
		})
		s.handleUpdateAgent(w, authed)
	}))
	t.Cleanup(srv.Close)

	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "default",
		HTTPClient:        srv.Client(),
		AllowPrivateHosts: true,
	})
	out, err := bridge.SetAgentScope(ctx, mcp.SetAgentScopeInput{
		AgentID:                  agent.ID,
		AllowedTools:             []string{"cordum_list_jobs"},
		PreapprovedMutatingTools: []string{"cordum_install_pack"},
	})
	if err != nil {
		t.Fatalf("SetAgentScope: %v", err)
	}
	assertStringSlice(t, "bridge PreapprovedMutatingTools", out.PreapprovedMutatingTools, []string{"cordum_install_pack"})

	stored, err := s.agentIdentityStore.Get(ctx, "default", agent.ID)
	if err != nil {
		t.Fatalf("get stored agent: %v", err)
	}
	assertStringSlice(t, "stored AllowedTools", stored.AllowedTools, []string{"cordum_list_jobs"})
	requireStorePreapprovedFields(t, stored, []string{"cordum_install_pack"})
}

func listAgentsForTenant(t *testing.T, s *server, tenant string) []agentResponse {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
		Tenant: tenant, Role: "admin", PrincipalID: "admin-" + tenant,
	})
	rr := httptest.NewRecorder()
	s.handleListAgents(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list agents for %s: got %d: %s", tenant, rr.Code, rr.Body.String())
	}
	var listResp struct {
		Items []agentResponse `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list for %s: %v", tenant, err)
	}
	return listResp.Items
}

func requireSingleTenantAgent(
	t *testing.T,
	items []agentResponse,
	wantID, wantServer, wantTool, wantResource, wantEntitlement, wantPreapproved string,
) {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("expected one tenant-scoped item, got %d: %#v", len(items), items)
	}
	got := items[0]
	if got.ID != wantID {
		t.Fatalf("list returned agent %q, want %q", got.ID, wantID)
	}
	assertStringSlice(t, "AllowedServers", got.AllowedServers, []string{wantServer})
	assertStringSlice(t, "AllowedTools", got.AllowedTools, []string{wantTool})
	assertStringSlice(t, "AllowedResources", got.AllowedResources, []string{wantResource})
	assertStringSlice(t, "Entitlements", got.Entitlements, []string{wantEntitlement})
	assertStringSlice(t, "PreapprovedMutatingTools", got.PreapprovedMutatingTools, []string{wantPreapproved})
}

func TestListAgentsIncludesLastActive(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	ctx := context.Background()

	// Create two agents.
	agentA, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		TenantID: "default", Name: "active-agent", Owner: "admin", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	agentB, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		TenantID: "default", Name: "quiet-agent", Owner: "admin", RiskTier: "low",
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
