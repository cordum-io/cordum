package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/maputil"
	wf "github.com/cordum/cordum/core/workflow"
)

type fakeRunFailurePersistenceStore struct {
	run               *wf.WorkflowRun
	getRunErr         error
	updateRunErr      error
	appendTimelineErr error
	updatedRun        *wf.WorkflowRun
	appendedRunID     string
	appendedTimeline  *wf.TimelineEvent
}

func (f *fakeRunFailurePersistenceStore) GetRun(_ context.Context, _ string) (*wf.WorkflowRun, error) {
	if f.getRunErr != nil {
		return nil, f.getRunErr
	}
	if f.run == nil {
		return nil, nil
	}
	runCopy := *f.run
	runCopy.Error = cloneAnyMap(f.run.Error)
	runCopy.Input = cloneAnyMap(f.run.Input)
	runCopy.Context = cloneAnyMap(f.run.Context)
	runCopy.Labels = cloneStringMap(f.run.Labels)
	runCopy.Metadata = cloneStringMap(f.run.Metadata)
	if f.run.Steps != nil {
		runCopy.Steps = make(map[string]*wf.StepRun, len(f.run.Steps))
		for key, value := range f.run.Steps {
			runCopy.Steps[key] = value
		}
	}
	return &runCopy, nil
}

func (f *fakeRunFailurePersistenceStore) UpdateRun(_ context.Context, run *wf.WorkflowRun) error {
	if run != nil {
		runCopy := *run
		runCopy.Error = cloneAnyMap(run.Error)
		runCopy.Input = cloneAnyMap(run.Input)
		runCopy.Context = cloneAnyMap(run.Context)
		runCopy.Labels = cloneStringMap(run.Labels)
		runCopy.Metadata = cloneStringMap(run.Metadata)
		if run.Steps != nil {
			runCopy.Steps = make(map[string]*wf.StepRun, len(run.Steps))
			for key, value := range run.Steps {
				runCopy.Steps[key] = value
			}
		}
		f.updatedRun = &runCopy
	}
	return f.updateRunErr
}

func (f *fakeRunFailurePersistenceStore) AppendTimelineEvent(_ context.Context, runID string, event *wf.TimelineEvent) error {
	f.appendedRunID = runID
	if event != nil {
		eventCopy := *event
		f.appendedTimeline = &eventCopy
	}
	return f.appendTimelineErr
}

var cloneAnyMap = maputil.CloneAnyMap
var cloneStringMap = maputil.CloneStringMap

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

func TestHandleStartRunIdempotencyConcurrentRequestsCreateSingleRun(t *testing.T) {
	s, _, _ := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-idempotent",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	runIDs := make([]string, 0, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs", bytes.NewReader([]byte(`{}`)))
			req.Header.Set("X-Tenant-ID", "default")
			req.Header.Set("Idempotency-Key", "same-key")
			req.SetPathValue("id", wfDef.ID)

			<-start

			rec := httptest.NewRecorder()
			s.handleStartRun(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				return
			}

			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Errorf("decode run response: %v", err)
				return
			}
			runID := resp["run_id"]
			if runID == "" {
				t.Error("expected run_id in response")
				return
			}

			mu.Lock()
			runIDs = append(runIDs, runID)
			mu.Unlock()
		}()
	}

	close(start)
	wg.Wait()

	if len(runIDs) != workers {
		t.Fatalf("expected %d run ids, got %d", workers, len(runIDs))
	}

	first := runIDs[0]
	for _, runID := range runIDs[1:] {
		if runID != first {
			t.Fatalf("expected all concurrent requests to return the same run id, got %v", runIDs)
		}
	}

	runs, err := s.workflowStore.ListRunsByWorkflow(context.Background(), wfDef.ID, 20)
	if err != nil {
		t.Fatalf("list runs by workflow: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 persisted run, got %d", len(runs))
	}
	if runs[0].ID != first {
		t.Fatalf("expected persisted run id %s, got %s", first, runs[0].ID)
	}
}

func TestHandleStartRunConcurrentRequestsRespectMaxConcurrentLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-concurrency-limit",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"limits": map[string]any{
				"max_concurrent_runs": 2,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan int, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs", bytes.NewReader([]byte(`{}`)))
			req.Header.Set("X-Tenant-ID", "default")
			req.SetPathValue("id", wfDef.ID)

			<-start

			rec := httptest.NewRecorder()
			s.handleStartRun(rec, req)
			results <- rec.Code
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	okCount := 0
	tooManyCount := 0
	for code := range results {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusTooManyRequests:
			tooManyCount++
		default:
			t.Fatalf("expected only 200 or 429 responses, got %d", code)
		}
	}
	if okCount != 2 || tooManyCount != workers-2 {
		t.Fatalf("expected 2 successes and %d rejections, got %d successes and %d rejections", workers-2, okCount, tooManyCount)
	}

	runs, err := s.workflowStore.ListRunsByWorkflow(context.Background(), wfDef.ID, 20)
	if err != nil {
		t.Fatalf("list runs by workflow: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected exactly 2 persisted runs, got %d", len(runs))
	}

	activeCount, err := s.workflowStore.CountActiveRuns(context.Background(), "default")
	if err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("expected exactly 2 active runs, got %d", activeCount)
	}
}

