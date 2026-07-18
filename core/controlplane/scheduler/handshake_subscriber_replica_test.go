package scheduler

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/redis/go-redis/v9"
)

func TestHandshakeSubscriberSharesChallengeAcrossSchedulerReplicas(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	busA, busB := &fakeHandshakeResponder{}, &fakeHandshakeResponder{}
	subscriberA := startReplicaSubscriber(t, busA, fixture.service)
	defer subscriberA.Close()
	subscriberB := startReplicaSubscriber(t, busB, newReplicaHandshakeService(t, fixture))
	defer subscriberB.Close()

	challenge := requestChallengeFromReplica(t, fixture, busA)
	authenticate := protocolAuthenticate(t, fixture, challenge, "")
	result := authenticateWithReplica(t, busB, authenticate)
	if _, err := fixture.issuer.VerifyBound(context.Background(), result.GetAuthToken(), true); err != nil {
		t.Fatalf("verify cross-replica session: %v", err)
	}
	assertReplicaReplayRejected(t, busA, authenticate)
}

func startReplicaSubscriber(t *testing.T, bus *fakeHandshakeResponder, service handshakeProtocolService) *HandshakeSubscriber {
	t.Helper()
	subscriber, err := NewHandshakeSubscriber(bus, service)
	if err != nil {
		t.Fatalf("new replica subscriber: %v", err)
	}
	if err := subscriber.Start(); err != nil {
		t.Fatalf("start replica subscriber: %v", err)
	}
	return subscriber
}

func requestChallengeFromReplica(t *testing.T, fixture *protocolHandshakeFixture, bus *fakeHandshakeResponder) *agentv1.WorkerHandshakeChallenge {
	t.Helper()
	request := protocolChallengeRequest(t, fixture, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE)
	raw, err := bus.invoke(context.Background(), WorkerHandshakeChallengeSubject, marshalHandshakeRaw(t, request))
	if err != nil {
		t.Fatalf("replica challenge: %v", err)
	}
	challenge := decodeHandshakeRaw(t, raw).GetWorkerHandshakeChallenge()
	if challenge == nil {
		t.Fatal("replica returned no challenge")
	}
	return challenge
}

func authenticateWithReplica(t *testing.T, bus *fakeHandshakeResponder, packet *agentv1.BusPacket) *agentv1.BusPacket {
	t.Helper()
	raw, err := bus.invoke(context.Background(), WorkerHandshakeAuthenticateSubject, marshalHandshakeRaw(t, packet))
	if err != nil {
		t.Fatalf("replica authenticate: %v", err)
	}
	result := decodeHandshakeRaw(t, raw)
	if !result.GetWorkerHandshakeResult().GetAccepted() || result.GetAuthToken() == "" {
		t.Fatalf("cross-replica result must be accepted with token: %+v", result.GetWorkerHandshakeResult())
	}
	return result
}

func assertReplicaReplayRejected(t *testing.T, bus *fakeHandshakeResponder, packet *agentv1.BusPacket) {
	t.Helper()
	raw, err := bus.invoke(context.Background(), WorkerHandshakeAuthenticateSubject, marshalHandshakeRaw(t, packet))
	if err != nil {
		t.Fatalf("cross-replica replay response: %v", err)
	}
	replayed := decodeHandshakeRaw(t, raw)
	if replayed.GetWorkerHandshakeResult().GetAccepted() || replayed.GetAuthToken() != "" {
		t.Fatalf("cross-replica replay minted a second token: %+v", replayed.GetWorkerHandshakeResult())
	}
}

func newReplicaHandshakeService(t *testing.T, fixture *protocolHandshakeFixture) *HandshakeService {
	t.Helper()
	redisClient := redis.NewClient(&redis.Options{Addr: fixture.sessionStore.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	service, err := NewHandshakeService(
		fixture.issuer, fixture.resolver, NewRedisHandshakeChallengeStore(redisClient), fixture.sink,
		HandshakeServiceOptions{
			Audience: WorkerHandshakeAudience, SchedulerID: fixture.service.schedulerID,
			SchedulerKeyID: fixture.service.schedulerKeyID, SchedulerPrivateKey: fixture.schedulerKey,
			Skew: fixture.service.skew, ChallengeTTL: fixture.service.challengeTTL,
			Now: func() time.Time { return fixture.now },
		},
	)
	if err != nil {
		t.Fatalf("new replica handshake service: %v", err)
	}
	return service
}
