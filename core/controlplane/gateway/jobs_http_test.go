package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	infraSchema "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
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

func TestSubmitJobUnknownTopicRejects400(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:   "job.allowed",
		Pool:   "pool-a",
		Status: topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.unknown",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error_code"] != "unknown_topic" {
		t.Fatalf("expected error_code unknown_topic, got %#v", resp["error_code"])
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("expected no bus publish, got %d", len(bus.published))
	}
}

func TestSubmitJobKnownTopicZeroWorkersAccepted(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:   "job.test",
		Pool:   "pool-a",
		Status: topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
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

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
}

func TestSubmitJobEmptyRegistryAllowsAll(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.unregistered",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
}

func TestSubmitJobSchemaEnforceRejectsInvalidPayload(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	s.schemaEnforcement = infraSchema.EnforcementEnforce
	registerSubmitSchemaTopic(t, s, "job.structured", "demo/input")

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.structured",
		"context": map[string]any{
			"message": 123,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error_code"] != "schema_validation_failed" {
		t.Fatalf("expected schema_validation_failed, got %#v", resp["error_code"])
	}
	violations, ok := resp["violations"].([]any)
	if !ok || len(violations) == 0 {
		t.Fatalf("expected violations array, got %#v", resp["violations"])
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("expected no publish on schema reject, got %d", len(bus.published))
	}
}

