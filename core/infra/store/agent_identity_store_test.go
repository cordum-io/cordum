package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func newTestAgentIdentityStore(t *testing.T) *AgentIdentityStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	client := testredis.NewClient(t, srv.Addr())

	return NewAgentIdentityStoreFromClient(client)
}

func TestAgentIdentityCreateAndGet(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	input := AgentIdentity{
		TenantID:            "tenant-a",
		Name:                "test-agent",
		Owner:               "admin@test.com",
		RiskTier:            "high",
		Team:                "platform",
		Description:         "A test agent",
		AllowedTopics:       []string{"job.default"},
		DataClassifications: []string{"pii", "financial"},
	}

	created, err := s.Create(ctx, input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated ID")
	}
	if created.Name != "test-agent" {
		t.Fatalf("expected name test-agent, got %q", created.Name)
	}
	if created.Status != "active" {
		t.Fatalf("expected default status active, got %q", created.Status)
	}
	if created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatal("expected timestamps to be set")
	}
	if len(created.DataClassifications) != 2 {
		t.Fatalf("expected 2 data classifications, got %d", len(created.DataClassifications))
	}

	got, err := s.Get(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil identity")
	}
	if got.Name != created.Name {
		t.Fatalf("got name %q, want %q", got.Name, created.Name)
	}
	if got.Owner != created.Owner {
		t.Fatalf("got owner %q, want %q", got.Owner, created.Owner)
	}
	if got.RiskTier != "high" {
		t.Fatalf("got risk_tier %q, want high", got.RiskTier)
	}
}

func TestAgentIdentityCreateValidation(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		input AgentIdentity
		err   string
	}{
		{
			name:  "missing name",
			input: AgentIdentity{TenantID: "tenant-a", Owner: "admin", RiskTier: "low"},
			err:   "name required",
		},
		{
			name:  "missing owner",
			input: AgentIdentity{TenantID: "tenant-a", Name: "agent", RiskTier: "low"},
			err:   "owner required",
		},
		{
			name:  "missing risk_tier",
			input: AgentIdentity{TenantID: "tenant-a", Name: "agent", Owner: "admin"},
			err:   "risk_tier must be one of",
		},
		{
			name:  "invalid risk_tier",
			input: AgentIdentity{TenantID: "tenant-a", Name: "agent", Owner: "admin", RiskTier: "extreme"},
			err:   "risk_tier must be one of",
		},
		{
			name:  "invalid status",
			input: AgentIdentity{TenantID: "tenant-a", Name: "agent", Owner: "admin", RiskTier: "low", Status: "unknown"},
			err:   "status must be one of",
		},
		{
			name:  "missing tenant_id",
			input: AgentIdentity{Name: "agent", Owner: "admin", RiskTier: "low"},
			err:   "tenant_id required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Create(ctx, tt.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); !contains(got, tt.err) {
				t.Fatalf("expected error containing %q, got %q", tt.err, got)
			}
		})
	}
}

