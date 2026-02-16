package scheduler

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestSagaRecordCompensation(t *testing.T) {
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

	saga := NewSagaManager(&fakeBus{}, rdb)
	req := &pb.JobRequest{
		JobId:       "job-1",
		Topic:       "job.primary",
		WorkflowId:  "wf-1",
		TenantId:    "tenant",
		PrincipalId: "principal",
		Meta: &pb.JobMetadata{
			Capability: "cap-base",
		},
		Env:    map[string]string{"base": "true"},
		Labels: map[string]string{"k": "v"},
		Compensation: &pb.Compensation{
			Topic:  "job.undo",
			Env:    map[string]string{"undo": "true"},
			Labels: map[string]string{"undo": "yes"},
			Meta: &pb.JobMetadata{
				IdempotencyKey: "comp-idem",
			},
		},
	}

	if err := saga.RecordCompensation(context.Background(), req); err != nil {
		t.Fatalf("record compensation: %v", err)
	}

	key := sagaStackKey("wf-1")
	if key == "" {
		t.Fatalf("expected saga key")
	}
	data, err := rdb.LPop(context.Background(), key).Bytes()
	if err != nil {
		t.Fatalf("pop saga entry: %v", err)
	}

	var stored pb.JobRequest
	if err := proto.Unmarshal(data, &stored); err != nil {
		t.Fatalf("unmarshal stored request: %v", err)
	}
	if stored.Compensation != nil {
		t.Fatalf("expected compensation to be cleared on stored request")
	}
	if stored.JobId != "" {
		t.Fatalf("expected stored job id to be empty, got %q", stored.JobId)
	}
	if stored.Topic != "job.undo" {
		t.Fatalf("expected topic override, got %q", stored.Topic)
	}
	if stored.Priority != pb.JobPriority_JOB_PRIORITY_CRITICAL {
		t.Fatalf("expected critical priority")
	}
	if stored.Env["base"] != "true" || stored.Env["undo"] != "true" {
		t.Fatalf("expected merged env, got %+v", stored.Env)
	}
	if stored.Labels["k"] != "v" || stored.Labels["undo"] != "yes" {
		t.Fatalf("expected merged labels, got %+v", stored.Labels)
	}
	if stored.Meta == nil || stored.Meta.Capability != "cap-base" || stored.Meta.IdempotencyKey != "comp-idem" {
		t.Fatalf("expected merged meta, got %+v", stored.Meta)
	}
}

func TestSagaRollbackDispatchesCompensations(t *testing.T) {
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

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb)

	entry := &pb.JobRequest{
		Topic:    "job.undo",
		TenantId: "tenant",
		Labels:   map[string]string{"k": "v"},
	}
	payload, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	key := sagaStackKey("wf-2")
	if err := rdb.LPush(context.Background(), key, payload).Err(); err != nil {
		t.Fatalf("push saga entry: %v", err)
	}

	if err := saga.Rollback(context.Background(), "wf-2"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if len(bus.published) != 1 {
		t.Fatalf("expected 1 compensation publish, got %d", len(bus.published))
	}
	msg := bus.published[0]
	if msg.subject != capsdk.SubjectSubmit {
		t.Fatalf("expected publish to %s, got %s", capsdk.SubjectSubmit, msg.subject)
	}
	req := msg.packet.GetJobRequest()
	if req == nil {
		t.Fatalf("missing job request payload")
	}
	if req.JobId == "" || req.Priority != pb.JobPriority_JOB_PRIORITY_CRITICAL {
		t.Fatalf("expected compensation job id and critical priority")
	}
	if req.Labels[sagaCompLabel] != "true" || req.Labels[sagaWorkflowLabel] != "wf-2" {
		t.Fatalf("expected saga labels, got %+v", req.Labels)
	}
	if req.Env[sagaCompLabel] != "true" {
		t.Fatalf("expected saga env flag")
	}
}

func TestSagaRecordCompensationGeneratesIdempotency(t *testing.T) {
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

	saga := NewSagaManager(&fakeBus{}, rdb)
	req := &pb.JobRequest{
		JobId:      "job-2",
		Topic:      "job.primary",
		WorkflowId: "wf-9",
		Meta: &pb.JobMetadata{
			IdempotencyKey: "base-idem",
			Capability:     "cap-base",
		},
		Compensation: &pb.Compensation{
			Topic: "job.undo",
		},
	}

	if err := saga.RecordCompensation(context.Background(), req); err != nil {
		t.Fatalf("record compensation: %v", err)
	}
	data, err := rdb.LPop(context.Background(), sagaStackKey("wf-9")).Bytes()
	if err != nil {
		t.Fatalf("pop saga entry: %v", err)
	}
	var stored pb.JobRequest
	if err := proto.Unmarshal(data, &stored); err != nil {
		t.Fatalf("unmarshal stored request: %v", err)
	}
	if stored.Meta == nil || stored.Meta.IdempotencyKey == "" {
		t.Fatalf("expected generated idempotency key")
	}
	if stored.Meta.IdempotencyKey == "base-idem" {
		t.Fatalf("expected compensation idempotency to differ from base")
	}
	if !strings.HasPrefix(stored.Meta.IdempotencyKey, "saga:") {
		t.Fatalf("expected saga idempotency prefix, got %q", stored.Meta.IdempotencyKey)
	}
}

