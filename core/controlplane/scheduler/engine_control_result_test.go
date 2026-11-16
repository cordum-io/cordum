package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
)

type controlResultCase struct {
	name         string
	status       pb.JobStatus
	initialState model.JobState
	worker       bool
	errorMessage string
	wantState    model.JobState
	wantAccepted int
	wantReason   string
}

func TestProductionControlResult(t *testing.T) {
	cases := []controlResultCase{
		{name: "cancel", status: pb.JobStatus_JOB_STATUS_CANCELLED,
			initialState: model.JobStateRunning, wantState: model.JobStateCancelled, wantAccepted: 1},
		{name: "approval_reject", status: pb.JobStatus_JOB_STATUS_DENIED, errorMessage: "approval rejected",
			initialState: model.JobStateDenied, wantState: model.JobStateDenied, wantAccepted: 1},
		{name: "approval_repair", status: pb.JobStatus_JOB_STATUS_DENIED, errorMessage: "operator rejected the gate",
			initialState: model.JobStateDenied, wantState: model.JobStateDenied, wantAccepted: 1},
		{name: "success_without_fence", status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
			initialState: model.JobStateRunning, wantState: model.JobStateRunning, wantReason: "no_dispatch_fence"},
		{name: "denial_on_running", status: pb.JobStatus_JOB_STATUS_DENIED,
			initialState: model.JobStateRunning, wantState: model.JobStateRunning},
		{name: "cancel_after_success", status: pb.JobStatus_JOB_STATUS_CANCELLED,
			initialState: model.JobStateSucceeded, wantState: model.JobStateSucceeded},
		{name: "worker_cancel_without_fence", status: pb.JobStatus_JOB_STATUS_CANCELLED, worker: true,
			initialState: model.JobStateRunning, wantState: model.JobStateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkProductionControlResult(t, tc) })
	}
}

func checkProductionControlResult(t *testing.T, tc controlResultCase) {
	t.Helper()
	f := newBusMatrixFixture(t, true)
	const jobID = "job-control-result"
	seedControlResultState(t, f, jobID, tc.initialState)
	signer, credential := servicetoken.IdentityGateway, identity.ServiceToken
	if tc.worker {
		signer, credential = matrixWorkerID, identity.WorkerSession
	}
	packet := f.packet(t, credential, signer)
	result := &pb.JobResult{JobId: jobID, WorkerId: signer, Status: tc.status, Identity: matrixJobIdentity()}
	if tc.status == pb.JobStatus_JOB_STATUS_DENIED {
		result.ErrorCode, result.ErrorCodeEnum = "approval_rejected", pb.ErrorCode_ERROR_CODE_SAFETY_DENIED
		result.ErrorMessage = tc.errorMessage
	}
	packet.Payload = &pb.BusPacket_JobResult{JobResult: result}
	var logs strings.Builder
	if tc.wantReason != "" {
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(previous) })
	}
	f.bus.mu.Lock()
	before := f.bus.published[capsdk.SubjectAcceptedResult]
	f.bus.mu.Unlock()
	if got := f.present(t, capsdk.SubjectResult, signer, packet); got != bus.RawAdmissionAccepted {
		t.Fatalf("admission = %v, want accepted", got)
	}
	if got := f.jobState(t, jobID); got != string(tc.wantState) {
		t.Errorf("state = %s, want %s", got, tc.wantState)
	}
	want := fmt.Sprintf("%d publishes on %s", before+tc.wantAccepted, capsdk.SubjectAcceptedResult)
	if got := f.bus.publishes(capsdk.SubjectAcceptedResult); got != want {
		t.Errorf("accepted results = %s, want %s", got, want)
	}
	if tc.wantReason != "" && !strings.Contains(logs.String(), "reason="+tc.wantReason) {
		t.Errorf("log = %q, want reason=%s", logs.String(), tc.wantReason)
	}
}

