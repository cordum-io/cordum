package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Well-known error sentinels.
var (
	// ErrNotSecretURI is returned when Resolve is called with a string
	// that is not a valid secret:// URI.
	ErrNotSecretURI = errors.New("not a secret:// URI")

	// ErrNoProvider is returned when the URI's provider scheme has no
	// registered Provider implementation.
	ErrNoProvider = errors.New("no provider registered for scheme")

	// ErrSecretNotFound is returned by providers when the requested
	// path does not exist in the backend.
	ErrSecretNotFound = errors.New("secret not found")

	// ErrKeyNotFound is returned when the secret exists but the
	// requested key (URI fragment) is not present in the value map.
	ErrKeyNotFound = errors.New("key not found in secret")

	// ErrAccessDenied is returned when the provider's credentials lack
	// permission to read the requested secret.
	ErrAccessDenied = errors.New("access denied")
)

// ---------------------------------------------------------------------------
// Prometheus metrics
// ---------------------------------------------------------------------------

var (
	secretResolveTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_secrets_resolve_total",
		Help: "Total secret resolution attempts by provider and status.",
	}, []string{"provider", "status"})

	secretResolveDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cordum_secrets_resolve_duration_seconds",
		Help:    "Latency of secret resolution calls by provider.",
		Buckets: prometheus.DefBuckets,
	}, []string{"provider"})

	secretCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cordum_secrets_cache_hits_total",
		Help: "Total cache hits for secret resolution.",
	})

	secretCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cordum_secrets_cache_misses_total",
		Help: "Total cache misses for secret resolution.",
	})
)

// ---------------------------------------------------------------------------
// Provider interface
// ---------------------------------------------------------------------------

// Provider resolves secret references for a specific backend (Vault,
// AWS Secrets Manager, Kubernetes, etc.).
//
// Implementations must be safe for concurrent use.
type Provider interface {
	// Scheme returns the provider identifier that matches the host
	// component of secret:// URIs (e.g. "vault", "aws-sm", "k8s").
	Scheme() string

	// Resolve fetches the secret value for the given ref.  When the
	// ref includes a Key (URI fragment), the provider should return
	// only that field from the secret.
	//
	// Errors should wrap the sentinel errors (ErrSecretNotFound,
	// ErrKeyNotFound, ErrAccessDenied) so callers can use errors.Is.
	Resolve(ctx context.Context, ref SecretRef) (string, error)

	// Close releases any resources held by the provider (HTTP clients,
	// connections, etc.).
	Close() error
}

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

type cachedSecret struct {
	value     string
	expiresAt time.Time
}

type secretCache struct {
	mu      sync.RWMutex
	entries map[string]cachedSecret
	ttl     time.Duration
}

func newSecretCache(ttl time.Duration) *secretCache {
	return &secretCache{
		entries: make(map[string]cachedSecret),
		ttl:     ttl,
	}
}

