package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var errInjectedRead = errors.New("injected store read failure")

const (
	storeReadSubmitKey = "sk-test-store-read-submit"
	storeReadReviewKey = "sk-test-store-read-review"
)

// approvalReadFault fails one exact single Redis command while armed. The
// first `skip` matching commands pass through; every later match fails with
// errInjectedRead. Pipelines (including the connection handshake) pass through.
type approvalReadFault struct {
	mu        sync.Mutex
	armed     bool
	want      []string
	skip      int
	sightings int
	firings   int
}

func (f *approvalReadFault) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *approvalReadFault) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *approvalReadFault) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if f.shouldFail(cmd) {
			cmd.SetErr(errInjectedRead)
			return errInjectedRead
		}
		return next(ctx, cmd)
	}
}

func (f *approvalReadFault) shouldFail(cmd redis.Cmder) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.armed || !slices.Equal(cmdWords(cmd), f.want) {
		return false
	}
	f.sightings++
	if f.sightings <= f.skip {
		return false
	}
	f.firings++
	return true
}

func (f *approvalReadFault) arm(want []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed, f.want, f.sightings, f.firings = true, want, 0, 0
}

func (f *approvalReadFault) disarm() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = false
	return f.firings
}

func cmdWords(cmd redis.Cmder) []string {
	args := cmd.Args()
	words := []string{strings.ToLower(cmd.Name())}
	for _, arg := range args[1:] {
		words = append(words, fmt.Sprint(arg))
	}
	return words
}

// RedisJobStore.GetState: HGET jobRuntimeKey(jobID) runtimeStateField.
func jobStateCmd(jobID, _ string) []string {
	return []string{"hget", "job:{" + base64.RawURLEncoding.EncodeToString([]byte(jobID)) + "}:runtime", "state"}
}

// RedisJobStore.GetTenant: HGET job:meta:<id> tenant.
func tenantCmd(jobID, _ string) []string { return []string{"hget", "job:meta:" + jobID, "tenant"} }

// RedisJobStore.GetJobRequest: GET job:req:<id>.
func storedRequestCmd(jobID, _ string) []string { return []string{"get", "job:req:" + jobID} }

// RedisJobStore.GetSubmittedBy: HGET job:meta:<id> submitted_by.
func submitterCmd(jobID, _ string) []string {
	return []string{"hget", "job:meta:" + jobID, "submitted_by"}
}

// wf.RedisStore.GetRun: GET wf:run:<runID>.
func workflowRunCmd(_, runID string) []string { return []string{"get", "wf:run:" + runID} }

// RedisJobStore.GetSafetyDecision: HGETALL job:meta:<id>.
func safetyDecisionCmd(jobID, _ string) []string { return []string{"hgetall", "job:meta:" + jobID} }

type storeReadJob struct {
	submitter  string
	jobHash    string // overrides the seeded safety decision's JobHash when set
	noRequest  bool   // seed meta and state without the stored request
	runStatus  wf.RunStatus
	missingRun bool // label a run_id that names no stored run
}

// seedStoreReadJob mirrors seedApprovalJob with labels, an optional run and
// an optional JobHash override. It returns the job ID, run ID and the hash of
// the stored request.
func seedStoreReadJob(t *testing.T, s *server, fx storeReadJob) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	var labels map[string]string
	runID := ""
	if fx.runStatus != "" {
		labels = seedStoreReadRun(t, s, fx.runStatus)
		runID = labels["run_id"]
	} else if fx.missingRun {
		runID = "run-store-read-missing-" + uuid.NewString()
		labels = map[string]string{"workflow_id": "wf-store-read-missing", "run_id": runID, "step_id": "s1"}
	}
	jobID := uuid.NewString()
	req := &pb.JobRequest{JobId: jobID, Topic: "job.test", TenantId: "default", Labels: labels}
	mustNoErr(t, "set job meta", s.jobStore.SetJobMeta(ctx, req))
	if !fx.noRequest {
		mustNoErr(t, "set job req", s.jobStore.SetJobRequest(ctx, req))
	}
	mustNoErr(t, "set state", s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	if fx.submitter != "" {
		mustNoErr(t, "set submitted_by", s.jobStore.SetSubmittedBy(ctx, jobID, fx.submitter))
	}
	hash, err := scheduler.HashJobRequest(req)
	mustNoErr(t, "hash job", err)
	seeded := hash
	if fx.jobHash != "" {
		seeded = fx.jobHash
	}
	mustNoErr(t, "set safety decision", s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-test",
		JobHash:          seeded,
	}))
	return jobID, runID, hash
}

