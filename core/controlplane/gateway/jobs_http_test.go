package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/memory"
)

func TestHandleSubmitJobHTTP(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	jobID := resp["job_id"]
	if jobID == "" {
		t.Fatalf("missing job_id")
	}

	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil || state != scheduler.JobStatePending {
		t.Fatalf("unexpected job state: %v %v", state, err)
	}
	topic, _ := s.jobStore.GetTopic(context.Background(), jobID)
	if topic != "job.test" {
		t.Fatalf("unexpected topic: %s", topic)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one bus publish, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectSubmit {
		t.Fatalf("unexpected publish subject: %s", bus.published[0].subject)
	}
}

func TestHandleListJobsAndGetJob(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-1"

	if err := s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")

	ctxKey := memory.MakeContextKey(jobID)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}
	resKey := memory.MakeResultKey(jobID)
	if err := s.memStore.PutResult(ctx, resKey, []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("put result: %v", err)
	}
	resPtr := memory.PointerForKey(resKey)
	if err := s.jobStore.SetResultPtr(ctx, jobID, resPtr); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?state=PENDING&topic=job.test", nil)
	listRec := httptest.NewRecorder()
	s.handleListJobs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d", listRec.Code)
	}
	var listResp map[string]any
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items, ok := listResp["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected items in list response")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	getReq.SetPathValue("id", jobID)
	getRec := httptest.NewRecorder()
	s.handleGetJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d", getRec.Code)
	}
	var jobResp map[string]any
	if err := json.NewDecoder(getRec.Body).Decode(&jobResp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if jobResp["id"] != jobID {
		t.Fatalf("unexpected job id")
	}
	if jobResp["topic"] != "job.test" {
		t.Fatalf("unexpected topic in job response")
	}
	if jobResp["context"] == nil {
		t.Fatalf("expected context in job response")
	}
	if jobResp["result"] == nil {
		t.Fatalf("expected result in job response")
	}
}

func TestHandleCancelJob(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-cancel"
	if err := s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/cancel", nil)
	cancelReq.SetPathValue("id", jobID)
	cancelRec := httptest.NewRecorder()
	s.handleCancelJob(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("unexpected cancel status: %d", cancelRec.Code)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) == 0 {
		t.Fatalf("expected cancel publish")
	}
	if bus.published[len(bus.published)-1].subject != capsdk.SubjectCancel {
		t.Fatalf("unexpected cancel subject: %s", bus.published[len(bus.published)-1].subject)
	}

}