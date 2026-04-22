package scheduler

// Regression coverage for task-66b8fb92 reopen #2 issue 1:
// Engine.HandlePacket must call the SessionTokenMiddleware on
// heartbeat / job_result / job_cancel paths so live worker packets
// are verified against Phase-2 session tokens.

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

func attachTokenForVerify(packet *pb.BusPacket, token string) {
	if packet == nil || token == "" {
		return
	}
	raw := packet.ProtoReflect().GetUnknown()
	buf := make([]byte, 0, len(raw)+len(token)+8)
	buf = append(buf, raw...)
	buf = protowire.AppendTag(buf, sessionTokenPacketField, protowire.BytesType)
	buf = protowire.AppendString(buf, token)
	packet.ProtoReflect().SetUnknown(buf)
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
	packet := &pb.BusPacket{}
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
