package scheduler

import (
	"context"
	"log/slog"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

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
