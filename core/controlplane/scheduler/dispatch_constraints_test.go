package scheduler

import (
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func schedulerOnlyConstraints() *pb.PolicyConstraints {
	return &pb.PolicyConstraints{
		Budgets: &pb.BudgetConstraints{MaxRuntimeMs: 1000, MaxRetries: 2, MaxConcurrentJobs: 1},
		Sandbox: &pb.SandboxProfile{}, Toolchain: &pb.ToolchainConstraints{}, Diff: &pb.DiffConstraints{},
	}
}

func TestRequiredWorkerConstraintKinds(t *testing.T) {
	mixed := schedulerOnlyConstraints()
	mixed.Budgets.MaxArtifactBytes = 100
	mixed.Sandbox = &pb.SandboxProfile{Isolated: true, NetworkAllowlist: []string{"example.org"}, FsReadOnly: []string{"/src"}, FsReadWrite: []string{"/out"}}
	mixed.Toolchain = &pb.ToolchainConstraints{AllowedTools: []string{"go"}, AllowedCommands: []string{"go test"}}
	mixed.Diff = &pb.DiffConstraints{MaxFiles: 1, MaxLines: 2, DenyPathGlobs: []string{"*.key"}}
	mixed.RedactionLevel = "strict"
	for _, tc := range []struct {
		name        string
		constraints *pb.PolicyConstraints
		want        []string
	}{
		{"nil", nil, nil},
		{"empty", &pb.PolicyConstraints{}, nil},
		{"kernel_shaped_budgets", schedulerOnlyConstraints(), nil},
		{"mixed", mixed, []string{
			"budgets.max_artifact_bytes", "diff.deny_path_globs", "diff.max_files", "diff.max_lines", "redaction_level",
			"sandbox.fs_read_only", "sandbox.fs_read_write", "sandbox.isolated", "sandbox.network_allowlist",
			"toolchain.allowed_commands", "toolchain.allowed_tools",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, requiredWorkerConstraintKinds(tc.constraints))
		})
	}
}

func TestMissingWorkerConstraintKinds(t *testing.T) {
	kinds := []string{"diff.max_files", "sandbox.isolated"}
	for _, tc := range []struct {
		name   string
		worker *pb.Heartbeat
		want   []string
	}{
		{"nil", nil, kinds},
		{"empty", &pb.Heartbeat{}, kinds},
		{"partial", &pb.Heartbeat{Capabilities: []string{"enforces.diff.max_files"}}, []string{"sandbox.isolated"}},
		{"exact_only", &pb.Heartbeat{Capabilities: []string{"enforces:sandbox.isolated", "enforces.sandbox", "enforces.sandbox.*", "ENFORCES.diff.max_files"}}, kinds},
		{"complete", &pb.Heartbeat{Capabilities: []string{"enforces.diff.max_files", "enforces.sandbox.isolated"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, missingWorkerConstraintKinds(kinds, tc.worker))
		})
	}
}

func TestDispatchConstraintTargets(t *testing.T) {
	sandbox := &pb.PolicyConstraints{Sandbox: &pb.SandboxProfile{Isolated: true}}
	w1 := &pb.Heartbeat{WorkerId: "w1", Pool: "default"}
	declared := &pb.Heartbeat{WorkerId: "w1", Pool: "default", Capabilities: []string{"enforces.sandbox.isolated"}}
	w2 := proto.Clone(declared).(*pb.Heartbeat)
	w2.WorkerId, w2.ActiveJobs = "w2", 1
	cases := []struct {
		name        string
		constraints *pb.PolicyConstraints
		workers     []*pb.Heartbeat
		poolSubject bool
		wantWorker  string
	}{
		{"undeclared", sandbox, []*pb.Heartbeat{w1}, false, ""},
		{"declared", sandbox, []*pb.Heartbeat{declared}, false, "w1"},
		{"prefer_declared_over_load_and_hint", sandbox, []*pb.Heartbeat{w1, w2}, false, "w2"},
		{"scheduler_budgets", schedulerOnlyConstraints(), []*pb.Heartbeat{w1}, false, "w1"},
		{"pool_subject", sandbox, []*pb.Heartbeat{declared}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus, store, registry := &fakeBus{}, newFakeJobStore(), newTestRegistry(t)
			for _, hb := range tc.workers {
				registry.UpdateHeartbeat(hb)
			}
			var strategy SchedulingStrategy = NewLeastLoadedStrategy(defaultRouting())
			if tc.poolSubject {
				strategy = NewNaiveStrategy()
			}
			safety := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{Decision: SafetyAllowWithConstraints, Constraints: tc.constraints}}
			engine := NewEngine(bus, safety, registry, strategy, store, nil)
			req := &pb.JobRequest{JobId: "constrained", Topic: "job.default", Labels: map[string]string{"preferred_worker_id": "w1"}}
			require.NoError(t, engine.handleJobRequest(req, "trace"))
			assertConstraintDispatch(t, bus, tc.constraints, tc.wantWorker)
			wantState := JobStateDenied
			if tc.wantWorker != "" {
				wantState = JobStateRunning
			}
			require.Equal(t, wantState, store.states[req.JobId])
			for _, msg := range bus.snapshotPublished() {
				if msg.subject == dlqSubject && !tc.poolSubject {
					require.Contains(t, msg.packet.GetJobResult().GetErrorMessage(), `worker "w1"`)
				}
			}
		})
	}
}

