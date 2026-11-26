package scheduler

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

func TestFailureBehaviorKernelUnavailableClosedDispatchesNothing(t *testing.T) {
	bus := &fakeBus{}
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	ctx := context.Background()
	req := &pb.JobRequest{JobId: "failure-kernel", Topic: "sys.unavailable"}

	err := engine.processJob(ctx, req, "trace-failure")
	var retryErr *retryableError
	require.ErrorAs(t, err, &retryErr)
	require.Contains(t, err.Error(), "safety unavailable")
	require.Empty(t, bus.snapshotPublished(), "outage must publish neither a job nor a result")
	state, err := store.GetState(ctx, req.JobId)
	require.NoError(t, err)
	require.NotContains(t, []JobState{JobStateDispatched, JobStateRunning, JobStateDenied}, state)

	// The same bus and store must observe a dispatch when policy allows it.
	control := &pb.JobRequest{JobId: "failure-control", Topic: "job.default"}
	require.NoError(t, engine.processJob(ctx, control, "trace-control"))
	published := bus.snapshotPublished()
	require.Len(t, published, 1)
	require.Equal(t, control.Topic, published[0].subject)
	require.Equal(t, control.JobId, published[0].packet.GetJobRequest().GetJobId())
	state, err = store.GetState(ctx, control.JobId)
	require.NoError(t, err)
	require.Equal(t, JobStateRunning, state)
}
