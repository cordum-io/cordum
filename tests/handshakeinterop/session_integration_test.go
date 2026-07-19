//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/nats-io/nats.go"
)

func (h *interopHarness) TestSessionSupersession(t *testing.T) {
	identity := h.server.identities["inline"]
	connection, err := nats.Connect(h.server.natsURL(), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	defer connection.Close()
	config := workerTrustConfig(identity, h.server)
	issued := exchangeSession(t, connection, config, issuePurpose(), "")
	renewed := exchangeSession(t, connection, config, renewPurpose(), issued.Token)
	if issued.Token == "" || renewed.Token == "" || issued.Token == renewed.Token {
		t.Fatal("ISSUE and RENEW must rotate a nonempty session")
	}
	if _, err := h.server.issuer.VerifyBound(context.Background(), renewed.Token, true); err != nil {
		t.Fatalf("renewed bound session rejected: %v", err)
	}
	_, oldErr := h.server.issuer.VerifyBound(context.Background(), issued.Token, true)
	if !errors.Is(oldErr, scheduler.ErrSessionTokenSuperseded) && !errors.Is(oldErr, scheduler.ErrSessionTokenRevoked) {
		t.Fatalf("old bound session error=%v, want superseded/revoked", oldErr)
	}
	middleware := scheduler.NewSessionTokenMiddleware(h.server.issuer, scheduler.HandshakeModeEnforce,
		scheduler.NewHandshakeMissingTracker())
	assertTokenVerdict(t, middleware, identity.workerID, renewed.Token, scheduler.TokenVerdictPass)
	assertTokenVerdict(t, middleware, identity.workerID, issued.Token, scheduler.TokenVerdictRejectInvalid)
}

func assertTokenVerdict(t *testing.T, middleware *scheduler.SessionTokenMiddleware, workerID, token string, want scheduler.TokenVerdict) {
	t.Helper()
	packet := &agentv1.BusPacket{AuthToken: token}
	result := middleware.Verify(context.Background(), workerID, packet)
	if result.Verdict != want {
		t.Fatalf("token verdict=%v want=%v", result.Verdict, want)
	}
}

func workerTrustConfig(identity *interopIdentity, server *interopServer) *capsdk.WorkerTrustConfig {
	return &capsdk.WorkerTrustConfig{
		WorkerID: identity.workerID, ExpectedAgentID: identity.agentID, TenantID: identity.tenantID,
		Audience: capsdk.WorkerHandshakeAudience, ProofKeyID: identity.keyID,
		ProofPrivateKey: identity.privateKey, ExpectedSchedulerID: server.schedulerID,
		SchedulerPublicKeys: map[string]*ecdsa.PublicKey{server.schedulerKeyID: &server.schedulerKey.PublicKey},
		SDKVersion:          identity.sdkVersion,
	}
}

func exchangeSession(t *testing.T, connection *nats.Conn, config *capsdk.WorkerTrustConfig,
	purpose agentv1.WorkerHandshakePurpose, currentToken string) *capsdk.WorkerHandshakeSession {
	t.Helper()
	now := time.Now().UTC()
	request, err := capsdk.BuildWorkerHandshakeChallengeRequest(config, capsdk.WorkerHandshakeRequestOptions{
		RequestID: randomID(t), TraceID: randomID(t), Purpose: purpose,
		ClientNonce: randomNonce(t), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("build challenge request: %v", err)
	}
	challenge := requestPacket(t, connection, capsdk.WorkerHandshakeChallengeSubject, request)
	verified, err := capsdk.VerifyWorkerHandshakeChallenge(config, request, challenge, time.Now().UTC())
	if err != nil {
		t.Fatalf("verify challenge: %v", err)
	}
	authenticate, err := capsdk.BuildWorkerHandshakeAuthenticate(config, verified, &agentv1.Handshake{
		ComponentId: config.WorkerID, Role: agentv1.ComponentRole_COMPONENT_ROLE_WORKER,
		SupportedVersions: []int32{1}, Capabilities: map[string]bool{"progress": true},
		SdkVersion: config.SDKVersion, ReadyTopics: []string{"job.interop"},
	}, currentToken, time.Now().UTC())
	if err != nil {
		t.Fatalf("build authenticate: %v", err)
	}
	result := requestPacket(t, connection, capsdk.WorkerHandshakeAuthenticateSubject, authenticate)
	session, err := capsdk.VerifyWorkerHandshakeResult(config, verified, authenticate, result, time.Now().UTC())
	if err != nil {
		t.Fatalf("verify result: %v", err)
	}
	return session
}

func requestPacket(t *testing.T, connection *nats.Conn, subject string, packet *agentv1.BusPacket) *agentv1.BusPacket {
	t.Helper()
	data, err := capsdk.MarshalWorkerTrustPacket(packet)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	message, err := connection.Request(subject, data, 3*time.Second)
	if err != nil {
		t.Fatalf("request %s: %v", subject, err)
	}
	result, err := capsdk.UnmarshalWorkerTrustPacket(message.Data)
	if err != nil {
		t.Fatalf("unmarshal %s response: %v", subject, err)
	}
	return result
}

func randomID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random ID: %v", err)
	}
	return hex.EncodeToString(value)
}

func randomNonce(t *testing.T) []byte {
	t.Helper()
	value := make([]byte, capsdk.WorkerHandshakeNonceSize)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random nonce: %v", err)
	}
	return value
}

func issuePurpose() agentv1.WorkerHandshakePurpose {
	return agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE
}

func renewPurpose() agentv1.WorkerHandshakePurpose {
	return agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW
}
