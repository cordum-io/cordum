package scheduler

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *HandshakeService) authenticate(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, *HandshakeTrustIdentity, *handshakeFailure) {
	authenticate, failure := s.validateAuthenticatePacket(packet)
	if failure != nil {
		return nil, s.resolveSafeRejectionIdentity(ctx, packet), failure
	}
	challenge := authenticate.GetChallenge()
	identity, err := s.resolver.Resolve(ctx, challenge.GetWorkerId(), challenge.GetProofKeyId())
	if err != nil || identity == nil {
		return nil, identity, resolutionFailure(err)
	}
	keys := map[string]*ecdsa.PublicKey{identity.ProofKeyID: identity.PublicKey}
	if err := capsdk.VerifyTrustHandshake(packet, keys); err != nil {
		return nil, identity, failHandshake(authenticationFailedReason(), "authenticate_signature_invalid")
	}
	if failure := validateAuthenticateAuthority(authenticate, identity); failure != nil {
		return nil, identity, failure
	}
	binding := sessionBindingFor(challenge, identity)
	if failure := s.preverifyRenewSession(ctx, packet, binding, challenge.GetPurpose()); failure != nil {
		return nil, identity, failure
	}
	if failure := s.consumeChallenge(ctx, challenge); failure != nil {
		return nil, identity, failure
	}
	token, claims, failure := s.mintHandshakeSession(ctx, packet, binding, challenge.GetPurpose())
	if failure != nil {
		return nil, identity, failure
	}
	response, err := s.buildHandshakeResult(challenge, token, claims.ExpiresAt, unspecifiedReason())
	if err != nil {
		return nil, identity, failHandshake(internalErrorReason(), "result_signing_failed")
	}
	return response, identity, nil
}

func (s *HandshakeService) resolveSafeRejectionIdentity(ctx context.Context, packet *agentv1.BusPacket) *HandshakeTrustIdentity {
	if !structurallySafeRejectionPacket(packet) {
		return nil
	}
	challenge := packet.GetWorkerHandshakeAuthenticate().GetChallenge()
	identity, _ := s.resolver.Resolve(ctx, challenge.GetWorkerId(), challenge.GetProofKeyId())
	return identity
}

func validateAuthenticateAuthority(authenticate *agentv1.WorkerHandshakeAuthenticate, identity *HandshakeTrustIdentity) *handshakeFailure {
	challenge := authenticate.GetChallenge()
	if identity.WorkerID != challenge.GetWorkerId() || identity.AgentID != challenge.GetAgentId() || identity.TenantID != challenge.GetTenantId() || identity.ProofKeyID != challenge.GetProofKeyId() {
		return failHandshake(authenticationFailedReason(), "authoritative_identity_mismatch")
	}
	if identity.PublicKey == nil {
		return failHandshake(authenticationFailedReason(), "proof_key_invalid")
	}
	return validateTopicSubset(authenticate.GetCapabilityHandshake().GetReadyTopics(), identity.AllowedTopics)
}

func sessionBindingFor(challenge *agentv1.WorkerHandshakeChallenge, identity *HandshakeTrustIdentity) SessionBinding {
	return SessionBinding{
		WorkerID: identity.WorkerID, AgentID: identity.AgentID, Tenant: identity.TenantID,
		Audience: challenge.GetAudience(), ProofKeyID: identity.ProofKeyID, SDKVersion: challenge.GetSdkVersion(),
	}
}

func (s *HandshakeService) preverifyRenewSession(ctx context.Context, packet *agentv1.BusPacket, expected SessionBinding, purpose agentv1.WorkerHandshakePurpose) *handshakeFailure {
	if purpose != agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW {
		return nil
	}
	claims, err := s.issuer.VerifyBound(ctx, packet.GetAuthToken(), true)
	if err != nil {
		return sessionVerificationFailure(err, "renew_session_invalid")
	}
	if claims.Binding() != expected {
		return failHandshake(sessionInvalidReason(), "renew_session_binding_mismatch")
	}
	return nil
}

func (s *HandshakeService) consumeChallenge(ctx context.Context, challenge *agentv1.WorkerHandshakeChallenge) *handshakeFailure {
	status, err := s.challenges.Consume(ctx, challenge)
	if err != nil {
		return failHandshake(internalErrorReason(), "challenge_store_unavailable")
	}
	switch status {
	case HandshakeConsumeMatched:
		return nil
	case HandshakeConsumeMismatch:
		return failHandshake(authenticationFailedReason(), "challenge_binding_mismatch")
	default:
		return failHandshake(replayReason(), "challenge_missing_or_replayed")
	}
}

