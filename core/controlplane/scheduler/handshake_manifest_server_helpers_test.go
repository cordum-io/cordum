package scheduler

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

func mutationString(t *testing.T, mutation serverMutation) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(mutation.Value, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func mutationInt(t *testing.T, mutation serverMutation) int {
	t.Helper()
	return int(mutationInt64(t, mutation))
}

func mutationInt64(t *testing.T, mutation serverMutation) int64 {
	t.Helper()
	var value int64
	if err := json.Unmarshal(mutation.Value, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func resignTrustPacket(t *testing.T, packet *agentv1.BusPacket, key *ecdsa.PrivateKey) {
	t.Helper()
	packet.Signature = nil
	if err := capsdk.SignTrustHandshake(packet, key); err != nil {
		t.Fatal(err)
	}
}

func handshakeOutcomeReason(result *agentv1.BusPacket, err error) string {
	if result != nil && result.GetWorkerHandshakeResult() != nil {
		return normalizeRejectionReason(result.GetWorkerHandshakeResult().GetRejectionReason())
	}
	var handshakeErr *HandshakeError
	if errors.As(err, &handshakeErr) {
		return normalizeRejectionReason(handshakeErr.reason)
	}
	if err == nil {
		return "UNSPECIFIED"
	}
	return "UNEXPECTED_ERROR"
}

func normalizeRejectionReason(reason agentv1.WorkerHandshakeRejectionReason) string {
	return strings.TrimPrefix(reason.String(), "WORKER_HANDSHAKE_REJECTION_REASON_")
}

func workerTrustConfig(fixture *protocolHandshakeFixture) *capsdk.WorkerTrustConfig {
	return &capsdk.WorkerTrustConfig{
		WorkerID: "worker-victim", ExpectedAgentID: "agent-victim", TenantID: "tenant-victim",
		Audience: WorkerHandshakeAudience, ProofKeyID: "worker-key-v1", ProofPrivateKey: fixture.workerKey,
		ExpectedSchedulerID: "cordum-scheduler",
		SchedulerPublicKeys: map[string]*ecdsa.PublicKey{"scheduler-key-v1": &fixture.schedulerKey.PublicKey},
		SDKVersion:          "v2.13.1",
	}
}

func mutateResultChallenge(t *testing.T, challenge *agentv1.WorkerHandshakeChallenge, mutation serverMutation) {
	t.Helper()
	value := mutationString(t, mutation)
	switch mutation.Path {
	case "worker_handshake_result.challenge.request_id":
		challenge.RequestId = value
	case "worker_handshake_result.challenge.trace_id":
		challenge.TraceId = value
	case "worker_handshake_result.challenge.challenge_id":
		challenge.ChallengeId = value
	default:
		t.Fatalf("unsupported result mutation path %q", mutation.Path)
	}
}

func mutateSessionClaim(t *testing.T, claims *SessionTokenClaims, mutation serverMutation) {
	t.Helper()
	value := mutationString(t, mutation)
	switch mutation.Path {
	case "sub":
		claims.Subject = value
	case "agent_id":
		claims.AgentID = value
	case "tenant_id":
		claims.Tenant = value
	case "aud":
		claims.Audience = value
	case "proof_key_id":
		claims.ProofKeyID = value
	default:
		t.Fatalf("unsupported token mutation path %q", mutation.Path)
	}
}

func protocolBinding() SessionBinding {
	return SessionBinding{WorkerID: "worker-victim", AgentID: "agent-victim", Tenant: "tenant-victim",
		Audience: WorkerHandshakeAudience, ProofKeyID: "worker-key-v1", SDKVersion: "v2.13.1"}
}

func issueProtocolSession(t *testing.T, fixture *protocolHandshakeFixture) string {
	t.Helper()
	challenge := issuedProtocolChallenge(t, fixture)
	result, err := fixture.service.HandleAuthenticate(context.Background(), protocolAuthenticate(t, fixture, challenge, ""))
	if err != nil {
		t.Fatal(err)
	}
	return result.GetAuthToken()
}

func issuedManifestRenewChallenge(t *testing.T, fixture *protocolHandshakeFixture) *agentv1.WorkerHandshakeChallenge {
	t.Helper()
	request := protocolChallengeRequest(t, fixture, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW)
	request.TraceId = "trace-renew"
	payload := request.GetWorkerHandshakeChallengeRequest()
	payload.TraceId, payload.RequestId = request.TraceId, "request-renew"
	payload.ClientNonce[0] ^= 0xff
	resignTrustPacket(t, request, fixture.workerKey)
	result, err := fixture.service.HandleChallenge(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result.GetWorkerHandshakeChallenge()
}
