package agentd

import (
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestSafeAllowCacheNilWhenMaxEntriesZero(t *testing.T) {
	if c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 0}); c != nil {
		t.Fatalf("MaxEntries=0 must return nil cache, got %v", c)
	}
	if c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: -10}); c != nil {
		t.Fatalf("negative MaxEntries must return nil cache, got %v", c)
	}
}

func TestSafeAllowCacheNilReceiverIsSafeNoOp(t *testing.T) {
	var c *SafeAllowCache // disabled cache
	if _, ok := c.Get(SafeAllowKey{TenantID: "t"}); ok {
		t.Fatal("nil cache Get returned ok=true")
	}
	c.Put(SafeAllowKey{TenantID: "t"}, safeAllowEntry{Reason: "ok"}) // no panic
	if got := c.InvalidateTenant("t"); got != 0 {
		t.Fatalf("nil cache InvalidateTenant = %d, want 0", got)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("nil cache Len = %d, want 0", got)
	}
}

func TestSafeAllowCacheHitMissAndExactKeyInvalidation(t *testing.T) {
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 8})
	key := SafeAllowKey{
		TenantID:       "tenant-a",
		PolicyMode:     edgecore.PolicyModeEnforce,
		PolicySnapshot: "snap-1",
		ActionHash:     "sha256:action",
		InputHash:      "sha256:input",
	}
	if _, ok := c.Get(key); ok {
		t.Fatal("Get on empty cache returned ok=true")
	}
	c.Put(key, safeAllowEntry{Reason: "safe-test", RuleID: "claude-code.allow-safe-build-test"})
	got, ok := c.Get(key)
	if !ok {
		t.Fatal("Get after Put returned miss")
	}
	if got.Reason != "safe-test" {
		t.Fatalf("Reason = %q, want safe-test", got.Reason)
	}

	// Any single field change must miss.
	for name, mut := range map[string]func(SafeAllowKey) SafeAllowKey{
		"different tenant":       func(k SafeAllowKey) SafeAllowKey { k.TenantID = "tenant-b"; return k },
		"different policy mode":  func(k SafeAllowKey) SafeAllowKey { k.PolicyMode = edgecore.PolicyModeObserve; return k },
		"different snapshot":     func(k SafeAllowKey) SafeAllowKey { k.PolicySnapshot = "snap-2"; return k },
		"different action_hash":  func(k SafeAllowKey) SafeAllowKey { k.ActionHash = "sha256:other"; return k },
		"different input_hash":   func(k SafeAllowKey) SafeAllowKey { k.InputHash = "sha256:other"; return k },
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := c.Get(mut(key)); ok {
				t.Fatalf("%s should miss the cache", name)
			}
		})
	}
}

func TestSafeAllowCacheTTLExpiry(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 4, TTL: 30 * time.Second, Clock: clock})
	key := SafeAllowKey{TenantID: "t", PolicyMode: edgecore.PolicyModeObserve, ActionHash: "h", InputHash: "i"}
	c.Put(key, safeAllowEntry{Reason: "in-window"})

	if _, ok := c.Get(key); !ok {
		t.Fatal("entry just inserted should be present")
	}
	now = now.Add(29 * time.Second)
	if _, ok := c.Get(key); !ok {
		t.Fatal("entry should still be present at 29s")
	}
	now = now.Add(2 * time.Second)
	if _, ok := c.Get(key); ok {
		t.Fatal("entry should have expired at 31s")
	}
}

func TestSafeAllowCacheMaxEntriesEvictsOldest(t *testing.T) {
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 2})
	keyA := SafeAllowKey{TenantID: "t", ActionHash: "a"}
	keyB := SafeAllowKey{TenantID: "t", ActionHash: "b"}
	keyC := SafeAllowKey{TenantID: "t", ActionHash: "c"}
	c.Put(keyA, safeAllowEntry{Reason: "a"})
	c.Put(keyB, safeAllowEntry{Reason: "b"})
	c.Put(keyC, safeAllowEntry{Reason: "c"})

	if _, ok := c.Get(keyA); ok {
		t.Fatal("oldest entry A must have been evicted by C")
	}
	if _, ok := c.Get(keyB); !ok {
		t.Fatal("B should still be present")
	}
	if _, ok := c.Get(keyC); !ok {
		t.Fatal("C should still be present")
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 after capacity-bounded insert", got)
	}
}

