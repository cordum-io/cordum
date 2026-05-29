package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	goredis "github.com/redis/go-redis/v9"
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
	if resp["code"] != "unknown_topic" {
		t.Fatalf("expected code unknown_topic, got %#v", resp["code"])
	}
	if _, ok := resp["error_code"]; ok {
		t.Fatalf("unexpected legacy error_code in response: %#v", resp)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("expected no bus publish, got %d", len(bus.published))
	}
}

func TestSubmitJob_UnknownTopic_IncludesRegisteredTopics(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	if err := s.topicRegistry.SetMany(context.Background(), []topicregistry.Registration{
		{Name: "job.allowed.alpha", Pool: "pool-a", Status: topicregistry.StatusActive},
		{Name: "job.allowed.beta", Pool: "pool-b", Status: topicregistry.StatusActive},
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	resp := submitUnknownTopicForTenant(t, s, "default", "job.unknown")

	if resp["code"] != "unknown_topic" {
		t.Fatalf("expected code unknown_topic, got %#v", resp["code"])
	}
	if _, ok := resp["error_code"]; ok {
		t.Fatalf("unexpected legacy error_code in response: %#v", resp)
	}
	if resp["topics_endpoint"] != "/api/v1/topics" {
		t.Fatalf("topics_endpoint = %#v, want /api/v1/topics", resp["topics_endpoint"])
	}
	if resp["truncated"] != false {
		t.Fatalf("truncated = %#v, want false", resp["truncated"])
	}
	registered, ok := resp["registered_topics"].([]any)
	if !ok {
		t.Fatalf("registered_topics missing or wrong type: %#v", resp["registered_topics"])
	}
	got := make([]string, 0, len(registered))
	for _, item := range registered {
		topic, ok := item.(string)
		if !ok {
			t.Fatalf("registered_topics item has type %T, want string: %#v", item, item)
		}
		got = append(got, topic)
	}
	want := []string{"job.allowed.alpha", "job.allowed.beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered_topics = %#v, want %#v", got, want)
	}
}

func TestSubmitJob_UnknownTopic_RegisteredTopicsRespectTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "topics",
		Data: map[string]any{
			"job.default.allowed": map[string]any{
				"name":      "job.default.allowed",
				"pool":      "pool-default",
				"status":    topicregistry.StatusActive,
				"tenant_id": "default",
			},
			"job.other.allowed": map[string]any{
				"name":      "job.other.allowed",
				"pool":      "pool-other",
				"status":    topicregistry.StatusActive,
				"tenant_id": "other",
			},
		},
	}); err != nil {
		t.Fatalf("seed topic registry document: %v", err)
	}

	resp := submitUnknownTopicForTenant(t, s, "default", "job.missing")

	registered, ok := resp["registered_topics"].([]any)
	if !ok {
		t.Fatalf("registered_topics missing or wrong type: %#v", resp["registered_topics"])
	}
	got := make([]string, 0, len(registered))
	for _, item := range registered {
		topic, ok := item.(string)
		if !ok {
			t.Fatalf("registered_topics item has type %T, want string: %#v", item, item)
		}
		got = append(got, topic)
	}
	if strings.Join(got, ",") != "job.default.allowed" {
		t.Fatalf("registered_topics = %#v, want only caller tenant topic", got)
	}
}