func seedStoreReadRun(t *testing.T, s *server, status wf.RunStatus) map[string]string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	def := &wf.Workflow{
		ID:         "wf-store-read-" + uuid.NewString(),
		OrgID:      "default",
		TimeoutSec: 10,
		Steps:      map[string]*wf.Step{"s1": {ID: "s1", Type: wf.StepTypeWorker, Topic: "job.test"}},
	}
	mustNoErr(t, "save workflow", s.workflowStore.SaveWorkflow(ctx, def))
	run := &wf.WorkflowRun{
		ID:         "run-store-read-" + uuid.NewString(),
		WorkflowID: def.ID,
		OrgID:      "default",
		Status:     status,
		Steps:      map[string]*wf.StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if wf.IsTerminalRunStatus(status) {
		run.CompletedAt = &now
	}
	mustNoErr(t, "create run", s.workflowStore.CreateRun(ctx, run))
	return map[string]string{"workflow_id": def.ID, "run_id": run.ID, "step_id": "s1"}
}

func mustNoErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// serveStoreReadDecision sends approve or reject as the reviewer, or as the
// submitter when self is true.
func serveStoreReadDecision(t *testing.T, s *server, action, jobID string, self bool) *httptest.ResponseRecorder {
	t.Helper()
	key, principal := storeReadReviewKey, "bob"
	if self {
		key, principal = storeReadSubmitKey, "alice"
	}
	req := approvalDecisionRequest(t, jobID, action, "{}", key, principal)
	rr := httptest.NewRecorder()
	if action == "approve" {
		s.handleApproveJob(rr, req)
	} else {
		s.handleRejectJob(rr, req)
	}
	return rr
}

func publishedSubjects(b *stubBus) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.published))
	for _, msg := range b.published {
		out = append(out, msg.subject)
	}
	return out
}

// assertNoDecisionEffects checks that nothing was published, the job is still
// in APPROVAL, no approval was recorded and the stored request is not marked
// as granted.
func assertNoDecisionEffects(t *testing.T, s *server, b *stubBus, jobID string) {
	t.Helper()
	ctx := context.Background()
	if got := publishedSubjects(b); len(got) != 0 {
		t.Errorf("published %v, want nothing", got)
	}
	if state, err := s.jobStore.GetState(ctx, jobID); err != nil || state != model.JobStateApproval {
		t.Errorf("job state = %q (err %v), want %q", state, err, model.JobStateApproval)
	}
	rec, err := s.jobStore.GetApprovalRecord(ctx, jobID)
	if err != nil {
		t.Errorf("get approval record: %v", err)
	} else if rec.Decision != "" || rec.ApprovedBy != "" || rec.ApprovedAt != 0 {
		t.Errorf("approval record = decision %q by %q at %d, want none", rec.Decision, rec.ApprovedBy, rec.ApprovedAt)
	}
	req, err := s.jobStore.GetJobRequest(ctx, jobID)
	switch {
	case errors.Is(err, redis.Nil): // fixture without a stored request
	case err != nil:
		t.Errorf("get job request: %v", err)
	case req.GetLabels()["approval_granted"] == "true":
		t.Errorf("stored request marked approval_granted")
	}
}

func assertUnavailableEnvelope(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	wantBody := map[string]any{
		"error":  want,
		"code":   fallbackErrorCode(http.StatusServiceUnavailable, want),
		"status": float64(http.StatusServiceUnavailable),
	}
	if got := decodeBody(t, rr); !reflect.DeepEqual(got, wantBody) {
		t.Fatalf("body = %v, want %v", got, wantBody)
	}
}

func assertErrorCode(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, status, rr.Body.String())
	}
	if got := decodeBody(t, rr)["code"]; got != code {
		t.Fatalf("code = %v, want %q", got, code)
	}
}

// assertDecisionApplied checks a successful approve (PENDING, one submit
// publish, approval_granted) or reject (DENIED, one DLQ publish).
func assertDecisionApplied(t *testing.T, s *server, b *stubBus, action, jobID string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body %s", action, rr.Code, rr.Body.String())
	}
	ctx := context.Background()
	wantSubject, wantState := capsdk.SubjectDLQ, model.JobStateDenied
	if action == "approve" {
		wantSubject, wantState = capsdk.SubjectSubmit, model.JobStatePending
	}
	if got := publishedSubjects(b); !slices.Equal(got, []string{wantSubject}) {
		t.Errorf("published %v, want exactly [%s]", got, wantSubject)
	}
	if state, err := s.jobStore.GetState(ctx, jobID); err != nil || state != wantState {
		t.Errorf("job state = %q (err %v), want %q", state, err, wantState)
	}
	if action != "approve" {
		return
	}
	req, err := s.jobStore.GetJobRequest(ctx, jobID)
	if err != nil || req.GetLabels()["approval_granted"] != "true" {
		t.Errorf("stored request approval_granted = %q (err %v), want true", req.GetLabels()["approval_granted"], err)
	}
}

type storeReadCase struct {
	name      string
	action    string
	cmd       func(jobID, runID string) []string
	skip      int
	runStatus wf.RunStatus
	self      bool   // the reviewer uses the submitter's identity
	jobHash   string // stale seeded JobHash, so the hash lock-in writes first
	want      string
}

