package delegation

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
)

type staticAgentPermissions struct {
	entries map[string]AgentPermissions
}

func (s staticAgentPermissions) ResolveAgentPermissions(_ context.Context, agentID string) (AgentPermissions, error) {
	if perms, ok := s.entries[agentID]; ok {
		return perms, nil
	}
	return AgentPermissions{}, nil
}

func newTestTokenService(t *testing.T) (*TokenService, *RedisRevocationStore, *miniredis.Miniredis) {
	t.Helper()

	signingKey, err := GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}

	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)

	client, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("redisutil.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	revocations := NewRedisRevocationStoreFromClient(client)
	service := NewTokenService(signingKey, map[string]ed25519.PublicKey{
		signingKey.KID: signingKey.PublicKey(),
	}, staticAgentPermissions{
		entries: map[string]AgentPermissions{
			"agent-a": {AllowedActions: []string{"read", "write", "deploy"}, AllowedTopics: []string{"job.alpha", "job.beta"}},
			"agent-b": {AllowedActions: []string{"read", "write"}, AllowedTopics: []string{"job.alpha"}},
			"agent-c": {AllowedActions: []string{"read"}, AllowedTopics: []string{"job.alpha"}},
		},
	}, revocations)
	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC() }
	return service, revocations, srv
}

func TestIssueAndVerifyDelegationToken(t *testing.T) {
	service, _, _ := newTestTokenService(t)

	token, claims, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"write", "read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	if claims.ChainDepth != 1 {
		t.Fatalf("claims.ChainDepth = %d, want 1", claims.ChainDepth)
	}

	verified, err := service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}
	if verified.Subject != "agent-a" || verified.Audience != "agent-b" {
		t.Fatalf("verified token = %+v", verified)
	}
	if strings.Join(verified.AllowedActions, ",") != "read,write" {
		t.Fatalf("verified.AllowedActions = %v", verified.AllowedActions)
	}
}

func TestVerifyDelegationTokenErrors(t *testing.T) {
	service, _, _ := newTestTokenService(t)

	baseToken, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	t.Run("audience mismatch", func(t *testing.T) {
		_, err := service.VerifyDelegationToken(context.Background(), baseToken, "agent-c")
		if !errors.Is(err, ErrAudienceMismatch) {
			t.Fatalf("VerifyDelegationToken() error = %v, want ErrAudienceMismatch", err)
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		parts := strings.Split(baseToken, ".")
		if len(parts) != 3 {
			t.Fatalf("unexpected JWT shape: %q", baseToken)
		}
		sig := []byte(parts[2])
		if len(sig) < 4 {
			t.Fatalf("signature segment too short: %q", parts[2])
		}
		idx := len(sig) / 2
		if sig[idx] == 'x' {
			sig[idx] = 'y'
		} else {
			sig[idx] = 'x'
		}
		badToken := strings.Join([]string{parts[0], parts[1], string(sig)}, ".")
		_, err := service.VerifyDelegationToken(context.Background(), badToken, "agent-b")
		if !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrMalformed) {
			t.Fatalf("VerifyDelegationToken() error = %v, want ErrBadSignature/ErrMalformed", err)
		}
	})

	t.Run("unknown kid", func(t *testing.T) {
		otherKey, err := GenerateSigningKey("dlg-9")
		if err != nil {
			t.Fatalf("GenerateSigningKey() error = %v", err)
		}
		otherService := NewTokenService(otherKey, map[string]ed25519.PublicKey{
			otherKey.KID: otherKey.PublicKey(),
		}, service.agentPermissions, nil)
		otherService.now = service.now
		token, _, err := otherService.IssueDelegationToken(context.Background(), IssueRequest{
			Tenant:            "tenant-a",
			DelegatingAgentID: "agent-a",
			TargetAgentID:     "agent-b",
			AllowedActions:    []string{"read"},
			AllowedTopics:     []string{"job.alpha"},
		})
		if err != nil {
			t.Fatalf("IssueDelegationToken() error = %v", err)
		}
		_, err = service.VerifyDelegationToken(context.Background(), token, "agent-b")
		if !errors.Is(err, ErrUnknownKeyId) {
			t.Fatalf("VerifyDelegationToken() error = %v, want ErrUnknownKeyId", err)
		}
	})
}

