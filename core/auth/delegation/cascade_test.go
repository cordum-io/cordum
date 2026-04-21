package delegation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisRevocationStoreCascadeRevoke(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	listStore := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()

	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-root",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-3 * time.Minute),
		ExpiresAt:      now.Add(time.Hour),
	})
	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-child",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-b",
		Audience:       "agent-c",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}, {AgentID: "agent-b", JTI: "dlg-child", ParentJTI: "dlg-root"}},
		ChainDepth:     2,
		IssuedAt:       now.Add(-2 * time.Minute),
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-root",
	})
	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-grandchild",
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        "agent-c",
		Audience:       "agent-d",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.alpha"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}, {AgentID: "agent-b", JTI: "dlg-child", ParentJTI: "dlg-root"}, {AgentID: "agent-c", JTI: "dlg-grandchild", ParentJTI: "dlg-child"}},
		ChainDepth:     3,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-child",
	})

	result, err := revocations.CascadeRevoke(context.Background(), "dlg-root", "compromised", now, true)
	if err != nil {
		t.Fatalf("CascadeRevoke() error = %v", err)
	}
	if result.CascadedCount != 2 || len(result.RevokedJTIs) != 3 {
		t.Fatalf("unexpected cascade result: %#v", result)
	}
	for _, jti := range []string{"dlg-root", "dlg-child", "dlg-grandchild"} {
		revoked, err := revocations.IsRevoked(context.Background(), jti)
		if err != nil {
			t.Fatalf("IsRevoked(%s) error = %v", jti, err)
		}
		if !revoked {
			t.Fatalf("expected %s to be revoked", jti)
		}
		view, ok, err := listStore.Get(context.Background(), jti)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", jti, err)
		}
		if !ok || !view.Revoked || view.RevokedReason != "compromised" {
			t.Fatalf("expected revoked metadata for %s, got %#v", jti, view)
		}
	}
}

func TestRedisRevocationStoreCascadeRevokeWithoutCascade(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	listStore := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()

	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-root",
		Tenant:         "default",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
	})
	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-child",
		Tenant:         "default",
		Subject:        "agent-b",
		Audience:       "agent-c",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}, {AgentID: "agent-b", JTI: "dlg-child", ParentJTI: "dlg-root"}},
		ChainDepth:     2,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-root",
	})

	result, err := revocations.CascadeRevoke(context.Background(), "dlg-root", "manual", now, false)
	if err != nil {
		t.Fatalf("CascadeRevoke(false) error = %v", err)
	}
	if result.CascadedCount != 0 || len(result.RevokedJTIs) != 1 {
		t.Fatalf("unexpected non-cascade result: %#v", result)
	}
	rootRevoked, err := revocations.IsRevoked(context.Background(), "dlg-root")
	if err != nil {
		t.Fatalf("IsRevoked(root) error = %v", err)
	}
	childRevoked, err := revocations.IsRevoked(context.Background(), "dlg-child")
	if err != nil {
		t.Fatalf("IsRevoked(child) error = %v", err)
	}
	if !rootRevoked || childRevoked {
		t.Fatalf("expected only root revoked, got root=%v child=%v", rootRevoked, childRevoked)
	}
}

func TestRedisRevocationStoreCascadeRevokeCycleSafe(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	listStore := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()

	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-a",
		Tenant:         "default",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-a"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-b",
	})
	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-b",
		Tenant:         "default",
		Subject:        "agent-b",
		Audience:       "agent-c",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-b", JTI: "dlg-b"}},
		ChainDepth:     1,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-a",
	})

	result, err := revocations.CascadeRevoke(context.Background(), "dlg-a", "manual", now, true)
	if err != nil {
		t.Fatalf("CascadeRevoke(cycle) error = %v", err)
	}
	if result.CascadedCount != 1 || len(result.RevokedJTIs) != 2 {
		t.Fatalf("unexpected cycle result: %#v", result)
	}
}

func TestRedisRevocationStoreCascadeRevokeEvalErrorIsAtomic(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	listStore := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()

	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-root",
		Tenant:         "default",
		Subject:        "agent-a",
		Audience:       "agent-b",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}},
		ChainDepth:     1,
		IssuedAt:       now.Add(-time.Minute),
		ExpiresAt:      now.Add(time.Hour),
	})
	recordDelegationToken(t, listStore, DelegationView{
		JTI:            "dlg-child",
		Tenant:         "default",
		Subject:        "agent-b",
		Audience:       "agent-c",
		AllowedActions: []string{"read"},
		Chain:          []ChainLink{{AgentID: "agent-a", JTI: "dlg-root"}, {AgentID: "agent-b", JTI: "dlg-child", ParentJTI: "dlg-root"}},
		ChainDepth:     2,
		IssuedAt:       now,
		ExpiresAt:      now.Add(time.Hour),
		ParentJTI:      "dlg-root",
	})

	failing := &RedisRevocationStore{client: evalErrorClient{
		UniversalClient: revocations.client,
		err:             errors.New("boom"),
	}}
	if _, err := failing.CascadeRevoke(context.Background(), "dlg-root", "manual", now, true); err == nil {
		t.Fatal("expected eval error")
	}
	for _, jti := range []string{"dlg-root", "dlg-child"} {
		revoked, err := revocations.IsRevoked(context.Background(), jti)
		if err != nil {
			t.Fatalf("IsRevoked(%s) error = %v", jti, err)
		}
		if revoked {
			t.Fatalf("expected %s to remain unrevoked on eval failure", jti)
		}
		view, ok, err := listStore.Get(context.Background(), jti)
		if err != nil {
			t.Fatalf("Get(%s) error = %v", jti, err)
		}
		if !ok || view.Revoked {
			t.Fatalf("expected %s metadata to remain active, got %#v", jti, view)
		}
	}
}

func recordDelegationToken(t *testing.T, store *RedisListStore, view DelegationView) {
	t.Helper()
	if err := store.RecordIssuedToken(context.Background(), view); err != nil {
		t.Fatalf("RecordIssuedToken(%s) error = %v", view.JTI, err)
	}
}

type evalErrorClient struct {
	redis.UniversalClient
	err error
}

func (c evalErrorClient) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetErr(c.err)
	return cmd
}
