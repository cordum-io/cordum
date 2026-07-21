package scheduler

import (
	"fmt"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type productionDirectStrategy struct{ workerID string }

func (s productionDirectStrategy) PickSubject(
	_ *pb.JobRequest,
	workers map[string]*pb.Heartbeat,
	_ map[string]WorkerReadiness,
) (string, error) {
	if workers[s.workerID] == nil {
		return "", fmt.Errorf("worker unavailable")
	}
	return "worker." + s.workerID + ".jobs", nil
}

func TestProductionDispatchBindsRequestTopicToActualTransportSubject(t *testing.T) {
	const workerID = "worker-production"
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: workerID, Pool: "production"})
	store := newFakeJobStore()
	target := &fakeBus{}
	engine := NewEngine(
		target,
		NewSafetyBasic(),
		registry,
		productionDirectStrategy{workerID: workerID},
		store,
		nil,
	).WithProductionIdentityEnforcement(true)
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	request := &pb.JobRequest{
		JobId: "job-production", Topic: "job.echo", Identity: identity,
	}

	if err := engine.handleJobRequest(request, "trace-production"); err != nil {
		t.Fatalf("handleJobRequest() error = %v", err)
	}
	published := target.snapshotPublished()
	if len(published) != 1 {
		t.Fatalf("published packets = %d, want 1", len(published))
	}
	if got, want := published[0].packet.GetJobRequest().GetTopic(), published[0].subject; got != want {
		t.Fatalf("dispatched request topic = %q, want actual subject %q", got, want)
	}
	if request.GetTopic() != "job.echo" {
		t.Fatalf("caller request topic mutated to %q", request.GetTopic())
	}
}
