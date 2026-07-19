package gateway

import (
	"errors"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

type failingServiceTokenMinter struct{}

func (failingServiceTokenMinter) MintServiceToken(string) (string, error) {
	return "", errors.New("signer unavailable")
}

type emptyServiceTokenMinter struct{}

func (emptyServiceTokenMinter) MintServiceToken(string) (string, error) {
	return "", nil
}

func TestPublishConfigChangedActiveModeRequiresServiceTokenIssuer(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.handshakeMode = scheduler.HandshakeModeEnforce

	s.publishConfigChanged("system", "workers")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := len(bus.published); got != 0 {
		t.Fatalf("published packets = %d, want 0 when active mode has no issuer", got)
	}
}

func TestPublishConfigChangedActiveModeFailsClosedOnMintFailure(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.handshakeMode = scheduler.HandshakeModeWarn
	s.serviceTokenMinter = failingServiceTokenMinter{}

	s.publishConfigChanged("system", "workers")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := len(bus.published); got != 0 {
		t.Fatalf("published packets = %d, want 0 after service-token mint failure", got)
	}
}

func TestPublishConfigChangedActiveModeFailsClosedOnEmptyToken(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.handshakeMode = scheduler.HandshakeModeEnforce
	s.serviceTokenMinter = emptyServiceTokenMinter{}

	s.publishConfigChanged("system", "workers")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := len(bus.published); got != 0 {
		t.Fatalf("published packets = %d, want 0 after empty service token", got)
	}
}

func TestPublishConfigChangedActiveModeAttachesValidGatewayToken(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	issuer := gatewayTestIssuer(t, s)
	s.WithGatewayHandshakeSecurity(
		scheduler.HandshakeModeEnforce, scheduler.HeartbeatModeTelemetry, issuer,
	)

	s.publishConfigChanged("system", "workers")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := len(bus.published); got != 1 {
		t.Fatalf("published packets = %d, want 1", got)
	}
	message := bus.published[0]
	if message.subject != capsdk.SubjectConfigChanged {
		t.Fatalf("subject = %q, want %q", message.subject, capsdk.SubjectConfigChanged)
	}
	packet := message.packet
	if err := capsdk.ValidateBusPacket(packet); err != nil {
		t.Fatalf("published envelope invalid: %v", err)
	}
	if packet.GetSenderId() != servicetoken.IdentityGateway {
		t.Fatalf("sender_id = %q, want %q", packet.GetSenderId(), servicetoken.IdentityGateway)
	}
	if packet.GetTraceId() == "" || packet.GetCreatedAt() == nil ||
		packet.GetProtocolVersion() != capsdk.DefaultProtocolVersion {
		t.Fatalf("security envelope incomplete: %+v", packet)
	}
	claims, err := issuer.VerifyService(packet.GetAuthToken())
	if err != nil {
		t.Fatalf("verify attached service token: %v", err)
	}
	if claims.Subject != servicetoken.IdentityGateway {
		t.Fatalf("token subject = %q, want %q", claims.Subject, servicetoken.IdentityGateway)
	}
}

func TestPublishConfigChangedOffModePreservesUnsignedCompatibility(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.handshakeMode = scheduler.HandshakeModeOff

	s.publishConfigChanged("system", "default")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if got := len(bus.published); got != 1 {
		t.Fatalf("published packets = %d, want 1", got)
	}
	if got := bus.published[0].packet.GetAuthToken(); got != "" {
		t.Fatalf("off-mode auth token = %q, want empty", got)
	}
}
