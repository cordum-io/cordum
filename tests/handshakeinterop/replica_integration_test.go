//go:build handshakeinterop

package handshakeinterop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/nats-io/nats.go"
)

type crossReplicaExchange struct {
	connection *nats.Conn
	config     *capsdk.WorkerTrustConfig
	identity   *interopIdentity
	verified   *capsdk.VerifiedWorkerHandshakeChallenge
}

type authenticateOutcome struct {
	packet *agentv1.BusPacket
	err    error
}

func (s *interopServer) proveCrossReplica() {
	s.t.Helper()
	s.startReplica()
	exchange := s.requestCrossReplicaChallenge()
	defer exchange.connection.Close()
	s.stopChallengeReplica()
	s.startReplica()
	authenticate := s.completeCrossReplicaExchange(exchange)
	s.assertCrossReplicaReplay(exchange, authenticate)
	// The verified result and replay rejection are the proof. Diagnostic handler
	// counts also include readiness probes and can settle after a baseline read.
	s.crossReplicaOK = true
	s.startReplica()
}

func (s *interopServer) requestCrossReplicaChallenge() *crossReplicaExchange {
	connection, err := nats.Connect(s.natsURL(), nats.Timeout(2*time.Second))
	if err != nil {
		s.t.Fatalf("connect cross-replica client: %v", err)
	}
	identity := s.identities["inline"]
	config := workerTrustConfig(identity, s)
	request, err := capsdk.BuildWorkerHandshakeChallengeRequest(config, capsdk.WorkerHandshakeRequestOptions{
		RequestID: randomID(s.t), TraceID: randomID(s.t), Purpose: issuePurpose(),
		ClientNonce: randomNonce(s.t), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		connection.Close()
		s.t.Fatalf("build cross-replica request: %v", err)
	}
	challenge := requestPacket(s.t, connection, capsdk.WorkerHandshakeChallengeSubject, request)
	verified, err := capsdk.VerifyWorkerHandshakeChallenge(config, request, challenge, time.Now().UTC())
	if err != nil {
		connection.Close()
		s.t.Fatalf("verify cross-replica challenge: %v", err)
	}
	return &crossReplicaExchange{connection: connection, config: config, identity: identity, verified: verified}
}

func (s *interopServer) stopChallengeReplica() {
	if err := s.subscribers[0].Close(); err != nil {
		s.t.Fatalf("close first replica: %v", err)
	}
	s.buses[0].Close()
	s.buses, s.subscribers = nil, nil
}

func (s *interopServer) completeCrossReplicaExchange(exchange *crossReplicaExchange) *agentv1.BusPacket {
	authenticate, err := capsdk.BuildWorkerHandshakeAuthenticate(
		exchange.config, exchange.verified, interopCapability(exchange.config), "", time.Now().UTC())
	if err != nil {
		s.t.Fatalf("build cross-replica authenticate: %v", err)
	}
	result := requestPacket(s.t, exchange.connection, capsdk.WorkerHandshakeAuthenticateSubject, authenticate)
	if _, err := capsdk.VerifyWorkerHandshakeResult(
		exchange.config, exchange.verified, authenticate, result, time.Now().UTC()); err != nil {
		s.t.Fatalf("verify cross-replica result: %v", err)
	}
	return authenticate
}

func (s *interopServer) assertCrossReplicaReplay(exchange *crossReplicaExchange, authenticate *agentv1.BusPacket) {
	before := s.activeRecord(exchange.identity)
	replay := requestPacket(s.t, exchange.connection, capsdk.WorkerHandshakeAuthenticateSubject, authenticate)
	result := replay.GetWorkerHandshakeResult()
	if replay.GetAuthToken() != "" || result.GetAccepted() ||
		result.GetRejectionReason() != agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_REPLAY_DETECTED {
		s.t.Fatal("cross-replica replay minted or installed authority")
	}
	if after := s.activeRecord(exchange.identity); !bytes.Equal(before, after) {
		s.t.Fatal("cross-replica replay changed Redis session authority")
	}
}

func (h *interopHarness) TestConcurrentReplay(t *testing.T) {
	connection, authenticate := h.buildConcurrentAuthenticate(t)
	defer connection.Close()
	wire, err := capsdk.MarshalWorkerTrustPacket(authenticate)
	if err != nil {
		t.Fatalf("marshal concurrent authenticate: %v", err)
	}
	before := h.mustOwnedSessionState(t)
	start := make(chan struct{})
	outcomes := make(chan authenticateOutcome, 2)
	for index := 0; index < 2; index++ {
		go requestAuthenticate(connection, wire, start, outcomes)
	}
	close(start)
	accepted, replayed := 0, 0
	var acceptedToken string
	for index := 0; index < 2; index++ {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent authenticate: %v", outcome.err)
		}
		result := outcome.packet.GetWorkerHandshakeResult()
		if result.GetAccepted() && outcome.packet.GetAuthToken() != "" {
			accepted++
			acceptedToken = outcome.packet.GetAuthToken()
		} else if !result.GetAccepted() && outcome.packet.GetAuthToken() == "" &&
			result.GetRejectionReason() == agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_REPLAY_DETECTED {
			replayed++
		}
	}
	after := h.mustOwnedSessionState(t)
	identity := h.server.identities["concurrent"]
	if accepted != 1 || replayed != 1 {
		t.Fatalf("concurrent accepted=%d replayed=%d", accepted, replayed)
	}
	h.assertSinglePersistedMint(t, identity, acceptedToken, before, after)
	t.Log("concurrent accepted=1 replayed=1 redis_mint_delta=1")
}

