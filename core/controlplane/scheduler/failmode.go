package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// FailModeConfigProvider is the interface used by FailModeResolver to fetch
// effective config for a given org. It is satisfied by *configsvc.Service.
type FailModeConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// tenantFailModes caches the resolved fail-mode overrides for a single org.
type tenantFailModes struct {
	inputMode string // "open", "closed", or "" (inherit system default)
	asyncMode string // "open", "closed", or "" (inherit system default)
	fetchedAt time.Time
}

// FailModeResolver resolves per-tenant fail modes with a sync.Map cache.
// The hot path (InputFailOpen / AsyncFailOpen) never blocks on I/O — it
// returns the system default when the cache is stale or missing and kicks
// off a background refresh.
type FailModeResolver struct {
	configProvider FailModeConfigProvider
	cacheTTL       time.Duration

	// System-wide defaults, updated by the reload loop.
	systemInput atomic.Value // string
	systemAsync atomic.Value // string

	// Per-tenant cache: orgID → *tenantFailModes
	tenants sync.Map

	// refreshing tracks in-flight background refreshes to avoid duplicate
	// goroutines for the same org.
	refreshing sync.Map // orgID → struct{}
}

// NewFailModeResolver creates a resolver with the given config provider and
// cache TTL. If ttl is zero, defaults to 30 seconds.
func NewFailModeResolver(provider FailModeConfigProvider, ttl time.Duration) *FailModeResolver {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	r := &FailModeResolver{
		configProvider: provider,
		cacheTTL:       ttl,
	}
	r.systemInput.Store("")
	r.systemAsync.Store("")
	return r
}

// SetSystemInputMode updates the system-wide input fail mode default.
func (r *FailModeResolver) SetSystemInputMode(mode string) {
	if r == nil {
		return
	}
	r.systemInput.Store(mode)
}

// SetSystemAsyncMode updates the system-wide async fail mode default.
func (r *FailModeResolver) SetSystemAsyncMode(mode string) {
	if r == nil {
		return
	}
	r.systemAsync.Store(mode)
}

// InputFailOpen returns true if the given tenant's input fail mode is "open".
// If no per-tenant override exists or the cache is stale, the system default
// is returned immediately and a background refresh is kicked off.
func (r *FailModeResolver) InputFailOpen(orgID string) bool {
	if r == nil {
		return false
	}
	mode := r.resolveInputMode(orgID)
	return mode == "open"
}

// AsyncFailOpen returns true if the given tenant's async output fail mode is
// "open". Same cache/fallback semantics as InputFailOpen.
func (r *FailModeResolver) AsyncFailOpen(orgID string) bool {
	if r == nil {
		return false
	}
	mode := r.resolveAsyncMode(orgID)
	return mode == "open"
}

// Invalidate evicts the cached entry for a tenant, forcing the next lookup to
// fall back to the system default until a background refresh completes.
func (r *FailModeResolver) Invalidate(orgID string) {
	if r == nil {
		return
	}
	r.tenants.Delete(orgID)
}

// RefreshTenant synchronously fetches the effective config for an org and
// updates the cache. Safe to call from background goroutines.
func (r *FailModeResolver) RefreshTenant(ctx context.Context, orgID string) {
	if r == nil || r.configProvider == nil || orgID == "" {
		return
	}
	data, err := r.configProvider.Effective(ctx, orgID, "", "", "")
	if err != nil {
		slog.Warn("failmode: tenant config refresh failed",
			"org_id", orgID, "error", err)
		return
	}
	entry := &tenantFailModes{
		fetchedAt: time.Now(),
	}
	if schedulerCfg, ok := data["scheduler"].(map[string]any); ok {
		if mode, ok := schedulerCfg["input_fail_mode"].(string); ok {
			entry.inputMode = mode
		}
		if mode, ok := schedulerCfg["output_fail_mode"].(string); ok {
			entry.asyncMode = mode
		}
	}
	r.tenants.Store(orgID, entry)
}

// resolveInputMode returns the input fail mode for the given tenant.
func (r *FailModeResolver) resolveInputMode(orgID string) string {
	if orgID == "" || orgID == "default" {
		return r.systemInputMode()
	}
	if entry, ok := r.loadTenantEntry(orgID); ok {
		if entry.inputMode != "" {
			return entry.inputMode
		}
		return r.systemInputMode()
	}
	return r.systemInputMode()
}

// resolveAsyncMode returns the async fail mode for the given tenant.
func (r *FailModeResolver) resolveAsyncMode(orgID string) string {
	if orgID == "" || orgID == "default" {
		return r.systemAsyncMode()
	}
	if entry, ok := r.loadTenantEntry(orgID); ok {
		if entry.asyncMode != "" {
			return entry.asyncMode
		}
		return r.systemAsyncMode()
	}
	return r.systemAsyncMode()
}

// loadTenantEntry returns the cached entry if fresh, or kicks off a background
// refresh and returns (nil, false) if stale/missing.
func (r *FailModeResolver) loadTenantEntry(orgID string) (*tenantFailModes, bool) {
	raw, ok := r.tenants.Load(orgID)
	if !ok {
		r.triggerBackgroundRefresh(orgID)
		return nil, false
	}
	entry, ok := raw.(*tenantFailModes)
	if !ok {
		r.tenants.Delete(orgID)
		r.triggerBackgroundRefresh(orgID)
		return nil, false
	}
	if time.Since(entry.fetchedAt) > r.cacheTTL {
		r.triggerBackgroundRefresh(orgID)
		// Return the stale entry so callers get the last-known value rather
		// than silently falling back to system default during refresh.
		return entry, true
	}
	return entry, true
}

// triggerBackgroundRefresh starts a goroutine to refresh the tenant's config.
// At most one refresh goroutine runs per org at a time.
func (r *FailModeResolver) triggerBackgroundRefresh(orgID string) {
	if r.configProvider == nil {
		return
	}
	if _, loaded := r.refreshing.LoadOrStore(orgID, struct{}{}); loaded {
		return // refresh already in flight
	}
	go func() {
		defer r.refreshing.Delete(orgID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.RefreshTenant(ctx, orgID)
	}()
}

func (r *FailModeResolver) systemInputMode() string {
	if v, ok := r.systemInput.Load().(string); ok {
		return v
	}
	return ""
}

func (r *FailModeResolver) systemAsyncMode() string {
	if v, ok := r.systemAsync.Load().(string); ok {
		return v
	}
	return ""
}
