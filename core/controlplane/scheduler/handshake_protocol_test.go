package scheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/audit"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type protocolTrustResolver struct {
	identity *HandshakeTrustIdentity
	err      error
}

func (r *protocolTrustResolver) Resolve(_ context.Context, workerID, keyID string) (*HandshakeTrustIdentity, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.identity == nil || r.identity.WorkerID != workerID || r.identity.ProofKeyID != keyID {
		return nil, nil
	}
	copy := *r.identity
	copy.AllowedTopics = append([]string(nil), r.identity.AllowedTopics...)
	return &copy, nil
}

type protocolAuditSink struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (s *protocolAuditSink) Emit(_ context.Context, event audit.SIEMEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *protocolAuditSink) snapshot() []audit.SIEMEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.SIEMEvent(nil), s.events...)
}

type protocolHandshakeFixture struct {
	service      *HandshakeService
	issuer       *SessionTokenIssuer
	workerKey    *ecdsa.PrivateKey
	schedulerKey *ecdsa.PrivateKey
	sink         *protocolAuditSink
	resolver     *protocolTrustResolver
	sessionStore *miniredis.Miniredis
	now          time.Time
	cleanup      func()
}

func newProtocolHandshakeFixture(t *testing.T) *protocolHandshakeFixture {
	t.Helper()
	workerKey := generateProtocolKey(t)
	schedulerKey := generateProtocolKey(t)
	issuer, sessionStore, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	identity := &HandshakeTrustIdentity{
		WorkerID: "worker-victim", AgentID: "agent-victim", TenantID: "tenant-victim",
		ProofKeyID: "worker-key-v1", PublicKey: &workerKey.PublicKey,
		AllowedTopics: []string{"job.allowed"}, AgentName: "Victim worker",
	}
	resolver := &protocolTrustResolver{identity: identity}
	sink := &protocolAuditSink{}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	service, err := NewHandshakeService(issuer, resolver, NewRedisHandshakeChallengeStore(rdb), sink, HandshakeServiceOptions{
		Audience: WorkerHandshakeAudience, SchedulerID: "cordum-scheduler",
		SchedulerKeyID: "scheduler-key-v1", SchedulerPrivateKey: schedulerKey,
		Skew: time.Minute, ChallengeTTL: 30 * time.Second, Now: func() time.Time { return now },
	})
	if err != nil {
		cleanup()
		t.Fatalf("new handshake service: %v", err)
	}
	return &protocolHandshakeFixture{
		service: service, issuer: issuer, workerKey: workerKey, schedulerKey: schedulerKey,
		sink: sink, resolver: resolver, sessionStore: sessionStore, now: now, cleanup: cleanup,
	}
}

func generateProtocolKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return key
}

func protocolChallengeRequest(t *testing.T, fixture *protocolHandshakeFixture, purpose agentv1.WorkerHandshakePurpose) *agentv1.BusPacket {
	t.Helper()
	request := &agentv1.WorkerHandshakeChallengeRequest{
		RequestId: "request-1", TraceId: "trace-1", WorkerId: "worker-victim",
		ProofKeyId: "worker-key-v1", ProofAlgorithm: agentv1.WorkerHandshakeProofAlgorithm_WORKER_HANDSHAKE_PROOF_ALGORITHM_ECDSA_P256_SHA256,
		Audience: WorkerHandshakeAudience, Purpose: purpose, ClientNonce: make([]byte, WorkerHandshakeNonceSize),
		ProtocolVersion: WorkerHandshakeProtocolVersion, SdkVersion: "v2.13.1",
	}
	for index := range request.ClientNonce {
		request.ClientNonce[index] = byte(index + 1)
	}
	packet := &agentv1.BusPacket{
		TraceId: request.TraceId, SenderId: request.WorkerId, CreatedAt: timestamppb.New(fixture.now),
		ProtocolVersion: WorkerHandshakeProtocolVersion,
		Payload:         &agentv1.BusPacket_WorkerHandshakeChallengeRequest{WorkerHandshakeChallengeRequest: request},
	}
	if err := capsdk.SignTrustHandshake(packet, fixture.workerKey); err != nil {
		t.Fatalf("sign challenge request: %v", err)
	}
	return packet
}

