package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/cordum/cordum/core/infra/memory"
)

func TestHandleDLQListAndDelete(t *testing.T) {
	s, _, _ := newTestGateway(t)
	entry := memory.DLQEntry{JobID: "job-dlq", Topic: "job.test", CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(context.Background(), entry); err != nil {
		t.Fatalf("add dlq: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil)
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
	pageRec := httptest.NewRecorder()
	s.handleListDLQPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("unexpected page status: %d", pageRec.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/dlq/job-dlq", nil)
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
}