func storeReadCases() []storeReadCase {
	const (
		state, tenant, request = "job state unavailable", "tenant lookup unavailable", "job request unavailable"
		submitter, run, safety = "submitter lookup unavailable", "workflow run lookup unavailable", "safety decision unavailable"
	)
	return []storeReadCase{
		{name: "approve/job_state", action: "approve", cmd: jobStateCmd, want: state},
		{name: "approve/tenant", action: "approve", cmd: tenantCmd, want: tenant},
		{name: "approve/stored_request", action: "approve", cmd: storedRequestCmd, want: request},
		{name: "approve/submitter", action: "approve", cmd: submitterCmd, self: true, want: submitter},
		{name: "approve/run_read", action: "approve", cmd: workflowRunCmd, runStatus: wf.RunStatusTimedOut, want: run},
		{name: "approve/run_recheck", action: "approve", cmd: workflowRunCmd, skip: 1, runStatus: wf.RunStatusRunning, want: run},
		{name: "approve/run_recheck_after_hash_refresh", action: "approve", cmd: workflowRunCmd, skip: 1,
			runStatus: wf.RunStatusRunning, jobHash: "stale-job-hash", want: run},
		{name: "approve/safety_decision", action: "approve", cmd: safetyDecisionCmd, want: safety},
		{name: "reject/job_state", action: "reject", cmd: jobStateCmd, want: state},
		{name: "reject/tenant", action: "reject", cmd: tenantCmd, want: tenant},
		{name: "reject/stored_request", action: "reject", cmd: storedRequestCmd, want: request},
		{name: "reject/submitter", action: "reject", cmd: submitterCmd, self: true, want: submitter},
		{name: "reject/run_read", action: "reject", cmd: workflowRunCmd, runStatus: wf.RunStatusTimedOut, want: run},
	}
}

func TestApproveRejectStoreReadErrorsReturn503(t *testing.T) {
	t.Parallel()
	for _, tc := range storeReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, b, safety := newTestGateway(t)
			safety.setSnapshots([]string{"snap-test"})
			fault := &approvalReadFault{skip: tc.skip}
			client := s.jobStore.Client()
			client.AddHook(fault)
			s.workflowStore = wf.NewRedisWorkflowStoreFromClient(client)

			jobID, runID, hash := seedStoreReadJob(t, s, storeReadJob{
				submitter: submitterIdentityForTest(storeReadSubmitKey, "alice"),
				runStatus: tc.runStatus,
				jobHash:   tc.jobHash,
			})
			fault.arm(tc.cmd(jobID, runID))
			rr := serveStoreReadDecision(t, s, tc.action, jobID, tc.self)
			if fired := fault.disarm(); fired != 1 {
				t.Errorf("fault fired %d times, want 1", fired)
			}
			assertNoDecisionEffects(t, s, b, jobID)
			if tc.jobHash != "" {
				rec, err := s.jobStore.GetSafetyDecision(context.Background(), jobID)
				if err != nil || rec.JobHash != hash {
					t.Errorf("stored JobHash = %q (err %v), want the recomputed %q", rec.JobHash, err, hash)
				}
			}
			assertUnavailableEnvelope(t, rr, tc.want)

			// Recovery control: the same request goes through once reads succeed.
			rr = serveStoreReadDecision(t, s, tc.action, jobID, tc.self)
			switch {
			case tc.self:
				assertErrorCode(t, rr, http.StatusForbidden, "self_approval_denied")
				assertNoDecisionEffects(t, s, b, jobID)
			case tc.runStatus == wf.RunStatusTimedOut:
				assertErrorCode(t, rr, http.StatusConflict, string(model.ApprovalConflictTerminalRun))
				assertNoDecisionEffects(t, s, b, jobID)
			default:
				assertDecisionApplied(t, s, b, tc.action, jobID, rr)
			}
		})
	}
}

func TestApproveRejectMissingRecordsKeepEarlierHandling(t *testing.T) {
	t.Parallel()
	submitter := submitterIdentityForTest(storeReadSubmitKey, "alice")
	cases := []struct {
		name string
		job  storeReadJob
	}{
		{name: "missing_run", job: storeReadJob{submitter: submitter, missingRun: true}},
		{name: "no_submitter", job: storeReadJob{}},
		{name: "missing_request", job: storeReadJob{submitter: submitter, noRequest: true}},
	}
	for _, action := range []string{"approve", "reject"} {
		for _, tc := range cases {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				s, b, safety := newTestGateway(t)
				safety.setSnapshots([]string{"snap-test"})
				jobID, _, _ := seedStoreReadJob(t, s, tc.job)
				rr := serveStoreReadDecision(t, s, action, jobID, false)
				if !tc.job.noRequest {
					assertDecisionApplied(t, s, b, action, jobID, rr)
					return
				}
				assertNoDecisionEffects(t, s, b, jobID)
				if rr.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
				}
				if got := decodeBody(t, rr)["error"]; got != "job request not found" {
					t.Fatalf("error = %v, want %q", got, "job request not found")
				}
			})
		}
	}
}
