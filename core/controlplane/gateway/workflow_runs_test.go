package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wf "github.com/cordum/cordum/core/workflow"
)

func TestWorkflowRunHandlers(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	wfDef := &wf.Workflow{
		ID:    "wf-run",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &wf.WorkflowRun{
		ID:         "run-1",
		WorkflowID: wfDef.ID,
		OrgID:      "default",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step": {StepID: "step", Status: wf.StepStatusRunning},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := s.workflowStore.AppendTimelineEvent(context.Background(), run.ID, &wf.TimelineEvent{Time: time.Now().UTC(), Type: "job.dispatched"}); err != nil {
		t.Fatalf("append timeline: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+wfDef.ID+"/runs", nil)
	listReq.SetPathValue("id", wfDef.ID)
	listRec := httptest.NewRecorder()
	s.handleListRuns(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list runs: %d %s", listRec.Code, listRec.Body.String())
	}

	allReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs", nil)
	allRec := httptest.NewRecorder()
	s.handleListAllRuns(allRec, allReq)
	if allRec.Code != http.StatusOK {
		t.Fatalf("list all runs: %d %s", allRec.Code, allRec.Body.String())
	}

	timelineReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+run.ID+"/timeline", nil)
	timelineReq.SetPathValue("id", run.ID)
	timelineRec := httptest.NewRecorder()
	s.handleGetRunTimeline(timelineRec, timelineReq)
	if timelineRec.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", timelineRec.Code, timelineRec.Body.String())
	}
	var events []map[string]any
	if err := json.NewDecoder(timelineRec.Body).Decode(&events); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected timeline events")
	}

	rerunBody, _ := json.Marshal(map[string]any{"dry_run": true})
	rerunReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/"+run.ID+"/rerun", bytes.NewReader(rerunBody))
	rerunReq.SetPathValue("id", run.ID)
	rerunRec := httptest.NewRecorder()
	s.handleRerunRun(rerunRec, rerunReq)
	if rerunRec.Code != http.StatusOK {
		t.Fatalf("rerun: %d %s", rerunRec.Code, rerunRec.Body.String())
	}
	var rerunResp map[string]string
	_ = json.NewDecoder(rerunRec.Body).Decode(&rerunResp)
	if rerunResp["run_id"] == "" {
		t.Fatalf("expected rerun id")
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs/"+run.ID+"/cancel", nil)
	cancelReq.SetPathValue("run_id", run.ID)
	cancelRec := httptest.NewRecorder()
	s.handleCancelRun(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusNoContent {
		t.Fatalf("cancel run: %d %s", cancelRec.Code, cancelRec.Body.String())
	}
}
