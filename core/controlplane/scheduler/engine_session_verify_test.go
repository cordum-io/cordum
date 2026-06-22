package scheduler

// Regression coverage for task-66b8fb92 reopen #2 issue 1:
// Engine.HandlePacket must call the SessionTokenMiddleware on
// heartbeat / job_result / job_cancel paths so live worker packets
// are verified against Phase-2 session tokens.

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// attachTokenForVerify attaches the token via the TYPED BusPacket.auth_token
// field — the real wire format a cap/v2 SDK worker emits. (Previously this
// wrote the legacy unknown-field encoding via SetUnknown, which masked the
// extractor's typed-field blindness; see task-5c18f890.)
func attachTokenForVerify(packet *pb.BusPacket, token string) {
	if packet == nil || token == "" {
		return
	}
	packet.AuthToken = token
}

func TestEngine_VerifySessionToken_OffModePassesEverything(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	e.ctx, e.cancel = context.Background(), func() {}
	// No sessionMiddleware wired = always admit (back-compat for
	// legacy deploys that haven't turned on handshake yet).
	if !e.verifySessionToken(&pb.BusPacket{}, "w1", "heartbeat") {
		t.Fatal("no-middleware path must admit; got reject")
	}
}

func TestEngine_VerifySessionToken_EnforceMissingRejects(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	mw := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	e := &Engine{sessionMiddleware: mw}
	e.ctx, e.cancel = context.Background(), func() {}

	// Packet without a token in enforce mode → reject.
	if e.verifySessionToken(&pb.BusPacket{}, "w-ghost", "heartbeat") {
		t.Fatal("enforce + missing token must reject")
	}
}

func TestEngine_VerifySessionToken_WarnMissingAdmitsWithLog(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tracker := NewHandshakeMissingTracker()
	mw := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, tracker)
	e := &Engine{sessionMiddleware: mw}
	e.ctx, e.cancel = context.Background(), func() {}

	// Warn mode + missing token → admit.
	if !e.verifySessionToken(&pb.BusPacket{}, "w-warn", "heartbeat") {
		t.Fatal("warn mode must admit missing-token packets")
	}
}

func TestEngine_VerifySessionToken_ValidTokenPasses(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "w-ok", "tenant-ok", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	mw := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	e := &Engine{sessionMiddleware: mw}
	e.ctx, e.cancel = context.Background(), func() {}
	packet := &pb.BusPacket{SenderId: "w-ok"}
	attachTokenForVerify(packet, token)
	if !e.verifySessionToken(packet, "w-ok", "job_result") {
		t.Fatal("valid token must pass")
	}
}

func TestEngine_VerifySessionToken_RevokedTokenRejects(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	token, claims, err := issuer.Issue(ctx, "w-rev", "tenant-rev", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	mw := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	e := &Engine{sessionMiddleware: mw}
	e.ctx, e.cancel = context.Background(), func() {}
	packet := &pb.BusPacket{}
	attachTokenForVerify(packet, token)
	if e.verifySessionToken(packet, "w-rev", "job_result") {
		t.Fatal("revoked token must reject regardless of mode")
	}
}
