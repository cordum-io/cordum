package policyshadow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
)

// ShadowScope is the configsvc Scope value used to partition shadow
// policies from every other config document. Storing shadows in a
// dedicated scope is what satisfies the epic rail "Shadow evaluation
// must NEVER affect actual job decisions" at the persistence layer —
// a bug in the active bundle write path cannot reach this scope.
const ShadowScope configsvc.Scope = "policy_shadow"

// maxPutAttempts is the retry budget for ETag-guarded Put/Delete calls.
// Concurrent writes are rare (admin operations), but the ConcurrentPutSameBundle
// torture test fires 8 racers at the same tenant+bundle — at 3 attempts the
// last loser can exhaust its budget before converging. 10 comfortably absorbs
// that worst case while still surfacing a real systemic contention issue
// (it would take 10 concurrent admins writing to the exact same bundle to
// saturate, which indicates a deeper operational problem worth the error).
const maxPutAttempts = 10

// Store persists shadow policies on top of configsvc. One configsvc
// document holds all shadows for a tenant (Data keyed by bundle ID)
// so the one-shadow-per-bundle constraint is enforced by map keying.
type Store struct {
	cfg *configsvc.Service

	// clock is overridden in tests so created_at/activated_at are
	// deterministic. Defaults to time.Now().UTC().
	clock func() time.Time
}

// StoreOption configures a Store at construction.
type StoreOption func(*Store)

// WithClock overrides the wall clock used to stamp CreatedAt/ActivatedAt.
func WithClock(clock func() time.Time) StoreOption {
	return func(s *Store) { s.clock = clock }
}

// NewStore constructs a Store. cfg must be non-nil at call time —
// passing nil panics because the store is unusable.
func NewStore(cfg *configsvc.Service, opts ...StoreOption) *Store {
	if cfg == nil {
		panic("policyshadow: configsvc is nil")
	}
	s := &Store{
		cfg:   cfg,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Get returns the shadow policy for (tenantID, bundleID) or nil when
// none is configured. Missing tenant document is not an error.
func (s *Store) Get(ctx context.Context, tenantID, bundleID string) (*ShadowPolicy, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("tenant id required")
	}
	if strings.TrimSpace(bundleID) == "" {
		return nil, fmt.Errorf("bundle id required")
	}
	shadows, err := s.loadTenantShadows(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	sp, ok := shadows[bundleID]
	if !ok {
		return nil, nil
	}
	// Defensive copy — callers must not be able to mutate the store's
	// cached map.
	out := sp
	return &out, nil
}

// List returns every shadow policy for the tenant, ordered by ActivatedAt ascending.
func (s *Store) List(ctx context.Context, tenantID string) ([]ShadowPolicy, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("tenant id required")
	}
	shadows, err := s.loadTenantShadows(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]ShadowPolicy, 0, len(shadows))
	for _, sp := range shadows {
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ActivatedAt.Equal(out[j].ActivatedAt) {
			return out[i].BundleID < out[j].BundleID
		}
		return out[i].ActivatedAt.Before(out[j].ActivatedAt)
	})
	return out, nil
}

// Put stores sp under (TenantID, BundleID). Replaces any existing
// shadow for the same bundle (one-shadow-per-bundle constraint). If a
// concurrent writer modifies the tenant's document between the read
// and the commit we retry up to maxPutAttempts times with jitter.
//
// On successful write the returned *ShadowPolicy carries the final
// stored state — importantly CreatedAt (first-seen timestamp) and
// ActivatedAt (current activation) are stamped by the store, not the
// caller, so audit events and API responses agree.
func (s *Store) Put(ctx context.Context, sp ShadowPolicy) (*ShadowPolicy, error) {
	if strings.TrimSpace(sp.TenantID) == "" {
		return nil, fmt.Errorf("shadow policy: tenant id required")
	}
	if strings.TrimSpace(sp.BundleID) == "" {
		return nil, fmt.Errorf("shadow policy: bundle id required")
	}
	if strings.TrimSpace(sp.ShadowBundleID) == "" {
		sp.ShadowBundleID = NewShadowBundleID()
	}
	if strings.TrimSpace(sp.Content) == "" {
		return nil, fmt.Errorf("shadow policy: content required")
	}

	var lastErr error
	for attempt := 0; attempt < maxPutAttempts; attempt++ {
		if attempt > 0 {
			if err := s.sleepWithJitter(ctx); err != nil {
				return nil, err
			}
		}
		err := s.cfg.SetWithRetry(ctx, ShadowScope, sp.TenantID, 1, func(doc *configsvc.Document) error {
			if doc.Data == nil {
				doc.Data = map[string]any{}
			}
			now := s.clock()
			// Preserve CreatedAt across replaces — a new ShadowBundleID
			// still points at "the shadow for this bundle", so its
			// first-seen timestamp is the inception of the slot, not the
			// last activation. ActivatedAt always reflects the most
			// recent Put.
			createdAt := now
			if existing, ok := decodeShadow(doc.Data[sp.BundleID]); ok && existing != nil {
				createdAt = existing.CreatedAt
			}
			final := sp
			final.CreatedAt = createdAt
			final.ActivatedAt = now
			doc.Data[sp.BundleID] = encodeShadow(final)
			sp = final
			return nil
		})
		if err == nil {
			out := sp
			return &out, nil
		}
		if !errors.Is(err, configsvc.ErrRevisionConflict) {
			return nil, fmt.Errorf("shadow policy put: %w", err)
		}
		lastErr = err
	}
	return nil, fmt.Errorf("shadow policy put: exhausted retries: %w", lastErr)
}

