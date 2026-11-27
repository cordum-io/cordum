package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

func TestReservedSubjectSessionConfigRefresh(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	for _, tc := range []struct {
		identity string
		session  bool
	}{
		{servicetoken.IdentityMCP, true},
		{servicetoken.IdentityGateway, false},
	} {
		t.Run(tc.identity, func(t *testing.T) {
			var token string
			var err error
			if tc.session {
				binding := boundaryBinding()
				binding.WorkerID = tc.identity
				token, _, err = issuer.IssueBound(context.Background(), binding)
			} else {
				token, err = issuer.MintServiceToken(tc.identity)
			}
			if err != nil {
				t.Fatalf("mint token: %v", err)
			}
			called := make(chan struct{}, 1)
			cache := NewWorkerCredentialCache(nil)
			cache.list = func(context.Context) ([]workercredentials.Credential, error) {
				called <- struct{}{}
				return nil, nil
			}
			middleware := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker())
			registry := NewMemoryRegistryWithTTL(time.Minute)
			t.Cleanup(registry.Close)
			engine := boundaryEngine(registry, middleware, cache)
			packet := configChangeSecurityPacket(token)
			packet.SenderId = tc.identity
			if err := engine.handleConfigChangedPacket(packet); err != nil {
				t.Fatalf("handle config change: %v", err)
			}
			assertRefreshCall(t, called, !tc.session)
			if tc.session {
				if _, err := issuer.VerifyBound(context.Background(), token, true); !errors.Is(err, ErrSessionTokenMalformed) {
					t.Fatalf("reserved subject session error = %v", err)
				}
			}
		})
	}
}
