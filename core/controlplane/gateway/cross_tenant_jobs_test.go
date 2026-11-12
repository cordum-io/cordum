package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("jobs-list", "/api/v1/jobs", true))
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("job-detail", "/api/v1/jobs/{id}", false))
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("job-decisions", "/api/v1/jobs/{id}/decisions", false))
	registerCrossTenantFamily("PR1-02", crossTenantCase{"GET /api/v1/jobs/{id}/stream", "job-stream", crossTenantJobStream})
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("cancel", "POST", "/api/v1/jobs/{id}/cancel", "{}", nil))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("remediate", "POST", "/api/v1/jobs/{id}/remediate", "{}", seedCrossTenantRemediation))
	registerCrossTenantFamily("PR1-02", crossTenantCase{"POST /api/v1/jobs", "submit", crossTenantJobSubmit})
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("dlq-list", "/api/v1/dlq", true, seedCrossTenantDLQ))
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("dlq-page", "/api/v1/dlq/page", true, seedCrossTenantDLQ))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("dlq-delete", "DELETE", "/api/v1/dlq/{job_id}", "", seedCrossTenantDLQ))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("dlq-retry", "POST", "/api/v1/dlq/{job_id}/retry", "", seedCrossTenantDLQ))
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("approvals-list", "/api/v1/approvals", true, seedCrossTenantApproval))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("approve", "POST", "/api/v1/approvals/{job_id}/approve", `{"reason":"reviewed"}`, seedCrossTenantApproval))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("reject", "POST", "/api/v1/approvals/{job_id}/reject", `{"reason":"declined"}`, seedCrossTenantApproval))
	registerCrossTenantFamily("PR1-02", crossTenantJobWrite("repair", "POST", "/api/v1/approvals/{job_id}/repair", `{"apply":true}`, seedCrossTenantRepair))
	registerCrossTenantFamily("PR1-02", crossTenantJobRead("approval-context", "/api/v1/approvals/{job_id}/context", false, seedCrossTenantApproval))
	registerCrossTenantFamily("PR1-02", crossTenantCase{"GRPC " + pb.CordumApi_ServiceDesc.ServiceName + "/GetJobStatus", "grpc-status", crossTenantGRPCStatus})
	registerCrossTenantFamily("PR1-02", crossTenantCase{"GRPC " + pb.CordumApi_ServiceDesc.ServiceName + "/SubmitJob", "grpc-submit", crossTenantGRPCSubmit})
}

func crossTenantGRPCSubmit(t *testing.T, sw *crossTenantSweep) {
	id, _ := seedCrossTenantJob(t, sw)
	before, lists := crossTenantJobMeta(t, sw, id), crossTenantOwnerLists(t, sw)
	mark := sw.publishMark()
	req := &pb.SubmitJobRequest{Prompt: "grpc-marker", Topic: "job.default", OrgId: sw.tenantB.tenantID}
	resp, err := sw.server.SubmitJob(crossTenantGRPCContext(t, sw.tenantA), req)
	if status.Code(err) != codes.PermissionDenied || resp != nil {
		t.Fatalf("A SubmitJob: response=%v, error=%v", resp, err)
	}
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) || !reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
		t.Fatal("cross-tenant gRPC submit changed B state")
	}
	if len(sw.publishedSince(mark)) != 0 {
		t.Fatal("cross-tenant gRPC submit published a packet")
	}
	resp, err = sw.server.SubmitJob(crossTenantGRPCContext(t, sw.tenantB), req)
	if err != nil || resp == nil || resp.JobId == "" {
		t.Fatalf("owner SubmitJob: response=%v, error=%v", resp, err)
	}
	crossTenantJobMeta(t, sw, resp.JobId)
	if reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) || len(sw.publishedSince(mark)) == 0 {
		t.Fatal("owner gRPC submit did not persist and publish a B job")
	}
}

func crossTenantGRPCContext(t *testing.T, who crossTenantSweepTenant) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return context.WithValue(ctx, auth.ContextKey{}, &auth.AuthContext{Tenant: who.tenantID, PrincipalID: who.principalID, Role: "admin"})
}

func crossTenantGRPCStatus(t *testing.T, sw *crossTenantSweep) {
	id, _ := seedCrossTenantJob(t, sw)
	before := crossTenantJobMeta(t, sw, id)
	req := &pb.GetJobStatusRequest{JobId: id}
	resp, err := sw.server.GetJobStatus(crossTenantGRPCContext(t, sw.tenantA), req)
	if status.Code(err) != codes.PermissionDenied || resp != nil {
		t.Fatalf("A GetJobStatus: response=%v, error=%v", resp, err)
	}
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Fatal("cross-tenant gRPC read changed B state")
	}
	resp, err = sw.server.GetJobStatus(crossTenantGRPCContext(t, sw.tenantB), req)
	if err != nil || resp == nil || resp.JobId != id || resp.Status != before["state"] {
		t.Fatalf("owner GetJobStatus: response=%v, error=%v", resp, err)
	}
}

