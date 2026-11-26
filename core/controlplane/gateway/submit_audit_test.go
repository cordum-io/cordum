package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// submitAuditGateway returns a test gateway whose policy audit log cannot be
// stored when failing is true.
func submitAuditGateway(t *testing.T, failing bool) (*server, *stubBus, *stubSafetyClient) {
	t.Helper()
	s, _, hooked, _ := auditPersistServer(t)
	if failing {
		hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	}
	s.tenant = "default"
	return s, s.bus.(*stubBus), s.safetyClient.(*stubSafetyClient)
}

func submitAuditHTTP(t *testing.T, s *server, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"prompt": "hi", "topic": "job.test", "idempotency_key": key})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	return rec
}

// submitAuditJobState returns the state of the job that key reserved.
func submitAuditJobState(t *testing.T, s *server, key string) (string, model.JobState) {
	t.Helper()
	ctx := context.Background()
	jobID, err := s.jobStore.GetJobByIdempotencyKeyScoped(ctx, "default", key)
	require.NoError(t, err)
	require.NotEmpty(t, jobID)
	state, err := s.jobStore.GetState(ctx, jobID)
	require.NoError(t, err)
	return jobID, state
}

func submitAuditPublished(bus *stubBus) []publishedMessage {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return append([]publishedMessage(nil), bus.published...)
}

func TestSubmitJobHTTP_AuditFailureFailsJobWithoutPublishing(t *testing.T) {
	s, bus, _ := submitAuditGateway(t, true)
	logs := storeWriterLogs(t)

	rec := submitAuditHTTP(t, s, "submit-audit-http")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), submitAuditNotStored)
	jobID, state := submitAuditJobState(t, s, "submit-audit-http")
	require.Equal(t, model.JobStateFailed, state)
	require.Empty(t, submitAuditPublished(bus), "a job whose audit entry was not stored was dispatched")
	_, err := s.memStore.GetContext(context.Background(), store.MakeContextKey(jobID))
	require.ErrorIs(t, err, redis.Nil, "the undispatched job's context was not collected")
	require.Len(t, auditPersistErrors(logs, "submit"), 1)
}

func TestSubmitJobGRPC_AuditFailureFailsJobWithoutPublishing(t *testing.T) {
	s, bus, _ := submitAuditGateway(t, true)

	_, err := s.SubmitJob(context.Background(), &pb.SubmitJobRequest{Prompt: "hi", Topic: "job.default", IdempotencyKey: "submit-audit-grpc"})

	require.Equal(t, codes.Unavailable, status.Code(err), "err=%v", err)
	require.Contains(t, status.Convert(err).Message(), submitAuditNotStored)
	_, state := submitAuditJobState(t, s, "submit-audit-grpc")
	require.Equal(t, model.JobStateFailed, state)
	require.Empty(t, submitAuditPublished(bus), "a job whose audit entry was not stored was dispatched")
}

func TestSubmitJobHTTP_StoredSubmitEntryReachesAuditAPIAndSIEM(t *testing.T) {
	s, bus, _ := submitAuditGateway(t, false)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	rec := submitAuditHTTP(t, s, "submit-audit-ok")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, submitAuditPublished(bus), 1)
	jobID, _ := submitAuditJobState(t, s, "submit-audit-ok")

	apiReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit?action=submit", nil)
	apiReq.Header.Set("X-Tenant-ID", "default")
	apiReq.Header.Set("X-Principal-Role", "admin")
	apiRec := httptest.NewRecorder()
	s.handleListPolicyAudit(apiRec, apiReq)
	require.Equal(t, http.StatusOK, apiRec.Code, apiRec.Body.String())
	var listed struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(apiRec.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 1)
	entry := listed.Items[0]
	require.Equal(t, "submit", entry.Action)
	require.Equal(t, "job", entry.ResourceType)
	require.Equal(t, jobID, entry.ResourceID)
	require.Equal(t, "job.test", entry.ResourceName)
	require.Equal(t, "submit job "+jobID, entry.Message)
	require.Equal(t, "allow", entry.Decision)
	require.Equal(t, "job.test", entry.Extra["topic"])

	var sent []audit.SIEMEvent
	for _, ev := range sink.events {
		if ev.Action == "submit" {
			sent = append(sent, ev)
		}
	}
	require.Equal(t, []audit.SIEMEvent{policybundles.AuditEntryToSIEM(entry, s.tenant)}, sent)
}

// The submit refusal and hold entries must complete: the request keeps its
// status when the entry is not stored, and appendPolicyAudit logs ERROR.
func TestSubmitJobHTTP_RefusalAndHoldKeepStatusWhenAuditFails(t *testing.T) {
	cases := []struct {
		name, action string
		resp         *pb.PolicyCheckResponse
		wantCode     int
		wantState    model.JobState
	}{
		{"deny", "submit_denied", &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "denied"}, http.StatusForbidden, model.JobStateDenied},
		{"throttle", "submit_throttled", &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_THROTTLE, Reason: "throttled"}, http.StatusTooManyRequests, ""},
		{"approval", "submit_approval_required", &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, Reason: "held", ApprovalRequired: true}, http.StatusOK, model.JobStateApproval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, safety := submitAuditGateway(t, true)
			safety.setResponse(tc.resp)
			logs := storeWriterLogs(t)

			rec := submitAuditHTTP(t, s, "submit-audit-"+tc.name)

			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			if tc.wantState != "" {
				_, state := submitAuditJobState(t, s, "submit-audit-"+tc.name)
				require.Equal(t, tc.wantState, state)
			}
			require.Len(t, auditPersistErrors(logs, tc.action), 1)
			require.Empty(t, auditPersistActions(t, s))
		})
	}
}
