package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
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
	listReq.Header.Set("X-Tenant-ID", "default")
	listReq.SetPathValue("id", wfDef.ID)
	listRec := httptest.NewRecorder()
	s.handleListRuns(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list runs: %d %s", listRec.Code, listRec.Body.String())
	}

	allReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs", nil)
	allReq.Header.Set("X-Tenant-ID", "default")
	allRec := httptest.NewRecorder()
	s.handleListAllRuns(allRec, allReq)
	if allRec.Code != http.StatusOK {
		t.Fatalf("list all runs: %d %s", allRec.Code, allRec.Body.String())
	}

	timelineReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+run.ID+"/timeline", nil)
	timelineReq.Header.Set("X-Tenant-ID", "default")
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
	rerunReq.Header.Set("X-Tenant-ID", "default")
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
	cancelReq.Header.Set("X-Tenant-ID", "default")
	cancelReq.SetPathValue("run_id", run.ID)
	cancelRec := httptest.NewRecorder()
	s.handleCancelRun(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusNoContent {
		t.Fatalf("cancel run: %d %s", cancelRec.Code, cancelRec.Body.String())
	}
}

func TestHandleStartRunRejectsDisallowedMemoryID(t *testing.T) {
	s, _, _ := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-memory",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"context": map[string]any{
				"allowed_memory_ids": []string{"repo:*"},
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"memory_id": "kb:secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", wfDef.ID)
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowRunCursorIsMicroseconds(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	wfDef := &wf.Workflow{
		ID:    "wf-cursor",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		run := &wf.WorkflowRun{
			ID:         "run-cursor-" + strconv.Itoa(i),
			WorkflowID: wfDef.ID,
			OrgID:      "default",
			Status:     wf.RunStatusRunning,
			Steps: map[string]*wf.StepRun{
				"step": {StepID: "step", Status: wf.StepStatusRunning},
			},
			CreatedAt: now.Add(time.Duration(-i) * time.Second),
			UpdatedAt: now.Add(time.Duration(-i) * time.Second),
		}
		if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs?limit=1", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleListAllRuns(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *int64            `json:"next_cursor"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NextCursor == nil {
		t.Fatal("expected next_cursor for pagination")
	}
	cursor := *resp.NextCursor
	// Microsecond cursors are > 1e12 (year ~2001 in micros ≈ 9.78e14)
	if cursor < 1_000_000_000_000 {
		t.Fatalf("cursor %d appears to be in seconds, expected microseconds", cursor)
	}

	// Verify round-trip: passing microsecond cursor back should work
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs?limit=1&cursor="+strconv.FormatInt(cursor, 10), nil)
	req2.Header.Set("X-Tenant-ID", "default")
	rec2 := httptest.NewRecorder()
	s.handleListAllRuns(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unexpected status on page 2: %d %s", rec2.Code, rec2.Body.String())
	}
}