func TestSubmitJobSchemaWarnAllowsInvalidPayload(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	s.schemaEnforcement = infraSchema.EnforcementWarn
	registerSubmitSchemaTopic(t, s, "job.structured", "demo/input")

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.structured",
		"context": map[string]any{
			"message": 123,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "submit payload violated topic input schema") {
		t.Fatalf("expected schema warning log, got %q", logBuf.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
}

func TestSubmitJobSchemaOffSkipsValidation(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	s.schemaEnforcement = infraSchema.EnforcementOff
	registerSubmitSchemaTopic(t, s, "job.structured", "demo/input")

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.structured",
		"context": map[string]any{
			"message": 123,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
}

func TestSubmitJobNoSchemaSkipsValidation(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	s.schemaEnforcement = infraSchema.EnforcementEnforce
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:   "job.structured",
		Pool:   "pool-a",
		Status: topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.structured",
		"context": map[string]any{
			"message": 123,
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
}

func registerSubmitSchemaTopic(t *testing.T, s *server, topic, schemaID string) {
	t.Helper()
	if err := s.schemaRegistry.Register(context.Background(), schemaID, []byte(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:          topic,
		Pool:          "pool-a",
		InputSchemaID: schemaID,
		Status:        topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}
}

func TestHandleSubmitJobHTTPViewerDenied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer","tenant":"default"}]`,
	})

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "viewer-key")
	// Authenticate to populate auth context.
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPAdminAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"admin-key","role":"admin","tenant":"default"}]`,
	})

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "admin-key")
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPUserAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"user-key","role":"user","tenant":"default"}]`,
	})

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "user-key")
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for user role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPOperatorAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"operator-key","role":"operator","tenant":"default"}]`,
	})

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "operator-key")
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for operator role (admin alias), got %d: %s", rec.Code, rec.Body.String())
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

func TestGetJob_RecoveredJob_NoDLQErrors(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-recovered"

	// Job recovered: transition through valid states to SUCCEEDED.
	for _, st := range []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateSucceeded} {
		if err := s.jobStore.SetState(ctx, jobID, st); err != nil {
			t.Fatalf("set state %s: %v", st, err)
		}
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")

	if err := s.dlqStore.Add(ctx, store.DLQEntry{
		JobID:      jobID,
		Reason:     "timeout exceeded",
		Status:     "TIMEOUT",
		ReasonCode: "DEADLINE_EXCEEDED",
		LastState:  "RUNNING",
		Attempts:   3,
	}); err != nil {
		t.Fatalf("add dlq entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	s.handleGetJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["state"] != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED, got %v", resp["state"])
	}
	// Stale DLQ error fields must NOT appear on a recovered job.
	for _, field := range []string{"error_message", "error_status", "error_code", "last_state"} {
		if v, ok := resp[field]; ok {
			t.Errorf("recovered job should not have %s, got %v", field, v)
		}
	}
}

func TestGetJob_FailedJob_ShowsDLQErrors(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-failed"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStateFailed); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")

	if err := s.dlqStore.Add(ctx, store.DLQEntry{
		JobID:      jobID,
		Reason:     "worker crashed",
		Status:     "FAILED",
		ReasonCode: "WORKER_ERROR",
		LastState:  "RUNNING",
		Attempts:   2,
	}); err != nil {
		t.Fatalf("add dlq entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	s.handleGetJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error_message"] != "worker crashed" {
		t.Errorf("expected error_message 'worker crashed', got %v", resp["error_message"])
	}
	if resp["error_status"] != "FAILED" {
		t.Errorf("expected error_status 'FAILED', got %v", resp["error_status"])
	}
	if resp["error_code"] != "WORKER_ERROR" {
		t.Errorf("expected error_code 'WORKER_ERROR', got %v", resp["error_code"])
	}
	if resp["last_state"] != "RUNNING" {
		t.Errorf("expected last_state 'RUNNING', got %v", resp["last_state"])
	}
}

func TestGetJob_AttemptCount_PrefersMetaOverDLQ(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-attempts-meta"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStateFailed); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")

	// Set meta attempts to 5 via IncrAttempts.
	for i := 0; i < 5; i++ {
		if err := s.jobStore.IncrAttempts(ctx, jobID); err != nil {
			t.Fatalf("incr attempts: %v", err)
		}
	}

	// DLQ has stale attempt count of 3.
	if err := s.dlqStore.Add(ctx, store.DLQEntry{
		JobID:      jobID,
		Reason:     "failed",
		Status:     "FAILED",
		ReasonCode: "ERR",
		Attempts:   3,
	}); err != nil {
		t.Fatalf("add dlq entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	s.handleGetJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Meta attempts (5) must win over DLQ attempts (3).
	attemptsVal, ok := resp["attempts"].(float64)
	if !ok {
		t.Fatalf("expected attempts in response, got %v (%T)", resp["attempts"], resp["attempts"])
	}
	if int(attemptsVal) != 5 {
		t.Errorf("expected attempts=5 (from meta), got %d", int(attemptsVal))
	}
}

func TestGetJob_AttemptCount_FallsThroughToDLQ(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-attempts-dlq"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStateFailed); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")

	// No meta attempts set (legacy job). DLQ has attempts=3.
	if err := s.dlqStore.Add(ctx, store.DLQEntry{
		JobID:      jobID,
		Reason:     "failed",
		Status:     "FAILED",
		ReasonCode: "ERR",
		Attempts:   3,
	}); err != nil {
		t.Fatalf("add dlq entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	s.handleGetJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// DLQ attempts (3) should backfill when meta has no value.
	attemptsVal, ok := resp["attempts"].(float64)
	if !ok {
		t.Fatalf("expected attempts in response, got %v (%T)", resp["attempts"], resp["attempts"])
	}
	if int(attemptsVal) != 3 {
		t.Errorf("expected attempts=3 (from DLQ fallback), got %d", int(attemptsVal))
	}
}

func TestHandleListJobEvents(t *testing.T) {
	tests := []struct {
		name       string
		setupJob   bool
		jobID      string
		states     []model.JobState
		tenant     string
		reqTenant  string
		wantCode   int
		wantEvents int
	}{
		{
			name:       "valid job with 3 events",
			setupJob:   true,
			jobID:      "job-events-ok",
			states:     []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateDispatched},
			tenant:     "default",
			reqTenant:  "default",
			wantCode:   http.StatusOK,
			wantEvents: 3,
		},
		{
			name:       "non-existent job returns 404",
			setupJob:   false,
			jobID:      "job-does-not-exist",
			tenant:     "",
			reqTenant:  "default",
			wantCode:   http.StatusNotFound,
			wantEvents: -1,
		},
		{
			name:       "job with zero events returns empty array",
			setupJob:   true,
			jobID:      "job-no-events",
			states:     nil,
			tenant:     "default",
			reqTenant:  "default",
			wantCode:   http.StatusOK,
			wantEvents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.tenant = tt.reqTenant
			ctx := context.Background()

			if tt.setupJob {
				if len(tt.states) == 0 {
					// Simulate a legacy job with state metadata but no events list.
					// Seed the meta hash directly via Redis so GetState returns non-empty
					// but the events LIST remains absent (zero events).
					rc := s.jobStore.Client()
					metaKey := "job:meta:" + tt.jobID
					rc.HSet(ctx, metaKey, "state", "pending", "tenant", tt.tenant)
				} else {
					// Set tenant first.
					if tt.tenant != "" {
						if err := s.jobStore.SetTenant(ctx, tt.jobID, tt.tenant); err != nil {
							t.Fatalf("set tenant: %v", err)
						}
					}
					// Walk through state transitions to create events.
					for _, st := range tt.states {
						if err := s.jobStore.SetState(ctx, tt.jobID, st); err != nil {
							t.Fatalf("set state %s: %v", st, err)
						}
					}
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+tt.jobID+"/events", nil)
			req.Header.Set("X-Tenant-ID", tt.reqTenant)
			req.SetPathValue("id", tt.jobID)
			rec := httptest.NewRecorder()

			s.handleListJobEvents(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantEvents < 0 {
				return // Don't check body for error responses.
			}

			var resp struct {
				Events []json.RawMessage `json:"events"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Events) != tt.wantEvents {
				t.Fatalf("events count=%d, want %d", len(resp.Events), tt.wantEvents)
			}
		})
	}
}

func TestHandleListJobEvents_TenantIsolation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key-a","role":"user","tenant":"tenant-a"}]`,
	})

	ctx := context.Background()
	jobID := "job-evt-tenant"

	// Create job owned by tenant-b.
	if err := s.jobStore.SetTenant(ctx, jobID, "tenant-b"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/events", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("X-API-Key", "key-a")
	authCtx, err := s.auth.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()

	s.handleListJobEvents(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleListJobEvents_MissingID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs//events", nil)
	// Don't set path value — simulates missing id.
	rec := httptest.NewRecorder()
	s.handleListJobEvents(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
