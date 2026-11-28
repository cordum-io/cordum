package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	capv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/replay"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

const claimTestLease = time.Minute

// claimFixture drives the production hook over a Redis replay store whose
// clock the test moves, with the job the signed packet reports on.
type claimFixture struct {
	hook bus.RawPacketAdmission
	raw  []byte
	jobs *store.RedisJobStore
	now  time.Time
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	f := &claimFixture{jobs: store.NewRedisJobStoreFromClient(client), now: time.Now()}
	key := newProductionTestKey(t)
	session := productionTestSession()
	boundary := productionTestBoundary(key, replay.NewRedisReplayStore(client, replay.WithClock(func() time.Time { return f.now })))
	boundary.ClaimLease = claimTestLease
	f.hook = NewProductionRawAdmissionHook(boundary, func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return cloneProductionSession(session), nil
	})
	f.raw = signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(1)))
	if err := f.jobs.SetState(context.Background(), "job-a", model.JobStateRunning); err != nil {
		t.Fatalf("set job running: %v", err)
	}
	return f
}

func (f *claimFixture) deliver() bus.RawAdmissionResult {
	return f.hook(context.Background(), productionTestSubject, f.raw)
}

// complete runs an accepted delivery the way the bus does: the handler records
// the reported job state, then the delivery settles with how it ended.
func (f *claimFixture) complete(t *testing.T, accepted bus.RawAdmissionResult) {
	t.Helper()
	jobID := accepted.Packet.GetJobResult().GetJobId()
	if err := f.jobs.SetState(context.Background(), jobID, model.JobStateSucceeded); err != nil {
		t.Fatalf("handler set job state: %v", err)
	}
	accepted.Settle(bus.RawAdmissionSucceeded)
}

// A delivery that was accepted but did not finish, because the process stopped
// or the handler asked for a retry, runs again on redelivery and completes the job.
func TestProductionRawAdmissionUnfinishedDeliveryRunsAgain(t *testing.T) {
	cases := []struct {
		name    string
		release func(*claimFixture, bus.RawAdmissionResult)
	}{
		{name: "process stopped before settle", release: func(f *claimFixture, _ bus.RawAdmissionResult) {
			f.now = f.now.Add(claimTestLease)
		}},
		{name: "handler asked for retry", release: func(_ *claimFixture, first bus.RawAdmissionResult) {
			first.Settle(bus.RawAdmissionReleased)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaimFixture(t)
			first := f.deliver()
			if first.Disposition != bus.RawAdmissionAccepted || first.Settle == nil {
				t.Fatalf("first delivery = %v (settle set %t), want accepted with Settle", first.Disposition, first.Settle != nil)
			}
			if busy := f.deliver(); busy.Disposition != bus.RawAdmissionRetry || busy.RetryAfter != claimTestLease {
				t.Fatalf("delivery during lease = %v/%v, want retry after %v", busy.Disposition, busy.RetryAfter, claimTestLease)
			}
			tc.release(f, first)
			redelivery := f.deliver()
			if redelivery.Disposition != bus.RawAdmissionAccepted {
				t.Fatalf("redelivery = %v, want accepted", redelivery.Disposition)
			}
			f.complete(t, redelivery)
			state, err := f.jobs.GetState(context.Background(), "job-a")
			if err != nil || state != model.JobStateSucceeded {
				t.Fatalf("job state = %s (%v), want SUCCEEDED", state, err)
			}
			if later := f.deliver(); later.Disposition != bus.RawAdmissionDuplicate {
				t.Fatalf("delivery after completion = %v, want duplicate", later.Disposition)
			}
		})
	}
}

// A delivery whose handler failed without asking for a retry has ended, so a
// later delivery of the same message is a duplicate and does not run again.
func TestProductionRawAdmissionFailedDeliveryIsNotRunAgain(t *testing.T) {
	f := newClaimFixture(t)
	first := f.deliver()
	if first.Settle == nil {
		t.Fatalf("first delivery = %v without Settle, want accepted", first.Disposition)
	}
	first.Settle(bus.RawAdmissionFailed)
	if later := f.deliver(); later.Disposition != bus.RawAdmissionDuplicate {
		t.Fatalf("delivery after a failed run = %v, want duplicate", later.Disposition)
	}
}

// The installed lease follows the bus AckWait, so a run that never settles is
// taken over once AckWait passes. Two consumers get the same message: the
// first run never finishes, and the other consumer, held off while that run's
// lease is live, runs the message once the lease ends.
func TestInstallProductionRawAdmissionLeaseFollowsAckWait(t *testing.T) {
	target, js := startClaimLeaseBus(t, "200ms")
	key := newProductionTestKey(t)
	session := productionTestSession()
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	boundary := productionTestBoundary(key, replay.NewRedisReplayStore(client))
	if err := InstallProductionRawAdmission(target, boundary, func(context.Context, string, []byte) (AuthenticatedProductionSession, error) {
		return cloneProductionSession(session), nil
	}); err != nil || boundary.ClaimLease != 0 {
		t.Fatalf("install = %v with caller lease %v, want nil with the caller's boundary unchanged", err, boundary.ClaimLease)
	}
	stuck, ran := make(chan struct{}), make(chan struct{}, 4)
	t.Cleanup(func() { close(stuck) })
	var calls atomic.Int32
	for _, queue := range []string{"lease-a", "lease-b"} {
		if err := target.Subscribe(productionTestSubject, queue, func(*capv1.BusPacket) error {
			if calls.Add(1) == 1 {
				<-stuck
			} else {
				ran <- struct{}{}
			}
			return nil
		}); err != nil {
			t.Fatalf("subscribe %s: %v", queue, err)
		}
	}
	raw := signProductionTestPacket(t, key, productionTestPacket(session.Identity, productionTestMessageID(30)))
	if _, err := js.Publish(productionTestSubject, raw); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("a run that never settled was not taken over after AckWait")
	}
}

// startClaimLeaseBus starts JetStream and a bus whose AckWait is ackWait, plus
// a separate publisher connection.
func startClaimLeaseBus(t *testing.T, ackWait string) (*bus.NatsBus, nats.JetStreamContext) {
	t.Helper()
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "true")
	t.Setenv("NATS_JS_ACK_WAIT", ackWait)
	ns, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, JetStream: true, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new NATS server: %v", err)
	}
	go ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	target, err := bus.NewNatsBus(ns.ClientURL())
	if err != nil {
		t.Fatalf("new NATS bus: %v", err)
	}
	t.Cleanup(target.Close)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect publisher: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream(nats.MaxWait(2 * time.Second))
	if err != nil {
		t.Fatalf("publisher JetStream: %v", err)
	}
	return target, js
}

func TestProductionRawAdmissionConcurrentDeliveriesRunOnce(t *testing.T) {
	f := newClaimFixture(t)
	var runs atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				switch result := f.deliver(); result.Disposition {
				case bus.RawAdmissionAccepted:
					runs.Add(1)
					result.Settle(bus.RawAdmissionSucceeded)
					return
				case bus.RawAdmissionDuplicate:
					return
				case bus.RawAdmissionRetry:
				default:
					t.Errorf("delivery disposition = %v", result.Disposition)
					return
				}
			}
			t.Error("delivery neither ran nor saw the completed message")
		}()
	}
	close(start)
	wg.Wait()
	if got := runs.Load(); got != 1 {
		t.Fatalf("handler runs = %d, want exactly 1", got)
	}
}
