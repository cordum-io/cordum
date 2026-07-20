package scheduler

import (
	"bytes"
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

type schedulerResourceResolver struct {
	content   []byte
	mediaType string
	err       error
	calls     int
	trusted   resource.TrustedContext
}

func (s *schedulerResourceResolver) Resolve(
	_ context.Context,
	_ *agentv1.ResourceRef,
	trusted resource.TrustedContext,
) (resource.ResolvedResource, error) {
	s.calls++
	s.trusted = trusted
	return resource.ResolvedResource{Content: append([]byte(nil), s.content...), MediaType: s.mediaType}, s.err
}

type resourceSafetyClient struct{ lastReq *pb.PolicyCheckRequest }

func (c *resourceSafetyClient) Check(_ context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	c.lastReq = req
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}
func (*resourceSafetyClient) Evaluate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}
func (*resourceSafetyClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}
func (*resourceSafetyClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}
func (*resourceSafetyClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return &pb.ListSnapshotsResponse{}, nil
}

type resourceOutputClient struct{ lastReq *pb.OutputCheckRequest }

func (c *resourceOutputClient) CheckOutput(_ context.Context, req *pb.OutputCheckRequest, _ ...grpc.CallOption) (*pb.OutputCheckResponse, error) {
	c.lastReq = req
	return &pb.OutputCheckResponse{Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW}, nil
}

func newResourceTestCB(name string) *RedisCircuitBreaker {
	return NewRedisCircuitBreaker(nil, name, CircuitBreakerOpts{
		FailThreshold: 3, OpenDuration: time.Minute, HalfOpenMax: 1, CloseAfter: 1,
	})
}

func TestSafetyClientRejectsLegacyPointerWithoutCompatibility(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(server.Close)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	unsafePointer := "https://evil.example/secret"
	if err = rdb.Set(ctx, unsafePointer, []byte("must not be read"), 0).Err(); err != nil {
		t.Fatalf("seed unsafe key: %v", err)
	}
	client := &SafetyClient{client: &resourceSafetyClient{}, cb: newResourceTestCB("input-legacy"), contextClient: rdb}
	record, err := client.Check(ctx, &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a", Topic: "job.demo", ContextPtr: unsafePointer,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if record.Decision != SafetyUnavailable {
		t.Fatalf("decision = %q, want unavailable", record.Decision)
	}
}

func TestLegacyResourcesCannotCrossJobScope(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mr.Set("ctx:job-b", `{"other":true}`)
	mr.Set("res:job-b", "other result")

	safety := (&SafetyClient{
		client: &resourceSafetyClient{}, cb: newResourceTestCB("input-cross-job"), contextClient: rdb,
	}).WithLegacyResourceCompatibility(nil)
	record, err := safety.Check(context.Background(), &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a", Topic: "job.demo", ContextPtr: "redis://ctx:job-b",
	})
	if err != nil || record.Decision != SafetyUnavailable {
		t.Fatalf("cross-job input = %#v, %v", record, err)
	}

	engine := (&Engine{contextClient: rdb}).WithLegacyResourceCompatibility(nil)
	_, violations, err := engine.loadSubmitValidationPayload(context.Background(), &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a", ContextPtr: "redis://ctx:job-b",
	})
	if err != nil || len(violations) != 1 {
		t.Fatalf("cross-job schema violations = %#v, %v", violations, err)
	}

	fake := &resourceOutputClient{}
	output := (&OutputSafetyClient{
		client: fake, cb: newResourceTestCB("output-cross-job"), resultClient: rdb,
	}).WithLegacyResourceCompatibility(nil)
	_, err = output.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID: "job-a", Tenant: "tenant-a", ResultPtr: "redis://res:job-b",
	})
	if err == nil || fake.lastReq != nil {
		t.Fatalf("cross-job output reached policy RPC: req=%#v err=%v", fake.lastReq, err)
	}
}

func TestSafetyClientResolvesStructuredContextBeforePolicyRPC(t *testing.T) {
	resolver := &schedulerResourceResolver{content: []byte(`{"amount":42}`), mediaType: "application/json"}
	handler := &resourceSafetyClient{}
	client := &SafetyClient{
		client: handler, cb: newResourceTestCB("input-structured"),
		resourceReader: resourceio.Reader{Resolver: resolver},
	}
	record, err := client.Check(context.Background(), &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a", Topic: "job.demo",
		ContextRef: &agentv1.ResourceRef{ResolverId: "cache"},
	})
	if err != nil || record.Decision != SafetyAllow {
		t.Fatalf("Check = %#v, %v", record, err)
	}
	if got := string(handler.lastReq.GetInputContent()); got != `{"amount":42}` {
		t.Fatalf("input content = %q", got)
	}
	if got := handler.lastReq.GetInputContentType(); got != "application/json" {
		t.Fatalf("input content type = %q", got)
	}
	if resolver.trusted.TenantID != "tenant-a" || resolver.trusted.JobID != "job-a" {
		t.Fatalf("trusted context = %s/%s", resolver.trusted.TenantID, resolver.trusted.JobID)
	}
}

