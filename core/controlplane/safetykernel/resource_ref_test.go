package safetykernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

type kernelResourceResolver struct {
	content   []byte
	mediaType string
	calls     int
	trusted   resource.TrustedContext
}

func (r *kernelResourceResolver) Resolve(
	_ context.Context,
	_ *agentv1.ResourceRef,
	trusted resource.TrustedContext,
) (resource.ResolvedResource, error) {
	r.calls++
	r.trusted = trusted
	return resource.ResolvedResource{Content: append([]byte(nil), r.content...), MediaType: r.mediaType}, nil
}

func TestEvaluateOutputResolvesStructuredReferenceBeforePolicy(t *testing.T) {
	resolver := &kernelResourceResolver{content: []byte("trusted output"), mediaType: "text/plain"}
	srv := &server{resourceReader: resourceio.Reader{Resolver: resolver}}
	req := &OutputEvaluateRequest{
		JobID: "job-a", Tenant: "tenant-a",
		ResultRef: &agentv1.ResourceRef{ResolverId: "cache"},
	}
	resp, err := srv.EvaluateOutput(context.Background(), req)
	if err != nil || resp.Decision != "allow" {
		t.Fatalf("EvaluateOutput = %#v, %v", resp, err)
	}
	if string(req.OutputContent) != "trusted output" || req.ContentType != "text/plain" {
		t.Fatalf("resolved request = %#v", req)
	}
	if resolver.calls != 1 || resolver.trusted.TenantID != "tenant-a" || resolver.trusted.JobID != "job-a" {
		t.Fatalf("resolver calls/context = %d, %#v", resolver.calls, resolver.trusted)
	}
}

func TestEvaluateOutputDerivesMetadataFromFullResolvedContent(t *testing.T) {
	tail := []byte("AKIAIOSFODNN7EXAMPLE")
	content := append(bytes.Repeat([]byte("a"), maxOutputScanBytes), tail...)
	resolver := &kernelResourceResolver{content: content, mediaType: "text/plain"}
	srv := &server{resourceReader: resourceio.Reader{Resolver: resolver}}
	req := &OutputEvaluateRequest{
		JobID: "job-a", Tenant: "tenant-a",
		ResultRef:       &agentv1.ResourceRef{ResolverId: "cache"},
		OutputSizeBytes: 1, ContentHash: "sha256:packet-controlled",
	}
	resp, err := srv.EvaluateOutput(context.Background(), req)
	if err != nil || resp.Decision != "allow" {
		t.Fatalf("EvaluateOutput = %#v, %v", resp, err)
	}
	if req.OutputSizeBytes != int64(len(content)) || len(req.OutputContent) != maxOutputScanBytes {
		t.Fatalf("resolved size/content = %d/%d", req.OutputSizeBytes, len(req.OutputContent))
	}
	wantHash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	if req.ContentHash != wantHash {
		t.Fatalf("content hash = %q, want full resolved digest", req.ContentHash)
	}
}

func TestEvaluateOutputRejectsRawPointerByDefault(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	mr.Set("res:job-a", "content that must not bypass structured mode")
	srv := &server{resultClient: client}
	resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID: "job-a", Tenant: "tenant-a", ResultPtr: "redis://res:job-a",
	})
	if err != nil {
		t.Fatalf("EvaluateOutput: %v", err)
	}
	if resp.Decision != "quarantine" || resp.Reason != "output resource rejected" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestCheckOutputRejectsInlineContentWithRawPointerByDefault(t *testing.T) {
	srv := &server{}
	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId: "job-a", Tenant: "tenant-a", ResultPtr: "redis://res:job-a",
		OutputContent: []byte("inline must not bypass raw pointer rejection"),
	})
	if err != nil {
		t.Fatalf("CheckOutput: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("response = %#v", resp)
	}
}

func TestEvaluateOutputComparesInlineAndStructuredContent(t *testing.T) {
	resolver := &kernelResourceResolver{content: []byte("trusted"), mediaType: "text/plain"}
	srv := &server{resourceReader: resourceio.Reader{Resolver: resolver}}
	for name, inline := range map[string]string{"equal": "trusted", "different": "tampered"} {
		t.Run(name, func(t *testing.T) {
			resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
				JobID: "job-a", Tenant: "tenant-a", OutputContent: []byte(inline),
				ResultRef: &agentv1.ResourceRef{ResolverId: "cache"},
			})
			if err != nil {
				t.Fatalf("EvaluateOutput: %v", err)
			}
			want := "allow"
			if name == "different" {
				want = "quarantine"
			}
			if resp.Decision != want {
				t.Fatalf("decision = %q, want %q", resp.Decision, want)
			}
		})
	}
}

func TestContentForScanCompatibilityUsesHardenedPointerParser(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	mr.Set("res:job-a", "legacy output")
	mr.Set("res:job-b", "cross-job output")
	srv := (&server{resultClient: client}).withLegacyResourceCompatibility(nil)
	content, _, err := srv.contentForScan(context.Background(), &pb.OutputCheckRequest{
		JobId: "job-a", Tenant: "tenant-a", ResultPtr: "redis://res:job-a",
	})
	if err != nil || string(content) != "legacy output" {
		t.Fatalf("contentForScan = %q, %v", content, err)
	}
	_, _, err = srv.contentForScan(context.Background(), &pb.OutputCheckRequest{
		JobId: "job-a", Tenant: "tenant-a", ResultPtr: "redis://res:job-b",
	})
	if err == nil {
		t.Fatal("cross-job compatibility pointer accepted")
	}
	unsafe := "https://user:secret@example.invalid/result"
	_, _, err = srv.contentForScan(context.Background(), &pb.OutputCheckRequest{
		JobId: "job-a", Tenant: "tenant-a", ResultPtr: unsafe,
	})
	if err == nil {
		t.Fatal("unsafe compatibility pointer accepted")
	}
	if strings.Contains(err.Error(), unsafe) || strings.Contains(strings.ToLower(err.Error()), "secret") {
		t.Fatalf("error leaked pointer: %v", err)
	}
}
