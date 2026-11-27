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

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
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

func runtimeWorkflow(t *testing.T, s *server) *wf.Workflow {
	t.Helper()
	s.workflowEng = wf.NewEngine(s.workflowStore, s.bus)
	wfDef := &wf.Workflow{ID: "wf-runtime-audit", OrgID: "default", Name: "Runtime audit",
		Steps: map[string]*wf.Step{"approve": {ID: "approve", Type: wf.StepTypeApproval}}}
	require.NoError(t, s.workflowStore.SaveWorkflow(context.Background(), wfDef))
	return wfDef
}

func runtimeRunRequest(path, id, idempotencyKey string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	r.Header.Set("X-Tenant-ID", "default")
	if idempotencyKey != "" {
		r.Header.Set("Idempotency-Key", idempotencyKey)
	}
	r.SetPathValue("id", id)
	return r
}

func TestRuntimeAudit_StartRunAuditFailureCreatesNoRun(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	wfDef := runtimeWorkflow(t, s)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	ctx := context.Background()

	rr := httptest.NewRecorder()
	s.handleStartRun(rr, runtimeRunRequest("/api/v1/workflows/"+wfDef.ID+"/runs", wfDef.ID, "start-key"))

	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "run not dispatched: its audit entry was not stored")
	runs, err := s.workflowStore.ListRunsByWorkflow(ctx, wfDef.ID, 10)
	require.NoError(t, err)
	require.Empty(t, runs, "no run record may exist")
	pending, err := s.workflowStore.ListRunIDsByStatus(ctx, wf.RunStatusPending, 10)
	require.NoError(t, err)
	require.Empty(t, pending, "the reconciler must find nothing to start")
	require.Empty(t, s.bus.(*stubBus).published)
	_, err = s.workflowStore.GetRunByIdempotencyKey(ctx, "default", wfDef.ID, "start-key")
	require.ErrorIs(t, err, redis.Nil, "the idempotency key is released for a retry")
	require.Len(t, auditPersistErrors(logs, "start"), 1)
}

func TestRuntimeAudit_StartRunReplayStoresOneEntry(t *testing.T) {
	s, _, _, _ := auditPersistServer(t)
	wfDef := runtimeWorkflow(t, s)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	var runIDs []string
	var r *http.Request
	for range 2 {
		r = runtimeRunRequest("/api/v1/workflows/"+wfDef.ID+"/runs", wfDef.ID, "replay-key")
		rr := httptest.NewRecorder()
		s.handleStartRun(rr, r)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var resp map[string]string
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		runIDs = append(runIDs, resp["run_id"])
	}

	require.Equal(t, runIDs[0], runIDs[1], "the replay must reuse the run")
	runtimeRequireOneEntry(t, s, sink, r, policybundles.PolicyAuditEntry{
		Action: "start", ResourceType: "run", ResourceID: runIDs[0], ResourceName: wfDef.Name,
		ActorID: policybundles.PolicyActorID(r), Role: policybundles.PolicyRole(r), Message: "start run " + runIDs[0],
	})
}

func TestRuntimeAudit_RerunAuditFailureLeavesNewRunFailed(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	wfDef := runtimeWorkflow(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	orig := &wf.WorkflowRun{ID: "run-orig", WorkflowID: wfDef.ID, OrgID: "default", Status: wf.RunStatusFailed,
		Steps: map[string]*wf.StepRun{}, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, s.workflowStore.CreateRun(ctx, orig))
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})

	rr := httptest.NewRecorder()
	s.handleRerunRun(rr, runtimeRunRequest("/api/v1/workflow-runs/"+orig.ID+"/rerun", orig.ID, ""))

	require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "rerun not dispatched: its audit entry was not stored")
	runs, err := s.workflowStore.ListRunsByWorkflow(ctx, wfDef.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	for _, run := range runs {
		if run.ID != orig.ID {
			require.Equal(t, wf.RunStatusFailed, run.Status, "the reconciler must not start the rerun")
			require.Empty(t, run.Steps, "the rerun must not have started")
		}
	}
	require.Empty(t, s.bus.(*stubBus).published)
}

// TestRuntimeAudit_SideEffectFirstReportsLostEntry covers callers whose change
// is stored before the entry: they answer 500 and say what was applied.
func TestRuntimeAudit_SideEffectFirstReportsLostEntry(t *testing.T) {
	cases := []struct {
		name, want string
		call       func(t *testing.T, s *server) *httptest.ResponseRecorder
		applied    func(t *testing.T, s *server)
	}{
		{
			name: "create workflow", want: "workflow created but its audit entry was not stored",
			call: func(t *testing.T, s *server) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows",
					strings.NewReader(`{"id":"wf-created","org_id":"default","name":"Created","steps":{"approve":{"type":"approval"}}}`))
				r.Header.Set("X-Tenant-ID", "default")
				rr := httptest.NewRecorder()
				s.handleCreateWorkflow(rr, r)
				return rr
			},
			applied: func(t *testing.T, s *server) {
				_, err := s.workflowStore.GetWorkflow(context.Background(), "wf-created")
				require.NoError(t, err)
			},
		},
		{
			name: "create eval dataset", want: "eval dataset created but its audit entry was not stored",
			call: func(t *testing.T, s *server) *httptest.ResponseRecorder {
				return evalPostCreate(t, bindEvalDatasetRoutes(t, s), evalCreateBody("audit-pack", 1, 1), "admin")
			},
			applied: func(t *testing.T, s *server) {
				_, err := s.evalDatasetStore.GetEvalDatasetByNameVersion(context.Background(), testEvalTenant, "audit-pack", 1)
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, hooked, _ := auditPersistServer(t)
			hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})

			rr := tc.call(t, s)

			require.Equal(t, http.StatusInternalServerError, rr.Code, rr.Body.String())
			require.Contains(t, rr.Body.String(), tc.want)
			tc.applied(t, s)
		})
	}
}

func TestRuntimeAudit_PolicyCheckAuditFailureStillAnswers(t *testing.T) {
	s, _, hooked, _ := auditPersistServer(t)
	s.auth = newBasicAuthForTest(t, nil)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(`{"topic": "job.default"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant-ID", "default")
	r.Header.Set("X-API-Key", "test-api-key")
	r = withAuth(r, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-user"})

	rr := httptest.NewRecorder()
	s.handlePolicyEvaluate(rr, r)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Len(t, auditPersistErrors(logs, "policy.evaluate"), 1)
}
