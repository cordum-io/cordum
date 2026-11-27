package gateway

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Bus tap names, as written in refusal logs.
const (
	busTapHeartbeat   = "heartbeat"
	busTapDLQ         = "dlq"
	busTapJobStream   = "job_stream"
	busTapAuditStream = "audit_stream"
)

// busTapAuth admits a bus tap packet only when it carries the credential the
// identity contract names for that tap (tests/identity/matrix.go).
type busTapAuth struct {
	mw         *scheduler.SessionTokenMiddleware
	publishers map[string]ed25519.PublicKey
}

// newBusTapAuth fixes the tap mode once: the production profile enforces,
// otherwise CORDUM_SDK_HANDSHAKE applies and an unset mode is off.
func (s *server) newBusTapAuth() (*busTapAuth, error) {
	mode := s.handshakeMode
	if s.capProfile.IsProduction() {
		mode = scheduler.HandshakeModeEnforce
	} else if mode == "" {
		mode = scheduler.HandshakeModeOff
	}
	a := &busTapAuth{mw: scheduler.NewSessionTokenMiddleware(s.sessionIssuer, mode, scheduler.NewHandshakeMissingTracker())}
	if mode == scheduler.HandshakeModeWarn || mode == scheduler.HandshakeModeEnforce {
		var err error
		a.publishers, err = audit.LoadPublisherTrustFromEnv()
		if err != nil {
			return nil, fmt.Errorf("load bus tap publisher trust: %w", err)
		}
	}
	return a, nil
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
		if tap == busTapAuditStream && audit.VerifyPublisherToken(p.GetAuthToken(), a.publishers, time.Now()) {
			return nil, true
		}
		a.refuse(tap, res.Verdict.String())
	}
	return nil, false
}

// refuse logs a refused packet with fixed fields only: never the payload, the
// token or the sender.
func (a *busTapAuth) refuse(tap, reason string) {
	slog.Warn("bus tap packet refused", "tap", tap, "reason", reason)
}

// busTapResultFromDispatchedWorker reports whether the taps may act on p's job
// result. A worker session's result counts only for a job recorded under the
// session's tenant whose dispatch record, when it has one, names that worker
// and tenant. Claims-less packets (off mode, warn without a token, an audit
// publisher token), packets without a result and reserved service identities
// are not checked here.
func (s *server) busTapResultFromDispatchedWorker(ctx context.Context, p *pb.BusPacket, c *scheduler.SessionTokenClaims) bool {
	res := p.GetJobResult()
	if c == nil || res == nil || servicetoken.IsReservedIdentity(c.Subject) {
		return true
	}
	jobID := strings.TrimSpace(res.GetJobId())
	worker, tenant := strings.TrimSpace(c.Subject), strings.TrimSpace(c.Tenant)
	if jobTenant, ok := s.tenantForJobID(ctx, jobID); !ok || jobTenant != tenant {
		return false
	}
	dispatchWorker, dispatchTenant, err := s.jobStore.GetDispatchAssignment(ctx, jobID)
	if err != nil {
		return false
	}
	if dispatchWorker == "" && dispatchTenant == "" {
		return true
	}
	return dispatchWorker == worker && dispatchTenant == tenant
}

// busTapPacketInWorkerTenant reports whether the taps may relay p's job
// request, progress or cancel. A worker session's packet counts only when the
// relay's own resolver gives it the session's tenant: a request's tenant, or
// the recorded tenant of the job a progress or cancel names. Claims-less
// packets, other payloads and reserved service identities are not checked here.
func (s *server) busTapPacketInWorkerTenant(ctx context.Context, p *pb.BusPacket, c *scheduler.SessionTokenClaims) bool {
	if c == nil || servicetoken.IsReservedIdentity(c.Subject) {
		return true
	}
	if p.GetJobRequest() == nil && p.GetJobProgress() == nil && p.GetJobCancel() == nil {
		return true
	}
	tenant, ok := s.tenantForBusPacket(ctx, p)
	return ok && tenant == strings.TrimSpace(c.Tenant)
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
	case busTapJobStream, busTapAuditStream:
		if servicetoken.IsReservedIdentity(sub) {
			return true
		}
		return strings.TrimSpace(c.Audience) == scheduler.WorkerHandshakeAudience &&
			sub == strings.TrimSpace(p.GetSenderId()) &&
			(p.GetJobResult() == nil || sub == strings.TrimSpace(p.GetJobResult().GetWorkerId()))
	}
	return false
}