func (h *interopHarness) assertSinglePersistedMint(t *testing.T, identity *interopIdentity,
	token string, before, after redisState) {
	t.Helper()
	activeKey := activeSessionKey(identity)
	if _, existed := before[activeKey]; existed || len(after) != len(before)+1 {
		t.Fatalf("concurrent Redis authority delta: %s", describeRedisStateChange(before, after))
	}
	for key, value := range before {
		if !bytes.Equal(value, after[key]) {
			t.Fatalf("concurrent mint changed existing Redis authority key %s", key)
		}
	}
	active, exists := after[activeKey]
	if !exists {
		t.Fatal("concurrent mint did not install active Redis authority")
	}
	claims, err := h.server.issuer.VerifyBound(context.Background(), token, true)
	if err != nil {
		t.Fatalf("verify concurrently minted session: %v", err)
	}
	var record struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(active, &record); err != nil || record.JTI != claims.JTI {
		t.Fatalf("persisted concurrent JTI=%q claims=%q err=%v", record.JTI, claims.JTI, err)
	}
}

func (h *interopHarness) buildConcurrentAuthenticate(t *testing.T) (*nats.Conn, *agentv1.BusPacket) {
	t.Helper()
	connection, err := nats.Connect(h.server.natsURL(), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect concurrent client: %v", err)
	}
	identity := h.server.identities["concurrent"]
	config := workerTrustConfig(identity, h.server)
	request, err := capsdk.BuildWorkerHandshakeChallengeRequest(config, capsdk.WorkerHandshakeRequestOptions{
		RequestID: randomID(t), TraceID: randomID(t), Purpose: issuePurpose(),
		ClientNonce: randomNonce(t), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		connection.Close()
		t.Fatalf("build concurrent challenge: %v", err)
	}
	challenge := requestPacket(t, connection, capsdk.WorkerHandshakeChallengeSubject, request)
	verified, err := capsdk.VerifyWorkerHandshakeChallenge(config, request, challenge, time.Now().UTC())
	if err != nil {
		connection.Close()
		t.Fatalf("verify concurrent challenge: %v", err)
	}
	authenticate, err := capsdk.BuildWorkerHandshakeAuthenticate(
		config, verified, interopCapability(config), "", time.Now().UTC())
	if err != nil {
		connection.Close()
		t.Fatalf("build concurrent authenticate: %v", err)
	}
	return connection, authenticate
}

func requestAuthenticate(connection *nats.Conn, wire []byte, start <-chan struct{}, outcomes chan<- authenticateOutcome) {
	<-start
	message, err := connection.Request(capsdk.WorkerHandshakeAuthenticateSubject, wire, 3*time.Second)
	if err != nil {
		outcomes <- authenticateOutcome{err: err}
		return
	}
	packet, err := capsdk.UnmarshalWorkerTrustPacket(message.Data)
	outcomes <- authenticateOutcome{packet: packet, err: err}
}

func activeSessionKey(identity *interopIdentity) string {
	return "session:worker:v2:" + encodedSessionKeyPart(identity.tenantID) + ":" + encodedSessionKeyPart(identity.workerID)
}

func encodedSessionKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(value)))
}

func revokedSessionPrefix(identity *interopIdentity) string {
	return "session:revoked:" + encodedSessionKeyPart(identity.tenantID) + ":"
}

func (s *interopServer) activeRecord(identity *interopIdentity) []byte {
	s.t.Helper()
	value, err := s.redis.Get(context.Background(), activeSessionKey(identity)).Bytes()
	if err != nil {
		s.t.Fatalf("read active session record: %v", err)
	}
	return value
}

func interopCapability(config *capsdk.WorkerTrustConfig) *agentv1.Handshake {
	return &agentv1.Handshake{
		ComponentId: config.WorkerID, Role: agentv1.ComponentRole_COMPONENT_ROLE_WORKER,
		SupportedVersions: []int32{1}, Capabilities: map[string]bool{"progress": true},
		SdkVersion: config.SDKVersion, ReadyTopics: []string{"job.interop"}, AgentName: config.WorkerID,
	}
}
