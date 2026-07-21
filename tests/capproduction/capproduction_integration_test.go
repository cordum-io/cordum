//go:build capproduction

package capproduction

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"io"
	"log"
	"sync/atomic"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	capworker "github.com/cordum-io/cap/v2/sdk/go/worker"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCAPProductionSchedulerWorkerEndToEnd(t *testing.T) {
	harness := newProductionHarness(t)
	worker, calls, runCancel, runDone := harness.startManagedWorker(t)
	stopped := false
	defer func() {
		if !stopped {
			stopManagedWorker(t, worker, runCancel, runDone)
		}
	}()

	wire, jobID := harness.signedGatewaySubmit(t)
	harness.replay.failNext.Store(true)
	harness.publishRaw(t, capsdk.SubjectSubmit, wire)
	time.Sleep(250 * time.Millisecond)
	if got, _ := harness.safety.snapshot(); got != 0 {
		t.Fatalf("replay backend outage reached safety %d times", got)
	}
	harness.publishRaw(t, capsdk.SubjectSubmit, wire)
	harness.publishRaw(t, capsdk.SubjectSubmit, wire)
	harness.awaitState(t, jobID, model.JobStateSucceeded)

	if got := calls.Load(); got != 1 {
		t.Fatalf("released CAP worker handler calls = %d, want 1", got)
	}
	if got, safetyErr := harness.safety.snapshot(); got != 1 || safetyErr != nil {
		t.Fatalf("normalized safety calls = %d error = %v, want one clean call", got, safetyErr)
	}
	harness.assertDurableResult(t, jobID, 1)

	stopManagedWorker(t, worker, runCancel, runDone)
	stopped = true
	harness.testAttemptAndReplayMatrix(t)
}

func stopManagedWorker(
	t *testing.T, worker *capworker.ManagedWorker, cancel context.CancelFunc, done <-chan error,
) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("managed worker stopped: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed worker did not stop")
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("close managed worker: %v", err)
	}
}

func (h *productionHarness) startManagedWorker(
	t *testing.T,
) (*capworker.ManagedWorker, *atomic.Int32, context.CancelFunc, <-chan error) {
	t.Helper()
	config := capworker.ManagedConfig{
		WorkerID: h.workerID, Pool: "production", Type: "cap-production-e2e",
		Subjects: []string{h.directSubject()}, Queue: h.workerID, NatsURL: h.natsURL(),
		MaxParallelJobs: 1, HeartbeatEvery: time.Hour, PrivateKey: h.workerKey,
		NATSTLSConfig: h.tls.client.Clone(), Logger: log.New(io.Discard, "", 0),
		WorkerTrustMode: capsdk.WorkerTrustModeEnforce,
		WorkerTrust: &capsdk.WorkerTrustConfig{
			WorkerID: h.workerID, ExpectedAgentID: h.agentID, TenantID: h.tenantID,
			Audience: capsdk.WorkerHandshakeAudience, ProofKeyID: "worker-key",
			ProofPrivateKey: h.workerKey, ExpectedSchedulerID: "cordum-scheduler",
			SchedulerPublicKeys: map[string]*ecdsa.PublicKey{"scheduler-key": &h.schedulerKey.PublicKey},
			SDKVersion:          "cap-go-production-e2e",
		},
		Production: capworker.ManagedProductionConfig{
			Enabled: true, KeyID: "worker-key", Stream: productionStream,
			Replay:            newRedisWorkerReplay(h.redis, "cap:worker-e2e:"+h.runID+":"),
			ResourceResolvers: []string{"cordum-redis"},
			Trust: capsdk.ProductionTrustStore{
				PublicKeys: map[string]*ecdsa.PublicKey{"scheduler-key": &h.schedulerKey.PublicKey},
			},
		},
	}
	worker, err := capworker.NewManagedWorker(config)
	if err != nil {
		t.Fatalf("new released CAP managed worker: %v", err)
	}
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx, func(_ context.Context, request *pb.JobRequest) (*pb.JobResult, error) {
			calls.Add(1)
			if request.GetTopic() != h.directSubject() || request.GetDispatch() == nil ||
				request.GetDispatch().GetAssignedWorkerId() != h.workerID {
				return nil, errors.New("worker received unbound transport or dispatch")
			}
			return &pb.JobResult{
				Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
				ResultRef: productionResourceRef("result-" + h.runID),
			}, nil
		})
	}()
	return worker, &calls, cancel, done
}

func (h *productionHarness) signedGatewaySubmit(t *testing.T) ([]byte, string) {
	t.Helper()
	token, err := h.issuer.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		t.Fatalf("mint gateway service token: %v", err)
	}
	jobID := "job-production-" + h.runID
	identity := &pb.IdentityBinding{
		TenantId: h.tenantID, PrincipalId: "principal-" + h.runID, ActorId: "actor-" + h.runID,
	}
	request, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: jobID, Topic: productionTopic}, identity,
	)
	if err != nil {
		t.Fatalf("normalize signed gateway request: %v", err)
	}
	packet := &pb.BusPacket{
		TraceId: "trace-" + h.runID, SenderId: servicetoken.IdentityGateway,
		ProtocolVersion: capsdk.DefaultProtocolVersion, CreatedAt: timestamppb.Now(),
		AuthToken: token, Identity: identity,
		SignatureMetadata: productionMetadata("gateway-key", capsdk.SubjectSubmit, randomBytes(t, 16)),
		Payload:           &pb.BusPacket_JobRequest{JobRequest: request},
	}
	wire, err := capsdk.SignProductionPacket(packet, h.gatewayKey)
	if err != nil {
		t.Fatalf("sign gateway submit: %v", err)
	}
	session, err := h.resolveSession(context.Background(), capsdk.SubjectSubmit, wire)
	if err != nil {
		t.Fatalf("resolve signed gateway session: %v", err)
	}
	if _, err := h.resolveProductionKey(session.Tenant, session.Subject, "gateway-key"); err != nil {
		t.Fatalf("gateway key lookup tenant=%q sender=%q: %v", session.Tenant, session.Subject, err)
	}
	boundary := &scheduler.ProductionRawBoundary{
		ResolveKey: h.resolveProductionKey, Replay: capsdk.NewInMemoryReplayStore(),
	}
	if _, err := boundary.Handle(
		context.Background(), capsdk.SubjectSubmit, session, wire,
		func(context.Context, *pb.BusPacket) error { return nil },
	); err != nil {
		t.Fatalf("locally admit signed gateway submit: %v", err)
	}
	return wire, jobID
}

func productionMetadata(keyID, audience string, messageID []byte) *pb.SignatureMetadata {
	return &pb.SignatureMetadata{
		ProfileVersion: capsdk.ProductionProfileVersion, Algorithm: capsdk.ProductionAlgorithm,
		MessageId: messageID, Audience: audience,
		ExpiresAt: timestamppb.New(time.Now().Add(time.Minute)), KeyId: keyID,
	}
}

func (h *productionHarness) publishRaw(t *testing.T, subject string, wire []byte) {
	t.Helper()
	if err := h.connection.Publish(subject, wire); err != nil {
		t.Fatalf("publish %s: %v", subject, err)
	}
	if err := h.connection.FlushTimeout(3 * time.Second); err != nil {
		t.Fatalf("flush %s: %v", subject, err)
	}
}

func (h *productionHarness) awaitState(t *testing.T, jobID string, want model.JobState) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		state, err := h.store.GetState(context.Background(), jobID)
		if err == nil && state == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, err := h.store.GetState(context.Background(), jobID)
	t.Fatalf("job %s state = %q error=%v, want %q", jobID, state, err, want)
}
