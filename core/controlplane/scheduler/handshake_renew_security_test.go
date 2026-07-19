package scheduler

import (
	"context"
	"fmt"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
)

func TestHandshakeServiceSecurity_UnknownSessionKeyIsSessionInvalid(t *testing.T) {
	failure := sessionVerificationFailure(fmt.Errorf("wrapped: %w", ErrSessionTokenUnknownKey), "renew_session_invalid")
	if failure.reason != sessionInvalidReason() || failure.detail != "renew_session_invalid" {
		t.Fatalf("unknown session key mapping = %+v, want opaque session invalid", failure)
	}
}

func TestHandshakeServiceSecurity_ValidRenewRotatesBoundSession(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	binding := victimSessionBinding()
	prior, _, err := fixture.issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("issue prior session: %v", err)
	}
	challenge := issuedRenewChallenge(t, fixture)
	result, err := fixture.service.HandleAuthenticate(context.Background(), protocolAuthenticate(t, fixture, challenge, prior))
	if err != nil || !result.GetWorkerHandshakeResult().GetAccepted() || result.GetAuthToken() == "" || result.GetAuthToken() == prior {
		t.Fatalf("renew result=%+v err=%v", result, err)
	}
	claims, err := fixture.issuer.VerifyBound(context.Background(), result.GetAuthToken(), true)
	if err != nil || claims.Binding() != binding {
		t.Fatalf("renewed authority=%+v err=%v", claims.Binding(), err)
	}
	if _, err := fixture.issuer.VerifyBound(context.Background(), prior, true); err == nil {
		t.Fatal("prior session remained active after renew")
	}
}

func TestHandshakeServiceSecurity_RenewRequiresCurrentBoundSession(t *testing.T) {
	tests := []struct {
		name  string
		token func(*testing.T, *protocolHandshakeFixture) string
		want  agentv1.WorkerHandshakeRejectionReason
	}{
		{name: "missing", want: sessionRequiredReason()},
		{name: "malformed", want: sessionInvalidReason(), token: func(_ *testing.T, _ *protocolHandshakeFixture) string { return "not-a-session" }},
		{name: "wrong worker", want: sessionInvalidReason(), token: func(t *testing.T, fixture *protocolHandshakeFixture) string {
			binding := victimSessionBinding()
			binding.WorkerID, binding.AgentID = "worker-attacker", "agent-attacker"
			return issueBoundForTest(t, fixture, binding)
		}},
		{name: "superseded", want: sessionInvalidReason(), token: func(t *testing.T, fixture *protocolHandshakeFixture) string {
			old := issueBoundForTest(t, fixture, victimSessionBinding())
			_ = issueBoundForTest(t, fixture, victimSessionBinding())
			return old
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProtocolHandshakeFixture(t)
			defer fixture.cleanup()
			token := ""
			if tc.token != nil {
				token = tc.token(t, fixture)
			}
			packet := protocolAuthenticate(t, fixture, issuedRenewChallenge(t, fixture), token)
			assertAuthenticateRejected(t, fixture, packet, tc.want)
		})
	}
}

func TestHandshakeServiceSecurity_InvalidRenewDoesNotConsumeChallenge(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challenge := issuedRenewChallenge(t, fixture)
	missing := protocolAuthenticate(t, fixture, challenge, "")
	assertAuthenticateRejected(t, fixture, missing, sessionRequiredReason())
	validToken := issueBoundForTest(t, fixture, victimSessionBinding())
	valid := protocolAuthenticate(t, fixture, challenge, validToken)
	result, err := fixture.service.HandleAuthenticate(context.Background(), valid)
	if err != nil || !result.GetWorkerHandshakeResult().GetAccepted() {
		t.Fatalf("valid proof after rejected renew = %+v, err=%v", result, err)
	}
}

func TestHandshakeServiceSecurity_AuthTokenIsCoveredByProof(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	token := issueBoundForTest(t, fixture, victimSessionBinding())
	packet := protocolAuthenticate(t, fixture, issuedRenewChallenge(t, fixture), token)
	packet.AuthToken = "substituted-session-token"
	assertAuthenticateRejected(t, fixture, packet, authenticationFailedReason())
}

func issuedRenewChallenge(t *testing.T, fixture *protocolHandshakeFixture) *agentv1.WorkerHandshakeChallenge {
	t.Helper()
	packet, err := fixture.service.HandleChallenge(context.Background(), protocolChallengeRequest(t, fixture, renewPurpose()))
	if err != nil {
		t.Fatalf("issue renew challenge: %v", err)
	}
	return packet.GetWorkerHandshakeChallenge()
}

func issueBoundForTest(t *testing.T, fixture *protocolHandshakeFixture, binding SessionBinding) string {
	t.Helper()
	token, _, err := fixture.issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("issue bound session: %v", err)
	}
	return token
}

func victimSessionBinding() SessionBinding {
	return SessionBinding{
		WorkerID: "worker-victim", AgentID: "agent-victim", Tenant: "tenant-victim",
		Audience: WorkerHandshakeAudience, ProofKeyID: "worker-key-v1", SDKVersion: "v2.13.1",
	}
}

func renewPurpose() agentv1.WorkerHandshakePurpose {
	return agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW
}
