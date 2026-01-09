package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	wf "github.com/cordum/cordum/core/workflow"
)

func TestWorkflowLifecycleHandlers(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	payload := map[string]any{
		"id":     "wf-approve",
		"org_id": "default",
		"name":   "Approval Only",
		"steps": map[string]any{
			"approve": map[string]any{
				"type": "approval",
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", rr.Code, rr.Body.String())
	}
	var createResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &createResp)
	wfID, _ := createResp["id"].(string)
	if wfID == "" {
		t.Fatalf("workflow id missing")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	listRR := httptest.NewRecorder()
	s.handleListWorkflows(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list workflows: %d %s", listRR.Code, listRR.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+wfID, nil)
	getReq.SetPathValue("id", wfID)
	getRR := httptest.NewRecorder()
	s.handleGetWorkflow(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get workflow: %d %s", getRR.Code, getRR.Body.String())
	}

	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfID+"/runs", bytes.NewReader([]byte(`{}`)))
	runReq.SetPathValue("id", wfID)
	runRR := httptest.NewRecorder()
	s.handleStartRun(runRR, runReq)
	if runRR.Code != http.StatusOK {
		t.Fatalf("start run: %d %s", runRR.Code, runRR.Body.String())
	}
	var runResp map[string]any
	_ = json.Unmarshal(runRR.Body.Bytes(), &runResp)
	runID, _ := runResp["run_id"].(string)
	if runID == "" {
		t.Fatalf("run id missing")
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfID+"/runs/"+runID+"/steps/approve/approve", bytes.NewReader([]byte(`{"approved":true}`)))
	approveReq.SetPathValue("id", wfID)
	approveReq.SetPathValue("run_id", runID)
	approveReq.SetPathValue("step_id", "approve")
	approveRR := httptest.NewRecorder()
	s.handleApproveStep(approveRR, approveReq)
	if approveRR.Code != http.StatusNoContent {
		t.Fatalf("approve step: %d %s", approveRR.Code, approveRR.Body.String())
	}

	runGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+runID, nil)
	runGetReq.SetPathValue("id", runID)
	runGetRR := httptest.NewRecorder()
	s.handleGetRun(runGetRR, runGetReq)
	if runGetRR.Code != http.StatusOK {
		t.Fatalf("get run: %d %s", runGetRR.Code, runGetRR.Body.String())
	}

	deleteRunReq := httptest.NewRequest(http.MethodDelete, "/api/v1/workflow-runs/"+runID, nil)
	deleteRunReq.SetPathValue("id", runID)
	deleteRunRR := httptest.NewRecorder()
	s.handleDeleteRun(deleteRunRR, deleteRunReq)
	if deleteRunRR.Code != http.StatusNoContent {
		t.Fatalf("delete run: %d %s", deleteRunRR.Code, deleteRunRR.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/workflows/"+wfID, nil)
	deleteReq.SetPathValue("id", wfID)
	deleteRR := httptest.NewRecorder()
	s.handleDeleteWorkflow(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("delete workflow: %d %s", deleteRR.Code, deleteRR.Body.String())
	}
}