func TestHandleStartRunRejectedByConcurrencyLimitReleasesIdempotencyReservation(t *testing.T) {
	s, _, _ := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-idempotency-release",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"limits": map[string]any{
				"max_concurrent_runs": 1,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	blockingRun := &wf.WorkflowRun{
		ID:         "run-blocking",
		WorkflowID: wfDef.ID,
		OrgID:      "default",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step": {StepID: "step", Status: wf.StepStatusRunning},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(context.Background(), blockingRun); err != nil {
		t.Fatalf("create blocking run: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Idempotency-Key", "retry-after-limit")
	req.SetPathValue("id", wfDef.ID)
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 while limit is full, got %d: %s", rec.Code, rec.Body.String())
	}

	blockingRun.Status = wf.RunStatusSucceeded
	completedAt := time.Now().UTC()
	blockingRun.CompletedAt = &completedAt
	blockingRun.UpdatedAt = completedAt
	if err := s.workflowStore.UpdateRun(context.Background(), blockingRun); err != nil {
		t.Fatalf("complete blocking run: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/runs", bytes.NewReader([]byte(`{}`)))
	retryReq.Header.Set("X-Tenant-ID", "default")
	retryReq.Header.Set("Idempotency-Key", "retry-after-limit")
	retryReq.SetPathValue("id", wfDef.ID)
	retryRec := httptest.NewRecorder()
	s.handleStartRun(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected retry to succeed after slot opens, got %d: %s", retryRec.Code, retryRec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(retryRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	runID := resp["run_id"]
	if runID == "" {
		t.Fatal("expected retry run_id in response")
	}
	run, err := s.workflowStore.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("expected retry run %s to exist: %v", runID, err)
	}
	if run == nil || run.WorkflowID != wfDef.ID {
		t.Fatalf("expected persisted retry run for workflow %s, got %#v", wfDef.ID, run)
	}
}

func TestCleanupRunIdempotencyReservationLogsDeleteFailures(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOutput)

	called := false
	cleanupRunIdempotencyReservation(
		context.Background(),
		"idem-key",
		"run-123",
		"failed to cleanup idempotency key after run creation failure",
		func(context.Context, string) error {
			called = true
			return errors.New("redis unavailable")
		},
	)

	if !called {
		t.Fatal("expected cleanup function to be called")
	}

	logOutput := buf.String()
	for _, want := range []string{
		"failed to cleanup idempotency key after run creation failure",
		"key=idem-key",
		"run_id=run-123",
		`error="redis unavailable"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected log output to contain %q, got %q", want, logOutput)
		}
	}
}

func TestMarkRunFailedAfterStartErrorLogsPersistenceFailures(t *testing.T) {
	var buf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOutput)

	store := &fakeRunFailurePersistenceStore{
		run: &wf.WorkflowRun{
			ID:     "run-log",
			Status: wf.RunStatusPending,
			Steps:  map[string]*wf.StepRun{},
		},
		updateRunErr:      errors.New("update failed"),
		appendTimelineErr: errors.New("timeline failed"),
	}

	markRunFailedAfterStartError(
		context.Background(),
		store,
		"run-log",
		errors.New("engine start failed"),
		"failed to persist run failure status",
		"failed to append run failure timeline event",
	)

	if store.updatedRun == nil {
		t.Fatal("expected failed run update to be attempted")
	}
	if store.updatedRun.Status != wf.RunStatusFailed {
		t.Fatalf("expected failed status, got %s", store.updatedRun.Status)
	}
	if store.updatedRun.CompletedAt == nil {
		t.Fatal("expected completed timestamp to be recorded")
	}
	if got := store.updatedRun.Error["message"]; got != "engine start failed" {
		t.Fatalf("expected stored error message, got %#v", got)
	}
	if store.appendedRunID != "run-log" {
		t.Fatalf("expected timeline append for run-log, got %q", store.appendedRunID)
	}
	if store.appendedTimeline == nil {
		t.Fatal("expected timeline event append to be attempted")
	}
	if store.appendedTimeline.Status != string(wf.RunStatusFailed) {
		t.Fatalf("expected failed timeline status, got %q", store.appendedTimeline.Status)
	}
	if store.appendedTimeline.Message != "engine start failed" {
		t.Fatalf("expected timeline message to match start error, got %q", store.appendedTimeline.Message)
	}

	logOutput := buf.String()
	for _, want := range []string{
		"failed to persist run failure status",
		"failed to append run failure timeline event",
		"run_id=run-log",
		`error="update failed"`,
		`error="timeline failed"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected log output to contain %q, got %q", want, logOutput)
		}
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

func TestHandleDeleteRunCancelsInFlightJobs(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).
		WithMemory(s.memStore).
		WithConfig(s.configSvc).
		WithSchemaRegistry(s.schemaRegistry)

	wfDef := &wf.Workflow{
		ID:    "wf-del-cancel",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &wf.WorkflowRun{
		ID: "run-del-cancel", WorkflowID: wfDef.ID, OrgID: "default",
		Steps:     map[string]*wf.StepRun{},
		Status:    wf.RunStatusPending,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.workflowEng.StartRun(context.Background(), wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Record bus messages before delete.
	bus.mu.Lock()
	beforeCount := len(bus.published)
	bus.mu.Unlock()

	// Delete the running run via the handler.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workflow-runs/"+run.ID, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", run.ID)
	rec := httptest.NewRecorder()
	s.handleDeleteRun(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// CancelRun should have published cancel messages for in-flight steps.
	bus.mu.Lock()
	afterCount := len(bus.published)
	bus.mu.Unlock()

	if afterCount <= beforeCount {
		t.Fatalf("expected cancel messages to be published before deletion, got %d messages before and %d after", beforeCount, afterCount)
	}

	// Run should be gone from the store.
	_, err := s.workflowStore.GetRun(context.Background(), run.ID)
	if err == nil {
		t.Fatal("expected run to be deleted from store")
	}
}
