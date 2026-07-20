package scheduler

// Behavioral RED-turned-GREEN evidence for task-a13f83fa: compensation must
// fail closed (never dispatch, never silently drop) when safety is
// unavailable, and must never escalate tenant/principal/capability/risk
// tags beyond its parent JobRequest.

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// errorSafety always returns a transport/availability error (Safety Kernel down).
type errorSafety struct{}

func (e *errorSafety) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return SafetyDecisionRecord{}, context.DeadlineExceeded
}

func newSagaRedisManager(t *testing.T, bus Bus, safety SafetyChecker) *SagaManager {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	rdb, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return NewSagaManager(bus, rdb).WithSafety(safety)
}

func pushSagaEntry(t *testing.T, saga *SagaManager, workflowID string, req *pb.JobRequest) {
	t.Helper()
	// Reuse the manager's own redis client via RecordCompensation's stack
	// key convention so this test doesn't depend on internal field layout.
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	key := sagaStackKey(workflowID)
	if err := saga.redis.LPush(context.Background(), key, payload).Err(); err != nil {
		t.Fatalf("push saga entry: %v", err)
	}
}

func TestSagaCompensation_SafetyErrorFailsClosedToDLQ(t *testing.T) {
	bus := &fakeBus{}
	saga := newSagaRedisManager(t, bus, &errorSafety{})
	pushSagaEntry(t, saga, "wf-safety-error", &pb.JobRequest{Topic: "job.undo", TenantId: "tenant"})

	if err := saga.Rollback(context.Background(), "wf-safety-error"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if len(bus.published) != 1 || bus.published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected exactly 1 DLQ publish, got %d: %+v", len(bus.published), bus.published)
	}
	result := bus.published[0].packet.GetJobResult()
	if result == nil || result.GetErrorCode() != "compensation_safety_unavailable" {
		t.Fatalf("expected compensation_safety_unavailable DLQ result, got %+v", result)
	}
}

func TestSagaCompensation_ExplicitUnavailableDecisionFailsClosedToDLQ(t *testing.T) {
	bus := &fakeBus{}
	saga := newSagaRedisManager(t, bus, &unavailableSafety{})
	pushSagaEntry(t, saga, "wf-unavailable-decision", &pb.JobRequest{Topic: "job.undo", TenantId: "tenant"})

	if err := saga.Rollback(context.Background(), "wf-unavailable-decision"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected exactly 1 DLQ publish, got %d", len(bus.published))
	}
	if bus.published[0].packet.GetJobResult().GetErrorCode() != "compensation_safety_unavailable" {
		t.Fatalf("unexpected DLQ payload: %+v", bus.published[0].packet)
	}
}

func TestSagaCompensationSafetyFailureEchoesCanonicalIdentity(t *testing.T) {
	bus := &fakeBus{}
	saga := newSagaRedisManager(t, bus, &errorSafety{})
	authority := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{Topic: "job.undo"}, authority,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}

	if err := saga.dispatchCompensation(req, "wf-identity"); err != nil {
		t.Fatalf("dispatchCompensation() error = %v", err)
	}
	packet := bus.published[0].packet
	if !proto.Equal(packet.GetIdentity(), authority) ||
		!proto.Equal(packet.GetJobResult().GetIdentity(), authority) {
		t.Fatalf("DLQ identity = envelope:%v result:%v", packet.GetIdentity(), packet.GetJobResult().GetIdentity())
	}
}

func TestMergeJobMetadata_RejectsCapabilityAndRiskTagEscalation(t *testing.T) {
	base := &pb.JobMetadata{Capability: "read-only", RiskTags: []string{"read"}}
	escalated := &pb.JobMetadata{Capability: "delete-prod", RiskTags: []string{"write", "prod"}}

	merged := mergeJobMetadata(base, escalated)

	if merged.Capability != "read-only" {
		t.Fatalf("mergeJobMetadata allowed capability escalation: got %q, want %q", merged.Capability, "read-only")
	}
	if len(merged.RiskTags) != 1 || merged.RiskTags[0] != "read" {
		t.Fatalf("mergeJobMetadata allowed risk-tag escalation: got %v, want [read]", merged.RiskTags)
	}
}

func TestMergeJobMetadata_NilBaseRejectsAnyCapability(t *testing.T) {
	escalated := &pb.JobMetadata{Capability: "delete-prod", RiskTags: []string{"write"}}

	merged := mergeJobMetadata(nil, escalated)

	if merged.Capability != "" {
		t.Fatalf("mergeJobMetadata with nil base allowed a capability: got %q, want empty", merged.Capability)
	}
	if len(merged.RiskTags) != 0 {
		t.Fatalf("mergeJobMetadata with nil base allowed risk tags: got %v, want none", merged.RiskTags)
	}
}

func TestMergeJobMetadata_AllowsRiskTagSubsetAndSameCapability(t *testing.T) {
	base := &pb.JobMetadata{Capability: "read-only", RiskTags: []string{"read", "network"}}
	legitimate := &pb.JobMetadata{Capability: "read-only", RiskTags: []string{"read"}}

	merged := mergeJobMetadata(base, legitimate)

	if merged.Capability != "read-only" {
		t.Fatalf("unexpected capability: %q", merged.Capability)
	}
	if len(merged.RiskTags) != 1 || merged.RiskTags[0] != "read" {
		t.Fatalf("unexpected risk tags: %v", merged.RiskTags)
	}
}

func TestValidateProductionStartup_RejectsMissingSafetyKernel(t *testing.T) {
	engine := NewEngine(&fakeBus{}, nil, newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil)
	if err := engine.ValidateProductionStartup(); !errors.Is(err, ErrProductionMissingSafety) {
		t.Fatalf("ValidateProductionStartup() = %v, want ErrProductionMissingSafety", err)
	}
}

func TestValidateProductionStartup_RejectsGlobalFailOpen(t *testing.T) {
	base := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil)
	if err := base.ValidateProductionStartup(); err != nil {
		t.Fatalf("baseline (no fail-open configured) ValidateProductionStartup() = %v, want nil", err)
	}

	inputFailOpen := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithInputFailMode("open")
	if err := inputFailOpen.ValidateProductionStartup(); !errors.Is(err, ErrProductionFailOpenConfigured) {
		t.Fatalf("ValidateProductionStartup() with input fail-open = %v, want ErrProductionFailOpenConfigured", err)
	}

	asyncFailOpen := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithAsyncFailMode("open")
	if err := asyncFailOpen.ValidateProductionStartup(); !errors.Is(err, ErrProductionFailOpenConfigured) {
		t.Fatalf("ValidateProductionStartup() with async fail-open = %v, want ErrProductionFailOpenConfigured", err)
	}
}

func TestBuildCompensationRequest_RejectsTenantAndPrincipalEscalation(t *testing.T) {
	base := &pb.JobRequest{
		JobId: "job-1", Topic: "job.original", TenantId: "tenant-a", PrincipalId: "principal-a",
		Meta:     &pb.JobMetadata{TenantId: "tenant-a", ActorId: "actor-a"},
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"},
		Compensation: &pb.Compensation{
			Topic: "job.undo", TenantId: "tenant-VICTIM", PrincipalId: "principal-ADMIN",
		},
	}
	before := proto.Clone(base)

	comp, err := buildCompensationRequest(base)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("buildCompensationRequest() error = %v, want identity mismatch", err)
	}
	if comp != nil {
		t.Fatalf("buildCompensationRequest() = %#v, want nil", comp)
	}
	if !proto.Equal(base, before) {
		t.Fatal("buildCompensationRequest() mutated rejected request")
	}
}
