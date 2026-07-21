package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// WithResourceRegistry installs the operator-controlled resource registry.
func (e *Engine) WithResourceRegistry(registry *resource.Registry) *Engine {
	e.resourceReader.Resolver = registry
	return e
}

// WithLegacyResourceCompatibility enables migration-only Redis pointers.
func (e *Engine) WithLegacyResourceCompatibility(observe func(resourceio.LegacyUse)) *Engine {
	e.resourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true, Observe: observe}
	return e
}

func (e *Engine) readContextResource(ctx context.Context, req *pb.JobRequest) ([]byte, error) {
	if e == nil || req == nil {
		return nil, fmt.Errorf("context resource unavailable")
	}
	trusted := resource.TrustedContext{TenantID: ExtractTenant(req), JobID: strings.TrimSpace(req.GetJobId())}
	resolved, err := e.resourceReader.Read(ctx, resourceio.ReadRequest{
		Reference: req.GetContextRef(), LegacyPointer: strings.TrimSpace(req.GetContextPtr()),
		Trusted: trusted, Component: "scheduler.schema",
		LoadLegacy: func(ctx context.Context, pointer string) ([]byte, error) {
			return e.loadLegacyContextResource(ctx, pointer, trusted)
		},
	})
	if err != nil {
		return nil, err
	}
	return resolved.Content, nil
}

func (e *Engine) loadLegacyContextResource(ctx context.Context, pointer string, trusted resource.TrustedContext) ([]byte, error) {
	if e.contextClient == nil {
		return nil, fmt.Errorf("context resource store unavailable")
	}
	key, err := resourceio.BoundLegacyKey(pointer, resourceio.LegacyContext, trusted)
	if err != nil {
		return nil, err
	}
	return e.contextClient.Get(ctx, key).Bytes()
}