func TestSagaRollbackRespectsContextTimeout(t *testing.T) {
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

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb)

	// Push a compensation entry
	entry := &pb.JobRequest{
		Topic:    "job.undo",
		TenantId: "tenant",
	}
	payload, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	key := sagaStackKey("wf-timeout")
	if err := rdb.LPush(context.Background(), key, payload).Err(); err != nil {
		t.Fatalf("push saga entry: %v", err)
	}

	// Call Rollback with an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = saga.Rollback(ctx, "wf-timeout")
	if err == nil {
		// Even if Rollback doesn't return an error (lock acquisition may skip),
		// the compensations should not have been dispatched due to the expired context
		if len(bus.published) > 0 {
			t.Fatalf("expected no compensations dispatched with expired context")
		}
		return
	}

	// If error returned, that's correct — context deadline exceeded
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context-related error, got: %v", err)
	}
}

// denySafety always denies.
type denySafety struct{}

func (d *denySafety) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "denied-by-test", RuleID: "test-rule"}, nil
}

// allowSafety always allows.
type allowSafety struct{}

func (a *allowSafety) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return SafetyDecisionRecord{Decision: SafetyAllow}, nil
}

func TestSagaCompensationSafetyDenySkips(t *testing.T) {
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

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb).WithSafety(&denySafety{})

	entry := &pb.JobRequest{
		Topic:    "job.undo",
		TenantId: "tenant",
	}
	payload, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	key := sagaStackKey("wf-deny")
	if err := rdb.LPush(context.Background(), key, payload).Err(); err != nil {
		t.Fatalf("push saga entry: %v", err)
	}

	if err := saga.Rollback(context.Background(), "wf-deny"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Compensation should be skipped — no publishes
	if len(bus.published) != 0 {
		t.Fatalf("expected 0 publishes when safety denies, got %d", len(bus.published))
	}

	// Stack should be consumed
	remaining, err := rdb.LLen(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("llen: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected empty saga stack, got %d entries", remaining)
	}
}

func TestSagaCompensationSafetyAllowDispatches(t *testing.T) {
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

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb).WithSafety(&allowSafety{})

	entry := &pb.JobRequest{
		Topic:    "job.undo",
		TenantId: "tenant",
	}
	payload, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	key := sagaStackKey("wf-allow")
	if err := rdb.LPush(context.Background(), key, payload).Err(); err != nil {
		t.Fatalf("push saga entry: %v", err)
	}

	if err := saga.Rollback(context.Background(), "wf-allow"); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Compensation should be dispatched
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish when safety allows, got %d", len(bus.published))
	}
	req := bus.published[0].packet.GetJobRequest()
	if req == nil {
		t.Fatalf("expected job request in published packet")
	}
	if req.Labels["is_compensation"] != "true" {
		t.Fatalf("expected is_compensation label, got %+v", req.Labels)
	}
	if req.Env["is_compensation"] != "true" {
		t.Fatalf("expected is_compensation env, got %+v", req.Env)
	}
}

func TestSagaRollbackUnmarshalFailureSendsToDLQ(t *testing.T) {
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

	bus := &fakeBus{}
	saga := NewSagaManager(bus, rdb)

	// Push invalid (non-protobuf) bytes to the saga stack
	key := sagaStackKey("wf-broken")
	badData := []byte("this is not valid protobuf")
	if err := rdb.LPush(context.Background(), key, badData).Err(); err != nil {
		t.Fatalf("push bad saga entry: %v", err)
	}

	// Rollback should not error or panic
	if err := saga.Rollback(context.Background(), "wf-broken"); err != nil {
		t.Fatalf("rollback should not error on unmarshal failure: %v", err)
	}

	// Verify the broken entry was sent to DLQ
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 DLQ publish, got %d", len(bus.published))
	}
	msg := bus.published[0]
	if msg.subject != capsdk.SubjectDLQ {
		t.Fatalf("expected publish to %s, got %s", capsdk.SubjectDLQ, msg.subject)
	}
	result := msg.packet.GetJobResult()
	if result == nil {
		t.Fatalf("expected JobResult payload in DLQ message")
	}
	if result.ErrorCode != "saga_unmarshal_failed" {
		t.Fatalf("expected error code saga_unmarshal_failed, got %q", result.ErrorCode)
	}
	if result.Status != pb.JobStatus_JOB_STATUS_FAILED_FATAL {
		t.Fatalf("expected FAILED_FATAL status")
	}

	// Verify the saga stack is now empty
	remaining, err := rdb.LLen(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("llen: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected empty saga stack, got %d entries", remaining)
	}
}
