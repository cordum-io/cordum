package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/memory"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleDLQListAndDelete(t *testing.T) {
	s, _, _ := newTestGateway(t)
	entry := memory.DLQEntry{JobID: "job-dlq", Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(context.Background(), entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
	listRec := httptest.NewRecorder()
	s.handleListDLQ(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d", listRec.Code)
	}
	var entries []memory.DLQEntry
	if err := json.NewDecoder(listRec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(entries) != 1 || entries[0].JobID != "job-dlq" {
		t.Fatalf("unexpected dlq entries")
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/api/v1/dlq/page", nil)
	pageReq.Header.Set("X-Tenant-ID", "default")
	pageRec := httptest.NewRecorder()
	s.handleListDLQPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("unexpected page status: %d", pageRec.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/dlq/job-dlq", nil)
	deleteReq.Header.Set("X-Tenant-ID", "default")
	deleteReq.SetPathValue("job_id", "job-dlq")
	deleteRec := httptest.NewRecorder()
	s.handleDeleteDLQ(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected delete status: %d", deleteRec.Code)
	}
}

func TestHandleRetryDLQ(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-retry"
	entry := memory.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(ctx, entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")
	_ = s.jobStore.SetTeam(ctx, jobID, "team")
	_ = s.jobStore.SetPrincipal(ctx, jobID, "principal")
	if err := s.memStore.PutContext(ctx, memory.MakeContextKey(jobID), []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil)
	retryReq.Header.Set("X-Tenant-ID", "default")
	retryReq.SetPathValue("job_id", jobID)
	retryRec := httptest.NewRecorder()
	s.handleRetryDLQ(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("unexpected retry status: %d", retryRec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(retryRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode retry: %v", err)
	}
	if resp["job_id"] == "" {
		t.Fatalf("expected new job id")
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) == 0 || bus.published[len(bus.published)-1].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected submit publish")
	}
	req := bus.published[len(bus.published)-1].packet.GetJobRequest()
	if req == nil {
		t.Fatalf("expected job request payload")
	}
	if meta := req.GetMeta(); meta != nil {
		if meta.GetCapability() != "" || len(meta.GetRiskTags()) != 0 || len(meta.GetRequires()) != 0 {
			t.Fatalf("expected fallback retry without risk metadata")
		}
	}
	if req.GetLabels()["retry"] != "true" || req.GetLabels()["retry_of_job"] != jobID {
		t.Fatalf("expected retry labels on fallback request")
	}
}

func TestHandleRetryDLQPreservesRequestFields(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-retry-preserve"
	entry := memory.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(ctx, entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")
	_ = s.jobStore.SetTeam(ctx, jobID, "team")
	_ = s.jobStore.SetPrincipal(ctx, jobID, "principal")
	if err := s.memStore.PutContext(ctx, memory.MakeContextKey(jobID), []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	origReq := &pb.JobRequest{
		JobId: jobID,
		Topic: "job.test",
		Env: map[string]string{
			"orig_env":     "true",
			"retry_of_job": "old",
		},
		Labels: map[string]string{
			"priority": "high",
			"retry":    "false",
		},
		Meta: &pb.JobMetadata{
			Capability: "code_exec",
			RiskTags:   []string{"data_deletion"},
			Requires:   []string{"approval"},
			PackId:     "pack-1",
		},
	}
	if err := s.jobStore.SetJobRequest(ctx, origReq); err != nil {
		t.Fatalf("set job request: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil)
	retryReq.Header.Set("X-Tenant-ID", "default")
	retryReq.SetPathValue("job_id", jobID)
	retryRec := httptest.NewRecorder()
	s.handleRetryDLQ(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("unexpected retry status: %d", retryRec.Code)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) == 0 || bus.published[len(bus.published)-1].subject != capsdk.SubjectSubmit {
		t.Fatalf("expected submit publish")
	}
	req := bus.published[len(bus.published)-1].packet.GetJobRequest()
	if req == nil {
		t.Fatalf("expected job request payload")
	}
	if req.GetMeta() == nil {
		t.Fatalf("expected metadata preserved")
	}
	if req.GetMeta().GetCapability() != "code_exec" {
		t.Fatalf("expected capability preserved, got %q", req.GetMeta().GetCapability())
	}
	if len(req.GetMeta().GetRiskTags()) != 1 || req.GetMeta().GetRiskTags()[0] != "data_deletion" {
		t.Fatalf("expected risk tags preserved, got %#v", req.GetMeta().GetRiskTags())
	}
	if len(req.GetMeta().GetRequires()) != 1 || req.GetMeta().GetRequires()[0] != "approval" {
		t.Fatalf("expected requires preserved, got %#v", req.GetMeta().GetRequires())
	}
	if req.GetMeta().GetPackId() != "pack-1" {
		t.Fatalf("expected metadata preserved, got %#v", req.GetMeta())
	}
	if req.GetEnv()["orig_env"] != "true" || req.GetEnv()["retry_of_job"] != jobID {
		t.Fatalf("expected env merged, got %#v", req.GetEnv())
	}
	if req.GetLabels()["priority"] != "high" || req.GetLabels()["retry"] != "true" {
		t.Fatalf("expected labels merged with retry overrides, got %#v", req.GetLabels())
	}
}

func TestRetryDLQConcurrent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-concurrent-retry"
	entry := memory.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(ctx, entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")
	_ = s.jobStore.SetTeam(ctx, jobID, "team")
	_ = s.jobStore.SetPrincipal(ctx, jobID, "principal")
	if err := s.memStore.PutContext(ctx, memory.MakeContextKey(jobID), []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("put context: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil)
			httpReq.Header.Set("X-Tenant-ID", "default")
			httpReq.SetPathValue("job_id", jobID)
			rr := httptest.NewRecorder()
			s.handleRetryDLQ(rr, httpReq)

			if rr.Code == http.StatusInternalServerError {
				t.Errorf("unexpected 500: %s", rr.Body.String())
			}
		}()
	}
	wg.Wait()
}

func TestRetryDLQMissingContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-no-context"
	entry := memory.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(ctx, entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}
	_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	_ = s.jobStore.SetTenant(ctx, jobID, "tenant")
	_ = s.jobStore.SetTeam(ctx, jobID, "team")
	_ = s.jobStore.SetPrincipal(ctx, jobID, "principal")
	// Intentionally skip setting context payload in memStore.

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil)
	retryReq.Header.Set("X-Tenant-ID", "default")
	retryReq.SetPathValue("job_id", jobID)
	retryRec := httptest.NewRecorder()
	s.handleRetryDLQ(retryRec, retryReq)

	// Handler should not panic or return an unhelpful 500.
	if retryRec.Code == http.StatusInternalServerError {
		t.Fatalf("expected graceful handling of missing context, got 500: %s", retryRec.Body.String())
	}
}