func (s *HandshakeService) mintHandshakeSession(ctx context.Context, packet *agentv1.BusPacket, binding SessionBinding, purpose agentv1.WorkerHandshakePurpose) (string, SessionTokenClaims, *handshakeFailure) {
	if purpose == agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW {
		token, claims, err := s.issuer.RenewBound(ctx, packet.GetAuthToken(), binding)
		if err != nil {
			return "", SessionTokenClaims{}, sessionVerificationFailure(err, "renew_session_rotation_failed")
		}
		return token, claims, nil
	}
	token, claims, err := s.issuer.IssueBound(ctx, binding)
	if err != nil {
		return "", SessionTokenClaims{}, failHandshake(internalErrorReason(), "session_mint_failed")
	}
	return token, claims, nil
}

func sessionVerificationFailure(err error, detail string) *handshakeFailure {
	if isSessionAuthenticationError(err) {
		return failHandshake(sessionInvalidReason(), detail)
	}
	return failHandshake(internalErrorReason(), "session_store_unavailable")
}

func isSessionAuthenticationError(err error) bool {
	return errors.Is(err, ErrSessionTokenMalformed) || errors.Is(err, ErrSessionTokenExpired) ||
		errors.Is(err, ErrSessionTokenNotYetValid) || errors.Is(err, ErrSessionTokenSignature) ||
		errors.Is(err, ErrSessionTokenUnknownKey) ||
		errors.Is(err, ErrSessionTokenRevoked) || errors.Is(err, ErrSessionTokenSuperseded) ||
		errors.Is(err, ErrSessionTokenMissingClaims) || errors.Is(err, ErrSessionTokenBindingMismatch)
}

func (s *HandshakeService) buildHandshakeResult(challenge *agentv1.WorkerHandshakeChallenge, token string, expiresAt time.Time, reason agentv1.WorkerHandshakeRejectionReason) (*agentv1.BusPacket, error) {
	now := s.now().UTC()
	accepted := token != "" && reason == unspecifiedReason()
	result := &agentv1.WorkerHandshakeResult{
		Challenge: proto.Clone(challenge).(*agentv1.WorkerHandshakeChallenge),
		Accepted:  accepted, RejectionReason: reason, IssuedAt: timestamppb.New(now),
	}
	if accepted {
		result.TokenExpiresAt = timestamppb.New(expiresAt.UTC())
	}
	packet := &agentv1.BusPacket{
		TraceId: challenge.GetTraceId(), SenderId: s.schedulerID, CreatedAt: timestamppb.New(now),
		ProtocolVersion: WorkerHandshakeProtocolVersion, AuthToken: token,
		Payload: &agentv1.BusPacket_WorkerHandshakeResult{WorkerHandshakeResult: result},
	}
	if err := capsdk.SignTrustHandshake(packet, s.schedulerKey); err != nil {
		return nil, err
	}
	return packet, nil
}

func (s *HandshakeService) challengeForSafeRejection(packet *agentv1.BusPacket, identity *HandshakeTrustIdentity) *agentv1.WorkerHandshakeChallenge {
	if !structurallySafeRejectionPacket(packet) {
		return nil
	}
	challenge := packet.GetWorkerHandshakeAuthenticate().GetChallenge()
	if challenge == nil || challenge.GetProofAlgorithm() != p256ProofAlgorithm() || challenge.GetServerKeyId() != s.schedulerKeyID {
		return nil
	}
	if !safeRejectionIdentityMatches(identity, challenge) {
		return nil
	}
	keys := map[string]*ecdsa.PublicKey{identity.ProofKeyID: identity.PublicKey}
	if capsdk.VerifyTrustHandshake(packet, keys) != nil {
		return nil
	}
	return challenge
}

func structurallySafeRejectionPacket(packet *agentv1.BusPacket) bool {
	if packet == nil || hasUnknownProtoFields(packet) || packet.GetWorkerHandshakeAuthenticate() == nil || len(packet.GetSignature()) == 0 {
		return false
	}
	challenge := packet.GetWorkerHandshakeAuthenticate().GetChallenge()
	if challenge == nil || stringsMissing(packet.GetTraceId(), packet.GetSenderId(), challenge.GetTraceId(), challenge.GetChallengeId(), challenge.GetWorkerId()) {
		return false
	}
	_, timestampOK := validProtoTime(packet.GetCreatedAt())
	return timestampOK && packet.GetTraceId() == challenge.GetTraceId() && packet.GetSenderId() == challenge.GetWorkerId()
}

func safeRejectionIdentityMatches(identity *HandshakeTrustIdentity, challenge *agentv1.WorkerHandshakeChallenge) bool {
	return identity != nil && identity.PublicKey != nil && identity.WorkerID == challenge.GetWorkerId() &&
		identity.AgentID == challenge.GetAgentId() && identity.TenantID == challenge.GetTenantId() && identity.ProofKeyID == challenge.GetProofKeyId()
}

func stringsMissing(values ...string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func unspecifiedReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_UNSPECIFIED
}

func replayReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_REPLAY_DETECTED
}

func sessionInvalidReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_SESSION_INVALID
}

func internalErrorReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_INTERNAL_ERROR
}