// Delete removes the shadow for (tenantID, bundleID). Deleting an
// absent shadow is not an error — repeated DELETE calls are
// idempotent which matches operator expectations (e.g. reconciliation
// scripts). Returns (true, nil) when an entry was removed and
// (false, nil) when nothing matched.
func (s *Store) Delete(ctx context.Context, tenantID, bundleID string) (bool, error) {
	if strings.TrimSpace(tenantID) == "" {
		return false, fmt.Errorf("tenant id required")
	}
	if strings.TrimSpace(bundleID) == "" {
		return false, fmt.Errorf("bundle id required")
	}

	var removed bool
	var lastErr error
	for attempt := 0; attempt < maxPutAttempts; attempt++ {
		if attempt > 0 {
			if err := s.sleepWithJitter(ctx); err != nil {
				return false, err
			}
		}
		removed = false
		err := s.cfg.SetWithRetry(ctx, ShadowScope, tenantID, 1, func(doc *configsvc.Document) error {
			if doc.Data == nil {
				return nil
			}
			if _, ok := doc.Data[bundleID]; ok {
				delete(doc.Data, bundleID)
				removed = true
			}
			return nil
		})
		if err == nil {
			return removed, nil
		}
		if !errors.Is(err, configsvc.ErrRevisionConflict) {
			return false, fmt.Errorf("shadow policy delete: %w", err)
		}
		lastErr = err
	}
	return false, fmt.Errorf("shadow policy delete: exhausted retries: %w", lastErr)
}

// loadTenantShadows reads the tenant's configsvc document and
// materialises it into a map keyed by bundleID. An absent document
// is returned as an empty map (not an error) so Get/List behave
// sensibly on a fresh tenant.
func (s *Store) loadTenantShadows(ctx context.Context, tenantID string) (map[string]ShadowPolicy, error) {
	doc, err := s.cfg.Get(ctx, ShadowScope, tenantID)
	if err != nil {
		if isConfigMissingErr(err) {
			return map[string]ShadowPolicy{}, nil
		}
		return nil, fmt.Errorf("shadow policy load: %w", err)
	}
	if doc == nil || doc.Data == nil {
		return map[string]ShadowPolicy{}, nil
	}
	out := make(map[string]ShadowPolicy, len(doc.Data))
	for bundleID, raw := range doc.Data {
		sp, ok := decodeShadow(raw)
		if !ok || sp == nil {
			// Defensive: skip unparseable entries rather than failing
			// the whole list. An operator repair tool can clean them
			// up separately; we don't want a single bad row to brick
			// the feature for every other bundle.
			continue
		}
		// Stamp the canonical keys on the loaded copy so callers can
		// trust TenantID/BundleID match the lookup path even if the
		// persisted document drifted.
		sp.TenantID = tenantID
		sp.BundleID = bundleID
		out[bundleID] = *sp
	}
	return out, nil
}

// encodeShadow normalises a ShadowPolicy into the map[string]any form
// configsvc stores. Using JSON as the intermediate lets us round-trip
// through the Document.Data map cleanly — configsvc marshals its Data
// via encoding/json so there's no conversion cost mismatch.
func encodeShadow(sp ShadowPolicy) map[string]any {
	raw, err := json.Marshal(&sp)
	if err != nil {
		// Marshal of a struct-of-strings-and-times cannot fail outside
		// of pathological reflection edge cases; panic preserves the
		// "Put never silently drops a valid input" contract.
		panic("policyshadow: encode: " + err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic("policyshadow: encode: " + err.Error())
	}
	return m
}

// decodeShadow reverses encodeShadow. Returns (nil, false) when the
// stored value is not a recognisable shadow-policy object.
func decodeShadow(raw any) (*ShadowPolicy, bool) {
	if raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var sp ShadowPolicy
	if err := json.Unmarshal(data, &sp); err != nil {
		return nil, false
	}
	return &sp, true
}

// sleepWithJitter blocks for up to ~20ms then returns. Respects ctx
// cancellation so shutdown is prompt. Uses the package-level rand
// source which is goroutine-safe; the custom *rand.Rand field this
// replaced raced under concurrent Put calls on the race-detector CI.
func (s *Store) sleepWithJitter(ctx context.Context) error {
	// #nosec G404 -- jitter only, not a security-sensitive RNG.
	d := time.Duration(rand.Intn(20)+5) * time.Millisecond
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// isConfigMissingErr reports whether err means "no document at that
// scope/id". configsvc returns redis.Nil in that case; rather than
// importing redis here we string-match on the well-known message.
func isConfigMissingErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "redis: nil")
}
