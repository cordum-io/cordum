package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestMakeJobPacketProductionRejectsCompensationEscalationWithoutMutation(t *testing.T) {
	run := productionIdentityRun()
	req := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Compensation: &pb.Compensation{Topic: "job.undo", PrincipalId: "principal-admin"},
	}
	before := proto.Clone(req)
	engine := NewEngine(nil, nil).WithProductionIdentityEnforcement(true)

	packet, err := engine.makeJobPacket("trace-1", run, req)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("makeJobPacket() error = %v, want mismatch", err)
	}
	if packet != nil {
		t.Fatalf("makeJobPacket() = %#v, want nil", packet)
	}
	if !proto.Equal(req, before) {
		t.Fatal("makeJobPacket mutated rejected request")
	}
}

func TestMakeJobPacketProductionBindsEnvelopeAndRequest(t *testing.T) {
	run := productionIdentityRun()
	req := &pb.JobRequest{JobId: "job-1", Topic: "job.test"}
	engine := NewEngine(nil, nil).WithProductionIdentityEnforcement(true)

	packet, err := engine.makeJobPacket("trace-1", run, req)
	if err != nil {
		t.Fatalf("makeJobPacket() error = %v", err)
	}
	if !proto.Equal(packet.GetIdentity(), run.Identity) || !proto.Equal(packet.GetJobRequest().GetIdentity(), run.Identity) {
		t.Fatalf("packet identities = envelope:%v request:%v", packet.GetIdentity(), packet.GetJobRequest().GetIdentity())
	}
	if req.GetIdentity() != nil {
		t.Fatal("makeJobPacket mutated input request")
	}
}

func TestCloneIdentityBindingPreservesUnknownsWithoutAliasing(t *testing.T) {
	original := productionIdentityRun().Identity
	original.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})

	cloned := cloneIdentityBinding(original)
	if !proto.Equal(cloned, original) {
		t.Fatalf("cloneIdentityBinding() = %v, want %v", cloned, original)
	}
	cloned.ActorId = "changed"
	if original.GetActorId() != "principal-a" {
		t.Fatal("cloneIdentityBinding returned an alias")
	}
}

func TestHandleJobResultProductionRejectsRunIdentityMismatchBeforeMutation(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	wf := &Workflow{ID: "wf-identity", OrgID: "tenant-a", Steps: map[string]*Step{
		"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
	}}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("SaveWorkflow() error = %v", err)
	}
	now := time.Now().UTC()
	run := productionIdentityRun()
	run.ID, run.WorkflowID, run.Status = "run-identity", wf.ID, RunStatusRunning
	run.CreatedAt, run.UpdatedAt = now, now
	run.Steps = map[string]*StepRun{"step": {
		StepID: "step", JobID: "run-identity:step@1", Status: StepStatusRunning,
	}}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	engine := NewEngine(store, &recordingBus{}).WithProductionIdentityEnforcement(true)
	spoofed := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "admin", ActorId: "admin"}

	err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId: "run-identity:step@1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, Identity: spoofed,
	})
	if err != nil {
		t.Fatalf("HandleJobResult() error = %v", err)
	}
	stored, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got := stored.Steps["step"].Status; got != StepStatusRunning {
		t.Fatalf("step status = %q, want unchanged running", got)
	}
	if err := engine.HandleJobResult(ctx, &pb.JobResult{
		JobId: "run-identity:step@1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, Identity: run.Identity,
	}); err != nil {
		t.Fatalf("HandleJobResult(valid identity) error = %v", err)
	}
	stored, err = store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun(valid identity) error = %v", err)
	}
	if got := stored.Steps["step"].Status; got != StepStatusSucceeded {
		t.Fatalf("step status = %q, want succeeded for canonical identity", got)
	}
}

func TestSubworkflowRunPropagatesParentIdentity(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	child := &Workflow{ID: "wf-child-identity", OrgID: "tenant-a", Steps: map[string]*Step{
		"work": {ID: "work", Type: StepTypeWorker, Topic: "job.test"},
	}}
	parent := &Workflow{ID: "wf-parent-identity", OrgID: "tenant-a", Steps: map[string]*Step{
		"child": {ID: "child", Type: StepTypeSubWorkflow, Input: map[string]any{"workflow_id": child.ID}},
	}}
	for _, wf := range []*Workflow{child, parent} {
		if err := store.SaveWorkflow(ctx, wf); err != nil {
			t.Fatalf("SaveWorkflow(%s) error = %v", wf.ID, err)
		}
	}
	now := time.Now().UTC()
	run := productionIdentityRun()
	run.ID, run.WorkflowID, run.Status = "run-parent-identity", parent.ID, RunStatusPending
	run.Input, run.Steps, run.CreatedAt, run.UpdatedAt = map[string]any{}, map[string]*StepRun{}, now, now
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	engine := NewEngine(store, &recordingBus{}).WithProductionIdentityEnforcement(true)

	if err := engine.StartRun(ctx, parent.ID, run.ID); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	updated, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun(parent) error = %v", err)
	}
	childRun, err := store.GetRun(ctx, updated.Steps["child"].JobID)
	if err != nil {
		t.Fatalf("GetRun(child) error = %v", err)
	}
	if !proto.Equal(childRun.Identity, run.Identity) {
		t.Fatalf("child identity = %v, want %v", childRun.Identity, run.Identity)
	}
}

func productionIdentityRun() *WorkflowRun {
	return &WorkflowRun{
		ID: "run-1", OrgID: "tenant-a",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a"},
	}
}