func submitUnknownTopicForTenant(t *testing.T, s *server, tenant, topic string) map[string]any {
	t.Helper()
	payload := map[string]any{
		"prompt": "hello",
		"topic":  topic,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenant)
	req = withAuth(req, &auth.AuthContext{Tenant: tenant, Role: "admin", PrincipalID: "test-admin"})
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
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
	if resp["code"] != "schema_validation_failed" {
		t.Fatalf("expected schema_validation_failed, got %#v", resp["code"])
	}
	if _, ok := resp["error_code"]; ok {
		t.Fatalf("unexpected legacy error_code in response: %#v", resp)
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

func TestGetJob_DelegatedJobIncludesDelegation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-delegated"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")
	if err := s.jobStore.SetJobRequest(ctx, &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"agent_id": "agent-b",
		},
	}); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetDelegationLineage(ctx, jobID, model.DelegationLineage{
		TokenJTI:     "dlg-123",
		Audience:     "agent-b",
		RootIssuer:   "agent-a",
		ParentIssuer: "agent-b",
		ChainDepth:   2,
		IssuerChain: []model.DelegationChainLink{
			{AgentID: "agent-a", IssuedAt: "2026-04-21T12:00:00Z", ExpiresAt: "2026-04-21T13:00:00Z", JTI: "dlg-root"},
			{AgentID: "agent-b", IssuedAt: "2026-04-21T12:05:00Z", ExpiresAt: "2026-04-21T13:00:00Z", JTI: "dlg-123", ParentJTI: "dlg-root"},
		},
		Scope:      []string{"read"},
		ExpiresAt:  "2026-04-21T13:00:00Z",
		VerifiedAt: 1713701100000000,
	}); err != nil {
		t.Fatalf("set delegation lineage: %v", err)
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
	rawDelegation, ok := resp["delegation"].(map[string]any)
	if !ok {
		t.Fatalf("expected delegation object, got %T (%v)", resp["delegation"], resp["delegation"])
	}
	if rawDelegation["jti"] != "dlg-123" {
		t.Fatalf("delegation.jti = %v, want dlg-123", rawDelegation["jti"])
	}
	if rawDelegation["audience"] != "agent-b" {
		t.Fatalf("delegation.audience = %v, want agent-b", rawDelegation["audience"])
	}
	if rawDelegation["root_issuer"] != "agent-a" {
		t.Fatalf("delegation.root_issuer = %v, want agent-a", rawDelegation["root_issuer"])
	}
	if rawDelegation["parent_issuer"] != "agent-b" {
		t.Fatalf("delegation.parent_issuer = %v, want agent-b", rawDelegation["parent_issuer"])
	}
	if rawDelegation["expires_at"] != "2026-04-21T13:00:00Z" {
		t.Fatalf("delegation.expires_at = %v, want 2026-04-21T13:00:00Z", rawDelegation["expires_at"])
	}
	if rawDelegation["reverified_at_dispatch"] != true {
		t.Fatalf("delegation.reverified_at_dispatch = %v, want true", rawDelegation["reverified_at_dispatch"])
	}
	if got, ok := rawDelegation["verified_at"].(float64); !ok || int64(got) != 1713701100000000 {
		t.Fatalf("delegation.verified_at = %v, want 1713701100000000", rawDelegation["verified_at"])
	}
	chain, ok := rawDelegation["chain"].([]any)
	if !ok || len(chain) != 2 {
		t.Fatalf("delegation.chain = %#v, want 2 entries", rawDelegation["chain"])
	}
	scope, ok := rawDelegation["scope"].([]any)
	if !ok || len(scope) != 1 || scope[0] != "read" {
		t.Fatalf("delegation.scope = %#v, want [read]", rawDelegation["scope"])
	}
}

func TestGetJob_NonDelegatedJobOmitsDelegation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-no-delegation"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "default")

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
	if _, ok := resp["delegation"]; ok {
		t.Fatalf("expected delegation to be omitted, got %#v", resp["delegation"])
	}
}

func TestGetJob_DelegatedJobCrossTenantForbidden(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	ctx := context.Background()
	jobID := "job-delegated-tenant-b"

	if err := s.jobStore.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant-b")
	if err := s.jobStore.SetDelegationLineage(ctx, jobID, model.DelegationLineage{
		TokenJTI:   "dlg-tenant-b",
		Audience:   "agent-b",
		RootIssuer: "agent-a",
		ChainDepth: 1,
		IssuerChain: []model.DelegationChainLink{
			{AgentID: "agent-a", JTI: "dlg-tenant-b"},
		},
		VerifiedAt: 1713701100000000,
	}); err != nil {
		t.Fatalf("set delegation lineage: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil), &auth.AuthContext{
		Tenant:      "tenant-a",
		PrincipalID: "operator-a",
		Role:        "admin",
	})
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.SetPathValue("id", jobID)
	rec := httptest.NewRecorder()
	s.handleGetJob(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Submit-time policy regression tests
// ---------------------------------------------------------------------------

func TestHandleSubmitJobHTTP_PolicyDeny(t *testing.T) {
	s, bus, safetyClient := newTestGateway(t)
	s.tenant = "default"
	ctx := context.Background()

	safetyClient.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_DENY,
		Reason:   "topic prohibited by policy",
	})

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for policy deny, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode deny response: %v", err)
	}
	jobID, _ := resp["job_id"].(string)
	if strings.TrimSpace(jobID) == "" {
		t.Fatalf("expected denied response to include job_id, got %#v", resp)
	}
	state, err := s.jobStore.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get denied job state: %v", err)
	}
	if state != model.JobStateDenied {
		t.Fatalf("expected denied job state, got %s", state)
	}
	record, err := s.jobStore.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get denied safety decision: %v", err)
	}
	if record.Decision != model.SafetyDeny {
		t.Fatalf("expected denied safety decision, got %s", record.Decision)
	}
	if record.Reason != "topic prohibited by policy" {
		t.Fatalf("unexpected deny reason: %q", record.Reason)
	}
	entry, err := s.dlqStore.Get(ctx, jobID)
	if err != nil || entry == nil {
		t.Fatalf("expected denied job in dlq, got entry=%v err=%v", entry, err)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one dlq publish on deny, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected deny publish to dlq subject, got %s", bus.published[0].subject)
	}
}