func seedControlResultState(t *testing.T, f *busMatrixFixture, jobID string, state model.JobState) {
	t.Helper()
	if state == model.JobStateDenied {
		ctx := context.Background()
		for _, next := range []model.JobState{model.JobStatePending, model.JobStateApproval, model.JobStateDenied} {
			if err := f.jobs.SetState(ctx, jobID, next); err != nil {
				t.Fatalf("seed approval state %s: %v", next, err)
			}
		}
		request := &pb.JobRequest{JobId: jobID, Topic: capsdk.SubjectWorkflowApprovalGate,
			TenantId: matrixTenant, PrincipalId: "principal-matrix", Identity: matrixJobIdentity(),
			Meta: &pb.JobMetadata{TenantId: matrixTenant, ActorId: "actor-matrix"}}
		if err := f.jobs.SetJobRequest(ctx, request); err != nil {
			t.Fatalf("seed approval request: %v", err)
		}
		return
	}
	fence := f.dispatchedJob(t, jobID)
	if state == model.JobStateSucceeded {
		packet := f.packet(t, identity.WorkerSession, matrixWorkerID)
		packet.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: jobID, WorkerId: matrixWorkerID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
			Dispatch: fence, Identity: matrixJobIdentity(),
		}}
		if got := f.present(t, capsdk.SubjectResult, matrixWorkerID, packet); got != bus.RawAdmissionAccepted {
			t.Fatalf("worker result admission = %v, want accepted", got)
		}
	}
	if got := f.jobState(t, jobID); got != string(state) {
		t.Fatalf("initial state = %s, want %s", got, state)
	}
}

func TestProductionControlResultGuards(t *testing.T) {
	for _, name := range []string{"nil_claims", "raw_authority_mismatch", "other_service"} {
		t.Run(name, func(t *testing.T) {
			engine, jobs, outbound, result, packet, claims := productionResultFixture(t)
			result.Dispatch, result.Status = nil, pb.JobStatus_JOB_STATUS_CANCELLED
			packet.SenderId, result.WorkerId = servicetoken.IdentityGateway, servicetoken.IdentityGateway
			claims.Subject, claims.Tenant = servicetoken.IdentityGateway, servicetoken.ReservedTenant
			if name == "other_service" {
				packet.SenderId, result.WorkerId = servicetoken.IdentityScheduler, servicetoken.IdentityScheduler
				claims.Subject = servicetoken.IdentityScheduler
			}
			authority := productionEventAuthority(t, packet)
			authority.TenantID = claims.Tenant
			if name == "nil_claims" {
				claims = nil
			}
			if name == "raw_authority_mismatch" {
				authority.SessionSubject = matrixWorkerID
			}
			if err := engine.handleProductionJobResult(authority, packet, result, claims); err != nil {
				t.Fatalf("handle result: %v", err)
			}
			if got, err := jobs.GetState(context.Background(), result.GetJobId()); err != nil || got != model.JobStateRunning {
				t.Errorf("state = %s, err = %v, want RUNNING", got, err)
			}
			if got := len(outbound.snapshotPublished()); got != 0 {
				t.Errorf("published = %d, want 0", got)
			}
		})
	}
}

func TestProductionControlResultRedelivery(t *testing.T) {
	engine, jobs, outbound, result, packet, claims := productionResultFixture(t)
	result.Dispatch, result.Status = nil, pb.JobStatus_JOB_STATUS_CANCELLED
	packet.SenderId, result.WorkerId = servicetoken.IdentityGateway, servicetoken.IdentityGateway
	claims.Subject, claims.Tenant = servicetoken.IdentityGateway, servicetoken.ReservedTenant
	authority := productionEventAuthority(t, packet)
	authority.TenantID = claims.Tenant
	for want := 1; want <= 2; want++ {
		if err := engine.handleProductionJobResult(authority, packet, result, claims); err != nil {
			t.Fatalf("delivery %d: %v", want, err)
		}
		if got, err := jobs.GetState(context.Background(), result.GetJobId()); err != nil || got != model.JobStateCancelled {
			t.Fatalf("state = %s, err = %v, want CANCELLED", got, err)
		}
		published := outbound.snapshotPublished()
		if len(published) != want || published[want-1].subject != capsdk.SubjectAcceptedResult {
			t.Fatalf("delivery %d: published = %+v, want %d accepted results", want, published, want)
		}
	}
}