func TestVerifyDelegationTokenExpiredAndNotYetValid(t *testing.T) {
	service, _, _ := newTestTokenService(t)

	token, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		TTL:               time.Minute,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC().Add(2 * time.Minute) }
	_, err = service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("VerifyDelegationToken() error = %v, want ErrExpired", err)
	}

	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC() }
	futureToken, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC().Add(-time.Minute) }
	_, err = service.VerifyDelegationToken(context.Background(), futureToken, "agent-b")
	if !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("VerifyDelegationToken() error = %v, want ErrNotYetValid", err)
	}
}

func TestIssueDelegationTokenScopeMonotonicityAcrossChain(t *testing.T) {
	service, _, _ := newTestTokenService(t)

	firstToken, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read", "write"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(step1) error = %v", err)
	}
	secondToken, secondClaims, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-b",
		TargetAgentID:     "agent-c",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       firstToken,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(step2) error = %v", err)
	}
	if secondClaims.ChainDepth != 2 {
		t.Fatalf("secondClaims.ChainDepth = %d, want 2", secondClaims.ChainDepth)
	}
	thirdToken, thirdClaims, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-c",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       secondToken,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(step3) error = %v", err)
	}
	if thirdClaims.ChainDepth != 3 {
		t.Fatalf("thirdClaims.ChainDepth = %d, want 3", thirdClaims.ChainDepth)
	}
	_, _, err = service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-b",
		TargetAgentID:     "agent-a",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       thirdToken,
	})
	if !errors.Is(err, ErrChainTooDeep) {
		t.Fatalf("IssueDelegationToken(depth4) error = %v, want ErrChainTooDeep", err)
	}
	_, _, err = service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-b",
		TargetAgentID:     "agent-c",
		AllowedActions:    []string{"deploy"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       firstToken,
	})
	if !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("IssueDelegationToken(scope increase) error = %v, want ErrScopeExceeded", err)
	}
}

func TestVerifyDelegationTokenRevocationAndScopeDowngrade(t *testing.T) {
	service, revocations, _ := newTestTokenService(t)

	token, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	verified, err := service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}
	if err := revocations.Revoke(context.Background(), verified.JTI, verified.ExpiresAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	_, err = service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("VerifyDelegationToken() error = %v, want ErrRevoked", err)
	}

	downgraded := NewTokenService(service.signingKey, service.keyring, staticAgentPermissions{
		entries: map[string]AgentPermissions{
			"agent-a": {AllowedActions: []string{"read"}, AllowedTopics: []string{}},
		},
	}, nil)
	downgraded.now = service.now
	_, err = downgraded.VerifyDelegationToken(context.Background(), token, "agent-b")
	if !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("VerifyDelegationToken() error = %v, want ErrScopeExceeded", err)
	}
}

func TestVerifyDelegationTokenClockSkewLeeway(t *testing.T) {
	service, _, _ := newTestTokenService(t)

	token, _, err := service.IssueDelegationToken(context.Background(), IssueRequest{
		Tenant:            "tenant-a",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		TTL:               time.Minute,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC().Add(time.Minute + 20*time.Second) }
	if _, err := service.VerifyDelegationToken(context.Background(), token, "agent-b"); err != nil {
		t.Fatalf("VerifyDelegationToken() within leeway error = %v", err)
	}
	service.now = func() time.Time { return time.Unix(1_710_000_000, 0).UTC().Add(time.Minute + 31*time.Second) }
	_, err = service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("VerifyDelegationToken() beyond leeway error = %v, want ErrExpired", err)
	}
}