func protocolAuthenticate(t *testing.T, fixture *protocolHandshakeFixture, challenge *agentv1.WorkerHandshakeChallenge, token string) *agentv1.BusPacket {
	t.Helper()
	authenticate := &agentv1.WorkerHandshakeAuthenticate{
		Challenge: proto.Clone(challenge).(*agentv1.WorkerHandshakeChallenge),
		CapabilityHandshake: &agentv1.Handshake{
			ComponentId: "worker-victim", Role: agentv1.ComponentRole_COMPONENT_ROLE_WORKER,
			SupportedVersions: []int32{WorkerHandshakeProtocolVersion}, Capabilities: map[string]bool{"progress": true},
			SdkVersion: "v2.13.1", ReadyTopics: []string{"job.allowed"},
		},
	}
	packet := &agentv1.BusPacket{
		TraceId: challenge.TraceId, SenderId: challenge.WorkerId, CreatedAt: timestamppb.New(fixture.now),
		ProtocolVersion: WorkerHandshakeProtocolVersion, AuthToken: token,
		Payload: &agentv1.BusPacket_WorkerHandshakeAuthenticate{WorkerHandshakeAuthenticate: authenticate},
	}
	if err := capsdk.SignTrustHandshake(packet, fixture.workerKey); err != nil {
		t.Fatalf("sign authenticate: %v", err)
	}
	return packet
}

func TestHandshakeProtocolIssueMintsAuthoritativelyBoundSession(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challengePacket, err := fixture.service.HandleChallenge(context.Background(), protocolChallengeRequest(t, fixture, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE))
	if err != nil {
		t.Fatalf("handle challenge: %v", err)
	}
	if err := capsdk.VerifyTrustHandshake(challengePacket, map[string]*ecdsa.PublicKey{"scheduler-key-v1": &fixture.schedulerKey.PublicKey}); err != nil {
		t.Fatalf("verify scheduler challenge: %v", err)
	}
	resultPacket, err := fixture.service.HandleAuthenticate(context.Background(), protocolAuthenticate(t, fixture, challengePacket.GetWorkerHandshakeChallenge(), ""))
	if err != nil {
		t.Fatalf("handle authenticate: %v", err)
	}
	if !resultPacket.GetWorkerHandshakeResult().GetAccepted() || resultPacket.GetAuthToken() == "" {
		t.Fatalf("accepted result/token required: %+v", resultPacket.GetWorkerHandshakeResult())
	}
	if err := capsdk.VerifyTrustHandshake(resultPacket, map[string]*ecdsa.PublicKey{"scheduler-key-v1": &fixture.schedulerKey.PublicKey}); err != nil {
		t.Fatalf("verify scheduler result: %v", err)
	}
	claims, err := fixture.issuer.VerifyBound(context.Background(), resultPacket.GetAuthToken(), true)
	if err != nil {
		t.Fatalf("verify bound token: %v", err)
	}
	if claims.Subject != "worker-victim" || claims.AgentID != "agent-victim" || claims.Tenant != "tenant-victim" || claims.Audience != WorkerHandshakeAudience || claims.ProofKeyID != "worker-key-v1" {
		t.Fatalf("unexpected authoritative claims: %+v", claims)
	}
}

func TestHandshakeProtocolExactAuthenticateReplayMintsOnce(t *testing.T) {
	fixture := newProtocolHandshakeFixture(t)
	defer fixture.cleanup()
	challengePacket, err := fixture.service.HandleChallenge(context.Background(), protocolChallengeRequest(t, fixture, agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE))
	if err != nil {
		t.Fatalf("handle challenge: %v", err)
	}
	authenticate := protocolAuthenticate(t, fixture, challengePacket.GetWorkerHandshakeChallenge(), "")
	first, err := fixture.service.HandleAuthenticate(context.Background(), authenticate)
	if err != nil || !first.GetWorkerHandshakeResult().GetAccepted() {
		t.Fatalf("first authenticate: result=%+v err=%v", first, err)
	}
	second, err := fixture.service.HandleAuthenticate(context.Background(), authenticate)
	if err != nil {
		t.Fatalf("replay authenticate: %v", err)
	}
	if second.GetWorkerHandshakeResult().GetAccepted() || second.GetAuthToken() != "" || second.GetWorkerHandshakeResult().GetRejectionReason() != agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_REPLAY_DETECTED {
		t.Fatalf("replay must be opaque no-token rejection: %+v", second)
	}
}
