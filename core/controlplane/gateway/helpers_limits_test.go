package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

func TestListJobsAndApprovalsLimitClamped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	tenant := "tenant-a"

	for i := 0; i < int(maxListLimit)+1; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
			t.Fatalf("set state: %v", err)
		}
		_ = s.jobStore.SetTenant(ctx, jobID, tenant)
		_ = s.jobStore.SetTopic(ctx, jobID, "job.test")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	listReq.Header.Set("X-Tenant-ID", tenant)
	listRec := httptest.NewRecorder()
	s.handleListJobs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list jobs: %d %s", listRec.Code, listRec.Body.String())
	}
	var listResp map[string]any
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items, _ := listResp["items"].([]any)
	if len(items) != 50 {
		t.Fatalf("expected default jobs limit 50, got %d", len(items))
	}

	overReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs?limit=%d", maxListLimit+100), nil)
	overReq.Header.Set("X-Tenant-ID", tenant)
	overRec := httptest.NewRecorder()
	s.handleListJobs(overRec, overReq)
	if overRec.Code != http.StatusOK {
		t.Fatalf("list jobs over-limit: %d %s", overRec.Code, overRec.Body.String())
	}
	listResp = map[string]any{}
	if err := json.NewDecoder(overRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items, _ = listResp["items"].([]any)
	if len(items) != int(maxListLimit) {
		t.Fatalf("expected clamped jobs limit %d, got %d", maxListLimit, len(items))
	}

	appReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/approvals?limit=%d", maxListLimit+100), nil)
	appReq.Header.Set("X-Tenant-ID", tenant)
	appRec := httptest.NewRecorder()
	s.handleListApprovals(appRec, appReq)
	if appRec.Code != http.StatusOK {
		t.Fatalf("list approvals: %d %s", appRec.Code, appRec.Body.String())
	}
	var appResp map[string]any
	if err := json.NewDecoder(appRec.Body).Decode(&appResp); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	appItems, _ := appResp["items"].([]any)
	if len(appItems) != int(maxListLimit) {
		t.Fatalf("expected clamped approvals limit %d, got %d", maxListLimit, len(appItems))
	}
}

func TestListDLQLimitClamped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	for i := 0; i < int(maxListLimit)+1; i++ {
		entry := store.DLQEntry{
			JobID:     fmt.Sprintf("job-%d", i),
			CreatedAt: time.Unix(int64(i+1), 0).UTC(),
		}
		if err := s.dlqStore.Add(ctx, entry); err != nil {
			t.Fatalf("add dlq: %v", err)
		}
	}

	listReq := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/dlq", nil))
	listReq.Header.Set("X-Tenant-ID", "default")
	listRec := httptest.NewRecorder()
	s.handleListDLQ(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list dlq: %d %s", listRec.Code, listRec.Body.String())
	}
	var dlqResp map[string]any
	if err := json.NewDecoder(listRec.Body).Decode(&dlqResp); err != nil {
		t.Fatalf("decode dlq: %v", err)
	}
	items, ok := dlqResp["items"].([]any)
	if !ok {
		t.Fatalf("expected items array in response, got %T", dlqResp["items"])
	}
	if len(items) != 100 {
		t.Fatalf("expected default dlq limit 100, got %d", len(items))
	}

	pageReq := adminCtx(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/dlq/page?limit=%d", maxListLimit+100), nil))
	pageReq.Header.Set("X-Tenant-ID", "default")
	pageRec := httptest.NewRecorder()
	s.handleListDLQPage(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("list dlq page: %d %s", pageRec.Code, pageRec.Body.String())
	}
	var pageResp map[string]any
	if err := json.NewDecoder(pageRec.Body).Decode(&pageResp); err != nil {
		t.Fatalf("decode dlq page: %v", err)
	}
	pageItems, _ := pageResp["items"].([]any)
	if len(pageItems) != int(maxListLimit) {
		t.Fatalf("expected clamped dlq limit %d, got %d", maxListLimit, len(pageItems))
	}
}
