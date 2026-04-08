package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/infra/registry"
)

func TestHandleListTopics(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.topicRegistry.SetMany(context.Background(), []topicregistry.Registration{
		{Name: "job.alpha", Pool: "pool-a", Status: topicregistry.StatusActive},
		{Name: "job.beta", Pool: "pool-b", Status: topicregistry.StatusDeprecated},
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}
	snapshot := registry.Snapshot{
		Workers: []registry.WorkerSummary{
			{WorkerID: "w-1", Pool: "pool-a"},
			{WorkerID: "w-2", Pool: "pool-a"},
			{WorkerID: "w-3", Pool: "pool-c"},
		},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, data); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topics", nil)
	rec := httptest.NewRecorder()
	s.handleListTopics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []topicResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(resp.Items))
	}
	if resp.Items[0].Name != "job.alpha" || resp.Items[0].ActiveWorkerCount != 2 {
		t.Fatalf("unexpected first topic: %+v", resp.Items[0])
	}
	if resp.Items[1].Name != "job.beta" || resp.Items[1].ActiveWorkerCount != 0 {
		t.Fatalf("unexpected second topic: %+v", resp.Items[1])
	}
}

func TestHandleCreateDeleteTopic(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{},
				"pools": map[string]any{
					"pool-a": map[string]any{"status": "active"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pools config: %v", err)
	}

	body := []byte(`{"name":"job.external","pool":"pool-a","requires":["cap.a"],"risk_tags":["safe"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reg, registryEmpty, err := s.topicRegistry.Get(context.Background(), "job.external")
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if registryEmpty || reg == nil {
		t.Fatalf("expected created topic to exist, got registryEmpty=%v reg=%v", registryEmpty, reg)
	}
	if reg.Pool != "pool-a" || reg.Status != topicregistry.StatusActive {
		t.Fatalf("unexpected topic record: %+v", reg)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/topics/job.external", nil)
	delReq.SetPathValue("name", "job.external")
	delRec := httptest.NewRecorder()
	s.handleDeleteTopic(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", delRec.Code, delRec.Body.String())
	}
	reg, registryEmpty, err = s.topicRegistry.Get(context.Background(), "job.external")
	if err != nil {
		t.Fatalf("get deleted topic: %v", err)
	}
	if reg != nil {
		t.Fatalf("expected deleted topic to be absent, got reg=%v registryEmpty=%v", reg, registryEmpty)
	}
}

func TestHandleCreateTopicAllowsDisabledPackTopicWithoutPool(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body := []byte(`{"name":"job.demo-pack.echo","pack_id":"demo-pack","status":"disabled","input_schema_id":"demo-pack/Input","output_schema_id":"demo-pack/Output"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reg, registryEmpty, err := s.topicRegistry.Get(context.Background(), "job.demo-pack.echo")
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if registryEmpty || reg == nil {
		t.Fatalf("expected created disabled topic to exist, got registryEmpty=%v reg=%v", registryEmpty, reg)
	}
	if reg.Pool != "" || reg.PackID != "demo-pack" || reg.Status != topicregistry.StatusDisabled {
		t.Fatalf("unexpected disabled topic record: %+v", reg)
	}
}

func TestCreateTopicInvalidName(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.invalid!topic"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTopicPoolNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.external","pool":"pool-a"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTopicArrayTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)

	requires := make([]string, maxTopicArrayItems+1)
	for i := range requires {
		requires[i] = "cap.test"
	}
	body, err := json.Marshal(map[string]any{
		"name":     "job.external",
		"requires": requires,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTopicServiceFailure(t *testing.T) {
	s, _, _ := newTestGateway(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.external"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}
