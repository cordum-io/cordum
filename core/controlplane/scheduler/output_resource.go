package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// WithResourceRegistry installs the operator-controlled result resolver.
func (c *OutputSafetyClient) WithResourceRegistry(registry *resource.Registry) *OutputSafetyClient {
	c.resourceReader.Resolver = registry
	return c
}

// WithLegacyResourceCompatibility enables migration-only Redis pointers.
func (c *OutputSafetyClient) WithLegacyResourceCompatibility(observe func(resourceio.LegacyUse)) *OutputSafetyClient {
	c.resourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true, Observe: observe}
	return c
}

func (c *OutputSafetyClient) attachResolvedOutput(
	ctx context.Context,
	req *OutputEvaluateRequest,
	checkReq *pb.OutputCheckRequest,
) error {
	if req.ResultRef == nil && strings.TrimSpace(req.ResultPtr) == "" {
		return nil
	}
	trusted := resource.TrustedContext{TenantID: strings.TrimSpace(req.Tenant), JobID: strings.TrimSpace(req.JobID)}
	resolved, err := c.resourceReader.Read(ctx, resourceio.ReadRequest{
		Reference: req.ResultRef, LegacyPointer: strings.TrimSpace(req.ResultPtr),
		Trusted: trusted, Component: "scheduler.output",
		LoadLegacy: func(ctx context.Context, pointer string) ([]byte, error) {
			return c.loadOutputContent(ctx, pointer, trusted)
		},
	})
	if err != nil {
		return err
	}
	if len(resolved.Content) == 0 {
		return fmt.Errorf("output resource empty")
	}
	if len(req.OutputContent) > 0 && !bytes.Equal(req.OutputContent, resolved.Content) {
		return resourceio.ErrDualContentMismatch
	}
	checkReq.OutputSizeBytes = int64(len(resolved.Content))
	checkReq.ContentHash = outputContentHash(resolved.Content)
	checkReq.OutputContent = resolved.Content
	checkReq.ResultPtr = ""
	if resolved.MediaType != "" {
		checkReq.ContentType = resolved.MediaType
	}
	return nil
}
