package bus_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/replay"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const admissionCompletionSubject = "sys.job.result"

func TestAdmissionRedeliveryCompletesJob(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "completed_duplicate"
		if retry {
			name = "unfinished_delivery"
		}
		t.Run(name, func(t *testing.T) { admissionCompletionCheck(t, retry) })
	}
}

func admissionCompletionCheck(t *testing.T, retry bool) {
	t.Helper()
	target, js := admissionCompletionBus(t)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { admissionCompletionMust(t, client.Close()) })
	jobs, err := store.NewRedisJobStore("redis://" + mr.Addr())
	admissionCompletionMust(t, err)
	t.Cleanup(func() { admissionCompletionMust(t, jobs.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admissionCompletionMust(t, jobs.SetState(ctx, "completion-job", model.JobStateRunning))
	raw := admissionCompletionInstall(t, target, replay.NewRedisReplayStore(client))
	var calls atomic.Int32
	admissionCompletionMust(t, target.SubscribeWithContext(admissionCompletionSubject, "completion",
		func(ctx context.Context, packet *pb.BusPacket) error {
			if calls.Add(1) == 1 && retry {
				return bus.RetryAfter(errors.New("processing interrupted"), 10*time.Millisecond)
			}
			return jobs.SetState(ctx, packet.GetJobResult().GetJobId(), model.JobStateSucceeded)
		}))
	ack, err := js.Publish(admissionCompletionSubject, raw)
	admissionCompletionMust(t, err)
	admissionCompletionSettled(t, js, ack)
	state, err := jobs.GetState(ctx, "completion-job")
	admissionCompletionMust(t, err)
	wantCalls := int32(1)
	if retry {
		wantCalls = 2
	}
	if state != model.JobStateSucceeded || calls.Load() != wantCalls {
		t.Fatalf("admitted delivery must complete: handler calls=%d state=%s, want %d/SUCCEEDED", calls.Load(), state, wantCalls)
	}
	ack, err = js.Publish(admissionCompletionSubject, raw)
	admissionCompletionMust(t, err)
	admissionCompletionSettled(t, js, ack)
	if calls.Load() != wantCalls {
		t.Fatalf("completed duplicate ran handler: calls=%d want=%d", calls.Load(), wantCalls)
	}
}

func admissionCompletionBus(t *testing.T) (*bus.NatsBus, nats.JetStreamContext) {
	t.Helper()
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "true")
	ns, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	admissionCompletionMust(t, err)
	t.Cleanup(ns.Shutdown)
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS did not become ready")
	}
	target, err := bus.NewNatsBus(ns.ClientURL())
	admissionCompletionMust(t, err)
	t.Cleanup(target.Close)
	nc, err := nats.Connect(ns.ClientURL())
	admissionCompletionMust(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream(nats.MaxWait(2 * time.Second))
	admissionCompletionMust(t, err)
	return target, js
}

func admissionCompletionInstall(t *testing.T, target *bus.NatsBus, replayStore capsdk.ReplayStore) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	admissionCompletionMust(t, err)
	identity := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "worker-a"}
	boundary := &scheduler.ProductionRawBoundary{
		Replay: replayStore,
		ResolveKey: func(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
			if tenant != "tenant-a" || sender != "worker-a" || keyID != "completion-key" {
				return nil, errors.New("unknown signing identity")
			}
			return &key.PublicKey, nil
		},
	}
	admissionCompletionMust(t, scheduler.InstallProductionRawAdmission(target, boundary,
		func(context.Context, string, []byte) (scheduler.AuthenticatedProductionSession, error) {
			return scheduler.AuthenticatedProductionSession{Subject: "worker-a", Tenant: "tenant-a", Identity: identity}, nil
		}))
	now := time.Now()
	packet := &pb.BusPacket{
		TraceId: "completion-trace", SenderId: "worker-a", CreatedAt: timestamppb.New(now), ProtocolVersion: 1,
		Identity: identity,
		SignatureMetadata: &pb.SignatureMetadata{
			ProfileVersion: capsdk.ProductionProfileVersion, Algorithm: capsdk.ProductionAlgorithm,
			MessageId: []byte("completion-msg-1"), Audience: admissionCompletionSubject,
			ExpiresAt: timestamppb.New(now.Add(time.Minute)), KeyId: "completion-key",
		},
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "completion-job", WorkerId: "worker-a", Identity: identity, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}},
	}
	raw, err := capsdk.SignProductionPacket(packet, key)
	admissionCompletionMust(t, err)
	return raw
}

func admissionCompletionSettled(t *testing.T, js nats.JetStreamContext, ack *nats.PubAck) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := js.ConsumerInfo(ack.Stream, "dur_completion__sys_job_result")
		admissionCompletionMust(t, err)
		if info.AckFloor.Stream >= ack.Sequence && info.NumAckPending == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("delivery was not acknowledged")
}

func admissionCompletionMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
