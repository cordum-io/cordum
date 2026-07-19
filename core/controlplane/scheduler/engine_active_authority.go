package scheduler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (e *Engine) activeSessionMode() bool {
	return e != nil && e.sessionMiddleware != nil && e.sessionMiddleware.Mode() != HandshakeModeOff
}

func (e *Engine) requiresTrustedReadiness() bool {
	return e != nil && (e.workerReadinessRequired || e.activeSessionMode())
}

func (e *Engine) filterBoundWorkers(workers map[string]*pb.Heartbeat, readiness map[string]WorkerReadiness, topic string) map[string]*pb.Heartbeat {
	if !e.activeSessionMode() {
		return workers
	}
	result := make(map[string]*pb.Heartbeat, len(workers))
	if !e.refreshActiveCredentialAuthority() {
		return result
	}
	for workerID, heartbeat := range workers {
		if e.boundWorkerAuthorized(workerID, heartbeat, readiness[workerID], topic) {
			result[workerID] = heartbeat
		}
	}
	return result
}

func (e *Engine) refreshActiveCredentialAuthority() bool {
	if e == nil || e.workerCredentialCache == nil {
		return false
	}
	parent := e.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, storeOpTimeout)
	defer cancel()
	return e.workerCredentialCache.RefreshAuthority(ctx)
}

func (e *Engine) boundWorkerAuthorized(workerID string, heartbeat *pb.Heartbeat, readiness WorkerReadiness, topic string) bool {
	if heartbeat == nil || !readiness.Trusted || !workerReadyForTopic(readiness, topic) || e.workerCredentialCache == nil {
		return false
	}
	record, ok := e.workerCredentialCache.Lookup(workerID)
	return ok && explicitlyAllowed(record.AllowedPools, heartbeat.GetPool()) &&
		explicitlyAllowed(record.AllowedTopics, topic)
}

func explicitlyAllowed(allowed []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

func (e *Engine) allowActiveHeartbeatPool(heartbeat *pb.Heartbeat, claims *SessionTokenClaims) bool {
	if !e.activeSessionMode() {
		return true
	}
	if !e.refreshActiveCredentialAuthority() {
		return false
	}
	if claims == nil {
		return e.sessionMiddleware.Mode() == HandshakeModeWarn
	}
	record, ok := e.workerCredentialCache.Lookup(heartbeat.GetWorkerId())
	if !ok || !sessionClaimsMatchCredential(claims, record) {
		return false
	}
	return explicitlyAllowed(record.AllowedPools, heartbeat.GetPool())
}

func sessionClaimsMatchCredential(claims *SessionTokenClaims, record *workercredentials.Credential) bool {
	if claims == nil || record == nil || record.Revoked() {
		return false
	}
	return nonemptyMatch(claims.Subject, record.WorkerID) &&
		strings.TrimSpace(claims.Audience) == WorkerHandshakeAudience &&
		nonemptyMatch(claims.Tenant, record.TenantID) &&
		nonemptyMatch(claims.AgentID, record.AgentID) &&
		nonemptyMatch(claims.ProofKeyID, record.ProofKeyID)
}

func (e *Engine) verifyConfigChangeAuthority(packet *pb.BusPacket) bool {
	return e.verifyReservedServiceAuthority(packet, "config change", false)
}

func (e *Engine) verifyJobSubmissionAuthority(packet *pb.BusPacket) bool {
	return e.verifyReservedServiceAuthority(packet, "job submission", true)
}

func (e *Engine) verifyReservedServiceAuthority(packet *pb.BusPacket, action string, admitWarnMissing bool) bool {
	if !e.activeSessionMode() {
		return true
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result := e.sessionMiddleware.Verify(ctx, packet.GetSenderId(), packet)
	claims := result.Claims
	allowed := result.Verdict == TokenVerdictPass && claims != nil &&
		servicetoken.IsReservedIdentity(claims.Subject) &&
		strings.TrimSpace(claims.Subject) == strings.TrimSpace(packet.GetSenderId())
	if !allowed {
		slog.Error("unauthorized reserved-service action rejected",
			"action", action,
			"sender_id", safeSenderID(packet), "verdict", result.Verdict.String(), "error", result.Err)
	}
	return allowed || (admitWarnMissing && e.sessionMiddleware.Mode() == HandshakeModeWarn && result.Verdict == TokenVerdictWarnMissing)
}
