package scheduler

// SessionTokenMiddleware tests. Real SessionTokenIssuer + miniredis +
// real BusPacket marshalling — no mocks.

import (
	"context"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// attachTokenToPacket mirrors cap/sdk/go/runtime/session_attach.go's
// attachSessionToken (scheduler can't import the SDK side due to
// module layering). Identical wire format; tests use this helper to
// produce packets that the middleware should recognise.
func attachTokenToPacket(packet *pb.BusPacket, token string) {
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

func TestSessionTokenMiddleware_OffModePasses(t *testing.T) {
	t.Parallel()
	m := NewSessionTokenMiddleware(nil, HandshakeModeOff, nil)
	res := m.Verify(context.Background(), "w1", &pb.BusPacket{})
	if res.Verdict != TokenVerdictPass {
		t.Fatalf("verdict=%v want pass", res.Verdict)
	}
}

func TestSessionTokenMiddleware_WarnMissingFirstCallLogs(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tracker := NewHandshakeMissingTracker().WithInterval(time.Hour)
	m := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, tracker)

	res := m.Verify(context.Background(), "w1", &pb.BusPacket{})
	if res.Verdict != TokenVerdictWarnMissing {
		t.Fatalf("verdict=%v want warn_missing", res.Verdict)
	}
	if res.Err == nil {
		t.Fatal("first call must carry a log reason")
	}

	// Second call within the interval — still warn_missing but Err is nil
	// so caller admits silently.
	res2 := m.Verify(context.Background(), "w1", &pb.BusPacket{})
	if res2.Verdict != TokenVerdictWarnMissing {
		t.Fatalf("verdict=%v want warn_missing", res2.Verdict)
	}
	if res2.Err != nil {
		t.Fatalf("second call must not re-log within interval; got err=%v", res2.Err)
	}
}

func TestSessionTokenMiddleware_EnforceMissingRejects(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	m := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	res := m.Verify(context.Background(), "w1", &pb.BusPacket{})
	if res.Verdict != TokenVerdictRejectMissing {
		t.Fatalf("verdict=%v want reject_missing", res.Verdict)
	}
	if res.Err == nil {
		t.Fatal("reject_missing must carry an error")
	}
}

func TestSessionTokenMiddleware_ValidTokenPasses(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	token, claims, err := issuer.Issue(ctx, "w1", "tenant-ok", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	packet := &pb.BusPacket{}
	attachTokenToPacket(packet, token)

	m := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	res := m.Verify(ctx, "w1", packet)
	if res.Verdict != TokenVerdictPass {
		t.Fatalf("verdict=%v want pass; err=%v", res.Verdict, res.Err)
	}
	if res.Claims == nil {
		t.Fatal("pass must carry claims")
	}
	if res.Claims.JTI != claims.JTI {
		t.Fatalf("claims.JTI=%q want %q", res.Claims.JTI, claims.JTI)
	}
}

func TestSessionTokenMiddleware_RevokedTokenRejects(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	token, claims, err := issuer.Issue(ctx, "w1", "tenant-rev", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	packet := &pb.BusPacket{}
	attachTokenToPacket(packet, token)

	m := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	res := m.Verify(ctx, "w1", packet)
	if res.Verdict != TokenVerdictRejectInvalid {
		t.Fatalf("verdict=%v want reject_invalid", res.Verdict)
	}
	if res.Err == nil {
		t.Fatal("reject_invalid must carry an error")
	}
}

func TestSessionTokenMiddleware_TamperedTokenRejects(t *testing.T) {
	// An attacker prepends "X" to a valid token. The signature check
	// fails → RejectInvalid regardless of mode.
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	token, _, err := issuer.Issue(ctx, "w1", "tenant-tamper", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tampered := "X" + token
	packet := &pb.BusPacket{}
	attachTokenToPacket(packet, tampered)

	for _, mode := range []HandshakeMode{HandshakeModeWarn, HandshakeModeEnforce} {
		m := NewSessionTokenMiddleware(issuer, mode, NewHandshakeMissingTracker())
		res := m.Verify(ctx, "w1", packet)
		if res.Verdict != TokenVerdictRejectInvalid {
			t.Fatalf("mode=%s verdict=%v want reject_invalid", mode, res.Verdict)
		}
	}
}

func TestSessionTokenMiddleware_ExpiredTokenRejects(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 10 * time.Minute,
		Now:      clk.Now,
	})
	defer cleanup()

	token, _, err := issuer.Issue(context.Background(), "w1", "tenant-exp", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	clk.Advance(30 * time.Minute)

	packet := &pb.BusPacket{}
	attachTokenToPacket(packet, token)

	m := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	res := m.Verify(context.Background(), "w1", packet)
	if res.Verdict != TokenVerdictRejectInvalid {
		t.Fatalf("verdict=%v want reject_invalid", res.Verdict)
	}
}

func TestSessionTokenMiddleware_RevokeMidSession(t *testing.T) {
	// Verify twice: first call passes, operator revokes between
	// calls, second call rejects. Mirrors the mid-job revocation
	// scenario called out in the plan.
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	token, claims, err := issuer.Issue(ctx, "w1", "tenant-rev", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	packet := &pb.BusPacket{}
	attachTokenToPacket(packet, token)

	m := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	if got := m.Verify(ctx, "w1", packet); got.Verdict != TokenVerdictPass {
		t.Fatalf("first verify=%v want pass", got.Verdict)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := m.Verify(ctx, "w1", packet); got.Verdict != TokenVerdictRejectInvalid {
		t.Fatalf("post-revoke verify=%v want reject_invalid", got.Verdict)
	}
}

func TestTokenVerdict_String(t *testing.T) {
	t.Parallel()
	cases := map[TokenVerdict]string{
		TokenVerdictPass:          "pass",
		TokenVerdictWarnMissing:   "warn_missing",
		TokenVerdictRejectMissing: "reject_missing",
		TokenVerdictRejectInvalid: "reject_invalid",
		TokenVerdict(99):          "unknown",
	}
	for v, want := range cases {
		if got := v.String(); got != want {
			t.Errorf("%d -> %q want %q", v, got, want)
		}
	}
}

func TestExtractSessionToken_HandlesMissingAndPresent(t *testing.T) {
	t.Parallel()
	empty := &pb.BusPacket{}
	if got := extractSessionToken(empty); got != "" {
		t.Errorf("empty packet token=%q want empty", got)
	}
	withToken := &pb.BusPacket{}
	attachTokenToPacket(withToken, "canonical-token")
	if got := extractSessionToken(withToken); got != "canonical-token" {
		t.Errorf("roundtrip token=%q", got)
	}
	if got := extractSessionToken(nil); got != "" {
		t.Errorf("nil packet token=%q want empty", got)
	}
}
