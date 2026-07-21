package scheduler

import (
	"context"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/proto"
)

func TestSchedulerHandshakeEmittersProduceValidCAPPackets(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()

	challenge, err := fixture.service.HandleChallenge(context.Background(),
		protocolChallengeRequest(t, fixture, issuePurpose()))
	if err != nil {
		t.Fatalf("emit challenge: %v", err)
	}
	assertWorkerTrustPacketValid(t, challenge)

	result, err := fixture.service.HandleAuthenticate(context.Background(),
		protocolAuthenticate(t, fixture, challenge.GetWorkerHandshakeChallenge(), ""))
	if err != nil {
		t.Fatalf("emit result: %v", err)
	}
	assertWorkerTrustPacketValid(t, result)

	invalidChallenge := proto.Clone(challenge).(*agentv1.BusPacket)
	invalidChallenge.GetWorkerHandshakeChallenge().ServerNonce = nil
	assertWorkerTrustPacketInvalid(t, invalidChallenge)
	invalidResult := proto.Clone(result).(*agentv1.BusPacket)
	invalidResult.GetWorkerHandshakeResult().TokenExpiresAt = nil
	assertWorkerTrustPacketInvalid(t, invalidResult)
}

func assertWorkerTrustPacketValid(t *testing.T, packet *agentv1.BusPacket) {
	t.Helper()
	if err := capsdk.ValidateWorkerTrustPacket(packet); err != nil {
		t.Fatalf("CAP worker-trust validation: %v", err)
	}
}

func assertWorkerTrustPacketInvalid(t *testing.T, packet *agentv1.BusPacket) {
	t.Helper()
	if err := capsdk.ValidateWorkerTrustPacket(packet); err == nil {
		t.Fatal("CAP worker-trust validation accepted malformed emitter packet")
	}
}
