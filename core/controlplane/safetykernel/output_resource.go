package safetykernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (s *server) withResourceRegistry(registry *resource.Registry) *server {
	s.resourceReader.Resolver = registry
	return s
}

func (s *server) withLegacyResourceCompatibility(observe func(resourceio.LegacyUse)) *server {
	s.resourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true, Observe: observe}
	return s
}

func (s *server) resolveEvaluateOutputResource(ctx context.Context, req *OutputEvaluateRequest) (bool, error) {
	if req == nil {
		return false, fmt.Errorf("output request unavailable")
	}
	if req.ResultRef == nil && strings.TrimSpace(req.ResultPtr) == "" {
		content, truncated := truncateOutputContent(req.OutputContent)
		req.OutputContent = content
		return outputContentWasTruncated(req.OutputSizeBytes, content, truncated), nil
	}
	trusted := resource.TrustedContext{TenantID: strings.TrimSpace(req.Tenant), JobID: strings.TrimSpace(req.JobID)}
	resolved, err := s.resourceReader.Read(ctx, resourceio.ReadRequest{
		Reference: req.ResultRef, LegacyPointer: strings.TrimSpace(req.ResultPtr),
		Trusted: trusted, Component: "safety-kernel.output",
		LoadLegacy: func(ctx context.Context, pointer string) ([]byte, error) {
			return s.loadLegacyOutputResource(ctx, pointer, trusted)
		},
	})
	if err != nil {
		return false, err
	}
	if len(resolved.Content) == 0 {
		return false, fmt.Errorf("output resource empty")
	}
	if len(req.OutputContent) > 0 && !bytes.Equal(req.OutputContent, resolved.Content) {
		return false, resourceio.ErrDualContentMismatch
	}
	req.OutputSizeBytes = int64(len(resolved.Content))
	req.ContentHash = resolvedOutputContentHash(resolved.Content)
	content, truncated := truncateOutputContent(resolved.Content)
	req.OutputContent = content
	req.ResultPtr = ""
	if resolved.MediaType != "" {
		req.ContentType = resolved.MediaType
	}
	return outputContentWasTruncated(req.OutputSizeBytes, content, truncated), nil
}

func rejectEvaluateOutputResource(resp *OutputEvaluateResponse) {
	resp.Decision = "quarantine"
	resp.Reason = "output resource rejected"
	resp.Findings = []outputFinding{{
		Type: "resource_rejected", Severity: "critical",
		Detail: "output resource rejected", Scanner: "resource_resolver",
	}}
}

func (s *server) contentForScan(ctx context.Context, req *pb.OutputCheckRequest) ([]byte, bool, error) {
	if req == nil {
		return nil, false, fmt.Errorf("output request unavailable")
	}
	content := req.GetOutputContent()
	pointer := strings.TrimSpace(req.GetResultPtr())
	if pointer != "" {
		resolved, err := s.resolveLegacyScanResource(ctx, req, pointer)
		if err != nil {
			return nil, false, err
		}
		if len(content) > 0 && !bytes.Equal(content, resolved) {
			return nil, false, resourceio.ErrDualContentMismatch
		}
		content = resolved
		req.OutputSizeBytes = int64(len(resolved))
		req.ContentHash = resolvedOutputContentHash(resolved)
	}
	if len(content) > 0 {
		content, truncated := truncateOutputContent(content)
		return content, outputContentWasTruncated(req.GetOutputSizeBytes(), content, truncated), nil
	}
	message := strings.TrimSpace(req.GetErrorMessage())
	if message == "" {
		return nil, false, nil
	}
	content, truncated := truncateOutputContent([]byte(message))
	return content, outputContentWasTruncated(req.GetOutputSizeBytes(), content, truncated), nil
}

func outputContentWasTruncated(declaredSize int64, content []byte, locallyTruncated bool) bool {
	return locallyTruncated || declaredSize > int64(len(content))
}

func resolvedOutputContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("sha256:%x", sum)
}

func (s *server) resolveLegacyScanResource(ctx context.Context, req *pb.OutputCheckRequest, pointer string) ([]byte, error) {
	trusted := resource.TrustedContext{
		TenantID: strings.TrimSpace(req.GetTenant()), JobID: strings.TrimSpace(req.GetJobId()),
	}
	resolved, err := s.resourceReader.Read(ctx, resourceio.ReadRequest{
		LegacyPointer: pointer,
		Trusted:       trusted, Component: "safety-kernel.grpc-output",
		LoadLegacy: func(ctx context.Context, pointer string) ([]byte, error) {
			return s.loadLegacyOutputResource(ctx, pointer, trusted)
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resolved.Content) == 0 {
		return nil, fmt.Errorf("output resource empty")
	}
	return resolved.Content, nil
}

func (s *server) loadLegacyOutputResource(
	ctx context.Context,
	pointer string,
	trusted resource.TrustedContext,
) ([]byte, error) {
	if s == nil || s.resultClient == nil {
		return nil, fmt.Errorf("output resource store unavailable")
	}
	key, err := resourceio.BoundLegacyKey(pointer, resourceio.LegacyResult, trusted)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rctx, cancel := context.WithTimeout(ctx, outputReadTimeout)
	defer cancel()
	return s.resultClient.Get(rctx, key).Bytes()
}
