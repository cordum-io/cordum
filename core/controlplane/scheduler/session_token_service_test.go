package scheduler

// RED coverage for task-948d913b: the stateless control-plane SERVICE token.
//
// A service token (typ=cordum-service) is minted by a control-plane producer
// with the Ed25519 key it already holds and verified by a peer scheduler
// WITHOUT a Redis active record (checkActive=false). Non-spoofable rests on two
// jointly-required guards: (1) header.Typ is asserted on BOTH verify paths so a
// worker's own cordum-session token cannot ride the no-active-check path, and a
// service token cannot be presented as a worker token; (2) the subject must be
// one of the reserved control-plane identities.

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
)

func TestMintServiceToken_VerifyServiceRoundTrip(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	token, err := issuer.MintServiceToken(servicetoken.IdentityScheduler)
	if err != nil {
		t.Fatalf("MintServiceToken: %v", err)
	}
	// No Issue() call precedes this, so there is NO active record in Redis.
	// VerifyService must still accept (it skips assertActive by design).
	claims, err := issuer.VerifyService(token)
	if err != nil {
		t.Fatalf("VerifyService: %v", err)
	}
	if claims.Subject != servicetoken.IdentityScheduler {
		t.Fatalf("subject = %q, want %q", claims.Subject, servicetoken.IdentityScheduler)
	}
	if claims.Tenant != servicetoken.ReservedTenant {
		t.Fatalf("tenant = %q, want %q", claims.Tenant, servicetoken.ReservedTenant)
	}
}

func TestVerifyService_RejectsWorkerSessionToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-a", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := issuer.VerifyService(token); err == nil {
		t.Fatal("VerifyService must reject a worker (cordum-session) token presented on the service path")
	}
}

func TestVerify_RejectsServiceToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, err := issuer.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		t.Fatalf("MintServiceToken: %v", err)
	}
	// The worker verify path must reject a cordum-service token (else a
	// service token could ride the worker path).
	if _, err := issuer.Verify(context.Background(), token, true); err == nil {
		t.Fatal("worker Verify must reject a cordum-service token")
	}
}

func TestVerifyService_RejectsNonReservedSubject(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	now := issuer.now().UTC()
	// White-box: forge a service-typ token whose subject is NOT a reserved
	// service identity, using the issuer's own signing key. The legitimate
	// Mint path can never produce this, but VerifyService must still reject
	// it on the reserved-set check (defense-in-depth).
	claims := servicetoken.Claims{
		Subject:    "w-not-a-service",
		Tenant:     servicetoken.ReservedTenant,
		SDKVersion: servicetoken.ServiceSDKVersion,
		JTI:        "jti-test",
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Minute),
	}
	token, err := servicetoken.Sign(issuer.privateKey, issuer.keyID, claims, servicetoken.TypService)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := issuer.VerifyService(token); err == nil {
		t.Fatal("VerifyService must reject a service token whose subject is not a reserved identity")
	}
}
