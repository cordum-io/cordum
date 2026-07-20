package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// WithResourceRegistry installs the operator-controlled structured resolver.
func (c *SafetyClient) WithResourceRegistry(registry *resource.Registry) *SafetyClient {
	c.resourceReader.Resolver = registry
	return c
}

// WithLegacyResourceCompatibility explicitly enables legacy Redis pointers.
func (c *SafetyClient) WithLegacyResourceCompatibility(observe func(resourceio.LegacyUse)) *SafetyClient {
	c.resourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true, Observe: observe}
	return c
}

func (c *SafetyClient) loadInputContent(ctx context.Context, req *pb.JobRequest) ([]byte, int64, string, error) {
	trusted := resource.TrustedContext{TenantID: ExtractTenant(req), JobID: strings.TrimSpace(req.GetJobId())}
	resolved, err := c.resourceReader.Read(ctx, resourceio.ReadRequest{
		Reference: req.GetContextRef(), LegacyPointer: strings.TrimSpace(req.GetContextPtr()),
		Trusted: trusted, Component: "scheduler.input",
		LoadLegacy: func(ctx context.Context, pointer string) ([]byte, error) {
			return c.loadLegacyInputContent(ctx, pointer, trusted)
		},
	})
	if err != nil {
		return nil, 0, "", err
	}
	originalSize := int64(len(resolved.Content))
	if len(resolved.Content) > inputContentMaxBytes {
		resolved.Content = resolved.Content[:inputContentMaxBytes]
	}
	return resolved.Content, originalSize, resolved.MediaType, nil
}

func (c *SafetyClient) loadLegacyInputContent(ctx context.Context, pointer string, trusted resource.TrustedContext) ([]byte, error) {
	if c.contextClient == nil {
		return nil, fmt.Errorf("input content store unavailable")
	}
	key, err := resourceio.BoundLegacyKey(pointer, resourceio.LegacyContext, trusted)
	if err != nil {
		return nil, err
	}
	return c.contextClient.Get(ctx, key).Bytes()
}

func (c *SafetyClient) attachInputContent(ctx context.Context, req *pb.JobRequest, checkReq *pb.PolicyCheckRequest) error {
	content, originalSize, mediaType, err := c.loadInputContent(ctx, req)
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return fmt.Errorf("input resource empty")
	}
	checkReq.InputContent = content
	checkReq.InputSizeBytes = originalSize
	checkReq.InputContentType = mediaType
	if checkReq.InputContentType == "" {
		checkReq.InputContentType = strings.TrimSpace(req.GetLabels()["content_type"])
	}
	if len(content) <= 64*1024 {
		attachInputContentLabel(checkReq, content)
	}
	return nil
}

func attachInputContentLabel(checkReq *pb.PolicyCheckRequest, content []byte) {
	if checkReq.Labels == nil {
		checkReq.Labels = make(map[string]string)
	}
	if _, exists := checkReq.Labels["_content.payload_json"]; !exists {
		checkReq.Labels["_content.payload_json"] = string(content)
	}
}
