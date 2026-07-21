package resource

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"google.golang.org/protobuf/proto"
)

// TrustedContext is authenticated authority, never derived from ResourceRef.
type TrustedContext struct {
	TenantID string
	JobID    string
}

// ResolvedResource contains bytes that passed all declared integrity checks.
type ResolvedResource struct {
	Content   []byte
	MediaType string
}

// Resolver is a sealed operator-installed backend. Only this package can add
// resolver kinds, preventing an implicit generic URI fallback.
type Resolver interface {
	ID() string
	resolve(context.Context, string, TrustedContext, string, uint64) ([]byte, error)
}

// Registry is immutable after construction and safe for concurrent use.
type Registry struct {
	now       func() time.Time
	resolvers map[string]Resolver
}

// NewRegistry snapshots the set of operator-installed resolvers.
func NewRegistry(now func() time.Time, resolvers ...Resolver) (*Registry, error) {
	if now == nil {
		now = time.Now
	}
	installed := make(map[string]Resolver, len(resolvers))
	for _, resolver := range resolvers {
		if interfaceIsNil(resolver) || !validResolverID(resolver.ID()) {
			return nil, ErrInvalidResolverConfig
		}
		if _, exists := installed[resolver.ID()]; exists {
			return nil, ErrInvalidResolverConfig
		}
		installed[resolver.ID()] = resolver
	}
	return &Registry{now: now, resolvers: installed}, nil
}

// Resolve fetches only through the selected local resolver, then rechecks
// expiry, exact size, and SHA-256 before returning content.
func (r *Registry) Resolve(
	ctx context.Context,
	ref *agentv1.ResourceRef,
	trusted TrustedContext,
) (ResolvedResource, error) {
	if r == nil || r.now == nil || r.resolvers == nil {
		return ResolvedResource{}, ErrUnavailable
	}
	if ctx == nil {
		return ResolvedResource{}, ErrInvalidReference
	}
	if err := ctx.Err(); err != nil {
		return ResolvedResource{}, err
	}
	if ref == nil {
		return ResolvedResource{}, ErrInvalidReference
	}
	snapshot := proto.Clone(ref).(*agentv1.ResourceRef)
	expires, err := validateReference(snapshot, trusted, r.now())
	if err != nil {
		return ResolvedResource{}, err
	}
	resolver, ok := r.resolvers[snapshot.GetResolverId()]
	if !ok {
		return ResolvedResource{}, ErrUnknownResolver
	}
	content, err := resolver.resolve(ctx, snapshot.GetUri(), trusted, snapshot.GetMediaType(), snapshot.GetSizeBytes())
	if err != nil {
		return ResolvedResource{}, err
	}
	if !expires.After(r.now()) {
		return ResolvedResource{}, ErrExpired
	}
	if err := verifyContent(snapshot, content); err != nil {
		return ResolvedResource{}, err
	}
	return ResolvedResource{Content: append([]byte(nil), content...), MediaType: snapshot.GetMediaType()}, nil
}

func verifyContent(ref *agentv1.ResourceRef, content []byte) error {
	if uint64(len(content)) != ref.GetSizeBytes() {
		return ErrSizeMismatch
	}
	digest := sha256.Sum256(content)
	if subtle.ConstantTimeCompare(digest[:], ref.GetSha256()) != 1 {
		return ErrDigestMismatch
	}
	return nil
}