func TestHandleSubmitJobHTTP_PolicyThrottle(t *testing.T) {
	s, bus, safetyClient := newTestGateway(t)
	s.tenant = "default"

	safetyClient.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_THROTTLE,
		Reason:   "rate limit exceeded",
	})

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
		t.Fatalf("expected 429 for policy throttle, got %d: %s", rec.Code, rec.Body.String())
	}
	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatalf("expected Retry-After header on throttle response")
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("expected no bus publish on throttle, got %d", len(bus.published))
	}
}

func TestHandleSubmitJobHTTP_PolicyApprovalRequired(t *testing.T) {
	s, bus, safetyClient := newTestGateway(t)
	s.tenant = "default"

	safetyClient.setResponse(&pb.PolicyCheckResponse{
		Decision:         pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		Reason:           "high-risk action requires approval",
		ApprovalRequired: true,
	})

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
		t.Fatalf("expected 200 for approval required, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["job_id"] == "" {
		t.Fatalf("expected job_id in response")
	}
	if resp["status"] != "approval_required" {
		t.Fatalf("expected status=approval_required, got %q", resp["status"])
	}

	// Verify job is in APPROVAL state.
	jobID := resp["job_id"]
	state, err := s.jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStateApproval {
		t.Fatalf("expected job state APPROVAL_REQUIRED, got %s", state)
	}

	// Verify safety decision record was persisted.
	safetyRec, err := s.jobStore.GetSafetyDecision(context.Background(), jobID)
	if err != nil {
		t.Fatalf("expected safety decision record, got error: %v", err)
	}
	if !safetyRec.ApprovalRequired {
		t.Fatalf("expected approval_required=true in safety record")
	}

	// Verify job request was persisted (needed by approval endpoint).
	jobReq, err := s.jobStore.GetJobRequest(context.Background(), jobID)
	if err != nil {
		t.Fatalf("expected job request persisted for approval, got error: %v", err)
	}
	if jobReq.GetTopic() != "job.test" {
		t.Fatalf("expected job request topic=job.test, got %q", jobReq.GetTopic())
	}

	// No bus publish — job awaits human approval.
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("expected no bus publish for approval-required job, got %d", len(bus.published))
	}
}

func TestHandleSubmitJobHTTP_PolicyFailClosed(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	ctx := context.Background()

	// Replace the safety client with one that returns errors from Evaluate.
	s.safetyClient = &failingSafetyClient{}

	// Default fail mode is "closed" — error from Evaluate should deny.
	t.Setenv("POLICY_CHECK_FAIL_MODE", "")

	payload := map[string]any{
		"prompt": "hello",
		"topic":  "job.test",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for fail-closed, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode fail-closed response: %v", err)
	}
	jobID, _ := resp["job_id"].(string)
	if strings.TrimSpace(jobID) == "" {
		t.Fatalf("expected fail-closed response to include job_id, got %#v", resp)
	}
	if state, err := s.jobStore.GetState(ctx, jobID); err != nil || state != model.JobStateDenied {
		t.Fatalf("expected fail-closed denied state, got state=%v err=%v", state, err)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected one dlq publish on fail-closed, got %d", len(bus.published))
	}
	if bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected fail-closed publish to dlq subject, got %s", bus.published[0].subject)
	}
}

func TestHandleSubmitJobHTTP_PolicyFailOpen(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"

	// Replace the safety client with one that returns errors from Evaluate.
	s.safetyClient = &failingSafetyClient{}

	// Set fail mode to "open" — error from Evaluate should allow.
	t.Setenv("POLICY_CHECK_FAIL_MODE", "open")

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
		t.Fatalf("expected 200 for fail-open, got %d: %s", rec.Code, rec.Body.String())
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 bus publish for fail-open, got %d", len(bus.published))
	}
}

