package scheduler

import (
	"context"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/audit"
)

func (s *HandshakeService) emitHandshakeAudit(ctx context.Context, packet *agentv1.BusPacket, identity *HandshakeTrustIdentity, phase, decision string, failure *handshakeFailure) {
	workerID := workerIDForAudit(packet)
	event := audit.SIEMEvent{
		Timestamp: s.now().UTC(), EventType: EventWorkerHandshake,
		Severity: audit.SeverityInfo, Action: "worker.handshake." + phase, Decision: decision,
		Extra: map[string]string{"phase": phase, "outcome": decision, "worker_id": workerID},
	}
	if identity != nil {
		event.TenantID = identity.TenantID
		event.AgentID = identity.AgentID
		event.AgentName = identity.AgentName
		event.Extra["worker_id"] = identity.WorkerID
	}
	if failure != nil {
		event.Severity = audit.SeverityMedium
		event.Reason = failure.detail
		event.Extra["reason"] = failure.reason.String()
		if failure.reason == internalErrorReason() {
			event.Severity = audit.SeverityHigh
		}
	}
	s.audit.Emit(ctx, event)
}

func workerIDForAudit(packet *agentv1.BusPacket) string {
	if packet == nil {
		return ""
	}
	if request := packet.GetWorkerHandshakeChallengeRequest(); request != nil {
		return request.GetWorkerId()
	}
	if authenticate := packet.GetWorkerHandshakeAuthenticate(); authenticate != nil && authenticate.GetChallenge() != nil {
		return authenticate.GetChallenge().GetWorkerId()
	}
	return packet.GetSenderId()
}
