package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestSessionTokenRenewBound_ConcurrentReplayHasOneWinner(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	token, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, renewErr := issuer.RenewBound(ctx, token, binding)
			results <- renewErr
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		renewErr := <-results
		if renewErr == nil {
			successes++
			continue
		}
		if !errors.Is(renewErr, ErrSessionTokenRevoked) && !errors.Is(renewErr, ErrSessionTokenSuperseded) {
			t.Fatalf("concurrent loser error = %v", renewErr)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent renew successes = %d, want exactly 1", successes)
	}
}
