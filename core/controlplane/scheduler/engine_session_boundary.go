package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var (
	ErrProductionResultIdentityMismatch    = errors.New("scheduler: production result identity mismatch")
	ErrProductionResultIdentityUnavailable = errors.New("scheduler: production result identity unavailable")
)

type jobRequestGetter interface {
	GetJobRequest(context.Context, string) (*pb.JobRequest, error)
}

func (e *Engine) verifySessionTokenResult(packet *pb.BusPacket, workerID, packetType string) (TokenVerificationResult, bool) {
	if e == nil || e.sessionMiddleware == nil {
		return TokenVerificationResult{Verdict: TokenVerdictPass}, true
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result := e.sessionMiddleware.Verify(ctx, workerID, packet)
	return result, e.evaluateTokenVerification(packet, workerID, packetType, result)
}

func (e *Engine) evaluateTokenVerification(packet *pb.BusPacket, workerID, packetType string, result TokenVerificationResult) bool {
	switch result.Verdict {
	case TokenVerdictPass, TokenVerdictWarnMissing:
		if !e.verifiedIdentityMatches(packet, workerID, packetType, result.Claims) {
			return false
		}
		if result.Err != nil {
			slog.Warn("session token missing; admitting packet",
				"packet_type", packetType, "worker_id", workerID,
				"mode", e.sessionMiddleware.Mode().String(), "error", result.Err)
		}
		return true
	case TokenVerdictRejectMissing, TokenVerdictRejectInvalid:
		e.logTokenRejection(workerID, packetType, result)
		return false
	default:
		e.logTokenRejection(workerID, packetType, result)
		return false
	}
}

func (e *Engine) verifiedIdentityMatches(packet *pb.BusPacket, workerID, packetType string, claims *SessionTokenClaims) bool {
	if claims == nil && (e.sessionMiddleware == nil || e.sessionMiddleware.Mode() != HandshakeModeWarn) {
		return true
	}
	claimedID := strings.TrimSpace(workerID)
	senderID := strings.TrimSpace(safeSenderID(packet))
	claimSubject := claimedID
	if claims != nil {
		claimSubject = strings.TrimSpace(claims.Subject)
	}
	if claimedID != "" && claimSubject == claimedID && senderID == claimedID {
		return true
	}
	slog.Error("session token identity mismatch; rejecting packet",
		"packet_type", packetType, "claimed_id", claimedID,
		"claim_subject", claimSubject, "sender_id", senderID,
		"mode", e.sessionMiddleware.Mode().String())
	return false
}

func (e *Engine) logTokenRejection(workerID, packetType string, result TokenVerificationResult) {
	fields := []interface{}{
		"packet_type", packetType, "worker_id", workerID,
		"mode", e.sessionMiddleware.Mode().String(), "verdict", result.Verdict.String(),
	}
	if result.Err != nil {
		fields = append(fields, "error", result.Err)
	}
	slog.Error("session token rejected inbound packet", fields...)
}

func (e *Engine) validateProductionJobResultIdentity(
	ctx context.Context,
	packet *pb.BusPacket,
	result *pb.JobResult,
	claims *SessionTokenClaims,
) error {
	if result == nil || claims == nil || claims.Subject != result.GetWorkerId() {
		return ErrProductionResultIdentityMismatch
	}
	return e.validateProductionJobEventIdentity(
		ctx, packet, result.GetJobId(), result.GetIdentity(), claims,
	)
}

func (e *Engine) validateProductionJobEventIdentity(
	ctx context.Context,
	packet *pb.BusPacket,
	jobID string,
	payloadIdentity *pb.IdentityBinding,
	claims *SessionTokenClaims,
) error {
	if packet == nil || !completeProductionIdentity(payloadIdentity) ||
		!sameProductionIdentity(packet.GetIdentity(), payloadIdentity) {
		return ErrProductionResultIdentityMismatch
	}
	if claims != nil && claims.Tenant != "" && claims.Tenant != payloadIdentity.GetTenantId() {
		return ErrProductionResultIdentityMismatch
	}
	jobIdentity, err := e.loadProductionJobIdentity(ctx, jobID)
	if err != nil {
		return err
	}
	if jobIdentity.GetTenantId() != payloadIdentity.GetTenantId() {
		return ErrProductionResultIdentityMismatch
	}
	return nil
}

func (e *Engine) loadProductionJobIdentity(
	ctx context.Context,
	jobID string,
) (*pb.IdentityBinding, error) {
	getter, ok := e.jobStore.(jobRequestGetter)
	if !ok || jobID == "" {
		return nil, ErrProductionResultIdentityUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
	defer cancel()
	req, err := getter.GetJobRequest(lookupCtx, jobID)
	if err != nil || req == nil {
		return nil, fmt.Errorf("%w: job request lookup", ErrProductionResultIdentityUnavailable)
	}
	if req.GetJobId() != jobID {
		return nil, fmt.Errorf("%w: stored job id", ErrProductionResultIdentityMismatch)
	}
	normalized, err := jobidentity.NormalizeProductionJobRequest(req, req.GetIdentity())
	if err != nil {
		return nil, fmt.Errorf("%w: stored job identity", ErrProductionResultIdentityMismatch)
	}
	return normalized.GetIdentity(), nil
}
