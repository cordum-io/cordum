package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockFailModeProvider implements FailModeConfigProvider for tests.
type mockFailModeProvider struct {
	configs        map[string]map[string]any // orgID → merged config
	calls          atomic.Int64
	err            error
	effectiveDelay time.Duration
}

func newMockFailModeProvider() *mockFailModeProvider {
	return &mockFailModeProvider{
		configs: make(map[string]map[string]any),
	}
}

func (m *mockFailModeProvider) Effective(_ context.Context, orgID, _, _, _ string) (map[string]any, error) {
	m.calls.Add(1)
	if m.effectiveDelay > 0 {
		time.Sleep(m.effectiveDelay)
	}
	if m.err != nil {
		return nil, m.err
	}
	if cfg, ok := m.configs[orgID]; ok {
		return cfg, nil
	}
	return map[string]any{}, nil
}

func (m *mockFailModeProvider) setTenantConfig(orgID string, inputMode, asyncMode string) {
	schedulerCfg := map[string]any{}
	if inputMode != "" {
		schedulerCfg["input_fail_mode"] = inputMode
	}
	if asyncMode != "" {
		schedulerCfg["output_fail_mode"] = asyncMode
	}
	m.configs[orgID] = map[string]any{
		"scheduler": schedulerCfg,
	}
}

func TestFailModeResolver_SystemDefault(t *testing.T) {
	provider := newMockFailModeProvider()
	resolver := NewFailModeResolver(provider, 1*time.Hour)

	// Default system mode is empty string (not "open"), so fail-closed.
	assert.False(t, resolver.InputFailOpen(""), "empty orgID should use system default (closed)")
	assert.False(t, resolver.AsyncFailOpen(""), "empty orgID should use system default (closed)")
	assert.False(t, resolver.InputFailOpen("default"), "default orgID should use system default (closed)")
	assert.False(t, resolver.AsyncFailOpen("default"), "default orgID should use system default (closed)")

	// Set system default to open.
	resolver.SetSystemInputMode("open")
	resolver.SetSystemAsyncMode("open")
	assert.True(t, resolver.InputFailOpen(""), "system default set to open")
	assert.True(t, resolver.AsyncFailOpen(""), "system default set to open")
	assert.True(t, resolver.InputFailOpen("default"), "system default set to open")
	assert.True(t, resolver.AsyncFailOpen("default"), "system default set to open")

	// Unrecognized tenant with no cached entry should also use system default.
	// The first call triggers a background refresh, so check the returned value.
	assert.True(t, resolver.InputFailOpen("unknown-org"), "unknown org should fall back to system default")
	assert.True(t, resolver.AsyncFailOpen("unknown-org"), "unknown org should fall back to system default")
}

func TestFailModeResolver_TenantOverride(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.setTenantConfig("org-prod", "closed", "closed")
	provider.setTenantConfig("org-sandbox", "open", "open")

	resolver := NewFailModeResolver(provider, 1*time.Hour)
	// System default is closed.
	resolver.SetSystemInputMode("closed")

	// Pre-populate cache by direct refresh (synchronous).
	ctx := context.Background()
	resolver.RefreshTenant(ctx, "org-prod")
	resolver.RefreshTenant(ctx, "org-sandbox")

	assert.False(t, resolver.InputFailOpen("org-prod"), "org-prod should be fail-closed")
	assert.False(t, resolver.AsyncFailOpen("org-prod"), "org-prod should be fail-closed")
	assert.True(t, resolver.InputFailOpen("org-sandbox"), "org-sandbox should be fail-open")
	assert.True(t, resolver.AsyncFailOpen("org-sandbox"), "org-sandbox should be fail-open")
}

func TestFailModeResolver_TenantInheritsSystemWhenNoOverride(t *testing.T) {
	provider := newMockFailModeProvider()
	// org-basic has no scheduler config — should inherit system default.
	provider.configs["org-basic"] = map[string]any{}

	resolver := NewFailModeResolver(provider, 1*time.Hour)
	resolver.SetSystemInputMode("open")
	resolver.SetSystemAsyncMode("closed")

	resolver.RefreshTenant(context.Background(), "org-basic")

	assert.True(t, resolver.InputFailOpen("org-basic"), "should inherit system input mode (open)")
	assert.False(t, resolver.AsyncFailOpen("org-basic"), "should inherit system async mode (closed)")
}