func assertConstraintDispatch(t *testing.T, bus *fakeBus, constraints *pb.PolicyConstraints, worker string) {
	t.Helper()
	var dispatched []publishedMsg
	var denied []*pb.JobResult
	for _, msg := range bus.snapshotPublished() {
		if msg.packet.GetJobRequest() != nil {
			dispatched = append(dispatched, msg)
		}
		if msg.subject == dlqSubject {
			denied = append(denied, msg.packet.GetJobResult())
		}
	}
	if worker == "" {
		require.Empty(t, dispatched, "no job may be published on any worker or pool subject")
		require.Len(t, denied, 1)
		require.Equal(t, pb.JobStatus_JOB_STATUS_DENIED, denied[0].GetStatus())
		require.Equal(t, "constraint_unenforced", denied[0].GetErrorCode())
		require.Contains(t, denied[0].GetErrorMessage(), "sandbox.isolated")
		return
	}
	require.Empty(t, denied)
	require.Len(t, dispatched, 1)
	require.Equal(t, "worker."+worker+".jobs", dispatched[0].subject)
	var attached pb.PolicyConstraints
	require.NoError(t, protojson.Unmarshal([]byte(dispatched[0].packet.GetJobRequest().GetEnv()["CORDUM_POLICY_CONSTRAINTS"]), &attached))
	require.True(t, proto.Equal(constraints, &attached), "published constraints must match the decision")
}

func TestDispatchConstraintsNoWorkersRetry(t *testing.T) {
	bus, store := &fakeBus{}, newFakeJobStore()
	safety := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision: SafetyAllowWithConstraints, Constraints: &pb.PolicyConstraints{Sandbox: &pb.SandboxProfile{Isolated: true}},
	}}
	engine := NewEngine(bus, safety, newTestRegistry(t), NewLeastLoadedStrategy(defaultRouting()), store, nil)
	req := &pb.JobRequest{JobId: "waiting", Topic: "job.default"}
	require.NoError(t, engine.handleJobRequest(req, "trace"))
	require.Equal(t, JobStatePending, store.states[req.JobId])
	require.Empty(t, bus.snapshotPublished())
}

func TestDispatchConstraintsApprovalAndTenantSlot(t *testing.T) {
	h := newApprovalHarness(t, defaultRouting())
	req := &pb.JobRequest{JobId: "approved-constrained", Topic: "job.default", TenantId: "tenant-a"}
	h.awaitApproval(req)
	record, err := h.store.GetSafetyDecision(testCtx(t), req.JobId)
	require.NoError(t, err)
	record.Constraints = schedulerOnlyConstraints()
	record.Constraints.Sandbox.Isolated = true
	require.NoError(t, h.store.SetSafetyDecision(testCtx(t), req.JobId, record))
	require.NoError(t, h.engine.handleJobRequest(h.approve(req.JobId), "trace"))
	assertConstraintDispatch(t, h.bus, record.Constraints, "")
	state, err := h.store.GetState(testCtx(t), req.JobId)
	require.NoError(t, err)
	require.Equal(t, JobStateDenied, state)
	active, err := h.store.CountActiveByTenant(testCtx(t), req.TenantId)
	require.NoError(t, err)
	require.Zero(t, active, "denied job must release its tenant slot")
}

type constraintRefreshRegistry struct{ *MemoryRegistry }

func (r *constraintRefreshRegistry) IsAlive(worker string) bool {
	if worker == "w1" {
		r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "default"})
		r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w2", Pool: "default", ActiveJobs: 1, Capabilities: []string{"enforces.sandbox.isolated"}})
		return false
	}
	return r.MemoryRegistry.IsAlive(worker)
}

func TestDispatchConstraintsRefreshAfterStaleWorker(t *testing.T) {
	bus, store := &fakeBus{}, newFakeJobStore()
	registry := &constraintRefreshRegistry{newTestRegistry(t)}
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "default", Capabilities: []string{"enforces.sandbox.isolated"}})
	constraints := &pb.PolicyConstraints{Sandbox: &pb.SandboxProfile{Isolated: true}}
	safety := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{Decision: SafetyAllowWithConstraints, Constraints: constraints}}
	engine := NewEngine(bus, safety, registry, NewLeastLoadedStrategy(defaultRouting()), store, nil)
	require.NoError(t, engine.handleJobRequest(&pb.JobRequest{JobId: "refresh", Topic: "job.default"}, "trace"))
	assertConstraintDispatch(t, bus, constraints, "w2")
	require.Equal(t, JobStateRunning, store.states["refresh"])
}

func TestDispatchConstraintDenialFailuresRetry(t *testing.T) {
	for _, failure := range []string{"state", "dlq"} {
		t.Run(failure, func(t *testing.T) {
			bus, store, registry := &fakeBus{}, newFakeJobStore(), newTestRegistry(t)
			registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "default"})
			fault := errors.New("unavailable")
			var jobStore JobStore = store
			if failure == "state" {
				jobStore = &failingSetStateStore{fakeJobStore: store, failOnState: JobStateDenied, setStateErr: fault}
			} else {
				bus.publishErr, bus.failSubject = fault, dlqSubject
			}
			constraints := &pb.PolicyConstraints{Sandbox: &pb.SandboxProfile{Isolated: true}}
			safety := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{Decision: SafetyAllowWithConstraints, Constraints: constraints}}
			engine := NewEngine(bus, safety, registry, NewLeastLoadedStrategy(defaultRouting()), jobStore, nil)
			err := engine.processJob(testCtx(t), &pb.JobRequest{JobId: "failed-denial", Topic: "job.default"}, "trace")
			var retry *retryableError
			require.ErrorAs(t, err, &retry)
			require.ErrorIs(t, err, fault)
			require.Empty(t, publishedJobIDs(bus))
		})
	}
}
