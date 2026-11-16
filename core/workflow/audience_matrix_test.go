package workflow_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/workflow"
	"github.com/cordum/cordum/tests/identity"
	"github.com/google/uuid"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	matrixRunID  = "run-matrix"
	matrixStepID = "step"
	matrixJobID  = matrixRunID + ":" + matrixStepID + "@1"
	matrixKeyID  = "matrix-scheduler"
)

// TestAudienceMatrixBusWorkflowResult presents every credential on
// sys.internal.job.result.accepted, as the scheduler publishes an accepted
// step result. The protected effect is the workflow run advancing.
func TestAudienceMatrixBusWorkflowResult(t *testing.T) {
	setMatrixProductionBus(t)
	identity.Run(t, identity.BusWorkflowResult, func(t *testing.T, c identity.Credential) identity.Result {
		f := newWorkflowMatrixFixture(t, true)
		var token string
		if c == identity.ServiceToken {
			var err error
			token, err = servicetoken.MintService(f.key, matrixKeyID, servicetoken.IdentityScheduler, time.Now())
			require.NoError(t, err)
		} else {
			token = audienceCredential(t, c)
		}
		packet := matrixAcceptedResult(token)
		if c == identity.AgentIDHeader {
			// CAP requires job_result.worker_id to equal sender_id, as the scheduler sends it.
			packet.SenderId, packet.GetJobResult().WorkerId = token, token
			packet.AuthToken = ""
		}
		runBefore, stepBefore := f.state(t)
		require.NoError(t, f.publisher.Publish(capsdk.SubjectAcceptedResult, packet))
		f.awaitHandled(t)
		runAfter, stepAfter := f.state(t)
		runChanged, stepChanged := runBefore != runAfter, stepBefore != stepAfter
		return identity.Result{
			Accepted: runChanged && stepChanged, Effect: runChanged || stepChanged,
			Evidence: fmt.Sprintf("state snapshots (Accepted from the same snapshots); workflow run status %s -> %s; workflow step status %s -> %s",
				runBefore, runAfter, stepBefore, stepAfter),
		}
	})
}

func matrixAcceptedResult(token string) *pb.BusPacket {
	return &pb.BusPacket{
		SenderId: servicetoken.IdentityScheduler, TraceId: uuid.NewString(), AuthToken: token,
		CreatedAt: timestamppb.Now(), ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: matrixJobID, WorkerId: servicetoken.IdentityScheduler,
			Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, Identity: matrixRunIdentity(),
		}},
	}
}

// setMatrixProductionBus sets the production profile with only the embedded
// server's plaintext and no-auth transport overrides, and core NATS delivery.
func setMatrixProductionBus(t *testing.T) {
	t.Helper()
	t.Setenv(env.EnvMode, "production")
	t.Setenv("CORDUM_NATS_ALLOW_PLAINTEXT", "true")
	t.Setenv("CORDUM_NATS_ALLOW_NOAUTH", "true")
	t.Setenv("NATS_USE_JETSTREAM", "false")
}

func matrixRunIdentity() *pb.IdentityBinding {
	return &pb.IdentityBinding{TenantId: "tenant-matrix", PrincipalId: "principal-matrix", ActorId: "principal-matrix"}
}

type workflowMatrixFixture struct {
	store     *workflow.RedisStore
	publisher *bus.NatsBus
	handled   chan error
	key       ed25519.PrivateKey
}

// newWorkflowMatrixFixture wires a fresh workflow store, engine and reconciler
// the way RunWithEntitlements does, subscribes the accepted-result handler on
// a production NatsBus, and returns a publisher signing like the scheduler's.
func newWorkflowMatrixFixture(t *testing.T, enforce bool) *workflowMatrixFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workflows := workflow.NewRedisWorkflowStoreFromClient(rdb)
	wf := &workflow.Workflow{ID: "wf-matrix", OrgID: "tenant-matrix", Steps: map[string]*workflow.Step{
		matrixStepID: {ID: matrixStepID, Type: workflow.StepTypeWorker, Topic: "job.matrix"},
	}}
	require.NoError(t, workflows.SaveWorkflow(ctx, wf))
	now := time.Now().UTC()
	require.NoError(t, workflows.CreateRun(ctx, &workflow.WorkflowRun{
		ID: matrixRunID, WorkflowID: wf.ID, OrgID: "tenant-matrix", Identity: matrixRunIdentity(),
		Status: workflow.RunStatusRunning, CreatedAt: now, UpdatedAt: now,
		Steps: map[string]*workflow.StepRun{matrixStepID: {
			StepID: matrixStepID, JobID: matrixJobID, Status: workflow.StepStatusRunning,
		}},
	}))

	ns := startMatrixNATS(t)
	consumer := matrixNatsBus(t, ns)
	engine := workflow.NewEngine(workflows, consumer).WithProductionIdentityEnforcement(true)
	rec := workflow.NewReconciler(workflows, engine, store.NewRedisJobStoreFromClient(rdb), 0, 0)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var trust *policysign.TrustStore
	if enforce {
		trust = policysign.NewTrustStore()
		require.NoError(t, trust.Add(matrixKeyID, pub))
	}
	handler := workflow.AcceptedResultHandler(rec, trust)
	f := &workflowMatrixFixture{store: workflows, handled: make(chan error, 1), key: priv}
	subs := ns.NumSubscriptions()
	require.NoError(t, consumer.SubscribeWithContext(capsdk.SubjectAcceptedResult, workflow.WorkflowEngineQueue,
		func(ctx context.Context, p *pb.BusPacket) error {
			err := handler(ctx, p)
			f.handled <- err
			return err
		}))
	require.Eventually(t, func() bool { return ns.NumSubscriptions() > subs }, 5*time.Second, 5*time.Millisecond,
		"accepted-result subscription not registered")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	encoder, err := bus.NewProductionPacketEncoder(bus.ProductionPacketEncoderOptions{Key: key, KeyID: "matrix-scheduler"})
	require.NoError(t, err)
	f.publisher = matrixNatsBus(t, ns)
	require.NoError(t, f.publisher.SetPacketEncoder(encoder))
	return f
}

// state is the run and step status; a store read error fails the test.
func (f *workflowMatrixFixture) state(t *testing.T) (run, step string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := f.store.GetRun(ctx, matrixRunID)
	if err != nil {
		t.Fatalf("read workflow run: %v", err)
	}
	stepRun := got.Steps[matrixStepID]
	if stepRun == nil {
		t.Fatalf("workflow run has no %s step", matrixStepID)
	}
	return string(got.Status), string(stepRun.Status)
}

// awaitHandled waits for the handler to return; a miss or an error fails the test.
func (f *workflowMatrixFixture) awaitHandled(t *testing.T) {
	t.Helper()
	select {
	case err := <-f.handled:
		if err != nil {
			t.Fatalf("accepted-result handler error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("packet not delivered to the accepted-result handler")
	}
}

// startMatrixNATS is core/infra/bus/handshake_test.go's startTestNATS.
func startMatrixNATS(t *testing.T) *server.Server {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("start test nats: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

func matrixNatsBus(t *testing.T, ns *server.Server) *bus.NatsBus {
	t.Helper()
	b, err := bus.NewNatsBus(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(b.Close)
	return b
}
