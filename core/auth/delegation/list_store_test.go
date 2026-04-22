package delegation

import (
	"context"
	"testing"
	"time"
)

func TestRedisListStoreRoundTripAndFilters(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	store := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()

	views := []DelegationView{
		{
			JTI:            "dlg-active",
			Tenant:         "default",
			Issuer:         "agent-a",
			Subject:        "agent-a",
			Audience:       "agent-b",
			AllowedActions: []string{"read", "write"},
			AllowedTopics:  []string{"job.alpha"},
			Chain:          []ChainLink{{AgentID: "agent-a"}},
			ChainDepth:     1,
			IssuedAt:       now.Add(-5 * time.Minute),
			ExpiresAt:      now.Add(30 * time.Minute),
		},
		{
			JTI:            "dlg-expired",
			Tenant:         "default",
			Issuer:         "agent-a",
			Subject:        "agent-a",
			Audience:       "agent-c",
			AllowedActions: []string{"deploy"},
			AllowedTopics:  []string{"job.beta"},
			Chain:          []ChainLink{{AgentID: "agent-a"}},
			ChainDepth:     1,
			IssuedAt:       now.Add(-2 * time.Hour),
			ExpiresAt:      now.Add(-time.Minute),
		},
		{
			JTI:            "dlg-other-tenant",
			Tenant:         "other",
			Issuer:         "agent-a",
			Subject:        "agent-a",
			Audience:       "agent-d",
			AllowedActions: []string{"read"},
			AllowedTopics:  []string{"job.gamma"},
			Chain:          []ChainLink{{AgentID: "agent-a"}},
			ChainDepth:     1,
			IssuedAt:       now.Add(-10 * time.Minute),
			ExpiresAt:      now.Add(10 * time.Minute),
		},
	}
	for _, view := range views {
		if err := store.RecordIssuedToken(context.Background(), view); err != nil {
			t.Fatalf("RecordIssuedToken(%s) error = %v", view.JTI, err)
		}
	}
	if err := store.MarkRevoked(context.Background(), "default", "dlg-active", "manual", now); err != nil {
		t.Fatalf("MarkRevoked() error = %v", err)
	}

	revokedPage, err := store.ListByAgent(context.Background(), "default", "agent-a", DelegationListFilter{Status: "revoked"}, "", 50)
	if err != nil {
		t.Fatalf("ListByAgent(revoked) error = %v", err)
	}
	if len(revokedPage.Items) != 1 || revokedPage.Items[0].JTI != "dlg-active" || !revokedPage.Items[0].Revoked {
		t.Fatalf("unexpected revoked page: %#v", revokedPage.Items)
	}

	expiredPage, err := store.ListAll(context.Background(), "default", DelegationListFilter{Status: "expired"}, "", 50)
	if err != nil {
		t.Fatalf("ListAll(expired) error = %v", err)
	}
	if len(expiredPage.Items) != 1 || expiredPage.Items[0].JTI != "dlg-expired" {
		t.Fatalf("unexpected expired page: %#v", expiredPage.Items)
	}

	scopePage, err := store.ListAll(context.Background(), "default", DelegationListFilter{Scope: "dep"}, "", 50)
	if err != nil {
		t.Fatalf("ListAll(scope) error = %v", err)
	}
	if len(scopePage.Items) != 1 || scopePage.Items[0].JTI != "dlg-expired" {
		t.Fatalf("unexpected scope page: %#v", scopePage.Items)
	}

	expiringPage, err := store.ListAll(context.Background(), "default", DelegationListFilter{BeforeExpiry: now.Add(5 * time.Minute)}, "", 50)
	if err != nil {
		t.Fatalf("ListAll(before_expiry) error = %v", err)
	}
	if len(expiringPage.Items) != 1 || expiringPage.Items[0].JTI != "dlg-expired" {
		t.Fatalf("unexpected before_expiry page: %#v", expiringPage.Items)
	}
}

func TestRedisListStorePagination(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	store := NewRedisListStoreFromClient(revocations.client)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := store.RecordIssuedToken(context.Background(), DelegationView{
			JTI:            "dlg-page-" + string(rune('a'+i)),
			Tenant:         "default",
			Issuer:         "agent-a",
			Subject:        "agent-a",
			Audience:       "agent-b",
			AllowedActions: []string{"read"},
			AllowedTopics:  []string{"job.alpha"},
			Chain:          []ChainLink{{AgentID: "agent-a"}},
			ChainDepth:     1,
			IssuedAt:       now.Add(time.Duration(i) * time.Minute),
			ExpiresAt:      now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("RecordIssuedToken(page-%d) error = %v", i, err)
		}
	}

	page1, err := store.ListAll(context.Background(), "default", DelegationListFilter{}, "", 1)
	if err != nil {
		t.Fatalf("ListAll(page1) error = %v", err)
	}
	if len(page1.Items) != 1 || page1.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v", page1)
	}
	page2, err := store.ListAll(context.Background(), "default", DelegationListFilter{}, page1.NextCursor, 1)
	if err != nil {
		t.Fatalf("ListAll(page2) error = %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].JTI == page1.Items[0].JTI {
		t.Fatalf("unexpected second page: %#v", page2)
	}
}