// TestHandleSubmitJob_DeniedIdempotencyReplay pins the contract for
// task-11152261: a client retrying a policy-denied submit with the
// same idempotency key must get the original job_id + trace_id back
// (pointing at the single stored denied job), NOT a newly-minted
// second denied job + second DLQ entry. Before this task the denied
// branch returned without reserving the idempotency key, so retries
// silently forked.
//
// Replay mechanic: the first POST hits the denied branch, which now
// reserves the key via TrySetIdempotencyKeyScoped before persisting.
// The second POST short-circuits at the top-of-handler idempotency
// check (handlers_jobs.go ~1549) which returns 200 with the same
// job_id + trace_id. Clients check the job's state via GET
// /api/v1/jobs/{id} to see it's DENIED. This matches the existing
// approval_required idempotency behavior.
func TestHandleSubmitJob_DeniedIdempotencyReplay(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.tenant = "default"
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "policy blocks this",
		PolicySnapshot: "snap-test",
	})

	submit := func() (int, map[string]any) {
		payload := map[string]any{
			"prompt":          "hello",
			"topic":           "job.test",
			"idempotency_key": "deny-replay-key",
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		req.Header.Set("X-Tenant-ID", "default")
		rec := httptest.NewRecorder()
		s.handleSubmitJobHTTP(rec, req)
		var resp map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return rec.Code, resp
	}

	firstCode, firstResp := submit()
	if firstCode != http.StatusForbidden {
		t.Fatalf("first submit: want 403, got %d (%v)", firstCode, firstResp)
	}
	firstJobID, _ := firstResp["job_id"].(string)
	firstTraceID, _ := firstResp["trace_id"].(string)
	if firstJobID == "" {
		t.Fatalf("first submit missing job_id: %v", firstResp)
	}

	secondCode, secondResp := submit()
	// Retry short-circuits at the top-of-handler idempotency check →
	// returns 200 with the existing job_id, same contract as
	// approval_required replay.
	if secondCode != http.StatusOK {
		t.Fatalf("second submit: want 200 (idempotency replay), got %d (%v)", secondCode, secondResp)
	}
	if got, _ := secondResp["job_id"].(string); got != firstJobID {
		t.Fatalf("second submit job_id: want %q (replay), got %q — idempotency reservation missing on first POST", firstJobID, got)
	}
	if got, _ := secondResp["trace_id"].(string); got != firstTraceID {
		t.Fatalf("second submit trace_id: want %q (replay), got %q", firstTraceID, got)
	}

	// Exactly one denied job in the store for this key — no second mint.
	existing, err := s.jobStore.GetJobByIdempotencyKeyScoped(context.Background(), "default", "deny-replay-key")
	if err != nil || existing != firstJobID {
		t.Fatalf("idempotency index lookup: want jobID=%q err=nil, got jobID=%q err=%v", firstJobID, existing, err)
	}
	state, _ := s.jobStore.GetState(context.Background(), firstJobID)
	if state != model.JobStateDenied {
		t.Fatalf("denied job state: want DENIED, got %s", state)
	}
}

// BUG-009: when submit fails AFTER PutContext (e.g. bus publish fails) the
// orphaned context_ptr used to live in Redis for the full 24h TTL. The
// gateway now early-GCs the context blob via DeleteContext.
func TestSubmitJobHTTP_PublishFails_GCsOrphanedContext(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"
	bus.publishErr = errors.New("bus down")

	payload := map[string]any{"prompt": "hi", "topic": "job.test"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code < 500 || rec.Code >= 600 {
		t.Fatalf("expected 5xx (publish failed), got %d: %s", rec.Code, rec.Body.String())
	}

	bus.mu.Lock()
	if len(bus.published) != 1 {
		bus.mu.Unlock()
		t.Fatalf("expected one publish attempt, got %d", len(bus.published))
	}
	jobID := bus.published[0].packet.GetJobRequest().GetJobId()
	bus.mu.Unlock()
	if jobID == "" {
		t.Fatal("jobID missing from published packet")
	}

	// Context blob must have been GC'd; GetContext returns redis.Nil.
	ctxKey := store.MakeContextKey(jobID)
	if _, err := s.memStore.GetContext(context.Background(), ctxKey); !errors.Is(err, goredis.Nil) {
		t.Fatalf("expected context GC after publish failure (redis.Nil), got err=%v", err)
	}
}

// Positive control: a fully-successful submit must NOT delete the context —
// the worker still needs it.
func TestSubmitJobHTTP_Success_KeepsContext(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.tenant = "default"

	payload := map[string]any{"prompt": "hi", "topic": "job.test"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	bus.mu.Lock()
	if len(bus.published) != 1 {
		bus.mu.Unlock()
		t.Fatalf("expected one publish, got %d", len(bus.published))
	}
	jobID := bus.published[0].packet.GetJobRequest().GetJobId()
	bus.mu.Unlock()

	data, err := s.memStore.GetContext(context.Background(), store.MakeContextKey(jobID))
	if err != nil {
		t.Fatalf("context should be present after successful submit, got err=%v", err)
	}
	if len(data) == 0 {
		t.Fatal("context blob empty after successful submit")
	}
}
