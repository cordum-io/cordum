package scheduler

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestProductionSessionResolverDerivesWorkerAuthorityFromVerifiedToken(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	binding := boundTestBinding("worker-a", "tenant-a", "go/1")
	token, _, err := issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("IssueBound: %v", err)
	}
	identity := &pb.IdentityBinding{
		TenantId: binding.Tenant, PrincipalId: binding.AgentID, ActorId: binding.WorkerID,
	}
	packet := &pb.BusPacket{SenderId: binding.WorkerID, AuthToken: token, Identity: identity}
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	resolver, err := NewProductionSessionResolver(
		NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker()),
	)
	if err != nil {
		t.Fatalf("NewProductionSessionResolver: %v", err)
	}
	session, err := resolver(context.Background(), "sys.job.result", raw)
	if err != nil {
		t.Fatalf("resolve production session: %v", err)
	}
	if session.Subject != binding.WorkerID || session.Tenant != binding.Tenant ||
		!proto.Equal(session.Identity, identity) || session.Identity == identity {
		t.Fatalf("session = %#v, want verified worker authority and cloned identity", session)
	}
}

func TestProductionSessionResolverPreservesSignedJobIdentityForService(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, err := issuer.MintServiceToken(servicetoken.IdentityScheduler)
	if err != nil {
		t.Fatalf("MintServiceToken: %v", err)
	}
	identity := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "user-a", ActorId: "agent-a"}
	packet := &pb.BusPacket{SenderId: servicetoken.IdentityScheduler, AuthToken: token, Identity: identity}
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	resolver, err := NewProductionSessionResolver(
		NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker()),
	)
	if err != nil {
		t.Fatalf("NewProductionSessionResolver: %v", err)
	}
	session, err := resolver(context.Background(), "sys.internal.job.result.accepted", raw)
	if err != nil {
		t.Fatalf("resolve service session: %v", err)
	}
	if session.Subject != servicetoken.IdentityScheduler || session.Tenant != servicetoken.ReservedTenant ||
		!proto.Equal(session.Identity, identity) {
		t.Fatalf("service session = %#v, want reserved transport plus signed job identity", session)
	}
}

func TestProductionSessionResolverRejectsPayloadTenantMismatch(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	binding := boundTestBinding("worker-a", "tenant-a", "go/1")
	token, _, err := issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("IssueBound: %v", err)
	}
	packet := &pb.BusPacket{
		SenderId: binding.WorkerID, AuthToken: token,
		Identity: &pb.IdentityBinding{TenantId: "tenant-evil", PrincipalId: binding.AgentID, ActorId: binding.WorkerID},
	}
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	resolver, err := NewProductionSessionResolver(
		NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker()),
	)
	if err != nil {
		t.Fatalf("NewProductionSessionResolver: %v", err)
	}
	if _, err := resolver(context.Background(), "sys.job.result", raw); err == nil {
		t.Fatal("resolver accepted token/payload tenant mismatch")
	}
}
