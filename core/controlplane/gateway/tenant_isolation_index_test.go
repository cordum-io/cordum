package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

func seedGatewayIndexJobs(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		id := tenant + "-job"
		req := &pb.JobRequest{JobId: id, TenantId: tenant, Topic: "job." + tenant}
		for _, err := range []error{
			s.jobStore.SetJobMeta(ctx, req), s.jobStore.SetJobRequest(ctx, req),
			s.jobStore.SetState(ctx, id, model.JobStateApproval),
			s.jobStore.SetSafetyDecision(ctx, id, model.SafetyDecisionRecord{Decision: model.SafetyRequireApproval, ApprovalRequired: true, Reason: tenant + "-marker"}),
			s.jobStore.AddJobToTrace(ctx, "shared-trace", id),
			s.jobStore.AddJobToTrace(ctx, tenant+"-trace", id),
			s.jobStore.SetWorkerID(ctx, id, tenant+"-worker"),
			s.dlqStore.Add(ctx, store.DLQEntry{JobID: id, Reason: tenant + "-marker", CreatedAt: time.Now().UTC()}),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func checkIndexList(t *testing.T, handler http.HandlerFunc, path, id, aMarker, bMarker string) {
	t.Helper()
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		rr := httptest.NewRecorder()
		handler(rr, tenantObjectRequest(http.MethodGet, path, tenant, id))
		want, forbidden := aMarker, "tenant-b"
		if tenant == "tenant-b" {
			want, forbidden = bMarker, "tenant-a"
		}
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("owner member absent: %d %s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), forbidden) {
			t.Errorf("foreign member released: %s", rr.Body.String())
		}
	}
}

func TestTenantIsolationGatewayJobIndexes(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	seedGatewayIndexJobs(t, s)
	tenantObjectUnchanged(t, s)
	for _, tc := range []struct {
		name, path, id string
		handler        http.HandlerFunc
	}{
		{"recent", "/api/v1/jobs", "", s.handleListJobs},
		{"trace-filter", "/api/v1/jobs?trace_id=shared-trace", "", s.handleListJobs},
		{"trace", "/api/v1/traces/shared-trace", "shared-trace", s.handleGetTrace},
		{"approvals", "/api/v1/approvals?include_resolved=false", "", s.handleListApprovals},
		{"dlq", "/api/v1/dlq", "", s.handleListDLQ},
		{"dlq-page", "/api/v1/dlq/page", "", s.handleListDLQPage},
	} {
		t.Run(tc.name, func(t *testing.T) { checkIndexList(t, tc.handler, tc.path, tc.id, "tenant-a-job", "tenant-b-job") })
	}
	checkTenantObjectRead(t, s.handleGetJob, "/api/v1/jobs/tenant-b-job", "tenant-b-job", "tenant-b-job")
	checkTenantObjectRead(t, s.handleGetTrace, "/api/v1/traces/tenant-b-trace", "tenant-b-trace", "tenant-b-job")
	checkTenantObjectRead(t, s.handleListJobs, "/api/v1/jobs?trace_id=tenant-b-trace", "", "tenant-b-job")
}

func seedGatewayIndexWorkflows(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	for _, org := range []string{"tenant-b", "tenant-a"} {
		if err := s.workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: org + "-wf", OrgID: org, Name: org + "-marker"}); err != nil {
			t.Fatal(err)
		}
		if err := s.workflowStore.CreateRun(ctx, &wf.WorkflowRun{ID: org + "-run", WorkflowID: org + "-wf", OrgID: org, Status: wf.RunStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTenantIsolationGatewayWorkflowIndexes(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	seedGatewayIndexWorkflows(t, s)
	tenantObjectUnchanged(t, s)
	checkIndexList(t, s.handleListWorkflows, "/api/v1/workflows", "", "tenant-a-wf", "tenant-b-wf")
	for _, path := range []string{"/api/v1/workflow-runs", "/api/v1/workflow-runs?status=running"} {
		checkIndexList(t, s.handleListAllRuns, path, "", "tenant-a-run", "tenant-b-run")
	}
	for _, org := range []string{"tenant-a", "tenant-b"} {
		forbidden := "tenant-b"
		if org == "tenant-b" {
			forbidden = "tenant-a"
		}
		rr := httptest.NewRecorder()
		s.handleListRuns(rr, tenantObjectRequest(http.MethodGet, "/api/v1/workflows/"+org+"-wf/runs", org, org+"-wf"))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), org+"-run") || strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("own workflow runs: %d %s", rr.Code, rr.Body.String())
		}
	}
	checkTenantObjectRead(t, s.handleListRuns, "/api/v1/workflows/tenant-b-wf/runs", "tenant-b-wf", "tenant-b-run")
	checkTenantObjectRead(t, s.handleListAllRuns, "/api/v1/workflow-runs?workflow_id=tenant-b-wf", "", "tenant-b-run")
	checkTenantObjectRead(t, s.handleGetRun, "/api/v1/workflow-runs/tenant-b-run", "tenant-b-run", "tenant-b-run")
}

func seedGatewayIndexWorkers(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	snap := registry.Snapshot{CapturedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		id := tenant + "-worker"
		if _, err := s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{TenantID: tenant, WorkerID: id, CreatedBy: tenant}); err != nil {
			t.Fatal(err)
		}
		snap.Workers = append(snap.Workers, registry.WorkerSummary{WorkerID: id, Pool: "shared-pool", Labels: map[string]string{"marker": tenant + "-marker"}})
	}
	seedSnapshot(t, s, snap)
}

func TestTenantIsolationGatewayWorkerIndexes(t *testing.T) {
	for _, surface := range []string{"snapshot", "jobs"} {
		t.Run(surface, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			seedGatewayIndexWorkers(t, s)
			seedGatewayIndexJobs(t, s)
			tenantObjectUnchanged(t, s)
			handler, marker := s.handleGetWorker, "tenant-b-worker"
			if surface == "jobs" {
				handler, marker = s.handleGetWorkerJobs, "tenant-b-job"
			}
			rr := httptest.NewRecorder()
			handler(rr, tenantObjectRequest(http.MethodGet, "/api/v1/workers/tenant-a-worker", "tenant-a", "tenant-a-worker"))
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "tenant-a-") || strings.Contains(rr.Body.String(), "tenant-b") {
				t.Fatalf("own worker: %d %s", rr.Code, rr.Body.String())
			}
			checkTenantObjectRead(t, handler, "/api/v1/workers/tenant-b-worker", "tenant-b-worker", marker)
		})
	}
}

func TestTenantIsolationGatewaySchemaIndex(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		r := tenantObjectRequest(http.MethodPost, "/api/v1/schemas", tenant, "")
		r.Body = io.NopCloser(strings.NewReader(`{"id":"` + tenant + `-schema","schema":{"type":"object","description":"` + tenant + `-marker"}}`))
		rr := httptest.NewRecorder()
		s.handleRegisterSchema(rr, r)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("tenant schema seed: %d %s", rr.Code, rr.Body.String())
		}
	}
	tenantObjectUnchanged(t, s)
	checkIndexList(t, s.handleListSchemas, "/api/v1/schemas", "", "tenant-a-schema", "tenant-b-schema")
	checkTenantObjectRead(t, s.handleGetSchema, "/api/v1/schemas/tenant-b-schema", "tenant-b-schema", "tenant-b-marker")
}
