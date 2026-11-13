package scheduler

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FailModeConfigProvider is the interface used by FailModeResolver to fetch
// effective config for a given org. It is satisfied by *configsvc.Service.
type FailModeConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

type failOpenScope map[string]string

func parseFailOpenScope(raw any) failOpenScope {
	scope := make(failOpenScope)
	entries, _ := raw.([]any)
	for i, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]any)
		rule, _ := entry["rule"].(string)
		rule = strings.TrimSpace(rule)
		if rule == "" {
			slog.Warn("failmode: scope entry skipped", "index", i, "reason", "missing rule name")
			continue
		}
		topics, _ := entry["topics"].([]any)
		if len(topics) == 0 {
			slog.Warn("failmode: scope entry skipped", "index", i, "reason", "topics must be a non-empty list")
			continue
		}
		for _, rawTopic := range topics {
			topic, _ := rawTopic.(string)
			topic = strings.TrimSpace(topic)
			if topic == "" || strings.ContainsAny(topic, "*>") || isApprovalGateTopic(topic) {
				continue
			}
			if _, exists := scope[topic]; !exists {
				scope[topic] = rule
			}
		}
	}
	return scope
}

// systemInputConfig is the system input fail mode and scope. It is published
// as one immutable value, so a reader never pairs one reload's mode with
// another reload's scope.
type systemInputConfig struct {
	mode  string // "open" admits scoped topics; anything else is closed
	scope failOpenScope
}

// SetSystemInput publishes the system input fail mode and exact-topic scope
// in one store. An empty mode means closed, and a nil scope admits no topic.
func (r *FailModeResolver) SetSystemInput(mode string, rawScope any) {
	if r != nil {
		r.systemInputCfg.Store(&systemInputConfig{mode: mode, scope: parseFailOpenScope(rawScope)})
	}
}

// InputFailOpenRule decides fail-open admission for one job from one
// configuration: the tenant's cached entry or the system snapshot, never a mix.
// ok is true only when that mode is "open" and the topic is in that scope.
// mode is "open", "closed", or "unloaded" while a tenant's config has not
// loaded: an unloaded tenant is never admitted, and the lookup starts its
// background refresh.
func (r *FailModeResolver) InputFailOpenRule(orgID, topic string) (rule, mode string, ok bool) {
	if r == nil {
		return "", "closed", false
	}
	sys := r.systemInputSnapshot()
	mode, scope := sys.mode, sys.scope
	if orgID != "" && orgID != "default" {
		entry, loaded := r.loadTenantEntry(orgID)
		switch {
		case loaded && (entry.inputMode != "" || entry.hasInputScope):
			// The entry is the provider's effective config, with the system keys
			// merged in when it was fetched, so it decides alone: a key it lacks
			// keeps its default instead of taking a newer system value.
			mode, scope = entry.inputMode, entry.inputScope
		case !loaded && r.configProvider != nil:
			// The tenant's config may restrict what the system allows.
			return "", "unloaded", false
		}
		// A loaded entry that sets neither key, or no provider: the system
		// snapshot decides alone.
	}
	if mode != "open" {
		return "", "closed", false
	}
	rule, ok = scope[topic]
	return rule, "open", ok
}

// tenantFailModes caches the resolved fail-mode overrides for a single org.
type tenantFailModes struct {
	inputMode     string // "open", "closed", or "" (inherit system default)
	asyncMode     string // "open", "closed", or "" (inherit system default)
	inputScope    failOpenScope
	hasInputScope bool
	fetchedAt     time.Time
}

// FailModeResolver resolves per-tenant fail modes with a sync.Map cache.
// The hot path (InputFailOpen / AsyncFailOpen) never blocks on I/O — it
// returns the system default when the cache is stale or missing and kicks
// off a background refresh.
type FailModeResolver struct {
	configProvider FailModeConfigProvider
	cacheTTL       time.Duration

	// System-wide defaults, updated by the reload loop.
	systemInputCfg atomic.Pointer[systemInputConfig]
	systemAsync    atomic.Value // string

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
	r.systemInputCfg.Store(&systemInputConfig{})
	r.systemAsync.Store("")
	return r
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
// is returned immediately and a background refresh is kicked off. It reports
// the mode only and never decides admission; see InputFailOpenRule.
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
		if raw, ok := schedulerCfg["input_fail_open_scope"]; ok {
			entry.inputScope = parseFailOpenScope(raw)
			entry.hasInputScope = true
		}
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
	return r.systemInputSnapshot().mode
}

// systemInputSnapshot returns the published system input config; a resolver
// that was never initialised reads as closed with no scope.
func (r *FailModeResolver) systemInputSnapshot() systemInputConfig {
	if c := r.systemInputCfg.Load(); c != nil {
		return *c
	}
	return systemInputConfig{}
}

func (r *FailModeResolver) systemAsyncMode() string {
	if v, ok := r.systemAsync.Load().(string); ok {
		return v
	}
	return ""
}
