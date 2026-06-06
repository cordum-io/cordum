package scheduler

// Coverage for task-948d913b: scheduler attaches a verifiable control-plane
// service token to its internal broadcasts, and attaches nothing when the gate
// is disabled (back-compat).

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestAttachServiceToken_AttachesVerifiableSchedulerToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	e := &Engine{sessionMiddleware: NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())}
	pkt := &pb.BusPacket{}
	e.attachServiceToken(pkt)
	if pkt.AuthToken == "" {
		t.Fatal("attachServiceToken must set a token under enforce")
	}
	claims, err := issuer.VerifyService(pkt.AuthToken)
	if err != nil {
		t.Fatalf("attached token must verify as a service token: %v", err)
	}
	if claims.Subject != defaultSenderID {
		t.Fatalf("attached token subject = %q, want %q", claims.Subject, defaultSenderID)
	}
}

func TestAttachServiceToken_NoTokenWhenGateDisabled(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	// Off mode: MintServiceToken returns ("",nil) so no token is attached — a
	// peer with the gate disabled admits token-less internal broadcasts anyway.
	eOff := &Engine{sessionMiddleware: NewSessionTokenMiddleware(issuer, HandshakeModeOff, NewHandshakeMissingTracker())}
	offPkt := &pb.BusPacket{}
	eOff.attachServiceToken(offPkt)
	if offPkt.AuthToken != "" {
		t.Fatalf("Off mode must not attach a token; got %q", offPkt.AuthToken)
	}
	// nil middleware: no token, no panic.
	eNil := &Engine{}
	nilPkt := &pb.BusPacket{}
	eNil.attachServiceToken(nilPkt)
	if nilPkt.AuthToken != "" {
		t.Fatal("nil middleware must not attach a token")
	}
}
