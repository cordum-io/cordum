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
