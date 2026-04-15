package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateAgent(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body := bytes.NewBufferString(`{
		"name": "fraud-detector",
		"owner": "risk-team",
		"risk_tier": "high",
		"team": "risk",
		"description": "Detects fraudulent transactions",
		"allowed_topics": ["job.fraud-detection.process"],
		"data_classifications": ["pii", "financial"]
	}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &AuthContext{
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
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(tt.body)), &AuthContext{
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

func TestListAgents(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Create 3 agents
	for _, name := range []string{"agent-a", "agent-b", "agent-c"} {
		body := bytes.NewBufferString(`{"name":"` + name + `","owner":"admin","risk_tier":"low"}`)
		req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &AuthContext{
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
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &AuthContext{
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

	// Create an agent
	body := bytes.NewBufferString(`{"name":"get-me","owner":"admin","risk_tier":"medium"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &AuthContext{
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
	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.ID, nil), &AuthContext{
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
	notFoundReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/nonexistent", nil), &AuthContext{
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

	// Create an agent
	body := bytes.NewBufferString(`{"name":"delete-me","owner":"admin","risk_tier":"low"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &AuthContext{
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
	delReq := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.ID, nil), &AuthContext{
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
	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.ID, nil), &AuthContext{
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

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/nonexistent", nil), &AuthContext{
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

	body := bytes.NewBufferString(`{"name":"updated"}`)
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/nonexistent", body), &AuthContext{
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

	// Create
	body := bytes.NewBufferString(`{"name":"original","owner":"admin","risk_tier":"low","team":"eng"}`)
	createReq := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents", body), &AuthContext{
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
	updateReq := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.ID, updateBody), &AuthContext{
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
