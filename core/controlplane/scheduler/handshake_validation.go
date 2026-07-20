package scheduler

import (
	"regexp"
	"strings"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var handshakeCapabilityKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func (s *HandshakeService) validateChallengeRequest(packet *agentv1.BusPacket) (*agentv1.WorkerHandshakeChallengeRequest, *handshakeFailure) {
	request := packet.GetWorkerHandshakeChallengeRequest()
	if failure := s.validateEnvelope(packet); failure != nil {
		return request, failure
	}
	if request == nil || packet.GetAuthToken() != "" {
		return request, failHandshake(invalidRequestReason(), "challenge_request_invalid")
	}
	if missingChallengeRequestField(request) || request.GetTraceId() != packet.GetTraceId() || request.GetWorkerId() != packet.GetSenderId() {
		return request, failHandshake(invalidRequestReason(), "challenge_request_binding_invalid")
	}
	if servicetoken.IsReservedIdentity(request.GetWorkerId()) {
		return request, failHandshake(authenticationFailedReason(), "reserved_worker_identity")
	}
	if request.GetProtocolVersion() != WorkerHandshakeProtocolVersion {
		return request, failHandshake(unsupportedVersionReason(), "challenge_request_version_unsupported")
	}
	if request.GetAudience() != s.audience {
		return request, failHandshake(authenticationFailedReason(), "challenge_request_audience_mismatch")
	}
	if !validHandshakePurpose(request.GetPurpose()) || len(request.GetClientNonce()) != WorkerHandshakeNonceSize {
		return request, failHandshake(invalidRequestReason(), "challenge_request_parameters_invalid")
	}
	if request.GetProofAlgorithm() != p256ProofAlgorithm() || len(packet.GetSignature()) == 0 {
		return request, failHandshake(authenticationFailedReason(), "challenge_request_proof_invalid")
	}
	return request, nil
}

func (s *HandshakeService) validateAuthenticatePacket(packet *agentv1.BusPacket) (*agentv1.WorkerHandshakeAuthenticate, *handshakeFailure) {
	authenticate := packet.GetWorkerHandshakeAuthenticate()
	if failure := s.validateEnvelope(packet); failure != nil {
		return authenticate, failure
	}
	if authenticate == nil || authenticate.GetChallenge() == nil || authenticate.GetCapabilityHandshake() == nil {
		return authenticate, failHandshake(invalidRequestReason(), "authenticate_payload_invalid")
	}
	challenge := authenticate.GetChallenge()
	if failure := s.validateNestedChallenge(packet, challenge); failure != nil {
		return authenticate, failure
	}
	if failure := validateCapabilityHandshake(authenticate.GetCapabilityHandshake(), challenge); failure != nil {
		return authenticate, failure
	}
	if len(packet.GetSignature()) == 0 {
		return authenticate, failHandshake(authenticationFailedReason(), "authenticate_signature_missing")
	}
	if challenge.GetPurpose() == agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE && packet.GetAuthToken() != "" {
		return authenticate, failHandshake(authenticationFailedReason(), "issue_session_token_present")
	}
	if challenge.GetPurpose() == agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW && strings.TrimSpace(packet.GetAuthToken()) == "" {
		return authenticate, failHandshake(sessionRequiredReason(), "renew_session_token_missing")
	}
	return authenticate, nil
}

func (s *HandshakeService) validateEnvelope(packet *agentv1.BusPacket) *handshakeFailure {
	if packet == nil || hasUnknownProtoFields(packet) || strings.TrimSpace(packet.GetTraceId()) == "" || strings.TrimSpace(packet.GetSenderId()) == "" {
		return failHandshake(invalidRequestReason(), "envelope_invalid")
	}
	if packet.GetProtocolVersion() != WorkerHandshakeProtocolVersion {
		return failHandshake(unsupportedVersionReason(), "envelope_version_unsupported")
	}
	createdAt, ok := validProtoTime(packet.GetCreatedAt())
	if !ok {
		return failHandshake(invalidRequestReason(), "envelope_timestamp_invalid")
	}
	delta := createdAt.Sub(s.now().UTC())
	if delta > s.skew || delta < -s.skew {
		return failHandshake(clockSkewReason(), "envelope_clock_skew")
	}
	if packet.GetPayload() == nil {
		return failHandshake(invalidRequestReason(), "envelope_payload_missing")
	}
	return nil
}

func (s *HandshakeService) validateNestedChallenge(packet *agentv1.BusPacket, challenge *agentv1.WorkerHandshakeChallenge) *handshakeFailure {
	if missingChallengeField(challenge) || packet.GetTraceId() != challenge.GetTraceId() || packet.GetSenderId() != challenge.GetWorkerId() {
		return failHandshake(authenticationFailedReason(), "challenge_binding_invalid")
	}
	if challenge.GetAudience() != s.audience || challenge.GetServerKeyId() != s.schedulerKeyID {
		return failHandshake(authenticationFailedReason(), "challenge_server_binding_invalid")
	}
	if challenge.GetProtocolVersion() != WorkerHandshakeProtocolVersion || !validHandshakePurpose(challenge.GetPurpose()) {
		return failHandshake(unsupportedVersionReason(), "challenge_version_unsupported")
	}
	if challenge.GetProofAlgorithm() != p256ProofAlgorithm() || len(challenge.GetClientNonce()) != WorkerHandshakeNonceSize || len(challenge.GetServerNonce()) != WorkerHandshakeNonceSize {
		return failHandshake(authenticationFailedReason(), "challenge_proof_binding_invalid")
	}
	issuedAt, issuedOK := validProtoTime(challenge.GetIssuedAt())
	expiresAt, expiresOK := validProtoTime(challenge.GetExpiresAt())
	if !issuedOK || !expiresOK || !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > maximumHandshakeLifetime {
		return failHandshake(invalidRequestReason(), "challenge_timestamp_invalid")
	}
	if !s.now().UTC().Before(expiresAt) {
		return failHandshake(challengeExpiredReason(), "challenge_expired")
	}
	return nil
}

func validateCapabilityHandshake(capability *agentv1.Handshake, challenge *agentv1.WorkerHandshakeChallenge) *handshakeFailure {
	if capability.GetComponentId() != challenge.GetWorkerId() || capability.GetRole() != agentv1.ComponentRole_COMPONENT_ROLE_WORKER || capability.GetSdkVersion() != challenge.GetSdkVersion() {
		return failHandshake(authenticationFailedReason(), "capability_identity_mismatch")
	}
	versions := capability.GetSupportedVersions()
	if len(versions) != 1 || versions[0] != WorkerHandshakeProtocolVersion {
		return failHandshake(unsupportedVersionReason(), "capability_version_unsupported")
	}
	for key, enabled := range capability.GetCapabilities() {
		if !enabled || !handshakeCapabilityKey.MatchString(key) {
			return failHandshake(authenticationFailedReason(), "capability_map_invalid")
		}
	}
	seen := make(map[string]struct{}, len(capability.GetReadyTopics()))
	for _, topic := range capability.GetReadyTopics() {
		if strings.TrimSpace(topic) == "" {
			return failHandshake(authenticationFailedReason(), "capability_topic_invalid")
		}
		if _, exists := seen[topic]; exists {
			return failHandshake(authenticationFailedReason(), "capability_topic_duplicate")
		}
		seen[topic] = struct{}{}
	}
	return nil
}

func validateTopicSubset(topics, allowed []string) *handshakeFailure {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, topic := range allowed {
		allowedSet[topic] = struct{}{}
	}
	for _, topic := range topics {
		if _, ok := allowedSet[topic]; !ok {
			return failHandshake(authenticationFailedReason(), "capability_topic_not_authorized")
		}
	}
	return nil
}

func missingChallengeRequestField(request *agentv1.WorkerHandshakeChallengeRequest) bool {
	return request == nil || strings.TrimSpace(request.GetRequestId()) == "" ||
		strings.TrimSpace(request.GetTraceId()) == "" || strings.TrimSpace(request.GetWorkerId()) == "" ||
		strings.TrimSpace(request.GetProofKeyId()) == "" || strings.TrimSpace(request.GetSdkVersion()) == ""
}

func missingChallengeField(challenge *agentv1.WorkerHandshakeChallenge) bool {
	return challenge == nil || strings.TrimSpace(challenge.GetRequestId()) == "" || strings.TrimSpace(challenge.GetChallengeId()) == "" ||
		strings.TrimSpace(challenge.GetTraceId()) == "" || strings.TrimSpace(challenge.GetWorkerId()) == "" ||
		strings.TrimSpace(challenge.GetAgentId()) == "" || strings.TrimSpace(challenge.GetTenantId()) == "" ||
		strings.TrimSpace(challenge.GetProofKeyId()) == "" || strings.TrimSpace(challenge.GetServerKeyId()) == "" || strings.TrimSpace(challenge.GetSdkVersion()) == ""
}

func validHandshakePurpose(purpose agentv1.WorkerHandshakePurpose) bool {
	return purpose == agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE || purpose == agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_RENEW
}

func p256ProofAlgorithm() agentv1.WorkerHandshakeProofAlgorithm {
	return agentv1.WorkerHandshakeProofAlgorithm_WORKER_HANDSHAKE_PROOF_ALGORITHM_ECDSA_P256_SHA256
}

func validProtoTime(timestamp *timestamppb.Timestamp) (time.Time, bool) {
	if timestamp == nil || timestamp.CheckValid() != nil {
		return time.Time{}, false
	}
	return timestamp.AsTime().UTC(), true
}

func hasUnknownProtoFields(message proto.Message) bool {
	if message == nil {
		return false
	}
	return reflectedMessageHasUnknown(message.ProtoReflect())
}

func reflectedMessageHasUnknown(message protoreflect.Message) bool {
	if len(message.GetUnknown()) != 0 {
		return true
	}
	found := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		found = nestedValueHasUnknown(field, value)
		return !found
	})
	return found
}

func nestedValueHasUnknown(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
	if field.IsList() && field.Kind() == protoreflect.MessageKind {
		list := value.List()
		for index := 0; index < list.Len(); index++ {
			if reflectedMessageHasUnknown(list.Get(index).Message()) {
				return true
			}
		}
	}
	if !field.IsList() && !field.IsMap() && field.Kind() == protoreflect.MessageKind {
		return reflectedMessageHasUnknown(value.Message())
	}
	return false
}

func invalidRequestReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_INVALID_REQUEST
}

func authenticationFailedReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_AUTHENTICATION_FAILED
}

func unsupportedVersionReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_UNSUPPORTED_VERSION
}

func clockSkewReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_CLOCK_SKEW
}

func challengeExpiredReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_CHALLENGE_EXPIRED
}

func sessionRequiredReason() agentv1.WorkerHandshakeRejectionReason {
	return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_SESSION_REQUIRED
}
