package gateway

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Bus tap names, as written in refusal logs.
const (
	busTapHeartbeat = "heartbeat"
	busTapDLQ       = "dlq"
)

// busTapAuth admits a bus tap packet only when it carries the credential the
// identity contract names for that tap (tests/identity/matrix.go).
type busTapAuth struct {
	mw *scheduler.SessionTokenMiddleware
}

// newBusTapAuth fixes the tap mode once: the production profile enforces,
// otherwise CORDUM_SDK_HANDSHAKE applies and an unset mode is off.
func (s *server) newBusTapAuth() *busTapAuth {
	mode := s.handshakeMode
	if s.capProfile.IsProduction() {
		mode = scheduler.HandshakeModeEnforce
	} else if mode == "" {
		mode = scheduler.HandshakeModeOff
	}
	return &busTapAuth{mw: scheduler.NewSessionTokenMiddleware(s.sessionIssuer, mode, scheduler.NewHandshakeMissingTracker())}
}

// admit reports whether the tap may act on p. Off admits every packet and
// warn admits a token-less one; a token that is present must verify and name
// the identity the tap accepts.
func (a *busTapAuth) admit(ctx context.Context, tap string, p *pb.BusPacket) (*scheduler.SessionTokenClaims, bool) {
	res := a.mw.Verify(ctx, p.GetSenderId(), p)
	switch res.Verdict {
	case scheduler.TokenVerdictPass:
		if a.mw.Mode() == scheduler.HandshakeModeOff {
			return nil, true
		}
		if res.Claims != nil && busTapIdentityMatches(tap, p, res.Claims) {
			return res.Claims, true
		}
		a.refuse(tap, "identity")
	case scheduler.TokenVerdictWarnMissing:
		if res.Err != nil {
			slog.Warn("bus tap admitted a packet without a session token", "tap", tap)
		}
		return nil, true
	default:
		a.refuse(tap, res.Verdict.String())
	}
	return nil, false
}

// refuse logs a refused packet with fixed fields only: never the payload, the
// token or the sender.
func (a *busTapAuth) refuse(tap, reason string) {
	slog.Warn("bus tap packet refused", "tap", tap, "reason", reason)
}

// busTapIdentityMatches applies the tap's credential rule to verified claims.
// A heartbeat needs the sender's own bound worker session, the scheduler's
// rule in engine_session_boundary.go verifiedIdentityMatches. A DLQ packet
// needs the service token of a DLQ publisher.
func busTapIdentityMatches(tap string, p *pb.BusPacket, c *scheduler.SessionTokenClaims) bool {
	sub := strings.TrimSpace(c.Subject)
	switch tap {
	case busTapHeartbeat:
		worker := strings.TrimSpace(p.GetHeartbeat().GetWorkerId())
		return worker != "" && sub == worker &&
			strings.TrimSpace(p.GetSenderId()) == worker &&
			strings.TrimSpace(c.Audience) == scheduler.WorkerHandshakeAudience
	case busTapDLQ:
		return sub == servicetoken.IdentityScheduler || sub == servicetoken.IdentityGateway
	}
	return false
}
