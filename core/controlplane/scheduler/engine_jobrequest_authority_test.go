package scheduler

import (
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandlePacketJobRequestAuthorityModes(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	gatewayToken, err := issuer.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, sender, token string
		mode                HandshakeMode
		wantAdmission       bool
	}{
		{name: "enforce rejects tokenless attacker", mode: HandshakeModeEnforce, sender: "attacker", wantAdmission: false},
		{name: "enforce admits gateway service", mode: HandshakeModeEnforce, sender: servicetoken.IdentityGateway, token: gatewayToken, wantAdmission: true},
		{name: "warn logs but admits tokenless", mode: HandshakeModeWarn, sender: "attacker", wantAdmission: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeJobStore()
			engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
			engine.sessionMiddleware = NewSessionTokenMiddleware(issuer, test.mode, NewHandshakeMissingTracker())
			packet := &pb.BusPacket{TraceId: "trace-job-authority", SenderId: test.sender, AuthToken: test.token,
				ProtocolVersion: 1, CreatedAt: timestamppb.Now(),
				Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
					JobId: "job-authority", Topic: "job.default", TenantId: "victim-tenant",
				}}}
			if err := engine.HandlePacket(packet); err != nil {
				t.Fatalf("HandlePacket: %v", err)
			}
			store.mu.RLock()
			_, admitted := store.states["job-authority"]
			store.mu.RUnlock()
			if admitted != test.wantAdmission {
				t.Fatalf("job admission=%v, want %v", admitted, test.wantAdmission)
			}
		})
	}
}