func TestFailModeResolver_CacheTTL(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.setTenantConfig("org-ttl", "open", "open")

	// Use a very short TTL so entries expire during the test.
	resolver := NewFailModeResolver(provider, 10*time.Millisecond)
	resolver.RefreshTenant(context.Background(), "org-ttl")

	assert.True(t, resolver.InputFailOpen("org-ttl"), "fresh cache should return tenant override")
	initialCalls := provider.calls.Load()

	// Wait for TTL to expire.
	time.Sleep(20 * time.Millisecond)

	// Next access should return the stale value (still open) but trigger refresh.
	assert.True(t, resolver.InputFailOpen("org-ttl"), "stale cache should return last-known value")

	// Give background goroutine time to complete.
	time.Sleep(50 * time.Millisecond)

	// Verify a background refresh was triggered.
	assert.Greater(t, provider.calls.Load(), initialCalls, "background refresh should have been triggered")
}

func TestFailModeResolver_Invalidate(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.setTenantConfig("org-inv", "open", "open")

	resolver := NewFailModeResolver(provider, 1*time.Hour)
	resolver.SetSystemInputMode("closed")
	resolver.RefreshTenant(context.Background(), "org-inv")

	assert.True(t, resolver.InputFailOpen("org-inv"), "cached tenant should be open")

	// Invalidate the tenant.
	resolver.Invalidate("org-inv")

	// After invalidation, the cache miss should return system default (closed)
	// and trigger a background refresh.
	assert.False(t, resolver.InputFailOpen("org-inv"), "after invalidation should fall back to system default (closed)")
}

func TestFailModeResolver_NilProvider(t *testing.T) {
	resolver := NewFailModeResolver(nil, 30*time.Second)
	resolver.SetSystemInputMode("open")

	// With nil provider, tenant lookups should gracefully fall back to system default.
	assert.True(t, resolver.InputFailOpen("any-org"), "nil provider should not panic, should return system default")
	assert.False(t, resolver.AsyncFailOpen("any-org"), "nil provider, async should be system default (not set)")

	// RefreshTenant with nil provider should be a no-op.
	resolver.RefreshTenant(context.Background(), "any-org")
}

func TestFailModeResolver_EmptyOrgID(t *testing.T) {
	provider := newMockFailModeProvider()
	resolver := NewFailModeResolver(provider, 30*time.Second)

	resolver.SetSystemInputMode("open")
	resolver.SetSystemAsyncMode("open")

	assert.True(t, resolver.InputFailOpen(""), "empty orgID should use system default")
	assert.True(t, resolver.AsyncFailOpen(""), "empty orgID should use system default")

	resolver.SetSystemInputMode("closed")
	assert.False(t, resolver.InputFailOpen(""), "empty orgID should track system default changes")
}

func TestFailModeResolver_NilReceiver(t *testing.T) {
	var resolver *FailModeResolver

	// All methods on a nil receiver should be safe no-ops.
	assert.False(t, resolver.InputFailOpen("org"))
	assert.False(t, resolver.AsyncFailOpen("org"))
	resolver.SetSystemInputMode("open")
	resolver.SetSystemAsyncMode("open")
	resolver.Invalidate("org")
	resolver.RefreshTenant(context.Background(), "org")
}

func TestFailModeResolver_ProviderError(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.err = fmt.Errorf("redis connection refused")

	resolver := NewFailModeResolver(provider, 30*time.Second)
	resolver.SetSystemInputMode("open")

	// Synchronous refresh should not panic, just log the error.
	resolver.RefreshTenant(context.Background(), "org-err")

	// No cached entry should exist, so fall back to system default.
	assert.True(t, resolver.InputFailOpen("org-err"), "provider error should fall back to system default")
}

func TestFailModeResolver_ConcurrentRefreshDedup(t *testing.T) {
	provider := newMockFailModeProvider()
	provider.setTenantConfig("org-dedup", "open", "open")
	provider.effectiveDelay = 50 * time.Millisecond

	resolver := NewFailModeResolver(provider, 1*time.Millisecond)

	// Trigger many lookups concurrently — should not spawn unbounded goroutines.
	for i := 0; i < 100; i++ {
		resolver.InputFailOpen("org-dedup")
	}

	// Wait for background refreshes to complete.
	time.Sleep(200 * time.Millisecond)

	// With dedup, we expect far fewer than 100 calls to the provider.
	calls := provider.calls.Load()
	assert.Less(t, calls, int64(10), "dedup should prevent excessive provider calls, got %d", calls)
}

func TestFailModeResolver_SystemModeUpdatesAffectCachedTenants(t *testing.T) {
	provider := newMockFailModeProvider()
	// org-inherit has empty modes — inherits system.
	provider.configs["org-inherit"] = map[string]any{
		"scheduler": map[string]any{},
	}

	resolver := NewFailModeResolver(provider, 1*time.Hour)
	resolver.SetSystemInputMode("closed")
	resolver.RefreshTenant(context.Background(), "org-inherit")

	assert.False(t, resolver.InputFailOpen("org-inherit"), "inherit system closed")

	// Update system default — tenant with empty override should follow.
	resolver.SetSystemInputMode("open")
	assert.True(t, resolver.InputFailOpen("org-inherit"), "inherit system open after update")
}
