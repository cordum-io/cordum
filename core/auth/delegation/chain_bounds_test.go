package delegation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestDelegationChainBoundsChildExpiry(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	for _, ttl := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		t.Run(ttl.String(), func(t *testing.T) {
			service, _ := attenuationService(t)
			parent := attenuationChain(t, service, 1)[0]
			req := attenuationChild(parent)
			req.TTL = ttl
			token, claims, err := service.IssueDelegationToken(context.Background(), req)
			if err != nil || token == "" {
				t.Fatalf("issue: err=%v, empty=%t", err, token == "")
			}
			child, err := service.VerifyDelegationToken(context.Background(), token, req.TargetAgentID)
			if err != nil {
				t.Fatal(err)
			}
			want := service.now().Add(min(ttl, 10*time.Minute))
			if !child.ExpiresAt.Equal(want) || !claims.ExpiresAt.Time.Equal(want) {
				t.Fatalf("expiry: verified=%v, issued=%v, want=%v", child.ExpiresAt, claims.ExpiresAt, want)
			}
			for _, chain := range [][]ChainLink{claims.DelegationChain, child.DelegationChain} {
				if chain[len(chain)-1].ExpiresAt != want.Format(time.RFC3339Nano) {
					t.Fatal("child chain link expiry differs from JWT expiry")
				}
			}
		})
	}
}

func TestDelegationChainBoundsRootTTL(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	service, _, _ := newTestTokenService(t)
	token, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant: "T", DelegatingAgentID: "agent-a", TargetAgentID: "agent-b", TTL: 20 * time.Minute,
	})
	if err != nil || token == "" {
		t.Fatalf("issue root: err=%v, empty=%t", err, token == "")
	}
	root, err := service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if err != nil || !root.ExpiresAt.Equal(service.now().Add(20*time.Minute)) {
		t.Fatalf("root expiry=%v, err=%v", root.ExpiresAt, err)
	}
}

func TestDelegationChainBoundsParentLeeway(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	for _, elapsed := range []time.Duration{0, time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			service, _ := attenuationService(t)
			parent := attenuationChain(t, service, 1)[0]
			service.now = func() time.Time { return parent.ExpiresAt.Add(elapsed) }
			if _, err := service.VerifyDelegationToken(context.Background(), parent.Token, parent.Audience); err != nil {
				t.Fatalf("parent within leeway: %v", err)
			}
			token, _, err := service.IssueDelegationToken(context.Background(), attenuationChild(parent))
			if !errors.Is(err, ErrExpired) || token != "" {
				t.Fatalf("issue under expired parent: err=%v, empty=%t", err, token == "")
			}
		})
	}
}

func TestDelegationChainBoundsVerifyLinkExpiry(t *testing.T) {
	t.Setenv(envDelegationMaxDepth, "3")
	for _, row := range []struct {
		name    string
		link    int
		expiry  string
		wantErr error
	}{
		{"expired-ancestor", 0, "2024-03-09T15:59:29Z", ErrExpired},
		{"at-leeway-boundary", 0, "2024-03-09T15:59:30Z", ErrExpired},
		{"within-leeway", 0, "2024-03-09T15:59:30.000000001Z", nil},
		{"expired-last-link", 1, "2024-03-09T15:59:29Z", ErrExpired},
		{"malformed", 0, "not-a-time", ErrMalformed},
		{"missing", 1, "", ErrMalformed},
	} {
		t.Run(row.name, func(t *testing.T) {
			service, _ := attenuationService(t)
			chain := attenuationChain(t, service, 2)
			claims := chain[1].Claims
			claims.DelegationChain[row.link].ExpiresAt = row.expiry
			signed := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
			signed.Header["kid"] = service.KeyID()
			token, err := signed.SignedString(service.signingKey.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := service.VerifyDelegationToken(context.Background(), token, chain[1].Audience)
			if !errors.Is(err, row.wantErr) || (row.wantErr != nil && verified.Token != "") {
				t.Fatalf("verify link: err=%v, want=%v, empty=%t", err, row.wantErr, verified.Token == "")
			}
			if row.wantErr == nil && verified.Token != token {
				t.Fatal("within-leeway control was not verified")
			}
		})
	}
}
