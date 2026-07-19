package scheduler

import (
	"context"
	"crypto/ecdsa"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandshakeServiceSecurity_RejectsTamperedChallengeBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentv1.BusPacket)
	}{
		{name: "agent", mutate: func(packet *agentv1.BusPacket) {
			packet.GetWorkerHandshakeAuthenticate().Challenge.AgentId = "agent-attacker"
		}},
		{name: "tenant", mutate: func(packet *agentv1.BusPacket) {
			packet.GetWorkerHandshakeAuthenticate().Challenge.TenantId = "tenant-attacker"
		}},
		{name: "key", mutate: func(packet *agentv1.BusPacket) {
			packet.GetWorkerHandshakeAuthenticate().Challenge.ProofKeyId = "key-attacker"
		}},
		{name: "audience", mutate: func(packet *agentv1.BusPacket) {
			packet.GetWorkerHandshakeAuthenticate().Challenge.Audience = "other-scheduler"
		}},
		{name: "client nonce", mutate: func(packet *agentv1.BusPacket) { packet.GetWorkerHandshakeAuthenticate().Challenge.ClientNonce[0] ^= 1 }},
		{name: "server nonce", mutate: func(packet *agentv1.BusPacket) { packet.GetWorkerHandshakeAuthenticate().Challenge.ServerNonce[0] ^= 1 }},
		{name: "trace", mutate: func(packet *agentv1.BusPacket) {
			packet.TraceId = "trace-attacker"
			packet.GetWorkerHandshakeAuthenticate().Challenge.TraceId = "trace-attacker"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProtocolHandshakeFixture(t)
			defer fixture.cleanup()
			challenge := issuedProtocolChallenge(t, fixture)
			packet := protocolAuthenticate(t, fixture, challenge, "")
			tc.mutate(packet)
			resignAuthenticate(t, packet, fixture.workerKey)
			assertAuthenticateRejected(t, fixture, packet, authenticationFailedReason())
			assertNoVictimSession(t, fixture)
		})
	}
}

func TestHandshakeServiceSecurity_RejectsTamperedSignedAuthenticate(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedProtocolChallenge(t, fixture)
	packet := protocolAuthenticate(t, fixture, challenge, "")
	packet.GetWorkerHandshakeAuthenticate().CapabilityHandshake.ComponentId = "worker-attacker"
	result, err := fixture.service.HandleAuthenticate(context.Background(), packet)
	assertHandshakeErrorReason(t, result, err, authenticationFailedReason())
	assertNoVictimSession(t, fixture)
}

func TestHandshakeServiceSecurity_SafeRejectionRequiresValidWorkerProof(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolAuthenticate(t, fixture, issuedProtocolChallenge(t, fixture), "")
	packet.GetWorkerHandshakeAuthenticate().CapabilityHandshake.Capabilities["progress"] = false
	resignAuthenticate(t, packet, fixture.workerKey)

	result, err := fixture.service.HandleAuthenticate(context.Background(), packet)
	if err != nil || result == nil || result.GetWorkerHandshakeResult().GetAccepted() || result.GetAuthToken() != "" {
		t.Fatalf("safe rejection = (%+v, %v), want signed no-token rejection", result, err)
	}
	keys := map[string]*ecdsa.PublicKey{"scheduler-key-v1": &fixture.schedulerKey.PublicKey}
	if err := capsdk.VerifyTrustHandshake(result, keys); err != nil {
		t.Fatalf("verify safe rejection: %v", err)
	}
}

func TestHandshakeServiceSecurity_RejectsInvalidCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		reason agentv1.WorkerHandshakeRejectionReason
		mutate func(*agentv1.Handshake)
	}{
		{name: "false map value", reason: authenticationFailedReason(), mutate: func(capability *agentv1.Handshake) { capability.Capabilities["progress"] = false }},
		{name: "invalid key", reason: authenticationFailedReason(), mutate: func(capability *agentv1.Handshake) { capability.Capabilities["Bad Key"] = true }},
		{name: "topic outside credential", reason: authenticationFailedReason(), mutate: func(capability *agentv1.Handshake) { capability.ReadyTopics = []string{"job.forbidden"} }},
		{name: "unsupported version", reason: unsupportedVersionReason(), mutate: func(capability *agentv1.Handshake) { capability.SupportedVersions = []int32{2} }},
		{name: "duplicate version", reason: unsupportedVersionReason(), mutate: func(capability *agentv1.Handshake) { capability.SupportedVersions = []int32{1, 1} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProtocolHandshakeFixture(t)
			defer fixture.cleanup()
			packet := protocolAuthenticate(t, fixture, issuedProtocolChallenge(t, fixture), "")
			tc.mutate(packet.GetWorkerHandshakeAuthenticate().CapabilityHandshake)
			resignAuthenticate(t, packet, fixture.workerKey)
			assertAuthenticateRejected(t, fixture, packet, tc.reason)
			assertNoVictimSession(t, fixture)
		})
	}
}

func TestHandshakeServiceSecurity_RejectsUnknownNestedFields(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolAuthenticate(t, fixture, issuedProtocolChallenge(t, fixture), "")
	packet.GetWorkerHandshakeAuthenticate().CapabilityHandshake.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	// Mutate after signing: current stable builders intentionally refuse to
	// sign a structurally invalid packet. The server must still reject the
	// malicious wire input for its unknown field before trusting the signature.
	assertAuthenticateRejected(t, fixture, packet, invalidRequestReason())
}

func TestHandshakeServiceSecurity_RejectsUnsupportedEnvelopeVersion(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	packet := protocolAuthenticate(t, fixture, issuedProtocolChallenge(t, fixture), "")
	packet.ProtocolVersion = 2
	resignAuthenticate(t, packet, fixture.workerKey)
	assertAuthenticateRejected(t, fixture, packet, unsupportedVersionReason())
}

func assertAuthenticateRejected(t *testing.T, fixture *protocolHandshakeFixture, packet *agentv1.BusPacket, reason agentv1.WorkerHandshakeRejectionReason) {
	t.Helper()
	result, err := fixture.service.HandleAuthenticate(context.Background(), packet)
	if err != nil {
		handshakeError, ok := err.(*HandshakeError)
		if !ok || handshakeError.Reason() != reason || result != nil {
			t.Fatalf("opaque authenticate error = (%+v, %T, %v), want %s", result, err, err, reason)
		}
		return
	}
	if result.GetAuthToken() != "" || result.GetWorkerHandshakeResult().GetAccepted() || result.GetWorkerHandshakeResult().GetRejectionReason() != reason {
		t.Fatalf("rejection leaked authority or wrong reason: %+v", result)
	}
	keys := map[string]*ecdsa.PublicKey{"scheduler-key-v1": &fixture.schedulerKey.PublicKey}
	if err := capsdk.VerifyTrustHandshake(result, keys); err != nil {
		t.Fatalf("signed rejection verification: %v", err)
	}
}

func resignAuthenticate(t *testing.T, packet *agentv1.BusPacket, key *ecdsa.PrivateKey) {
	t.Helper()
	packet.Signature = nil
	packet.CreatedAt = timestamppb.New(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	if err := capsdk.SignTrustHandshake(packet, key); err != nil {
		t.Fatalf("re-sign authenticate: %v", err)
	}
}
