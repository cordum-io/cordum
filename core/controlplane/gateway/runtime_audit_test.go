package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

// runtimeDispatch is a handler that starts a new job, seeded for one test.
type runtimeDispatch struct {
	action, notStored string
	failStatus        int
	// seed stores the source job and returns its id and the trace the new job joins.
	seed func(t *testing.T, s *server) (jobID, traceID string)
	call func(s *server, w http.ResponseWriter, r *http.Request)
}

var runtimeDispatches = []runtimeDispatch{
	{
		action: "remediate", notStored: "remediate not dispatched: its audit entry was not stored", failStatus: http.StatusBadGateway,
		seed: seedRuntimeRemediation,
		call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleRemediateJob(w, r) },
	},
	{
		action: "retry", notStored: "retry not dispatched: its audit entry was not stored", failStatus: http.StatusInternalServerError,
		seed: seedRuntimeDLQ,
		call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleRetryDLQ(w, r) },
	},
}

func seedRuntimeRemediation(t *testing.T, s *server) (string, string) {
	t.Helper()
	ctx := context.Background()
	orig := &pb.JobRequest{JobId: "job-remediate", Topic: "job.db.delete", TenantId: "default", Meta: &pb.JobMetadata{Capability: "db.delete"}}
	require.NoError(t, s.jobStore.SetJobRequest(ctx, orig))
	require.NoError(t, s.jobStore.SetJobMeta(ctx, orig))
	require.NoError(t, s.jobStore.AddJobToTrace(ctx, "trace-remediate", orig.JobId))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, orig.JobId, model.SafetyDecisionRecord{
		Decision:     model.SafetyDeny,
		Remediations: []*pb.PolicyRemediation{{Id: "archive", ReplacementTopic: "job.db.archive"}},
	}))
	return orig.JobId, "trace-remediate"
}

func seedRuntimeDLQ(t *testing.T, s *server) (string, string) {
	t.Helper()
	ctx := context.Background()
	jobID := "job-retry"
	require.NoError(t, s.dlqStore.Add(ctx, store.DLQEntry{JobID: jobID, Topic: "job.test", CreatedAt: time.Now().UTC()}))
	require.NoError(t, s.jobStore.SetTopic(ctx, jobID, "job.test"))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	return jobID, "dlq-retry-" + jobID
}

func runtimeJobRequest(jobID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID, strings.NewReader(`{}`))
	r.Header.Set("X-Tenant-ID", "default")
	r.SetPathValue("id", jobID)
	r.SetPathValue("job_id", jobID)
	return r
}

// runtimeNewJobState returns the state of the one job that joined traceID besides jobID.
func runtimeNewJobState(t *testing.T, s *server, traceID, jobID string) model.JobState {
	t.Helper()
	records, err := s.jobStore.GetTraceJobs(context.Background(), traceID)
	require.NoError(t, err)
	var states []model.JobState
	for _, rec := range records {
		if rec.ID != jobID {
			state, err := s.jobStore.GetState(context.Background(), rec.ID)
			require.NoError(t, err)
			states = append(states, state)
		}
	}
	require.Len(t, states, 1, "the handler must create exactly one new job")
	return states[0]
}

func TestRuntimeAudit_EntryFailureDispatchesNothing(t *testing.T) {
	for _, tc := range runtimeDispatches {
		t.Run(tc.action, func(t *testing.T) {
			s, _, hooked, _ := auditPersistServer(t)
			jobID, traceID := tc.seed(t, s)
			hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
			logs := storeWriterLogs(t)
			sink := &recordingAuditSender{}
			s.auditExporter = sink

			rr := httptest.NewRecorder()
			tc.call(s, rr, runtimeJobRequest(jobID))

			require.Equal(t, tc.failStatus, rr.Code, rr.Body.String())
			require.Contains(t, rr.Body.String(), tc.notStored)
			require.Empty(t, s.bus.(*stubBus).published, "nothing may be dispatched")
			require.Equal(t, model.JobStateFailed, runtimeNewJobState(t, s, traceID, jobID), "the replayer must not pick the new job up")
			require.Empty(t, sink.events)
			require.Len(t, auditPersistErrors(logs, tc.action), 1)
			if tc.action == "retry" {
				_, err := s.dlqStore.Get(context.Background(), jobID)
				require.NoError(t, err, "the DLQ entry must stay for a later retry")
			}
		})
	}
}

func TestRuntimeAudit_PublishFailureRecordsFailedAttempt(t *testing.T) {
	for _, tc := range runtimeDispatches {
		t.Run(tc.action, func(t *testing.T) {
			s, _, _, _ := auditPersistServer(t)
			jobID, _ := tc.seed(t, s)
			s.bus.(*stubBus).publishErr = errors.New("bus down")

			rr := httptest.NewRecorder()
			tc.call(s, rr, runtimeJobRequest(jobID))

			require.GreaterOrEqual(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
			require.Equal(t, []string{tc.action + "_failed", tc.action}, auditPersistActions(t, s))
		})
	}
}

func TestRuntimeAudit_CancelJobAuditFailureStillCancels(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	jobID := "job-cancel"
	require.NoError(t, s.jobStore.SetState(context.Background(), jobID, model.JobStatePending))
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)

	rr := httptest.NewRecorder()
	s.handleCancelJob(rr, runtimeJobRequest(jobID))

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	state, err := s.jobStore.GetState(context.Background(), jobID)
	require.NoError(t, err)
	require.Equal(t, model.JobStateCancelled, state)
	require.Len(t, auditPersistErrors(logs, "cancel"), 1)
}

func TestRuntimeAudit_DLQRetryStoresOneEntry(t *testing.T) {
	s, _, _, _ := auditPersistServer(t)
	jobID, _ := seedRuntimeDLQ(t, s)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	r := runtimeJobRequest(jobID)

	rr := httptest.NewRecorder()
	s.handleRetryDLQ(rr, r)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	runtimeRequireOneEntry(t, s, sink, r, policybundles.PolicyAuditEntry{
		Action: "retry", ResourceType: "dlq", ResourceID: jobID, ResourceName: "job.test",
		ActorID: policybundles.PolicyActorID(r), Role: policybundles.PolicyRole(r), Message: "retry dlq entry " + jobID,
	})
}

// runtimeRequireOneEntry checks that want is the only stored entry, that the
// policy audit API returns it and that the SIEM export got it once.
func runtimeRequireOneEntry(t *testing.T, s *server, sink *recordingAuditSender, r *http.Request, want policybundles.PolicyAuditEntry) {
	t.Helper()
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	want.ID, want.CreatedAt = entries[0].ID, entries[0].CreatedAt
	require.Equal(t, want, entries[0])
	require.Len(t, sink.events, 1)
	require.Equal(t, policybundles.AuditEntryToSIEM(entries[0], s.tenant), sink.events[0])
	api := httptest.NewRecorder()
	s.handleListPolicyAudit(api, r)
	require.Equal(t, http.StatusOK, api.Code, api.Body.String())
	var response struct {
		Items []policybundles.PolicyAuditEntry `json:"items"`
	}
	require.NoError(t, json.Unmarshal(api.Body.Bytes(), &response))
	require.Equal(t, entries, response.Items)
}
