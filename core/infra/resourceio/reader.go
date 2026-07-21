// Package resourceio centralizes structured resource consumption and the
// deliberately explicit compatibility bridge for legacy Redis pointers.
package resourceio

import (
	"bytes"
	"context"
	"errors"
	"reflect"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resource"
)

var (
	ErrStructuredRequired    = errors.New("resource access: structured reference required")
	ErrResolverUnavailable   = errors.New("resource access: resolver unavailable")
	ErrLegacyLoaderMissing   = errors.New("resource access: legacy loader unavailable")
	ErrDualContentMismatch   = errors.New("resource access: legacy and structured content mismatch")
	ErrInvalidTrustedContext = errors.New("resource access: invalid trusted context")
)

// Resolver is implemented by resource.Registry.
type Resolver interface {
	Resolve(context.Context, *agentv1.ResourceRef, resource.TrustedContext) (resource.ResolvedResource, error)
}

// LegacyUse records that a compatibility-only pointer path was attempted.
// Pointer values are intentionally excluded so telemetry cannot leak secrets.
type LegacyUse struct {
	Component string
	TenantID  string
	JobID     string
}

type LegacyCompatibility struct {
	Enabled bool
	Observe func(LegacyUse)
}

type Reader struct {
	Resolver      Resolver
	Compatibility LegacyCompatibility
}

type ReadRequest struct {
	Reference     *agentv1.ResourceRef
	LegacyPointer string
	Trusted       resource.TrustedContext
	Component     string
	LoadLegacy    func(context.Context, string) ([]byte, error)
}

// Read resolves a structured reference, or a legacy pointer only when the
// caller explicitly enables compatibility. When both are present, both
// sources must produce identical bytes.
func (r Reader) Read(ctx context.Context, req ReadRequest) (resource.ResolvedResource, error) {
	if resource.ValidateTrustedContext(req.Trusted) != nil {
		return resource.ResolvedResource{}, ErrInvalidTrustedContext
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Reference == nil {
		return r.readLegacy(ctx, req)
	}
	if resolverIsNil(r.Resolver) {
		return resource.ResolvedResource{}, ErrResolverUnavailable
	}
	resolved, err := r.Resolver.Resolve(ctx, req.Reference, req.Trusted)
	if err != nil {
		return resource.ResolvedResource{}, err
	}
	if req.LegacyPointer == "" {
		return resolved, nil
	}
	legacy, err := r.readLegacy(ctx, req)
	if err != nil {
		return resource.ResolvedResource{}, err
	}
	if !bytes.Equal(resolved.Content, legacy.Content) {
		return resource.ResolvedResource{}, ErrDualContentMismatch
	}
	return resolved, nil
}

func (r Reader) readLegacy(ctx context.Context, req ReadRequest) (resource.ResolvedResource, error) {
	if !r.Compatibility.Enabled {
		return resource.ResolvedResource{}, ErrStructuredRequired
	}
	if req.LegacyPointer == "" || req.LoadLegacy == nil {
		return resource.ResolvedResource{}, ErrLegacyLoaderMissing
	}
	if r.Compatibility.Observe != nil {
		r.Compatibility.Observe(LegacyUse{
			Component: req.Component,
			TenantID:  req.Trusted.TenantID,
			JobID:     req.Trusted.JobID,
		})
	}
	content, err := req.LoadLegacy(ctx, req.LegacyPointer)
	if err != nil {
		return resource.ResolvedResource{}, err
	}
	return resource.ResolvedResource{Content: append([]byte(nil), content...)}, nil
}

func resolverIsNil(resolver Resolver) bool {
	if resolver == nil {
		return true
	}
	value := reflect.ValueOf(resolver)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