func TestEngineSchemaPayloadUsesStructuredContext(t *testing.T) {
	resolver := &schedulerResourceResolver{content: []byte(`{"safe":true}`), mediaType: "application/json"}
	engine := &Engine{resourceReader: resourceio.Reader{Resolver: resolver}}
	payload, violations, err := engine.loadSubmitValidationPayload(context.Background(), &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a",
		ContextRef: &agentv1.ResourceRef{ResolverId: "cache"},
	})
	if err != nil || len(violations) != 0 || string(payload) != `{"safe":true}` {
		t.Fatalf("load payload = %q, %#v, %v", payload, violations, err)
	}
}

func TestOutputClientResolvesStructuredResultBeforePolicyRPC(t *testing.T) {
	resolver := &schedulerResourceResolver{content: []byte("trusted output"), mediaType: "text/plain"}
	fake := &resourceOutputClient{}
	client := &OutputSafetyClient{
		client: fake, cb: newResourceTestCB("output-structured"),
		resourceReader: resourceio.Reader{Resolver: resolver},
	}
	res := &pb.JobResult{JobId: "job-a", ResultRef: &agentv1.ResourceRef{ResolverId: "cache"}}
	req := &pb.JobRequest{JobId: "job-a", TenantId: "tenant-a", Topic: "job.demo"}
	eval, err := outputEvaluateRequestFromJob(res, req, true)
	if err != nil || eval.ResultRef == nil {
		t.Fatalf("outputEvaluateRequestFromJob = %#v, %v", eval, err)
	}
	if _, err = client.EvaluateOutput(context.Background(), eval); err != nil {
		t.Fatalf("EvaluateOutput: %v", err)
	}
	got := fake.lastReq
	if got == nil || string(got.GetOutputContent()) != "trusted output" {
		t.Fatalf("policy request = %#v", got)
	}
	if got.GetResultPtr() != "" {
		t.Fatalf("raw result pointer crossed policy RPC: %q", got.GetResultPtr())
	}
	if got.GetContentType() != "text/plain" {
		t.Fatalf("content type = %q, want validated ResourceRef media type", got.GetContentType())
	}
	if resolver.trusted.TenantID != "tenant-a" || resolver.trusted.JobID != "job-a" {
		t.Fatalf("trusted context = %s/%s", resolver.trusted.TenantID, resolver.trusted.JobID)
	}
}

func TestSafetyClientPreservesFullStructuredInputSizeAfterTruncation(t *testing.T) {
	tail := []byte(`{"forbidden":"tail"}`)
	content := append(bytes.Repeat([]byte("a"), inputContentMaxBytes), tail...)
	resolver := &schedulerResourceResolver{content: content, mediaType: "application/json"}
	handler := &resourceSafetyClient{}
	client := &SafetyClient{
		client: handler, cb: newResourceTestCB("input-truncated"),
		resourceReader: resourceio.Reader{Resolver: resolver},
	}
	_, err := client.Check(context.Background(), &pb.JobRequest{
		JobId: "job-a", TenantId: "tenant-a", Topic: "job.demo",
		ContextRef: &agentv1.ResourceRef{ResolverId: "cache"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := handler.lastReq
	if got.GetInputSizeBytes() != int64(len(content)) {
		t.Fatalf("input size = %d, want verified full size %d", got.GetInputSizeBytes(), len(content))
	}
	if len(got.GetInputContent()) != inputContentMaxBytes || bytes.Contains(got.GetInputContent(), tail) {
		t.Fatalf("input content was not safely truncated: len=%d", len(got.GetInputContent()))
	}
}

func TestOutputClientPreservesResolvedFullMetadataBeforeTruncation(t *testing.T) {
	tail := []byte("AKIAIOSFODNN7EXAMPLE")
	content := append(bytes.Repeat([]byte("a"), outputContentMaxBytes), tail...)
	resolver := &schedulerResourceResolver{content: content, mediaType: "text/plain"}
	handler := &resourceOutputClient{}
	client := &OutputSafetyClient{
		client: handler, cb: newResourceTestCB("output-truncated"),
		resourceReader: resourceio.Reader{Resolver: resolver},
	}
	_, err := client.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID: "job-a", Tenant: "tenant-a", ResultRef: &agentv1.ResourceRef{ResolverId: "cache"},
		OutputSizeBytes: 1, ContentHash: "sha256:packet-controlled",
	})
	if err != nil {
		t.Fatalf("EvaluateOutput: %v", err)
	}
	got := handler.lastReq
	if got.GetOutputSizeBytes() != int64(len(content)) {
		t.Fatalf("output size = %d, want verified full size %d", got.GetOutputSizeBytes(), len(content))
	}
	if got.GetContentHash() != outputContentHash(content) {
		t.Fatalf("content hash = %q, want digest of full resolved bytes", got.GetContentHash())
	}
	if len(got.GetOutputContent()) != outputContentMaxBytes || bytes.Contains(got.GetOutputContent(), tail) {
		t.Fatalf("output content was not safely truncated: len=%d", len(got.GetOutputContent()))
	}
}