func TestSafeAllowCacheInvalidateTenantClearsTenantOnly(t *testing.T) {
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 8})
	c.Put(SafeAllowKey{TenantID: "tenant-a", ActionHash: "a1"}, safeAllowEntry{Reason: "a1"})
	c.Put(SafeAllowKey{TenantID: "tenant-a", ActionHash: "a2"}, safeAllowEntry{Reason: "a2"})
	c.Put(SafeAllowKey{TenantID: "tenant-b", ActionHash: "b1"}, safeAllowEntry{Reason: "b1"})

	removed := c.InvalidateTenant("tenant-a")
	if removed != 2 {
		t.Fatalf("InvalidateTenant(tenant-a) = %d, want 2", removed)
	}
	if _, ok := c.Get(SafeAllowKey{TenantID: "tenant-a", ActionHash: "a1"}); ok {
		t.Fatal("tenant-a a1 should be evicted")
	}
	if _, ok := c.Get(SafeAllowKey{TenantID: "tenant-b", ActionHash: "b1"}); !ok {
		t.Fatal("tenant-b should NOT be touched by tenant-a invalidation")
	}
}

func TestSafeAllowEligibilityVetoesUnsafeOutcomes(t *testing.T) {
	cases := []struct {
		name string
		e    SafeAllowEligibility
		want bool
	}{
		{"allowed + known safe + low risk", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"test", "build"}}, true},
		{"allowed + known safe + destructive", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"destructive"}}, false},
		{"allowed + known safe + secrets", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"secrets"}}, false},
		{"allowed + known safe + network", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"network"}}, false},
		{"allowed + known safe + deploy", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"deploy"}}, false},
		{"allowed + known safe + write", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"write"}}, false},
		{"allowed + known safe + unknown", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"unknown"}}, false},
		{"allowed + known safe + review_required", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"review_required"}}, false},
		{"allowed but classifier said not known-safe", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: false, RiskTags: []string{"test"}}, false},
		{"not allowed (DENY)", SafeAllowEligibility{IsAllowed: false, IsKnownSafe: true, RiskTags: []string{"test"}}, false},
		{"approval-derived ALLOW must not be cached", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"test"}, HasApprovalRef: true}, false},
		{"degraded ALLOW must not be cached", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"test"}, WasDegraded: true}, false},
		{"case-insensitive risk tag rejected", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true, RiskTags: []string{"DESTRUCTIVE"}}, false},
		{"empty risk tags + known safe + allowed", SafeAllowEligibility{IsAllowed: true, IsKnownSafe: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.EligibleForCache(); got != tc.want {
				t.Fatalf("EligibleForCache = %v, want %v (case=%s)", got, tc.want, tc.name)
			}
		})
	}
}

func TestSafeAllowCacheUpdateOverwritesWithoutGrowing(t *testing.T) {
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 2})
	key := SafeAllowKey{TenantID: "t", ActionHash: "h"}
	c.Put(key, safeAllowEntry{Reason: "first"})
	c.Put(key, safeAllowEntry{Reason: "second"})
	if got := c.Len(); got != 1 {
		t.Fatalf("after overwrite Len = %d, want 1", got)
	}
	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected hit after overwrite")
	}
	if got.Reason != "second" {
		t.Fatalf("Reason = %q, want second", got.Reason)
	}
}

func TestSafeAllowCacheConcurrentReadsAndWritesAreRaceFree(t *testing.T) {
	c := NewSafeAllowCache(SafeAllowCacheConfig{MaxEntries: 16})
	keys := make([]SafeAllowKey, 16)
	for i := range keys {
		keys[i] = SafeAllowKey{TenantID: "t", ActionHash: "a", InputHash: time.Now().Add(time.Duration(i) * time.Second).String()}
	}
	done := make(chan struct{}, 32)
	for i := 0; i < 16; i++ {
		i := i
		go func() {
			defer func() { done <- struct{}{} }()
			c.Put(keys[i], safeAllowEntry{Reason: "w"})
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = c.Get(keys[i])
		}()
	}
	for i := 0; i < 32; i++ {
		<-done
	}
}
