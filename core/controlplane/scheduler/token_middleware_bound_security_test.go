package scheduler

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSessionTokenMiddlewareRejectsLegacyUnboundWorkerSession(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "worker-1", "tenant-1", "node/1")
	if err != nil {
		t.Fatalf("issue legacy token: %v", err)
	}
	packet := &pb.BusPacket{AuthToken: token}
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	result := middleware.Verify(ctx, "worker-1", packet)
	if result.Verdict != TokenVerdictRejectInvalid {
		t.Fatalf("legacy unbound verdict = %s, want reject_invalid", result.Verdict)
	}
}

func TestSessionTokenMiddlewareAcceptsCompleteBoundWorkerSession(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := SessionBinding{
		WorkerID: "worker-1", AgentID: "agent-1", Tenant: "tenant-1",
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-1", SDKVersion: "node/1",
	}
	token, want, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound token: %v", err)
	}
	packet := &pb.BusPacket{AuthToken: token}
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	result := middleware.Verify(ctx, "worker-1", packet)
	if result.Verdict != TokenVerdictPass || result.Claims == nil {
		t.Fatalf("bound verdict = %s claims=%v err=%v", result.Verdict, result.Claims, result.Err)
	}
	if result.Claims.JTI != want.JTI {
		t.Fatalf("verified JTI = %q, want %q", result.Claims.JTI, want.JTI)
	}
}

func TestSessionTokenMiddlewareActiveModeRejectsMissingIssuer(t *testing.T) {
	t.Parallel()
	middleware := NewSessionTokenMiddleware(nil, HandshakeModeWarn, NewHandshakeMissingTracker())
	result := middleware.Verify(context.Background(), "worker-1", &pb.BusPacket{})
	if result.Verdict != TokenVerdictRejectInvalid {
		t.Fatalf("missing issuer verdict = %s, want reject_invalid", result.Verdict)
	}
}

func TestEngineUnknownTokenVerdictFailsClosed(t *testing.T) {
	t.Parallel()
	engine := &Engine{sessionMiddleware: NewSessionTokenMiddleware(
		nil, HandshakeModeWarn, NewHandshakeMissingTracker(),
	)}
	result := TokenVerificationResult{Verdict: TokenVerdict(99)}
	if engine.evaluateTokenVerification(&pb.BusPacket{}, "worker-1", "heartbeat", result) {
		t.Fatal("unknown token verdict was admitted")
	}
}