func (c *secretCache) get(key string) (string, bool) {
	if c.ttl <= 0 {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.value, true
}

func (c *secretCache) set(key, value string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cachedSecret{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// ---------------------------------------------------------------------------
// Resolver
// ---------------------------------------------------------------------------

// ResolverOption configures optional Resolver behaviour.
type ResolverOption func(*Resolver)

// WithCacheTTL sets the TTL for resolved secret values.  A zero or
// negative value disables caching entirely.  Default: 5 minutes.
func WithCacheTTL(ttl time.Duration) ResolverOption {
	return func(r *Resolver) { r.cache = newSecretCache(ttl) }
}

// Resolver is the top-level secret resolution engine.  It maintains a
// registry of Provider implementations keyed by scheme and an optional
// in-memory cache with TTL.
//
// Resolver is safe for concurrent use.
type Resolver struct {
	mu        sync.RWMutex
	providers map[string]Provider
	cache     *secretCache
}

// NewResolver creates an empty Resolver with default options.  Providers
// must be registered via Register before Resolve calls will succeed.
func NewResolver(opts ...ResolverOption) *Resolver {
	r := &Resolver{
		providers: make(map[string]Provider),
		cache:     newSecretCache(5 * time.Minute),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Register adds a provider for the given scheme.  If a provider with
// the same scheme is already registered it is replaced (and the old
// provider is NOT closed — callers must manage lifecycle).
func (r *Resolver) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Scheme()] = p
}

// HasProvider returns true if a provider is registered for the given
// scheme (e.g. "vault", "aws-sm").
func (r *Resolver) HasProvider(scheme string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[scheme]
	return ok
}

// Providers returns the list of registered provider scheme names.
func (r *Resolver) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}

// Resolve parses a secret:// URI and delegates to the matching provider.
// The resolved value is cached for the configured TTL (default 5m).
//
// Errors wrap the sentinel errors (ErrNotSecretURI, ErrNoProvider,
// ErrSecretNotFound, etc.) so callers can use errors.Is for dispatch.
func (r *Resolver) Resolve(ctx context.Context, uri string) (string, error) {
	ref, ok := ParseSecretRef(uri)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotSecretURI, uri)
	}

	// Cache lookup.
	cacheKey := ref.Raw
	if val, hit := r.cache.get(cacheKey); hit {
		secretCacheHits.Inc()
		return val, nil
	}
	secretCacheMisses.Inc()

	r.mu.RLock()
	p, ok := r.providers[ref.Provider]
	r.mu.RUnlock()
	if !ok {
		secretResolveTotal.WithLabelValues(ref.Provider, "no_provider").Inc()
		return "", fmt.Errorf("%w: %q", ErrNoProvider, ref.Provider)
	}

	start := time.Now()
	val, err := p.Resolve(ctx, ref)
	elapsed := time.Since(start)
	secretResolveDuration.WithLabelValues(ref.Provider).Observe(elapsed.Seconds())

	if err != nil {
		status := "error"
		if errors.Is(err, ErrSecretNotFound) {
			status = "not_found"
		} else if errors.Is(err, ErrAccessDenied) {
			status = "access_denied"
		}
		secretResolveTotal.WithLabelValues(ref.Provider, status).Inc()
		return "", err
	}

	secretResolveTotal.WithLabelValues(ref.Provider, "ok").Inc()
	r.cache.set(cacheKey, val)

	slog.Debug("secret resolved",
		"provider", ref.Provider,
		"path", MaskSecretPath(ref.Path),
		"has_key", ref.Key != "",
		"duration_ms", elapsed.Milliseconds(),
	)

	return val, nil
}

// ResolveAll walks a value tree (maps, slices, strings) and resolves
// every string that is a valid secret:// URI.  Non-secret strings and
// non-string values are left unchanged.  The first resolution error
// aborts the walk.
//
// The input value is NOT mutated — a new tree is returned.
func (r *Resolver) ResolveAll(ctx context.Context, value any) (any, error) {
	return r.resolveWalk(ctx, value)
}

func (r *Resolver) resolveWalk(ctx context.Context, value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return v, nil

	case string:
		if !IsSecretRef(v) {
			return v, nil
		}
		resolved, err := r.Resolve(ctx, v)
		if err != nil {
			ref, _ := ParseSecretRef(v)
			return nil, fmt.Errorf("resolve secret://%s/%s: %w",
				ref.Provider, MaskSecretPath(ref.Path), err)
		}
		return resolved, nil

	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			resolved, err := r.resolveWalk(ctx, child)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil

	case map[string]string:
		out := make(map[string]any, len(v))
		for k, child := range v {
			resolved, err := r.resolveWalk(ctx, child)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil

	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			resolved, err := r.resolveWalk(ctx, child)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil

	case []string:
		out := make([]any, len(v))
		for i, child := range v {
			resolved, err := r.resolveWalk(ctx, child)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil

	default:
		return v, nil
	}
}

// Close releases all provider resources and clears the cache.
func (r *Resolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, p := range r.providers {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	r.providers = make(map[string]Provider)
	r.cache.entries = make(map[string]cachedSecret)
	return errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// ResolveOrRedact is a convenience that resolves all secret:// refs in
// a value tree when a Resolver is available, or redacts them when no
// resolver is configured (r == nil).  This allows handlers to call a
// single function regardless of configuration:
//
//	resolved, err := secrets.ResolveOrRedact(ctx, r, payload)
// ---------------------------------------------------------------------------

// ResolveOrRedact resolves secret refs when r is non-nil, or redacts
// them when r is nil.  This is the primary integration point for
// gateway handlers.
func ResolveOrRedact(ctx context.Context, r *Resolver, value any) (any, bool, error) {
	if r == nil {
		redacted, changed := RedactSecretRefs(value)
		return redacted, changed, nil
	}
	resolved, err := r.ResolveAll(ctx, value)
	if err != nil {
		return nil, false, err
	}
	return resolved, !isEqual(value, resolved), nil
}

// isEqual is a shallow equality check for the common case.
func isEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers for formatting
// ---------------------------------------------------------------------------

// MaskSecretPath returns a partially masked version of a secret path
// for use in log messages and error output.  The last path segment is
// masked, e.g. "database/creds" → "database/****".
func MaskSecretPath(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "****"
	}
	return path[:idx+1] + "****"
}
