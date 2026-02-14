package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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
	req.Header.Set("X-Tenant-ID", "default")
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
	if err != nil || state != model.JobStatePending {
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

func TestHandleSubmitJobHTTPRejectsDisallowedMemoryID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"

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

	payload := map[string]any{
		"prompt":    "hello",
		"topic":     "job.test",
		"memory_id": "kb:secret",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPRespectsConcurrentJobsLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"

	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"rate_limits": map[string]any{
				"concurrent_jobs": 1,
				"queue_size":      0,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	seedJobID := "job-seed"
	if err := s.jobStore.SetTenant(ctx, seedJobID, "default"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetState(ctx, seedJobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected too many requests, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleListJobsAndGetJob(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-1"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")

	ctxKey := store.MakeContextKey(jobID)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}
	resKey := store.MakeResultKey(jobID)
	if err := s.memStore.PutResult(ctx, resKey, []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("put result: %v", err)
	}
	resPtr := store.PointerForKey(resKey)
	if err := s.jobStore.SetResultPtr(ctx, jobID, resPtr); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?state=PENDING&topic=job.test", nil)
	listReq.Header.Set("X-Tenant-ID", "tenant")
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
	getReq.Header.Set("X-Tenant-ID", "tenant")
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
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/cancel", nil)
	cancelReq.Header.Set("X-Tenant-ID", "default")
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

func TestHandleRemediateJob(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()

	orig := &pb.JobRequest{
		JobId:    "job-remediate",
		Topic:    "job.db.delete",
		TenantId: "default",
		Labels:   map[string]string{"env": "prod", "keep": "yes"},
		Meta:     &pb.JobMetadata{Capability: "db.delete", Labels: map[string]string{"env": "prod", "keep": "yes"}},
	}
	if err := s.jobStore.SetJobRequest(ctx, orig); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetJobMeta(ctx, orig); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	record := model.SafetyDecisionRecord{
		Decision: model.SafetyDeny,
		Remediations: []*pb.PolicyRemediation{
			{
				Id:                    "archive",
				Title:                 "Archive instead of delete",
				ReplacementTopic:      "job.db.archive",
				ReplacementCapability: "db.archive",
				AddLabels:             map[string]string{"policy": "remediation"},
				RemoveLabels:          []string{"env"},
			},
		},
	}
	if err := s.jobStore.SetSafetyDecision(ctx, orig.GetJobId(), record); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	body := bytes.NewBufferString(`{"remediation_id":"archive"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+orig.GetJobId()+"/remediate", body)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", orig.GetJobId())
	rec := httptest.NewRecorder()
	s.handleRemediateJob(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	newID := resp["job_id"]
	if newID == "" || newID == orig.GetJobId() {
		t.Fatalf("expected new job id")
	}

	newReq, err := s.jobStore.GetJobRequest(ctx, newID)
	if err != nil || newReq == nil {
		t.Fatalf("load new job request: %v", err)
	}
	if newReq.GetTopic() != "job.db.archive" {
		t.Fatalf("unexpected new topic: %s", newReq.GetTopic())
	}
	if newReq.GetMeta().GetCapability() != "db.archive" {
		t.Fatalf("unexpected new capability: %s", newReq.GetMeta().GetCapability())
	}
	if _, ok := newReq.GetLabels()["env"]; ok {
		t.Fatalf("expected env label removed")
	}
	if newReq.GetLabels()["policy"] != "remediation" {
		t.Fatalf("expected remediation label applied")
	}
	if newReq.GetLabels()["keep"] != "yes" {
		t.Fatalf("expected existing label retained")
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) == 0 {
		t.Fatalf("expected publish")
	}
	if bus.published[len(bus.published)-1].subject != capsdk.SubjectSubmit {
		t.Fatalf("unexpected publish subject: %s", bus.published[len(bus.published)-1].subject)
	}
}
