package scheduler

// RED coverage for task-948d913b: reserve the control-plane service identities
// at handshake-issue time. Without this guard a worker could legitimately
// handshake AS a control-plane service and obtain a real signed token with
// Subject == that service identity — which would then satisfy subject-binding
// on forged internal broadcasts. This guard + the typ assertion are jointly
// required for the service-token scheme to be non-spoofable.

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

func TestHandshakeService_ReservedServiceIdentityRejected(t *testing.T) {
	t.Parallel()
	for _, id := range []string{servicetoken.IdentityScheduler, servicetoken.IdentityGateway, servicetoken.IdentityWorkflow} {
		id := id
		t.Run(id, func(t *testing.T) {
			svc, _, _, identities, cleanup := newHandshakeFixture(t)
			defer cleanup()
			// Register the reserved name as a fully VALID, active, tenant-matched
			// identity, so that absent the guard this handshake would SUCCEED.
			identities.records[id] = &store.AgentIdentity{ID: id, Name: id, Owner: "tenant-acme", Status: "active"}
			raw := validRequestBytes(t, func(r *capsdk.HandshakeRequest) { r.AgentID = id })
			body, err := svc.HandleHandshake(context.Background(), raw)
			if err != nil {
				t.Fatalf("handle: %v", err)
			}
			resp, err := capsdk.UnmarshalHandshakeResponse(body)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !resp.Rejected {
				t.Fatalf("handshake as reserved identity %q must be rejected", id)
			}
			if resp.Reason != capsdk.HandshakeRejectCapabilityDenied {
				t.Fatalf("reason = %q, want %q", resp.Reason, capsdk.HandshakeRejectCapabilityDenied)
			}
			if resp.SessionToken != "" {
				t.Fatal("a rejected handshake must not return a session token")
			}
		})
	}
}
