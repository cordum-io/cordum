package safetykernel

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policyshadow"
)

// ShadowLoader watches the policyshadow.Store and keeps a live
// compiled snapshot of each tenant's shadow bundles so the
// ShadowEvaluator can run dual-evaluation on every incoming request
// without paying the YAML-parse cost per call.
//
// Refresh cadence is configurable (the kernel wires 15s). Every tick:
//  1. Resolve the tenant list via tenantsFn().
//  2. For each tenant, call store.List() → []ShadowPolicy.
//  3. Parse each ShadowPolicy.Content through config.ParseSafetyPolicy.
//  4. Atomically swap the snapshot under a write lock.
//
// Malformed YAML from any single shadow is slog.Warn-logged and that
// shadow is skipped — the rest of the tenant's shadows remain visible.
// This matches the kernel's active-policy fragment loader (see
// loadFragments in kernel.go) so operator mental models line up.
//
// Snapshot() returns shallow copies of the top-level tenant maps so a
// concurrent refresh cannot swap the slice out mid-iteration. The
// *config.SafetyPolicy pointers inside are immutable after compile —
// ParseSafetyPolicy always returns a fresh struct and the loader never
// mutates it — so sharing them across readers is safe without copying.

// TenantsFunc returns the current tenant IDs the loader should poll.
// The kernel supplies this as a closure over its active-policy tenant
// list; the loader has no independent way to enumerate configsvc
// documents in the ShadowScope, so it needs an explicit source.
//
// Returning an empty or nil slice is valid — the loader simply clears
// its snapshot on the next tick. That's the right behaviour for a
// tenant that was deleted.
type TenantsFunc func() []string

// ShadowLoader is safe for concurrent Snapshot/Close calls.
type ShadowLoader struct {
	store     *policyshadow.Store
	refresh   time.Duration
	tenantsFn TenantsFunc

	mu       sync.RWMutex
	compiled map[string]map[string]*config.SafetyPolicy
	meta     map[string]map[string]policyshadow.ShadowPolicy

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// NewShadowLoader constructs a ShadowLoader and starts its refresh
// goroutine. Close() stops the goroutine and blocks until it exits.
//
// A nil store or nil tenantsFn is allowed for dev/degraded deploys:
// the loader then behaves as an empty snapshot source and its run loop
// exits immediately. This keeps the kernel wiring uniform — the kernel
// can call NewShadowLoader unconditionally and let it no-op when the
// shadow feature is disabled.
func NewShadowLoader(store *policyshadow.Store, refresh time.Duration, tenantsFn TenantsFunc) *ShadowLoader {
	if refresh <= 0 {
		refresh = 15 * time.Second
	}
	l := &ShadowLoader{
		store:     store,
		refresh:   refresh,
		tenantsFn: tenantsFn,
		compiled:  map[string]map[string]*config.SafetyPolicy{},
		meta:      map[string]map[string]policyshadow.ShadowPolicy{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	// Warm the snapshot on construction so the first dual-eval sees
	// shadow data without waiting for the initial tick. Errors here are
	// logged but not fatal — the ticker will retry on schedule.
	if store != nil && tenantsFn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := l.refreshOnce(ctx); err != nil {
			slog.Warn("shadow-loader: initial refresh failed", "error", err)
		}
		cancel()
	}
	go l.run()
	return l
}

// Snapshot returns the current compiled shadows and their metadata,
// keyed by tenantID → bundleID. Callers MUST NOT mutate the returned
// maps. The outer map is a shallow copy so iteration is safe across a
// concurrent refresh; the inner maps and policy pointers are the same
// objects the loader holds internally (immutable by convention).
func (l *ShadowLoader) Snapshot() (
	compiled map[string]map[string]*config.SafetyPolicy,
	meta map[string]map[string]policyshadow.ShadowPolicy,
) {
	if l == nil {
		return nil, nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	compiledCopy := make(map[string]map[string]*config.SafetyPolicy, len(l.compiled))
	for k, v := range l.compiled {
		compiledCopy[k] = v
	}
	metaCopy := make(map[string]map[string]policyshadow.ShadowPolicy, len(l.meta))
	for k, v := range l.meta {
		metaCopy[k] = v
	}
	return compiledCopy, metaCopy
}

// Close stops the refresh goroutine and blocks until it exits.
// Safe to call multiple times; only the first call does real work.
func (l *ShadowLoader) Close() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.stop) })
	<-l.done
}

func (l *ShadowLoader) run() {
	defer close(l.done)
	if l.store == nil || l.tenantsFn == nil {
		// No-op loader: block until Close() so the caller can treat
		// the goroutine uniformly regardless of whether the feature is
		// active. Avoiding the ticker here saves timer allocations for
		// disabled deploys.
		<-l.stop
		return
	}
	t := time.NewTicker(l.refresh)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := l.refreshOnce(ctx); err != nil {
				slog.Warn("shadow-loader: refresh failed", "error", err)
			}
			cancel()
		}
	}
}

// refreshOnce performs a single store scan + compile pass and swaps
// the snapshot atomically. Exported via a thin wrapper in tests so
// deterministic assertions don't have to sleep past a ticker period.
func (l *ShadowLoader) refreshOnce(ctx context.Context) error {
	if l.store == nil || l.tenantsFn == nil {
		return nil
	}
	tenants := l.tenantsFn()
	newCompiled := make(map[string]map[string]*config.SafetyPolicy, len(tenants))
	newMeta := make(map[string]map[string]policyshadow.ShadowPolicy, len(tenants))
	var firstErr error
	for _, tenant := range tenants {
		shadows, err := l.store.List(ctx, tenant)
		if err != nil {
			slog.Warn("shadow-loader: list failed for tenant", "tenant", tenant, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(shadows) == 0 {
			continue
		}
		tenantCompiled := make(map[string]*config.SafetyPolicy, len(shadows))
		tenantMeta := make(map[string]policyshadow.ShadowPolicy, len(shadows))
		for _, sp := range shadows {
			compiled, err := config.ParseSafetyPolicy([]byte(sp.Content))
			if err != nil {
				slog.Warn("shadow-loader: skipping malformed shadow",
					"tenant", tenant,
					"bundle_id", sp.BundleID,
					"shadow_bundle_id", sp.ShadowBundleID,
					"error", err,
				)
				continue
			}
			if compiled == nil {
				// Empty content — not an error, but nothing to evaluate.
				continue
			}
			tenantCompiled[sp.BundleID] = compiled
			tenantMeta[sp.BundleID] = sp
		}
		if len(tenantCompiled) > 0 {
			newCompiled[tenant] = tenantCompiled
			newMeta[tenant] = tenantMeta
		}
	}
	l.mu.Lock()
	l.compiled = newCompiled
	l.meta = newMeta
	l.mu.Unlock()
	return firstErr
}