func seedCrossTenantRepair(t *testing.T, sw *crossTenantSweep, id, marker string) {
	t.Helper()
	req, err := sw.server.jobStore.GetJobRequest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	req.Labels["approval_granted"] = "true"
	if err := sw.server.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	seedCrossTenantApproval(t, sw, id, marker)
}

func seedCrossTenantApproval(t *testing.T, sw *crossTenantSweep, id, marker string) {
	t.Helper()
	ctx := context.Background()
	req, err := sw.server.jobStore.GetJobRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	req.PrincipalId = "principal-submitter"
	req.Identity.PrincipalId = req.PrincipalId
	if err := sw.server.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := sw.server.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := sw.server.jobStore.SetSubmittedBy(ctx, id, "principal-submitter"); err != nil {
		t.Fatal(err)
	}
	if err := sw.server.jobStore.SetState(ctx, id, model.JobStateApproval); err != nil {
		t.Fatal(err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.server.jobStore.SetSafetyDecision(ctx, id, model.SafetyDecisionRecord{
		Decision: model.SafetyRequireApproval, ApprovalRequired: true, Reason: marker,
		PolicySnapshot: "snap-test", JobHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedCrossTenantDLQ(t *testing.T, sw *crossTenantSweep, id, marker string) {
	t.Helper()
	if err := sw.server.dlqStore.Add(context.Background(), store.DLQEntry{
		JobID: id, Topic: "job." + marker, Reason: marker, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func crossTenantJobSubmit(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	before, lists := crossTenantJobMeta(t, sw, id), crossTenantOwnerLists(t, sw)
	mark := sw.publishMark()
	body := fmt.Sprintf(`{"prompt":"new job","topic":"job.default","org_id":%q}`, sw.tenantB.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/jobs", body)
	if rr.Code >= 200 && rr.Code < 300 {
		var result struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || result.JobID == "" {
			t.Fatalf("decode submit: %v", err)
		}
		tenant, err := sw.server.jobStore.GetTenant(context.Background(), result.JobID)
		if err != nil || tenant != sw.tenantA.tenantID {
			t.Fatalf("submit was not bound to A: tenant=%q, err=%v", tenant, err)
		}
	} else {
		assertCrossTenantRefused(t, rr, "submit", marker)
	}
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) || !reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
		t.Fatal("cross-tenant submit changed B state")
	}
	for _, message := range sw.publishedSince(mark) {
		if strings.Contains(message.packet.String(), sw.tenantB.tenantID) || strings.Contains(message.packet.String(), id) {
			t.Fatal("cross-tenant submit published B data")
		}
	}
	assertCrossTenantAbsent(t, sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/jobs", body), "owner submit")
	if reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
		t.Fatal("owner submit did not create a B job")
	}
}

func seedCrossTenantRemediation(t *testing.T, sw *crossTenantSweep, id, marker string) {
	t.Helper()
	if err := sw.server.jobStore.SetSafetyDecision(context.Background(), id, model.SafetyDecisionRecord{
		Decision: model.SafetyDeny, Reason: marker,
		Remediations: []*pb.PolicyRemediation{{Id: "safe", ReplacementTopic: "job.safe"}},
	}); err != nil {
		t.Fatal(err)
	}
}

type crossTenantJobSetup func(t *testing.T, sw *crossTenantSweep, id, marker string)

func crossTenantOwnerLists(t *testing.T, sw *crossTenantSweep) map[string]string {
	t.Helper()
	lists := map[string]string{}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/dlq", "/api/v1/approvals"} {
		rr := sw.as(t, sw.tenantB, http.MethodGet, path, "")
		assertCrossTenantAbsent(t, rr, "B state snapshot")
		lists[path] = rr.Body.String()
	}
	return lists
}

func crossTenantJobWrite(name, method, path, body string, setup crossTenantJobSetup) crossTenantCase {
	return crossTenantCase{method + " " + path, name, func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantJob(t, sw)
		if setup != nil {
			setup(t, sw, id, marker)
		}
		before, lists := crossTenantJobMeta(t, sw, id), crossTenantOwnerLists(t, sw)
		mark := sw.publishMark()
		target := strings.NewReplacer("{id}", id, "{job_id}", id).Replace(path)
		rr := sw.as(t, sw.tenantA, method, target, body)
		assertCrossTenantRefused(t, rr, name, marker, sw.tenantB.tenantID, sw.tenantB.principalID)
		if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) || !reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
			t.Fatal("cross-tenant write changed B state")
		}
		if packets := sw.publishedSince(mark); len(packets) != 0 {
			t.Fatalf("cross-tenant write published %d packets", len(packets))
		}
		rr = sw.as(t, sw.tenantB, method, target, body)
		assertCrossTenantAbsent(t, rr, "owner write")
		if reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
			t.Fatal("owner write did not change B state")
		}
	}}
}

func crossTenantJobStream(t *testing.T, sw *crossTenantSweep) {
	id, marker := seedCrossTenantJob(t, sw)
	before := crossTenantJobMeta(t, sw, id)
	path := "/api/v1/jobs/" + id + "/stream"
	rr := sw.as(t, sw.tenantA, http.MethodGet, path, "")
	assertCrossTenantRefused(t, rr, "stream", marker, sw.tenantB.tenantID, sw.tenantB.principalID)
	if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Fatal("cross-tenant stream changed B metadata")
	}
	sw.server.clientsMu.Lock()
	clients := len(sw.server.clients)
	sw.server.clientsMu.Unlock()
	if clients != 0 {
		t.Fatal("cross-tenant stream registered a client")
	}
	// This route upgrades to WebSocket (101), not an HTTP 200 stream.
	sw.server.shutdownCh = make(chan struct{})
	defer close(sw.server.shutdownCh)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		sw.handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer srv.Close()
	headers := http.Header{"X-Api-Key": {sw.tenantB.apiKey}, "X-Tenant-Id": {sw.tenantB.tenantID}}
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+path, headers)
	if err != nil {
		t.Fatalf("owner stream handshake: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("owner stream status=%d", response.StatusCode)
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream handler did not stop")
	}
}

func TestCrossTenantSweepJobs(t *testing.T) { runCrossTenantFamily(t, "PR1-02") }

func seedCrossTenantJob(t *testing.T, sw *crossTenantSweep) (string, string) {
	t.Helper()
	marker := "private-" + uuid.NewString()
	body := fmt.Sprintf(`{"prompt":%q,"topic":%q}`, marker, "job."+marker)
	rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/jobs", body)
	assertCrossTenantAbsent(t, rr, "owner submit")
	var result struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil || result.JobID == "" {
		t.Fatalf("decode owner job: %v; %s", err, rr.Body.String())
	}
	if err := sw.server.jobStore.SetSafetyDecision(context.Background(), result.JobID,
		model.SafetyDecisionRecord{Decision: model.SafetyAllow, Reason: marker}); err != nil {
		t.Fatal(err)
	}
	return result.JobID, marker
}

func crossTenantJobMeta(t *testing.T, sw *crossTenantSweep, id string) map[string]string {
	t.Helper()
	meta, err := sw.server.jobStore.GetAllMeta(context.Background(), id)
	if err != nil || len(meta) == 0 || meta["tenant"] != sw.tenantB.tenantID {
		t.Fatalf("B job metadata unavailable: %v, %v", err, meta)
	}
	return meta
}

func crossTenantJobRead(name, path string, list bool, setup ...crossTenantJobSetup) crossTenantCase {
	return crossTenantCase{"GET " + path, name, func(t *testing.T, sw *crossTenantSweep) {
		id, marker := seedCrossTenantJob(t, sw)
		for _, prepare := range setup {
			prepare(t, sw, id, marker)
		}
		before, lists := crossTenantJobMeta(t, sw, id), crossTenantOwnerLists(t, sw)
		target := strings.NewReplacer("{id}", id, "{job_id}", id).Replace(path)
		rr := sw.as(t, sw.tenantA, http.MethodGet, target, "")
		if list {
			assertCrossTenantAbsent(t, rr, name, id, marker)
		} else {
			assertCrossTenantRefused(t, rr, name, marker, sw.tenantB.tenantID, before["principal"])
		}
		if !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) || !reflect.DeepEqual(lists, crossTenantOwnerLists(t, sw)) {
			t.Fatal("cross-tenant read changed B state")
		}
		rr = sw.as(t, sw.tenantB, http.MethodGet, target, "")
		assertCrossTenantAbsent(t, rr, "owner read")
		if !strings.Contains(rr.Body.String(), marker) {
			t.Fatalf("owner response missing seeded marker: %s", rr.Body.String())
		}
	}}
}