func TestAgentIdentityList(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	for _, name := range []string{"agent-a", "agent-b", "agent-c"} {
		_, err := s.Create(ctx, AgentIdentity{
			TenantID: "tenant-a",
			Name:     name,
			Owner:    "admin",
			RiskTier: "low",
		})
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	if _, err := s.Create(ctx, AgentIdentity{
		TenantID: "tenant-b",
		Name:     "agent-foreign",
		Owner:    "admin",
		RiskTier: "low",
	}); err != nil {
		t.Fatalf("Create foreign tenant agent: %v", err)
	}

	results, _, err := s.List(ctx, "tenant-a", "", 10, AgentIdentityFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Test filter by risk_tier
	_, err = s.Create(ctx, AgentIdentity{
		TenantID: "tenant-a",
		Name:     "agent-critical",
		Owner:    "admin",
		RiskTier: "critical",
	})
	if err != nil {
		t.Fatalf("Create agent-critical: %v", err)
	}

	filtered, _, err := s.List(ctx, "tenant-a", "", 10, AgentIdentityFilter{RiskTier: "critical"})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered result, got %d", len(filtered))
	}
	if filtered[0].Name != "agent-critical" {
		t.Fatalf("expected agent-critical, got %q", filtered[0].Name)
	}
}

func TestAgentIdentityListTenantScoped(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		tenant string
		name   string
		server string
	}{
		{"tenant-a", "agent-a", "a-mcp"},
		{"tenant-b", "agent-b", "b-mcp"},
	} {
		if _, err := s.Create(ctx, AgentIdentity{
			TenantID:       tc.tenant,
			Name:           tc.name,
			Owner:          "admin",
			RiskTier:       "high",
			AllowedServers: []string{tc.server},
			AllowedTools:   []string{tc.tenant + "-tool"},
			Entitlements:   []string{tc.tenant + ".entitlement"},
		}); err != nil {
			t.Fatalf("Create %s: %v", tc.name, err)
		}
	}
	if _, _, err := s.List(ctx, "", "", 10, AgentIdentityFilter{}); err == nil {
		t.Fatal("expected empty-tenant list to fail closed")
	}
	for _, tc := range []struct {
		tenant string
		name   string
		server string
	}{
		{"tenant-a", "agent-a", "a-mcp"},
		{"tenant-b", "agent-b", "b-mcp"},
	} {
		got, _, err := s.List(ctx, tc.tenant, "", 10, AgentIdentityFilter{})
		if err != nil {
			t.Fatalf("List %s: %v", tc.tenant, err)
		}
		if len(got) != 1 || got[0].Name != tc.name {
			t.Fatalf("List %s = %#v, want only %s", tc.tenant, got, tc.name)
		}
		if len(got[0].AllowedServers) != 1 || got[0].AllowedServers[0] != tc.server {
			t.Fatalf("List %s allowed servers leaked/missing: %#v", tc.tenant, got[0].AllowedServers)
		}
	}
}

