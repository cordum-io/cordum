package scheduler

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSagaRecordCompensationIsIdempotentPerCompletedJob(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	saga := NewSagaManager(&fakeBus{}, rdb)
	req := &pb.JobRequest{
		JobId: "job-once", Topic: "job.primary", WorkflowId: "workflow-once",
		Compensation: &pb.Compensation{Topic: "job.undo"},
	}
	for range 2 {
		if err := saga.RecordCompensation(context.Background(), req); err != nil {
			t.Fatalf("RecordCompensation() error = %v", err)
		}
	}
	count, err := rdb.LLen(context.Background(), sagaStackKey(req.GetWorkflowId())).Result()
	if err != nil {
		t.Fatalf("LLen() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("compensation stack entries = %d, want 1", count)
	}
}
