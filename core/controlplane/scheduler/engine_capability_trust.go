package scheduler

import (
	"strings"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

type handshakeTrustRegistry interface {
	UpdateHandshakeTrust(*pb.Handshake, bool)
}

func (e *Engine) handleCapabilityHandshake(packet *pb.BusPacket, handshake *pb.Handshake) {
	if e.sessionMiddleware == nil || e.sessionMiddleware.Mode() == HandshakeModeOff {
		e.registry.UpdateHandshake(handshake)
		return
	}
	result, allowed := e.verifySessionTokenResult(packet, handshake.GetComponentId(), "handshake")
	if !allowed {
		return
	}
	trusted, ok := e.authorizedCapability(handshake, result.Claims)
	if ok {
		e.updateHandshakeTrust(trusted, true)
		return
	}
	if e.sessionMiddleware.Mode() == HandshakeModeWarn {
		e.updateHandshakeTrust(handshake, false)
	}
}

func (e *Engine) authorizedCapability(handshake *pb.Handshake, claims *SessionTokenClaims) (*pb.Handshake, bool) {
	if claims == nil || e.workerCredentialCache == nil || !e.refreshActiveCredentialAuthority() {
		return nil, false
	}
	record, ok := e.workerCredentialCache.Lookup(handshake.GetComponentId())
	if !ok || !capabilityBindingMatches(handshake, claims, record) {
		return nil, false
	}
	trusted := proto.Clone(handshake).(*pb.Handshake)
	trusted.ReadyTopics = intersectReadyTopics(handshake, record.AllowedTopics)
	trusted.ProtoReflect().SetUnknown(nil)
	return trusted, true
}

func capabilityBindingMatches(handshake *pb.Handshake, claims *SessionTokenClaims, record *workercredentials.Credential) bool {
	if handshake == nil || claims == nil || record == nil || record.Revoked() {
		return false
	}
	workerID := strings.TrimSpace(handshake.GetComponentId())
	return workerID != "" && workerID == strings.TrimSpace(record.WorkerID) &&
		workerID == strings.TrimSpace(claims.Subject) &&
		strings.TrimSpace(claims.Audience) == WorkerHandshakeAudience &&
		nonemptyMatch(claims.Tenant, record.TenantID) &&
		nonemptyMatch(claims.AgentID, record.AgentID) &&
		nonemptyMatch(claims.ProofKeyID, record.ProofKeyID) &&
		nonemptyMatch(claims.SDKVersion, handshake.GetSdkVersion())
}

func nonemptyMatch(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return left != "" && left == right
}

func intersectReadyTopics(handshake *pb.Handshake, allowed []string) []string {
	authorized := make(map[string]struct{}, len(allowed))
	for _, topic := range sanitizeReadyTopics(allowed) {
		authorized[topic] = struct{}{}
	}
	result := make([]string, 0, len(authorized))
	for _, topic := range readyTopicsFromHandshake(handshake) {
		if _, ok := authorized[topic]; ok {
			result = append(result, topic)
		}
	}
	return result
}

func (e *Engine) updateHandshakeTrust(handshake *pb.Handshake, trusted bool) {
	if registry, ok := e.registry.(handshakeTrustRegistry); ok {
		registry.UpdateHandshakeTrust(handshake, trusted)
		return
	}
	if trusted {
		e.registry.UpdateHandshake(handshake)
	}
}