func TestAgentIdentityUpdate(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, AgentIdentity{
		TenantID: "tenant-a",
		Name:     "original-name",
		Owner:    "admin",
		RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := s.Update(ctx, "", created.ID, AgentIdentity{
		Name:     "updated-name",
		RiskTier: "high",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "updated-name" {
		t.Fatalf("expected updated name, got %q", updated.Name)
	}
	if updated.RiskTier != "high" {
		t.Fatalf("expected risk_tier high, got %q", updated.RiskTier)
	}
	if updated.Owner != "admin" {
		t.Fatalf("expected owner preserved, got %q", updated.Owner)
	}
	if updated.UpdatedAt == "" {
		t.Fatal("expected updated_at to be set")
	}

	// Verify persisted correctly by re-reading
	refetched, err := s.Get(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if refetched.Name != "updated-name" {
		t.Fatalf("expected persisted name updated-name, got %q", refetched.Name)
	}
	if refetched.RiskTier != "high" {
		t.Fatalf("expected persisted risk_tier high, got %q", refetched.RiskTier)
	}

	// Verify invalid update is rejected
	_, err = s.Update(ctx, "", created.ID, AgentIdentity{RiskTier: "extreme"})
	if err == nil {
		t.Fatal("expected error for invalid risk_tier")
	}

	// Verify update of non-existent identity returns error
	_, err = s.Update(ctx, "", "nonexistent", AgentIdentity{Name: "x"})
	if err == nil {
		t.Fatal("expected error for nonexistent identity")
	}
}

func TestAgentIdentityMCPAllowlistsCreateAndUpdate(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, AgentIdentity{
		TenantID:         "tenant-a",
		Name:             "mcp-agent",
		Owner:            "admin",
		RiskTier:         "high",
		AllowedServers:   []string{" prod-mcp ", "prod-mcp", "ci-mcp"},
		AllowedTools:     []string{"repo.*"},
		AllowedResources: []string{"cordum://repos/*", " cordum://docs/* "},
		Entitlements:     []string{"repo.read", "repo.read", "billing.export"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertStoreStrings(t, "AllowedServers", created.AllowedServers, []string{"ci-mcp", "prod-mcp"})
	assertStoreStrings(t, "AllowedResources", created.AllowedResources, []string{"cordum://docs/*", "cordum://repos/*"})
	assertStoreStrings(t, "Entitlements", created.Entitlements, []string{"billing.export", "repo.read"})

	updated, err := s.Update(ctx, "", created.ID, AgentIdentity{
		AllowedServers:   []string{"staging-mcp"},
		AllowedResources: []string{},
		Entitlements:     []string{"repo.write"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertStoreStrings(t, "AllowedServers update", updated.AllowedServers, []string{"staging-mcp"})
	assertStoreStrings(t, "AllowedResources clear", updated.AllowedResources, []string{})
	assertStoreStrings(t, "Entitlements update", updated.Entitlements, []string{"repo.write"})
	assertStoreStrings(t, "AllowedTools preserved", updated.AllowedTools, []string{"repo.*"})
}

func TestAgentIdentityGetNormalizesMissingMCPAllowlists(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	raw := fmt.Sprintf(`{
		"id":"legacy-agent","name":"legacy","owner":"admin","risk_tier":"high",
		"status":"active","created_at":%q,"updated_at":%q
	}`, now, now)
	if err := s.client.Set(ctx, agentIdentityKeyPrefix+"legacy-agent", raw, 0).Err(); err != nil {
		t.Fatalf("seed legacy identity: %v", err)
	}

	got, err := s.Get(ctx, "", "legacy-agent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected legacy identity")
	}
	assertStoreStrings(t, "AllowedServers legacy", got.AllowedServers, []string{})
	assertStoreStrings(t, "AllowedResources legacy", got.AllowedResources, []string{})
	assertStoreStrings(t, "Entitlements legacy", got.Entitlements, []string{})
}

func TestAgentIdentityDelete(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, AgentIdentity{
		TenantID: "tenant-a",
		Name:     "delete-me",
		Owner:    "admin",
		RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Delete(ctx, "", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should still be retrievable with revoked status
	got, err := s.Get(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got == nil {
		t.Fatal("expected soft-deleted identity to be retrievable")
	}
	if got.Status != "revoked" {
		t.Fatalf("expected status revoked, got %q", got.Status)
	}

	// Delete of non-existent should error
	if err := s.Delete(ctx, "", "nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent identity")
	}
}

func TestAgentIdentityWorkerLinkLifecycle(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	a := createTenantAgent(t, s, "tenant-a", "linked-agent")
	assertLinkedAgent(t, s, "tenant-a", "worker-1", "")

	if err := s.LinkWorker(ctx, " "+a.ID+" ", " worker-1 "); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	assertLinkedAgent(t, s, "tenant-a", "worker-1", a.ID)
	assertLinkedAgent(t, s, "tenant-b", "worker-1", "")
	assertRawWorkerLink(t, s, "tenant-a", "worker-1", a.ID, false)

	if err := s.UnlinkWorker(ctx, " tenant-a ", " worker-1 "); err != nil {
		t.Fatalf("UnlinkWorker: %v", err)
	}
	assertLinkedAgent(t, s, "tenant-a", "worker-1", "")
	assertRawWorkerLink(t, s, "tenant-a", "worker-1", "", false)
}

func TestAgentIdentityLinkWorkerIsTenantQualified(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	a := createTenantAgent(t, s, "tenant-a", "agent-a")
	b := createTenantAgent(t, s, "tenant-b", "agent-b")
	for _, agentID := range []string{a.ID, b.ID} {
		if err := s.LinkWorker(ctx, agentID, "p"); err != nil {
			t.Fatalf("LinkWorker(%s): %v", agentID, err)
		}
	}
	assertLinkedAgent(t, s, "tenant-a", "p", a.ID)
	assertLinkedAgent(t, s, "tenant-b", "p", b.ID)

	if err := s.UnlinkWorker(ctx, "tenant-b", "p"); err != nil {
		t.Fatalf("UnlinkWorker: %v", err)
	}
	assertLinkedAgent(t, s, "tenant-b", "p", "")
	assertLinkedAgent(t, s, "tenant-a", "p", a.ID)
}

func TestAgentIdentityLinkWorkerRejectsUnknownOrUntenantedAgent(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	plantUntenantedAgent(t, s, "agent-untenanted")
	for agentID, wantErr := range map[string]string{
		"agent-missing":    "agent identity agent-missing not found",
		"agent-untenanted": "agent identity agent-untenanted has no tenant",
	} {
		if err := s.LinkWorker(ctx, agentID, "p"); err == nil || err.Error() != wantErr {
			t.Fatalf("LinkWorker(%s) err = %v, want %q", agentID, err, wantErr)
		}
	}
	keys, err := s.client.Keys(ctx, agentWorkerLinksKeyPrefix+"*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("worker-link hashes after rejected links = %v (err %v), want none", keys, err)
	}
	assertRawWorkerLink(t, s, "", "p", "", false)

	closed := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	_ = closed.Close()
	if err := NewAgentIdentityStoreFromClient(closed).LinkWorker(ctx, "agent-a", "p"); err == nil ||
		!strings.Contains(err.Error(), "link worker p: get agent identity agent-a") {
		t.Fatalf("LinkWorker on closed client err = %v, want wrapped lookup error", err)
	}
}

func TestAgentIdentityUnlinkWorkerRetiresOnlySameTenantLegacyLink(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	a := createTenantAgent(t, s, "tenant-a", "agent-a")
	plantLegacyLink(t, s, "p", a.ID)
	assertLinkedAgent(t, s, "tenant-a", "p", a.ID)

	// Another tenant's unlink never removes tenant-a's pre-migration link.
	if err := s.UnlinkWorker(ctx, "tenant-b", "p"); err != nil {
		t.Fatalf("UnlinkWorker(tenant-b): %v", err)
	}
	assertLinkedAgent(t, s, "tenant-a", "p", a.ID)
	assertRawWorkerLink(t, s, "tenant-a", "p", "", true)

	// After relink+unlink in tenant-a, the legacy link must not reappear.
	a2 := createTenantAgent(t, s, "tenant-a", "agent-a2")
	if err := s.LinkWorker(ctx, a2.ID, "p"); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	assertLinkedAgent(t, s, "tenant-a", "p", a2.ID)
	if err := s.UnlinkWorker(ctx, "tenant-a", "p"); err != nil {
		t.Fatalf("UnlinkWorker(tenant-a): %v", err)
	}
	assertLinkedAgent(t, s, "tenant-a", "p", "")
	assertRawWorkerLink(t, s, "tenant-a", "p", "", false)

	// A legacy link naming an untenanted agent belongs to no tenant: it stays.
	plantUntenantedAgent(t, s, "agent-untenanted")
	plantLegacyLink(t, s, "q", "agent-untenanted")
	if err := s.UnlinkWorker(ctx, "tenant-a", "q"); err != nil {
		t.Fatalf("UnlinkWorker(q): %v", err)
	}
	assertRawWorkerLink(t, s, "tenant-a", "q", "", true)
}

func TestAgentLegacyLinkDeleteScriptComparesValue(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()
	key := agentByWorkerKeyPrefix + "p"
	plantLegacyLink(t, s, "p", "agent-x")
	for _, tc := range []struct {
		expect      string
		wantDeleted int64
		wantExists  int64
	}{{"agent-y", 0, 1}, {"agent-x", 1, 0}} {
		deleted, err := agentLegacyLinkDeleteScript.Run(ctx, s.client, []string{key}, tc.expect).Int64()
		if err != nil || deleted != tc.wantDeleted {
			t.Fatalf("delete expecting %s = %d (err %v), want %d", tc.expect, deleted, err, tc.wantDeleted)
		}
		if exists, err := s.client.Exists(ctx, key).Result(); err != nil || exists != tc.wantExists {
			t.Fatalf("after delete expecting %s: exists = %d (err %v), want %d", tc.expect, exists, err, tc.wantExists)
		}
	}
}

func TestAgentIdentityUnlinkWorkerValidation(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	var nilStore *AgentIdentityStore
	closed := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	_ = closed.Close()
	cases := []struct {
		name, tenantID, worker, wantErr string
		store                           *AgentIdentityStore
	}{
		{"empty_tenant", " ", "w", "tenant id required", s},
		{"empty_worker", "tenant-a", " ", "worker id required", s},
		{"nil_store", "tenant-a", "w", "redis client not initialized", nilStore},
		{"redis_error", "tenant-a", "w", "unlink worker w in tenant tenant-a", NewAgentIdentityStoreFromClient(closed)},
	}
	for _, tc := range cases {
		if err := tc.store.UnlinkWorker(context.Background(), tc.tenantID, tc.worker); err == nil ||
			!strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: err = %v, want it to contain %q", tc.name, err, tc.wantErr)
		}
	}
}

// assertRawWorkerLink checks the raw keys: the tenant hash field holds
// wantHash ("" = absent) and the legacy key exists only when wantLegacy.
func assertRawWorkerLink(t *testing.T, s *AgentIdentityStore, tenantID, workerID, wantHash string, wantLegacy bool) {
	t.Helper()
	ctx := context.Background()
	got, err := s.client.HGet(ctx, agentWorkerLinksKeyPrefix+tenantID, workerID).Result()
	if err == redis.Nil {
		got, err = "", nil
	}
	if err != nil || got != wantHash {
		t.Fatalf("hash %s[%s] = %q (err %v), want %q", tenantID, workerID, got, err, wantHash)
	}
	exists, err := s.client.Exists(ctx, agentByWorkerKeyPrefix+workerID).Result()
	if err != nil || (exists == 1) != wantLegacy {
		t.Fatalf("legacy key for %s: exists = %d (err %v), want %v", workerID, exists, err, wantLegacy)
	}
}

func plantUntenantedAgent(t *testing.T, s *AgentIdentityStore, agentID string) {
	t.Helper()
	data, err := json.Marshal(AgentIdentity{ID: agentID, Name: "legacy", Status: "active"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.client.Set(context.Background(), agentIdentityKeyPrefix+agentID, data, 0).Err(); err != nil {
		t.Fatalf("plant untenanted agent: %v", err)
	}
}

// Links in these tests are planted with raw Redis writes, never LinkWorker:
// the capstone changes what LinkWorker writes, and a LinkWorker-planted
// "legacy" link would then silently stop exercising the fallback.

func plantTenantLink(t *testing.T, s *AgentIdentityStore, tenantID, workerID, agentID string) {
	t.Helper()
	if err := s.client.HSet(context.Background(), agentWorkerLinksKeyPrefix+tenantID, workerID, agentID).Err(); err != nil {
		t.Fatalf("plant tenant link: %v", err)
	}
}

func plantLegacyLink(t *testing.T, s *AgentIdentityStore, workerID, agentID string) {
	t.Helper()
	if err := s.client.Set(context.Background(), agentByWorkerKeyPrefix+workerID, agentID, 0).Err(); err != nil {
		t.Fatalf("plant legacy link: %v", err)
	}
}

func createTenantAgent(t *testing.T, s *AgentIdentityStore, tenantID, name string) *AgentIdentity {
	t.Helper()
	created, err := s.Create(context.Background(), AgentIdentity{
		TenantID: tenantID,
		Name:     name,
		Owner:    "admin",
		RiskTier: "medium",
	})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return created
}

// assertLinkedAgent asserts GetByWorkerIDForTenant returns exactly wantID,
// or (nil, nil) when wantID is "".
func assertLinkedAgent(t *testing.T, s *AgentIdentityStore, tenantID, workerID, wantID string) {
	t.Helper()
	got, err := s.GetByWorkerIDForTenant(context.Background(), tenantID, workerID)
	if err != nil {
		t.Fatalf("GetByWorkerIDForTenant(%q, %q): %v", tenantID, workerID, err)
	}
	switch {
	case wantID == "" && got != nil:
		t.Fatalf("GetByWorkerIDForTenant(%q, %q) = %q, want nil", tenantID, workerID, got.ID)
	case wantID != "" && got == nil:
		t.Fatalf("GetByWorkerIDForTenant(%q, %q) = nil, want %q", tenantID, workerID, wantID)
	case wantID != "" && got.ID != wantID:
		t.Fatalf("GetByWorkerIDForTenant(%q, %q) = %q, want %q", tenantID, workerID, got.ID, wantID)
	}
}

func TestAgentIdentityGetByWorkerIDForTenant(t *testing.T) {
	t.Run("two_tenants_same_worker", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		a := createTenantAgent(t, s, "tenant-a", "agent-a")
		b := createTenantAgent(t, s, "tenant-b", "agent-b")
		plantTenantLink(t, s, "tenant-a", "w", a.ID)
		plantTenantLink(t, s, "tenant-b", "w", b.ID)
		assertLinkedAgent(t, s, "tenant-a", "w", a.ID)
		assertLinkedAgent(t, s, "tenant-b", "w", b.ID)
		assertLinkedAgent(t, s, " tenant-a ", " w ", a.ID)
	})

	t.Run("legacy_owning_tenant_only", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		a := createTenantAgent(t, s, "tenant-a", "agent-a")
		plantLegacyLink(t, s, "w", a.ID)
		assertLinkedAgent(t, s, "tenant-a", "w", a.ID)
		assertLinkedAgent(t, s, "tenant-b", "w", "")
	})

	t.Run("hash_wins_over_legacy", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		a1 := createTenantAgent(t, s, "tenant-a", "agent-a1")
		a2 := createTenantAgent(t, s, "tenant-a", "agent-a2")
		plantTenantLink(t, s, "tenant-a", "w", a1.ID)
		plantLegacyLink(t, s, "w", a2.ID)
		assertLinkedAgent(t, s, "tenant-a", "w", a1.ID)
	})

	t.Run("foreign_hash_entry_no_fallback", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		c := createTenantAgent(t, s, "tenant-b", "agent-c")
		a := createTenantAgent(t, s, "tenant-a", "agent-a")
		plantTenantLink(t, s, "tenant-a", "w", c.ID)
		plantLegacyLink(t, s, "w", a.ID)
		// The hash entry is authoritative for tenant-a: it names a foreign
		// agent, so the read yields nil instead of falling back to agent-a.
		assertLinkedAgent(t, s, "tenant-a", "w", "")
	})

	t.Run("untenanted_agent", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		data, err := json.Marshal(AgentIdentity{ID: "agent-untenanted", Name: "legacy", Status: "active"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := s.client.Set(context.Background(), agentIdentityKeyPrefix+"agent-untenanted", data, 0).Err(); err != nil {
			t.Fatalf("plant untenanted agent: %v", err)
		}
		plantLegacyLink(t, s, "w", "agent-untenanted")
		assertLinkedAgent(t, s, "tenant-a", "w", "")
	})

	t.Run("unlinked", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		assertLinkedAgent(t, s, "tenant-a", "w", "")
	})

	t.Run("invalid_input", func(t *testing.T) {
		s := newTestAgentIdentityStore(t)
		var nilStore *AgentIdentityStore
		cases := []struct {
			name             string
			store            *AgentIdentityStore
			tenantID, worker string
			wantErr          string
		}{
			{"empty_tenant", s, "", "w", "tenant id required"},
			{"blank_tenant", s, "   ", "w", "tenant id required"},
			{"blank_worker", s, "tenant-a", "  ", "worker id required"},
			{"nil_store", nilStore, "tenant-a", "w", "redis client not initialized"},
		}
		for _, tc := range cases {
			got, err := tc.store.GetByWorkerIDForTenant(context.Background(), tc.tenantID, tc.worker)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%s: err = %v, want it to contain %q", tc.name, err, tc.wantErr)
			}
			if got != nil {
				t.Fatalf("%s: identity = %q, want nil", tc.name, got.ID)
			}
		}
	})

	t.Run("redis_error", func(t *testing.T) {
		c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
		_ = c.Close()
		s := NewAgentIdentityStoreFromClient(c)
		got, err := s.GetByWorkerIDForTenant(context.Background(), "tenant-a", "w")
		if err == nil {
			t.Fatal("expected an error from a closed redis client")
		}
		if got != nil {
			t.Fatalf("identity = %q, want nil", got.ID)
		}
	})
}

// TestAgentIdentityListPaginationSameScore is a regression test for the case where
// multiple identities share the same sorted-set score (e.g., created in the same
// second with old RFC3339 precision). The composite cursor (score:lastID) must
// correctly advance through all items at the same score without skipping any.
func TestAgentIdentityListPaginationSameScore(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	client := testredis.NewClient(t, srv.Addr())

	s := NewAgentIdentityStoreFromClient(client)
	ctx := context.Background()

	// Create 5 identities.
	var ids []string
	for i := 0; i < 5; i++ {
		created, err := s.Create(ctx, AgentIdentity{
			TenantID: "tenant-a",
			Name:     fmt.Sprintf("agent-%d", i),
			Owner:    "admin",
			RiskTier: "low",
		})
		if err != nil {
			t.Fatalf("Create agent-%d: %v", i, err)
		}
		ids = append(ids, created.ID)
	}

	// Force all identities to the exact same score, simulating the old bug where
	// RFC3339 second precision caused score collisions.
	sameScore := float64(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	for _, id := range ids {
		client.ZAdd(ctx, agentIdentityIndexKey, redis.Z{
			Score:  sameScore,
			Member: id,
		})
	}

	// Paginate with limit=2 — should take 3 pages to get all 5.
	var allResults []*AgentIdentity
	cursor := ""
	pages := 0
	for {
		results, nextCursor, err := s.List(ctx, "tenant-a", cursor, 2, AgentIdentityFilter{})
		if err != nil {
			t.Fatalf("List page %d: %v", pages+1, err)
		}
		allResults = append(allResults, results...)
		pages++
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate within 10 pages")
		}
	}

	if len(allResults) != 5 {
		t.Fatalf("expected 5 total results across all pages, got %d", len(allResults))
	}

	// Verify no duplicates.
	seen := make(map[string]bool)
	for _, r := range allResults {
		if seen[r.ID] {
			t.Fatalf("duplicate identity %s in pagination results", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestAgentIdentityGetNotFound(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	got, err := s.Get(ctx, "", "nonexistent-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent identity")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstr(s, substr)
}

func assertStoreStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d (%v), want %d (%v)", name, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q (full=%v)", name, i, got[i], want[i], got)
		}
	}
}

func TestAgentIdentityListPaginationLargeSameScore(t *testing.T) {
	// Regression test: pagination must work correctly when >fetchCount identities
	// share the same sorted-set score (simulates burst creation in the same nanosecond).
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	totalItems := 40
	limit := 10

	// Force all identities to the same score by overriding the sorted set directly.
	for i := range totalItems {
		id := fmt.Sprintf("agent-%03d", i)
		identity := AgentIdentity{
			TenantID: "tenant-a",
			ID:       id,
			Name:     fmt.Sprintf("agent-%d", i),
			Owner:    "admin",
			RiskTier: "low",
			Status:   "active",
		}
		// Create normally first (sets the key + index entry).
		_, err := s.Create(ctx, identity)
		if err != nil {
			t.Fatalf("Create agent-%d: %v", i, err)
		}
		// Force all to the same score to simulate same-nanosecond creation.
		s.client.ZAdd(ctx, agentIdentityIndexKey, redis.Z{
			Score:  1000000000.0, // fixed score
			Member: id,
		})
	}

	// Paginate through all items.
	var allIDs []string
	cursor := ""
	pages := 0
	for {
		results, nextCursor, err := s.List(ctx, "tenant-a", cursor, limit, AgentIdentityFilter{})
		if err != nil {
			t.Fatalf("List page %d: %v", pages+1, err)
		}
		for _, r := range results {
			allIDs = append(allIDs, r.ID)
		}
		pages++
		if nextCursor == "" || len(results) < limit {
			break
		}
		cursor = nextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(allIDs) != totalItems {
		t.Fatalf("expected %d total identities across all pages, got %d (pages=%d)", totalItems, len(allIDs), pages)
	}

	// Verify no duplicates.
	seen := make(map[string]bool)
	for _, id := range allIDs {
		if seen[id] {
			t.Fatalf("duplicate ID in pagination: %s", id)
		}
		seen[id] = true
	}
}

func TestAgentIdentityListPaginationLimitFillsInsideFetchedBatch(t *testing.T) {
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	const (
		totalItems = 75
		pageLimit  = 50
		tenantID   = "tenant-a"
		score      = 1000000000.0
	)
	expected := make(map[string]struct{}, totalItems)
	for i := range totalItems {
		id := fmt.Sprintf("agent-midbatch-%03d", i)
		identity := AgentIdentity{
			TenantID: tenantID,
			ID:       id,
			Name:     fmt.Sprintf("agent midbatch %03d", i),
			Owner:    "admin",
			RiskTier: "low",
			Status:   "active",
		}
		if _, err := s.Create(ctx, identity); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		if err := s.client.ZAdd(ctx, agentIdentityIndexKey, redis.Z{
			Score:  score,
			Member: id,
		}).Err(); err != nil {
			t.Fatalf("force score for %s: %v", id, err)
		}
		expected[id] = struct{}{}
	}

	first, cursor, err := s.List(ctx, tenantID, "", pageLimit, AgentIdentityFilter{})
	if err != nil {
		t.Fatalf("List first page: %v", err)
	}
	if len(first) != pageLimit {
		t.Fatalf("first page length = %d, want %d", len(first), pageLimit)
	}
	if cursor == "" {
		t.Fatal("first page cursor is empty; expected cursor for remaining records")
	}
	second, nextCursor, err := s.List(ctx, tenantID, cursor, pageLimit, AgentIdentityFilter{})
	if err != nil {
		t.Fatalf("List second page: %v", err)
	}
	if len(second) != totalItems-pageLimit {
		t.Fatalf("second page length = %d, want %d", len(second), totalItems-pageLimit)
	}
	if nextCursor != "" {
		t.Fatalf("second page cursor = %q, want empty after exhausting records", nextCursor)
	}
	assertAgentIDPagesCoverExpected(t, [][]*AgentIdentity{first, second}, expected)
}

func assertAgentIDPagesCoverExpected(t *testing.T, pages [][]*AgentIdentity, expected map[string]struct{}) {
	t.Helper()
	seen := make(map[string]struct{}, len(expected))
	for _, page := range pages {
		for _, identity := range page {
			if _, duplicate := seen[identity.ID]; duplicate {
				t.Fatalf("duplicate ID in pagination: %s", identity.ID)
			}
			seen[identity.ID] = struct{}{}
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("paginated ID count = %d, want %d", len(seen), len(expected))
	}
	for id := range expected {
		if _, ok := seen[id]; !ok {
			t.Fatalf("missing identity %s in paginated results", id)
		}
	}
}

func TestAgentIdentityListPaginationMixedScores(t *testing.T) {
	// Regression test: pagination must work when pages cross score boundaries.
	// Repro from QA: scores [1,1,2,2,2,3] with limit=2 must return all 6 items.
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	// Create 6 identities and force specific scores to simulate mixed timestamps.
	type entry struct {
		id    string
		score float64
	}
	entries := []entry{
		{"agent-a1", 1}, {"agent-a2", 1},
		{"agent-b1", 2}, {"agent-b2", 2}, {"agent-b3", 2},
		{"agent-c1", 3},
	}
	for _, e := range entries {
		identity := AgentIdentity{
			TenantID: "tenant-a", ID: e.id, Name: e.id, Owner: "admin", RiskTier: "low", Status: "active",
		}
		if _, err := s.Create(ctx, identity); err != nil {
			t.Fatalf("Create %s: %v", e.id, err)
		}
		// Override score to simulate specific created_at values.
		s.client.ZAdd(ctx, agentIdentityIndexKey, redis.Z{Score: e.score, Member: e.id})
	}

	// Paginate with limit=2.
	limit := 2
	var allIDs []string
	cursor := ""
	pages := 0
	for {
		results, nextCursor, err := s.List(ctx, "tenant-a", cursor, limit, AgentIdentityFilter{})
		if err != nil {
			t.Fatalf("List page %d: %v", pages+1, err)
		}
		for _, r := range results {
			allIDs = append(allIDs, r.ID)
		}
		pages++
		if nextCursor == "" || len(results) < limit {
			break
		}
		cursor = nextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(allIDs) != len(entries) {
		t.Fatalf("expected %d total identities, got %d: %v (pages=%d)", len(entries), len(allIDs), allIDs, pages)
	}

	// Verify no duplicates.
	seen := make(map[string]bool)
	for _, id := range allIDs {
		if seen[id] {
			t.Fatalf("duplicate ID in pagination: %s", id)
		}
		seen[id] = true
	}

	// Verify all entries present.
	for _, e := range entries {
		if !seen[e.id] {
			t.Fatalf("missing identity %s in paginated results", e.id)
		}
	}
}

func TestAgentIdentityListFilteredLateMatch(t *testing.T) {
	// Regression test: filtered pagination must scan past non-matching entries
	// to find matches later in the sorted set. QA repro: 40 identities with
	// only the 40th having risk_tier=critical. List(limit=10, filter=critical)
	// must return that 1 identity, not 0.
	s := newTestAgentIdentityStore(t)
	ctx := context.Background()

	// Create 39 low-risk identities + 1 critical at the end.
	for i := range 40 {
		tier := "low"
		if i == 39 {
			tier = "critical"
		}
		identity := AgentIdentity{
			TenantID: "tenant-a",
			ID:       fmt.Sprintf("agent-%03d", i),
			Name:     fmt.Sprintf("agent-%d", i),
			Owner:    "admin",
			RiskTier: tier,
			Status:   "active",
		}
		if _, err := s.Create(ctx, identity); err != nil {
			t.Fatalf("Create agent-%d: %v", i, err)
		}
	}

	// Filter for critical — should find the 1 matching identity.
	results, nextCursor, err := s.List(ctx, "tenant-a", "", 10, AgentIdentityFilter{RiskTier: "critical"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 critical identity, got %d", len(results))
	}
	if results[0].ID != "agent-039" {
		t.Fatalf("expected agent-039, got %s", results[0].ID)
	}
	// Only 1 result < limit → no next page.
	if nextCursor != "" {
		t.Fatalf("expected empty cursor (only 1 match), got %q", nextCursor)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
