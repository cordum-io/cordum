package scheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrHandshakeChallengeStoreUnavailable = errors.New("scheduler: handshake challenge store unavailable")

const handshakeStorePrefix = "session:handshake:"

var createHandshakeChallengeScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1], KEYS[2], KEYS[3]) ~= 0 then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
redis.call('SET', KEYS[3], ARGV[2], 'PX', ARGV[3])
return 1
`)

var consumeHandshakeChallengeScript = redis.NewScript(`
local stored = redis.call('GET', KEYS[1])
if not stored then
  return 0
end
if stored ~= ARGV[1] then
  return -1
end
if redis.call('GET', KEYS[2]) ~= ARGV[2] or redis.call('GET', KEYS[3]) ~= ARGV[2] then
  return -1
end
redis.call('DEL', KEYS[1])
return 1
`)

// RedisHandshakeChallengeStore atomically protects request IDs and nonces.
type RedisHandshakeChallengeStore struct {
	client redis.UniversalClient
}

func NewRedisHandshakeChallengeStore(client redis.UniversalClient) *RedisHandshakeChallengeStore {
	return &RedisHandshakeChallengeStore{client: client}
}

func (s *RedisHandshakeChallengeStore) Create(ctx context.Context, challenge *agentv1.WorkerHandshakeChallenge, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrHandshakeChallengeStoreUnavailable
	}
	serialized, keys, challengeID, err := handshakeStoreArguments(challenge)
	if err != nil || ttl <= 0 {
		return false, ErrHandshakeChallengeStoreUnavailable
	}
	result, err := createHandshakeChallengeScript.Run(ctx, s.client, keys, serialized, challengeID, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, ErrHandshakeChallengeStoreUnavailable
	}
	return result == 1, nil
}

func (s *RedisHandshakeChallengeStore) Consume(ctx context.Context, challenge *agentv1.WorkerHandshakeChallenge) (HandshakeConsumeStatus, error) {
	if s == nil || s.client == nil {
		return HandshakeConsumeMissing, ErrHandshakeChallengeStoreUnavailable
	}
	serialized, keys, challengeID, err := handshakeStoreArguments(challenge)
	if err != nil {
		return HandshakeConsumeMismatch, nil
	}
	result, err := consumeHandshakeChallengeScript.Run(ctx, s.client, keys, serialized, challengeID).Int64()
	if err != nil {
		return HandshakeConsumeMissing, ErrHandshakeChallengeStoreUnavailable
	}
	switch result {
	case 1:
		return HandshakeConsumeMatched, nil
	case -1:
		return HandshakeConsumeMismatch, nil
	default:
		return HandshakeConsumeMissing, nil
	}
}

func handshakeStoreArguments(challenge *agentv1.WorkerHandshakeChallenge) ([]byte, []string, string, error) {
	if challenge == nil || strings.TrimSpace(challenge.GetChallengeId()) == "" {
		return nil, nil, "", errors.New("challenge required")
	}
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(challenge)
	if err != nil {
		return nil, nil, "", err
	}
	keys := []string{
		handshakeStoreKey("challenge", []byte(challenge.GetChallengeId())),
		handshakeStoreScopedKey("request", challenge.GetWorkerId(), []byte(challenge.GetRequestId())),
		handshakeStoreScopedKey("nonce", challenge.GetWorkerId(), challenge.GetClientNonce()),
	}
	return serialized, keys, challenge.GetChallengeId(), nil
}

func handshakeStoreKey(kind string, value []byte) string {
	return handshakeStorePrefix + kind + ":" + base64.RawURLEncoding.EncodeToString(value)
}

func handshakeStoreScopedKey(kind, workerID string, value []byte) string {
	worker := base64.RawURLEncoding.EncodeToString([]byte(workerID))
	return handshakeStorePrefix + kind + ":" + worker + ":" + base64.RawURLEncoding.EncodeToString(value)
}

func (s *HandshakeService) issueChallenge(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, *HandshakeTrustIdentity, *handshakeFailure) {
	request, failure := s.validateChallengeRequest(packet)
	if failure != nil {
		return nil, nil, failure
	}
	identity, err := s.resolver.Resolve(ctx, request.GetWorkerId(), request.GetProofKeyId())
	if err != nil || identity == nil {
		return nil, identity, resolutionFailure(err)
	}
	if !resolvedIdentityMatchesRequest(identity, request) {
		return nil, identity, failHandshake(authenticationFailedReason(), "resolved_worker_binding_mismatch")
	}
	keys := map[string]*ecdsa.PublicKey{identity.ProofKeyID: identity.PublicKey}
	if err := capsdk.VerifyTrustHandshake(packet, keys); err != nil {
		return nil, identity, failHandshake(authenticationFailedReason(), "challenge_request_signature_invalid")
	}
	response, challenge, failure := s.newSignedChallenge(request, identity)
	if failure != nil {
		return nil, identity, failure
	}
	created, err := s.challenges.Create(ctx, challenge, s.challengeTTL)
	if err != nil {
		return nil, identity, failHandshake(internalErrorReason(), "challenge_store_unavailable")
	}
	if !created {
		return nil, identity, failHandshake(replayReason(), "challenge_request_replayed")
	}
	return response, identity, nil
}

func (s *HandshakeService) newSignedChallenge(request *agentv1.WorkerHandshakeChallengeRequest, identity *HandshakeTrustIdentity) (*agentv1.BusPacket, *agentv1.WorkerHandshakeChallenge, *handshakeFailure) {
	challengeID, err := randomHandshakeValue(WorkerHandshakeNonceSize)
	if err != nil {
		return nil, nil, failHandshake(internalErrorReason(), "challenge_random_unavailable")
	}
	serverNonce := make([]byte, WorkerHandshakeNonceSize)
	if _, err := rand.Read(serverNonce); err != nil {
		return nil, nil, failHandshake(internalErrorReason(), "challenge_random_unavailable")
	}
	now := s.now().UTC()
	challenge := challengeFromRequest(request, identity, challengeID, serverNonce, s.schedulerKeyID, now, s.challengeTTL)
	packet := &agentv1.BusPacket{
		TraceId: challenge.GetTraceId(), SenderId: s.schedulerID, CreatedAt: timestamppb.New(now),
		ProtocolVersion: WorkerHandshakeProtocolVersion,
		Payload:         &agentv1.BusPacket_WorkerHandshakeChallenge{WorkerHandshakeChallenge: challenge},
	}
	if err := capsdk.SignTrustHandshake(packet, s.schedulerKey); err != nil {
		return nil, nil, failHandshake(internalErrorReason(), "challenge_signing_failed")
	}
	return packet, challenge, nil
}

func challengeFromRequest(request *agentv1.WorkerHandshakeChallengeRequest, identity *HandshakeTrustIdentity, challengeID string, serverNonce []byte, serverKeyID string, now time.Time, ttl time.Duration) *agentv1.WorkerHandshakeChallenge {
	return &agentv1.WorkerHandshakeChallenge{
		RequestId: request.GetRequestId(), ChallengeId: challengeID, TraceId: request.GetTraceId(),
		WorkerId: identity.WorkerID, AgentId: identity.AgentID, TenantId: identity.TenantID,
		ProofKeyId: identity.ProofKeyID, ProofAlgorithm: request.GetProofAlgorithm(), ServerKeyId: serverKeyID,
		Audience: request.GetAudience(), Purpose: request.GetPurpose(),
		ClientNonce: append([]byte(nil), request.GetClientNonce()...), ServerNonce: append([]byte(nil), serverNonce...),
		ProtocolVersion: request.GetProtocolVersion(), SdkVersion: request.GetSdkVersion(),
		IssuedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(ttl)),
	}
}

func resolvedIdentityMatchesRequest(identity *HandshakeTrustIdentity, request *agentv1.WorkerHandshakeChallengeRequest) bool {
	return identity != nil && identity.WorkerID == request.GetWorkerId() && identity.ProofKeyID == request.GetProofKeyId() &&
		strings.TrimSpace(identity.AgentID) != "" && strings.TrimSpace(identity.TenantID) != "" && identity.PublicKey != nil
}

func randomHandshakeValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
